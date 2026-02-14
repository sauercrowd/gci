package cmd

import (
	"fmt"

	servercmd "github.com/sauercrowd/gci/cmd/server"
	"github.com/sauercrowd/gci/service"
	gcissh "github.com/sauercrowd/gci/ssh"
	"github.com/spf13/cobra"
)

func newLogCommand() *cobra.Command {
	var serverName string
	var lines int
	var follow bool
	var services []string

	logCmd := &cobra.Command{
		Use:          "logs [config_file]",
		Short:        "Show service logs from the remote server",
		SilenceUsage: true,
		Args:         cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if lines <= 0 {
				return fmt.Errorf("--lines must be greater than 0")
			}

			var explicitConfigPath string
			if len(args) > 0 {
				explicitConfigPath = args[0]
			}

			configPath, err := resolveServiceConfigPath(explicitConfigPath)
			if err != nil {
				return err
			}

			cfg, err := service.ReadConfigFile(configPath)
			if err != nil {
				return err
			}
			if cfg.DriverDockerSwarm != nil && len(services) > 0 {
				cfg.DriverDockerSwarm.LogServices = services
			}

			resolvedServer := serverName
			if resolvedServer == "" {
				resolvedServer = cfg.Server
			}

			srv, err := servercmd.ResolveServer(resolvedServer)
			if err != nil {
				return err
			}

			target := gcissh.Target{
				User:           srv.User,
				Host:           srv.Host,
				PrivateKeyPath: srv.PrivateKey,
				Timeout:        deployConnectTimeout,
			}

			driver, err := service.ResolveDriver(cfg)
			if err != nil {
				return err
			}
			runner := newSSHRemoteRunner(target)

			if follow {
				return driver.LogsStream(cmd.Context(), runner, cfg, lines, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}

			result, err := driver.Logs(cmd.Context(), runner, cfg, lines)
			if err != nil {
				return err
			}

			if result.Stdout != "" {
				fmt.Fprint(cmd.OutOrStdout(), result.Stdout)
			}
			if result.Stderr != "" {
				fmt.Fprint(cmd.ErrOrStderr(), result.Stderr)
			}

			return nil
		},
	}

	logCmd.Flags().StringVar(&serverName, "server", "", "Override server from service config")
	logCmd.Flags().IntVar(&lines, "lines", 100, "Number of log lines to fetch")
	logCmd.Flags().BoolVarP(&follow, "follow", "f", false, "Stream logs continuously")
	logCmd.Flags().StringSliceVar(&services, "service", nil, "Service name(s) to fetch logs for (repeat or comma-separated)")

	return logCmd
}

func init() {
	rootCmd.AddCommand(newLogCommand())
}
