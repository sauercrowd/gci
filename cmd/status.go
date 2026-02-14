package cmd

import (
	"fmt"
	"strings"

	servercmd "github.com/sauercrowd/gci/cmd/server"
	"github.com/sauercrowd/gci/service"
	gcissh "github.com/sauercrowd/gci/ssh"
	"github.com/spf13/cobra"
)

func newStatusCommand() *cobra.Command {
	var serverName string

	statusCmd := &cobra.Command{
		Use:          "status [config_file]",
		Short:        "Show remote service status",
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

			cfg, err := service.ReadConfigFile(configPath)
			if err != nil {
				return err
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

			result, err := driver.Status(cmd.Context(), runner, cfg)
			if err != nil {
				return err
			}

			if strings.TrimSpace(result.Stdout) != "" {
				fmt.Fprint(cmd.OutOrStdout(), result.Stdout)
			}
			if strings.TrimSpace(result.Stderr) != "" {
				fmt.Fprint(cmd.ErrOrStderr(), result.Stderr)
			}
			return nil
		},
	}

	statusCmd.Flags().StringVar(&serverName, "server", "", "Override server from service config")

	return statusCmd
}

func init() {
	rootCmd.AddCommand(newStatusCommand())
}
