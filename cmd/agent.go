package cmd

import (
	"github.com/sauercrowd/gci/internal/host"
	"github.com/spf13/cobra"
)

func init() {
	agent := &cobra.Command{Use: "agent", Short: "Host-side commands (invoked by the CLI)", Hidden: true}
	var opts host.BootstrapOptions
	bootstrap := &cobra.Command{
		Use: "bootstrap",
		RunE: func(cmd *cobra.Command, args []string) error {
			return host.Bootstrap(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}
	bootstrap.Flags().StringVar(&opts.Subnet, "subnet", "", "")
	bootstrap.Flags().IntVar(&opts.HTTPPort, "http-port", 0, "")
	bootstrap.Flags().StringVar(&opts.VIPRange, "vip-range", "", "")
	bootstrap.Flags().StringVar(&opts.StorageSize, "storage-size", "50GiB", "")
	firewall := &cobra.Command{
		Use: "firewall",
		RunE: func(cmd *cobra.Command, args []string) error {
			return host.Firewall(cmd.Context())
		},
	}
	agent.AddCommand(bootstrap, firewall)
	rootCmd.AddCommand(agent)
}
