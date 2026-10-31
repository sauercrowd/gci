package cmd

import (
	"os"
	"strconv"

	"github.com/sauercrowd/gci/internal/config"
	"github.com/sauercrowd/gci/internal/remote"
	"github.com/spf13/cobra"
)

func project(cmd *cobra.Command, needConfig bool) (*config.Config, *remote.Host, error) {
	path, _ := cmd.Flags().GetString("config")
	name, _ := cmd.Flags().GetString("server")
	var cfg *config.Config
	if needConfig || fileExists(path) {
		var err error
		cfg, err = config.Load(path)
		if err != nil {
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func init() {
	var subnet, vipRange, storageSize string
	var httpPort int
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Prepare a host for GCI services",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, h, err := project(cmd, false)
			if err != nil {
				return err
			}
			defer h.Close()
			return h.Agent(cmd.Context(), []string{
				"bootstrap", "--subnet", subnet, "--http-port", strconv.Itoa(httpPort),
				"--vip-range", vipRange, "--storage-size", storageSize,
			}, nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&subnet, "subnet", "10.77.0.1/24", "gateway address/CIDR of the gci bridge")
	cmd.Flags().StringVar(&vipRange, "vip-range", "10.77.1.0/24", "service VIP range")
	cmd.Flags().StringVar(&storageSize, "storage-size", "50GiB", "size of the btrfs-backed pool")
	cmd.Flags().IntVar(&httpPort, "http-port", 80, "host port Envoy listens on")
	rootCmd.AddCommand(cmd)
}
