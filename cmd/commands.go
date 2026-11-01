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

// project loads gci.toml (if required) and connects to the selected server.
func project(cmd *cobra.Command, needConfig bool) (*config.Config, *remote.Host, error) {
	path, _ := cmd.Flags().GetString("config")
	name, _ := cmd.Flags().GetString("server")
	var cfg *config.Config
	if needConfig || fileExists(path) {
		var err error
		if cfg, err = config.Load(path); err != nil {
			return nil, nil, err
		}
		if name == "" {
			name = cfg.Server
		}
	}
	s, err := findServer(name)
	if err != nil {
		return nil, nil, err
	}
	h, err := connect(cmd.Context(), s)
	if err != nil {
		return nil, nil, err
	}
	if err := h.EnsureAgent(cmd.Context(), cmd.ErrOrStderr()); err != nil {
		h.Close()
		return nil, nil, err
	}
	return cfg, h, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

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
	bootstrap.Flags().StringVar(&subnet, "subnet", "", "gateway address/CIDR of the gci bridge (defaults to the existing bridge or 10.77.0.1/24)")
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
				if h.Agent(cmd.Context(), []string{"has-source", cfg.App, name, hash}, nil, io.Discard, io.Discard) == nil {
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "==> %s: uploading source %s (%d KiB)\n", name, hash, len(tarball)/1024)
				if err := h.Agent(cmd.Context(), []string{"receive-source", cfg.App, name, hash}, bytes.NewReader(tarball), cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
					return err
				}
			}
			raw, err := json.Marshal(plan)
			if err != nil {
				return err
			}
			return h.Agent(cmd.Context(), []string{"apply"}, bytes.NewReader(raw), cmd.OutOrStdout(), cmd.ErrOrStderr())
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

	rootCmd.AddCommand(bootstrap, deploy, status, logs, rm)
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
