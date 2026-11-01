package cmd

import (
	"encoding/json"
	"fmt"
	"os"

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

	apply := &cobra.Command{
		Use: "apply",
		RunE: func(cmd *cobra.Command, args []string) error {
			var plan host.Plan
			if err := json.NewDecoder(os.Stdin).Decode(&plan); err != nil {
				return fmt.Errorf("read plan: %w", err)
			}
			if plan.Config == nil {
				return fmt.Errorf("plan has no config")
			}
			return host.Apply(cmd.Context(), cmd.OutOrStdout(), plan)
		},
	}

	hasSource := &cobra.Command{
		Use:  "has-source <app> <service> <hash>",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !host.HasSource(args[0], args[1], args[2]) {
				return fmt.Errorf("missing")
			}
			return nil
		},
	}

	receive := &cobra.Command{
		Use:  "receive-source <app> <service> <hash>",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return host.ReceiveSource(args[0], args[1], args[2], os.Stdin)
		},
	}

	status := &cobra.Command{
		Use:  "status <app>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return host.Status(cmd.Context(), cmd.OutOrStdout(), args[0])
		},
	}

	var lines int
	var follow bool
	logs := &cobra.Command{
		Use:  "logs <app> <service>",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return host.Logs(cmd.Context(), cmd.OutOrStdout(), args[0], args[1], lines, follow)
		},
	}
	logs.Flags().IntVarP(&lines, "lines", "n", 100, "")
	logs.Flags().BoolVarP(&follow, "follow", "f", false, "")

	var volumes bool
	rm := &cobra.Command{
		Use:  "rm <app>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return host.Remove(cmd.Context(), cmd.OutOrStdout(), args[0], volumes)
		},
	}
	rm.Flags().BoolVar(&volumes, "volumes", false, "")

	firewall := &cobra.Command{
		Use: "firewall",
		RunE: func(cmd *cobra.Command, args []string) error {
			return host.Firewall(cmd.Context())
		},
	}

	agent.AddCommand(bootstrap, apply, hasSource, receive, status, logs, rm, firewall)
	rootCmd.AddCommand(agent)
}
