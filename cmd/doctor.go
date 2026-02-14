package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	servercmd "github.com/sauercrowd/gci/cmd/server"
	"github.com/sauercrowd/gci/service"
	gcissh "github.com/sauercrowd/gci/ssh"
	"github.com/spf13/cobra"
)

func newDoctorCommand() *cobra.Command {
	var serverName string

	doctorCmd := &cobra.Command{
		Use:          "doctor [config_file]",
		Short:        "Validate local configuration and remote deploy prerequisites",
		SilenceUsage: true,
		Args:         cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var explicitConfigPath string
			if len(args) > 0 {
				explicitConfigPath = args[0]
			}

			configPath, err := resolveServiceConfigPath(explicitConfigPath)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "service config: %s\n", configPath)

			cfg, err := service.ReadConfigFile(configPath)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "service config: OK")

			resolvedServerName := serverName
			if resolvedServerName == "" {
				resolvedServerName = cfg.Server
			}

			srv, err := servercmd.ResolveServer(resolvedServerName)
			if err != nil {
				return err
			}
			if err := validateServerEntry(srv); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "server config (%s): OK\n", srv.Name)

			target := gcissh.Target{
				User:           srv.User,
				Host:           srv.Host,
				PrivateKeyPath: srv.PrivateKey,
				Timeout:        deployConnectTimeout,
			}

			checkCtx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			if err := gcissh.CheckReachable(checkCtx, target); err != nil {
				return fmt.Errorf("server %q is not reachable over SSH: %w", srv.Name, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ssh connectivity (%s): OK\n", srv.Host)

			driver, err := service.ResolveDriver(cfg)
			if err != nil {
				return err
			}
			runner := newSSHRemoteRunner(target)
			if err := driver.Doctor(cmd.Context(), runner, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "driver checks (%s): OK\n", driver.Name())

			fmt.Fprintln(cmd.OutOrStdout(), "doctor: all checks passed")
			return nil
		},
	}

	doctorCmd.Flags().StringVar(&serverName, "server", "", "Override server from service config")

	return doctorCmd
}

func validateServerEntry(srv servercmd.Server) error {
	if strings.TrimSpace(srv.User) == "" {
		return fmt.Errorf("server %q has empty user", srv.Name)
	}
	if strings.TrimSpace(srv.Host) == "" {
		return fmt.Errorf("server %q has empty host", srv.Name)
	}
	if strings.TrimSpace(srv.PrivateKey) == "" {
		return fmt.Errorf("server %q has empty private_key", srv.Name)
	}
	if strings.TrimSpace(srv.ServiceDir) == "" {
		return fmt.Errorf("server %q has empty service_dir", srv.Name)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(newDoctorCommand())
}
