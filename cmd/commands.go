package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/sauercrowd/gci/internal/config"
	"github.com/sauercrowd/gci/internal/host"
	"github.com/sauercrowd/gci/internal/remote"
	"github.com/spf13/cobra"
)

func init() {
	var subnet, vipRange string
	var httpPort int
	var storageSize string
	bootstrap := &cobra.Command{
		Use:   "bootstrap",
		Short: "Prepare a host once: Incus, Nix, network, base image, Envoy front door",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, h, err := project(cmd, false)
			if err != nil {
				return err
			}
			defer h.Close()
			return h.Agent(cmd.Context(), []string{"bootstrap", "--subnet", subnet, "--http-port", strconv.Itoa(httpPort), "--vip-range", vipRange, "--storage-size", storageSize},
				nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	bootstrap.Flags().StringVar(&subnet, "subnet", "10.77.0.1/24", "gateway address/CIDR of the gci bridge")
	bootstrap.Flags().IntVar(&httpPort, "http-port", 80, "host port Envoy listens on")
	bootstrap.Flags().StringVar(&vipRange, "vip-range", "10.77.1.0/24", "service VIP range, routed to Envoy")
	bootstrap.Flags().StringVar(&storageSize, "storage-size", "50GiB", "size of the btrfs-backed gci storage pool")

	var clean bool
	deploy := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the services in gci.toml",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			ctx := cmd.Context()
			plan := host.Plan{Config: cfg, Sources: map[string]string{}, Clean: clean || cleanEnabled(os.Getenv("GCI_CLEAN"))}
			for _, name := range sortedNames(cfg) {
				svc := cfg.Services[name]
				if svc.Source == "" {
					continue
				}
				tarball, hash, err := remote.PackSource(svc.Source)
				if err != nil {
					return fmt.Errorf("%s: pack %s: %w", name, svc.Source, err)
				}
				plan.Sources[name] = hash
				var out bytes.Buffer
				if err := h.Agent(ctx, []string{"has-source", cfg.App, name, hash}, nil, &out, io.Discard); err == nil {
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "==> %s: uploading source %s (%d KiB)\n", name, hash, len(tarball)/1024)
				if err := h.Agent(ctx, []string{"receive-source", cfg.App, name, hash}, bytes.NewReader(tarball), cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
					return fmt.Errorf("%s: upload source: %w", name, err)
				}
			}
			raw, err := json.Marshal(plan)
			if err != nil {
				return err
			}
			return h.Agent(ctx, []string{"apply"}, bytes.NewReader(raw), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	deploy.Flags().BoolVar(&clean, "clean", false, "rebuild source services from a fresh base")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show the services of this app",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			return h.Agent(cmd.Context(), []string{"status", cfg.App}, nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}

	var lines int
	var follow bool
	logs := &cobra.Command{
		Use:   "logs <service>",
		Short: "Show a service's logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			a := []string{"logs", cfg.App, args[0], "-n", strconv.Itoa(lines)}
			if follow {
				a = append(a, "-f")
			}
			return h.Agent(cmd.Context(), a, nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	logs.Flags().IntVarP(&lines, "lines", "n", 100, "number of lines")
	logs.Flags().BoolVarP(&follow, "follow", "f", false, "follow")

	var volumes bool
	rm := &cobra.Command{
		Use:   "rm",
		Short: "Remove all containers of this app (volumes are kept unless --volumes)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			a := []string{"rm", cfg.App}
			if volumes {
				a = append(a, "--volumes")
			}
			return h.Agent(cmd.Context(), a, nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	rm.Flags().BoolVar(&volumes, "volumes", false, "also delete the app's volumes (data loss)")

	secret := &cobra.Command{Use: "secret", Short: "Manage app secrets"}
	secretSet := &cobra.Command{
		Use:   "set <name>",
		Short: "Read a secret value from stdin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "reading secret from stdin, end with Ctrl-D")
			}
			value, err := io.ReadAll(io.LimitReader(os.Stdin, 64*1024+1))
			if err != nil {
				return err
			}
			if len(value) > 64*1024 {
				return fmt.Errorf("secret exceeds 64 KiB")
			}
			if bytes.HasSuffix(value, []byte("\r\n")) {
				value = value[:len(value)-2]
			} else if bytes.HasSuffix(value, []byte("\n")) {
				value = value[:len(value)-1]
			}
			if err := h.Agent(cmd.Context(), []string{"secret-set", cfg.App, args[0]}, bytes.NewReader(value), cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			return nil
		},
	}
	secretList := &cobra.Command{
		Use:   "ls",
		Short: "List secret names",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			return h.Agent(cmd.Context(), []string{"secret-ls", cfg.App}, nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	secretRemove := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			return h.Agent(cmd.Context(), []string{"secret-rm", cfg.App, args[0]}, nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	secret.AddCommand(secretSet, secretList, secretRemove)
	rootCmd.AddCommand(bootstrap, deploy, status, logs, rm, secret)
}

func cleanEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func sortedNames(cfg *config.Config) []string {
	order, _ := cfg.Order()
	return order
}
