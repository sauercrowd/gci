package cmd

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/sauercrowd/gci/internal/host"
	"github.com/spf13/cobra"
)

func init() {
	deploy := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the services in gci.toml",
		Args:  cobra.NoArgs,
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
		Args:  cobra.NoArgs,
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
			agentArgs := []string{"logs", cfg.App, args[0], "-n", strconv.Itoa(lines)}
			if follow {
				agentArgs = append(agentArgs, "-f")
			}
			return h.Agent(cmd.Context(), agentArgs, nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	logs.Flags().IntVarP(&lines, "lines", "n", 100, "number of lines")
	logs.Flags().BoolVarP(&follow, "follow", "f", false, "follow")

	var volumes bool
	rm := &cobra.Command{
		Use:   "rm",
		Short: "Remove all containers of this app",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, h, err := project(cmd, true)
			if err != nil {
				return err
			}
			defer h.Close()
			agentArgs := []string{"rm", cfg.App}
			if volumes {
				agentArgs = append(agentArgs, "--volumes")
			}
			return h.Agent(cmd.Context(), agentArgs, nil, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	rm.Flags().BoolVar(&volumes, "volumes", false, "also delete the app's volumes (data loss)")

	rootCmd.AddCommand(deploy, status, logs, rm)
}
