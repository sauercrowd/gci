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
	var clean bool
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
