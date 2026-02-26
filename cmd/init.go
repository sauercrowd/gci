package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	servercmd "github.com/sauercrowd/gci/cmd/server"
	"github.com/sauercrowd/gci/service"
	gcissh "github.com/sauercrowd/gci/ssh"
	"github.com/spf13/cobra"
)

func newInitCommand() *cobra.Command {
	var serverName string

	initCmd := &cobra.Command{
		Use:          "init <service_name>",
		Short:        "Initialize a service configuration file",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceName := args[0]
			configPath := "gci.toml"
			resolvedServerName := serverName

			if resolvedServerName == "" {
				defaultServerName, found, err := servercmd.DefaultServerName()
				if err != nil {
					return err
				}
				if found {
					resolvedServerName = defaultServerName
				}
			}

			if _, err := os.Stat(configPath); err == nil {
				return fmt.Errorf("refusing to overwrite existing file %q", configPath)
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("failed to check %q: %w", configPath, err)
			}

			cfg := service.NewDefaultConfig(serviceName, resolvedServerName)
			if resolvedServerName != "" {
				exists, err := remoteStackExists(cmd, cfg)
				if err != nil {
					return err
				}
				if exists {
					confirmed, err := confirmInitOverwrite(cmd, cfg.Name, resolvedServerName)
					if err != nil {
						return err
					}
					if !confirmed {
						return fmt.Errorf("aborted")
					}
				}
			}

			if err := os.WriteFile(configPath, []byte(renderInitConfig(cfg)), 0o644); err != nil {
				return fmt.Errorf("failed to write %q: %w", configPath, err)
			}

			composePath := cfg.DriverDockerSwarm.Stacks[0].ComposeFile
			if _, err := os.Stat(composePath); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "left existing compose file untouched at %s\n", composePath)
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("failed to check %q: %w", composePath, err)
			} else if err := os.WriteFile(composePath, []byte(renderExampleCompose(cfg)), 0o644); err != nil {
				return fmt.Errorf("failed to write %q: %w", composePath, err)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "created example compose file at %s\n", composePath)
			}

			if _, err := service.ReadConfigFile(configPath); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "created service config at %s\n", configPath)
			return nil
		},
	}

	initCmd.Flags().StringVar(&serverName, "server", "", "Default server name for this service")

	return initCmd
}

func init() {
	rootCmd.AddCommand(newInitCommand())
}

func remoteStackExists(cmd *cobra.Command, cfg service.Config) (bool, error) {
	srv, err := servercmd.ResolveServer(cfg.Server)
	if err != nil {
		return false, err
	}

	target := gcissh.Target{
		User:           srv.User,
		Host:           srv.Host,
		PrivateKeyPath: srv.PrivateKey,
		Timeout:        deployConnectTimeout,
	}
	runner := newSSHRemoteRunner(target)
	result, err := runner.Run(cmd.Context(), "docker stack ls --format '{{.Name}}'")
	if err != nil {
		return false, fmt.Errorf("failed to check remote stacks on %q: %w", srv.Name, err)
	}

	existing := map[string]struct{}{}
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		existing[name] = struct{}{}
	}
	for _, stack := range cfg.DriverDockerSwarm.Stacks {
		if _, ok := existing[stack.Name]; ok {
			return true, nil
		}
	}
	return false, nil
}

func confirmInitOverwrite(cmd *cobra.Command, serviceName, serverName string) (bool, error) {
	fmt.Fprintf(
		cmd.OutOrStdout(),
		"Stack %q already exists on server %q. Create init files anyway? [y/N]: ",
		serviceName,
		serverName,
	)
	reader := bufio.NewReader(cmd.InOrStdin())
	input, err := reader.ReadString('\n')
	if err != nil && err.Error() != "EOF" {
		return false, fmt.Errorf("failed to read confirmation input: %w", err)
	}

	answer := strings.ToLower(strings.TrimSpace(input))
	return answer == "y" || answer == "yes", nil
}

func renderInitConfig(cfg service.Config) string {
	serverLine := ""
	if cfg.Server != "" {
		serverLine = fmt.Sprintf("server = %q\n", cfg.Server)
	}
	logServices := cfg.DriverDockerSwarm.LogServices
	if len(logServices) == 0 {
		logServices = []string{"app"}
	}
	pruneContainersAfter := cfg.DriverDockerSwarm.ResolvedPruneContainersAfterLiteral()

	return fmt.Sprintf(`# Service identity
name = %q
%s
# Optional local build command (multiline strings are supported with ''' ... ''')
build_local = %q

# Optional remote build command executed after sync in the remote service directory
# build_remote = """
# docker build -t 127.0.0.1:41114/my_service .
# docker push 127.0.0.1:41114/my_service
# """

# Optional: local TCP forwards active during build (SSH -L style)
# build_forwards = [
#   "127.0.0.1:5433:127.0.0.1:5432",
#   "0.0.0.0:6380:127.0.0.1:6379",
# ]
#
# Optional extra paths (relative to this file's directory) to sync.
# Compose files referenced by driver_docker_swarm.stacks[].compose_file
# are always synced automatically.
# sync_paths = ["./ops", "./scripts"]

# Exclude patterns for sync (glob + path segment matching)
exclude_patterns = [".git", "node_modules", "__pycache__", "*.pyc"]

[driver_docker_swarm]
# Shared app network. "auto" => gci_net_<app_name>
app_network = "auto"

# Optional: stack to target for gci logs (defaults to last stack below)
# log_stack = "my_service_app"

# Services included in gci logs by default
log_services = [%s]

[[driver_docker_swarm.stacks]]
name = %q
compose_file = %q
mode = "services"

# Prune unused images after successful deploy
prune_images = %t

# Remove stopped containers for this service once they are older than the duration below
# Set to "none" to disable
prune_containers_after = %q
`, cfg.Name, serverLine, cfg.BuildLocal, quotedCSV(logServices), cfg.DriverDockerSwarm.Stacks[0].Name, cfg.DriverDockerSwarm.Stacks[0].ComposeFile, cfg.DriverDockerSwarm.PruneImagesEnabled(), pruneContainersAfter)
}

func quotedCSV(values []string) string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, fmt.Sprintf("%q", value))
	}
	return strings.Join(out, ", ")
}

func renderExampleCompose(cfg service.Config) string {
	stack := cfg.Name
	return fmt.Sprintf(`version: "3.9"

services:
  app:
    image: your-registry/%s-app:latest
    networks:
      - app_net
    deploy:
      replicas: 2
      update_config:
        parallelism: 1
        delay: 5s
        order: start-first
        failure_action: rollback
      rollback_config:
        parallelism: 1
        delay: 5s
        order: stop-first
      restart_policy:
        condition: on-failure
      healthcheck:
        test: ["CMD-SHELL", "wget -qO- http://localhost:8080/health || exit 1"]
        interval: 10s
        timeout: 3s
        retries: 5

  nginx:
    image: your-registry/%s-nginx:latest
    ports:
      - "80:80"
      - "443:443"
    networks:
      - app_net
    deploy:
      replicas: 2
      update_config:
        parallelism: 1
        delay: 5s
        order: start-first
        failure_action: rollback
      rollback_config:
        parallelism: 1
        delay: 5s
      restart_policy:
        condition: on-failure

  migrate:
    image: your-registry/%s-app:latest
    command: ["./bin/migrate"]
    networks:
      - app_net
    deploy:
      replicas: 1
      restart_policy:
        condition: none

networks:
  app_net:
    external: true
    name: "${GCI_APP_NETWORK}"
`, stack, stack, stack)
}
