package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"

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
	bootstrap.Flags().StringVar(&subnet, "subnet", "10.77.0.1/24", "gateway address/CIDR of the gci bridge")
	bootstrap.Flags().IntVar(&httpPort, "http-port", 80, "host port Envoy listens on")
	bootstrap.Flags().StringVar(&vipRange, "vip-range", "10.77.1.0/24", "service VIP range, routed to Envoy")
	bootstrap.Flags().StringVar(&storageSize, "storage-size", "50GiB", "size of the btrfs-backed gci storage pool")

	deploy := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the services in gci.toml",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			raw, err := json.Marshal(host.Plan{Config: cfg})
			if err != nil {
				return err
			}
			return h.Agent(cmd.Context(), []string{"apply"}, bytes.NewReader(raw), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}

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
