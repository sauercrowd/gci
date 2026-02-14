package cmd

import (
	"bufio"
	"fmt"
	"path"
	"strings"

	servercmd "github.com/sauercrowd/gci/cmd/server"
	"github.com/sauercrowd/gci/service"
	gcissh "github.com/sauercrowd/gci/ssh"
	"github.com/spf13/cobra"
)

func newRemoveCommand() *cobra.Command {
	var serverName string
	var assumeYes bool

	rmCmd := &cobra.Command{
		Use:          "rm [config_file]",
		Short:        "Remove deployed service from remote server",
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

			remoteServiceDir := path.Join(srv.ServiceDir, cfg.Name)
			if !assumeYes {
				confirmed, err := confirmRemoval(cmd, cfg.Name, srv.Name, remoteServiceDir)
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("aborted")
				}
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

			if err := driver.Remove(cmd.Context(), newSSHRemoteRunner(target), cfg, remoteServiceDir); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "removed service %q from server %q\n", cfg.Name, srv.Name)
			return nil
		},
	}

	rmCmd.Flags().StringVar(&serverName, "server", "", "Override server from service config")
	rmCmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "Skip confirmation prompt")

	return rmCmd
}

func confirmRemoval(cmd *cobra.Command, serviceName, serverName, remoteDir string) (bool, error) {
	fmt.Fprintf(
		cmd.OutOrStdout(),
		"Remove service %q from server %q and delete %q? [y/N]: ",
		serviceName,
		serverName,
		remoteDir,
	)

	reader := bufio.NewReader(cmd.InOrStdin())
	input, err := reader.ReadString('\n')
	if err != nil && err.Error() != "EOF" {
		return false, fmt.Errorf("failed to read confirmation input: %w", err)
	}

	answer := strings.ToLower(strings.TrimSpace(input))
	return answer == "y" || answer == "yes", nil
}

func init() {
	rootCmd.AddCommand(newRemoveCommand())
}
