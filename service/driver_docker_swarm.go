package service

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type dockerSwarmDriver struct{}

func (dockerSwarmDriver) Name() string {
	return "docker_swarm"
}

func (dockerSwarmDriver) Validate(cfg Config) error {
	if cfg.DriverDockerSwarm == nil {
		return fmt.Errorf("driver_docker_swarm is required")
	}

	return validateDockerSwarmConfig(*cfg.DriverDockerSwarm)
}

func validateDockerSwarmConfig(cfg DockerSwarmConfig) error {
	if cfg.StackName == "" {
		return fmt.Errorf("driver_docker_swarm.stack_name is required")
	}
	if cfg.ComposeFile == "" {
		return fmt.Errorf("driver_docker_swarm.compose_file is required")
	}
	return nil
}

func (dockerSwarmDriver) Deploy(ctx context.Context, runner RemoteRunner, cfg Config, remoteServiceDir string, stdout, stderr io.Writer) error {
	stackName := cfg.DriverDockerSwarm.StackName
	composeFile := cfg.DriverDockerSwarm.ComposeFile

	deployCommand := fmt.Sprintf(
		"cd %s && docker stack deploy -c %s %s",
		shellQuote(remoteServiceDir),
		shellQuote(composeFile),
		shellQuote(stackName),
	)
	if err := runner.Stream(ctx, deployCommand, stdout, stderr); err != nil {
		return fmt.Errorf("failed to deploy docker stack: %w", err)
	}

	if cfg.DriverDockerSwarm.MigrationService != "" {
		migrationServiceRef := fmt.Sprintf("%s_%s", stackName, cfg.DriverDockerSwarm.MigrationService)
		migrationCommand := fmt.Sprintf("docker service update --force --detach=false %s", shellQuote(migrationServiceRef))
		err := runner.Stream(ctx, migrationCommand, stdout, stderr)
		if err != nil {
			completed, details, verifyErr := dockerSwarmMigrationCompleted(ctx, runner, migrationServiceRef)
			if verifyErr == nil && completed {
				fmt.Fprintf(stdout, "migration service %s completed successfully\n", migrationServiceRef)
			} else if cfg.DriverDockerSwarm.MigrationStrict {
				if verifyErr != nil {
					return fmt.Errorf("failed to trigger migration service: %w (and failed to verify migration completion: %v)", err, verifyErr)
				}
				return fmt.Errorf("failed to trigger migration service: %w (latest task: %s)", err, details)
			} else {
				if verifyErr != nil {
					fmt.Fprintf(
						stderr,
						"warning: migration trigger failed but continuing (migration_strict=false): %v (and failed to verify migration completion: %v)\n",
						err,
						verifyErr,
					)
				} else {
					fmt.Fprintf(
						stderr,
						"warning: migration trigger failed but continuing (migration_strict=false): %v (latest task: %s)\n",
						err,
						details,
					)
				}
			}
		}
	}

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	lastState := ""
	ignoredServices := map[string]struct{}{}
	if cfg.DriverDockerSwarm.MigrationService != "" {
		ignoredServices[fmt.Sprintf("%s_%s", stackName, cfg.DriverDockerSwarm.MigrationService)] = struct{}{}
	}
	for {
		stable, state, err := dockerSwarmStackStable(waitCtx, runner, stackName, ignoredServices)
		if err != nil {
			return err
		}
		lastState = state
		if stable {
			fmt.Fprintln(stdout, "stack is stable")
			return nil
		}
		fmt.Fprintf(stdout, "waiting for stack stability: %s\n", state)

		select {
		case <-waitCtx.Done():
			if lastState == "" {
				lastState = "no services reported"
			}
			return fmt.Errorf("timed out waiting for docker stack %q to become stable (last state: %s)", stackName, lastState)
		case <-ticker.C:
		}
	}
}

func (dockerSwarmDriver) Remove(ctx context.Context, runner RemoteRunner, cfg Config, remoteServiceDir string) error {
	stackName := cfg.DriverDockerSwarm.StackName

	removeCommand := fmt.Sprintf("docker stack rm %s || true", shellQuote(stackName))
	if _, err := runner.Run(ctx, removeCommand); err != nil {
		return fmt.Errorf("failed to remove docker stack: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		exists, err := dockerSwarmStackExists(waitCtx, runner, stackName)
		if err != nil {
			return err
		}
		if !exists {
			break
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for docker stack %q to be removed", stackName)
		case <-ticker.C:
		}
	}

	deleteDirCommand := fmt.Sprintf("rm -rf %s", shellQuote(remoteServiceDir))
	if _, err := runner.Run(ctx, deleteDirCommand); err != nil {
		return fmt.Errorf("failed to remove remote service directory %q: %w", remoteServiceDir, err)
	}

	return nil
}

func (dockerSwarmDriver) Logs(ctx context.Context, runner RemoteRunner, cfg Config, lines int) (CommandResult, error) {
	services := selectedLogServices(*cfg.DriverDockerSwarm)
	output := strings.Builder{}
	errOutput := strings.Builder{}

	for i, serviceName := range services {
		serviceRef := fmt.Sprintf("%s_%s", cfg.DriverDockerSwarm.StackName, serviceName)
		command := fmt.Sprintf("docker service logs --tail %d %s", lines, shellQuote(serviceRef))
		result, err := runner.Run(ctx, command)
		if err != nil {
			return CommandResult{}, commandError(fmt.Sprintf("failed to fetch docker swarm logs for service %q", serviceName), err, result)
		}

		if i > 0 {
			output.WriteString("\n")
		}
		output.WriteString(fmt.Sprintf("===== %s =====\n", serviceName))
		output.WriteString(result.Stdout)
		if strings.TrimSpace(result.Stderr) != "" {
			errOutput.WriteString(fmt.Sprintf("===== %s =====\n", serviceName))
			errOutput.WriteString(result.Stderr)
			if !strings.HasSuffix(result.Stderr, "\n") {
				errOutput.WriteString("\n")
			}
		}
	}

	return CommandResult{Stdout: output.String(), Stderr: errOutput.String()}, nil
}

func (dockerSwarmDriver) LogsStream(ctx context.Context, runner RemoteRunner, cfg Config, lines int, stdout, stderr io.Writer) error {
	services := selectedLogServices(*cfg.DriverDockerSwarm)
	if len(services) != 1 {
		return fmt.Errorf("streaming logs requires exactly one service; set driver_docker_swarm.log_services to one entry or pass --service")
	}

	serviceRef := fmt.Sprintf("%s_%s", cfg.DriverDockerSwarm.StackName, services[0])
	command := fmt.Sprintf("docker service logs --tail %d --follow %s", lines, shellQuote(serviceRef))
	if err := runner.Stream(ctx, command, stdout, stderr); err != nil {
		return fmt.Errorf("failed to stream docker swarm logs: %w", err)
	}
	return nil
}

func (dockerSwarmDriver) Status(ctx context.Context, runner RemoteRunner, cfg Config) (CommandResult, error) {
	stackName := cfg.DriverDockerSwarm.StackName
	command := fmt.Sprintf(
		"set -euo pipefail\n"+
			"echo 'STACK SERVICES'\n"+
			"docker stack services %s --format '  {{.Name}}  replicas={{.Replicas}}  image={{.Image}}  ports={{.Ports}}'\n",
		shellQuote(stackName),
	)

	result, err := runner.Run(ctx, command)
	if err != nil {
		return CommandResult{}, fmt.Errorf("failed to fetch docker swarm status: %w", err)
	}
	return result, nil
}

func (dockerSwarmDriver) Doctor(ctx context.Context, runner RemoteRunner, _ Config) error {
	result, err := runner.Run(ctx, "docker info --format '{{.Swarm.LocalNodeState}}'")
	if err != nil {
		return fmt.Errorf("failed to query docker swarm state: %w", err)
	}

	state := strings.TrimSpace(result.Stdout)
	if state == "" {
		return fmt.Errorf("docker swarm state is empty")
	}
	if state == "inactive" || state == "error" {
		return fmt.Errorf("docker swarm is not ready (state=%s)", state)
	}
	return nil
}

func dockerSwarmStackExists(ctx context.Context, runner RemoteRunner, stackName string) (bool, error) {
	result, err := runner.Run(ctx, "docker stack ls --format '{{.Name}}'")
	if err != nil {
		return false, fmt.Errorf("failed to query docker stacks: %w", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		if strings.TrimSpace(line) == stackName {
			return true, nil
		}
	}
	return false, nil
}

func dockerSwarmMigrationCompleted(ctx context.Context, runner RemoteRunner, migrationServiceRef string) (bool, string, error) {
	command := fmt.Sprintf("docker service ps --no-trunc --format '{{.CurrentState}}|{{.Error}}|{{.DesiredState}}' %s | head -n 1", shellQuote(migrationServiceRef))
	result, err := runner.Run(ctx, command)
	if err != nil {
		return false, "", commandError("failed to inspect migration service tasks", err, result)
	}

	line := strings.TrimSpace(result.Stdout)
	if line == "" {
		return false, "no task info", nil
	}

	parts := strings.SplitN(line, "|", 3)
	if len(parts) != 3 {
		return false, line, nil
	}

	currentState := strings.ToLower(strings.TrimSpace(parts[0]))
	errorMessage := strings.TrimSpace(parts[1])
	desiredState := strings.ToLower(strings.TrimSpace(parts[2]))

	isComplete := strings.HasPrefix(currentState, "complete")
	if desiredState == "shutdown" && isComplete && errorMessage == "" {
		return true, line, nil
	}

	return false, line, nil
}

func dockerSwarmStackStable(ctx context.Context, runner RemoteRunner, stackName string, ignoredServices map[string]struct{}) (bool, string, error) {
	command := fmt.Sprintf("docker stack services %s --format '{{.Name}} {{.Replicas}}'", shellQuote(stackName))
	result, err := runner.Run(ctx, command)
	if err != nil {
		return false, "", commandError("failed to read docker stack service status", err, result)
	}

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return false, "no services reported", nil
	}

	unstable := make([]string, 0)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			unstable = append(unstable, line)
			continue
		}

		replicas := parts[len(parts)-1]
		serviceName := strings.Join(parts[:len(parts)-1], " ")
		if _, ignored := ignoredServices[serviceName]; ignored {
			continue
		}
		desired, running, ok := parseReplicas(replicas)
		if !ok || running != desired {
			unstable = append(unstable, fmt.Sprintf("%s=%s", serviceName, replicas))
		}
	}

	if len(unstable) > 0 {
		return false, strings.Join(unstable, ", "), nil
	}

	return true, "stable", nil
}

func parseReplicas(value string) (desired int, running int, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(value), "/", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}

	left, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	right, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}

	return right, left, true
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func commandError(prefix string, err error, result CommandResult) error {
	stderr := strings.TrimSpace(result.Stderr)
	stdout := strings.TrimSpace(result.Stdout)
	if stderr == "" && stdout == "" {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if stderr != "" && stdout != "" {
		return fmt.Errorf("%s: %w\nstderr:\n%s\nstdout:\n%s", prefix, err, stderr, stdout)
	}
	if stderr != "" {
		return fmt.Errorf("%s: %w\nstderr:\n%s", prefix, err, stderr)
	}
	return fmt.Errorf("%s: %w\nstdout:\n%s", prefix, err, stdout)
}

func selectedLogServices(cfg DockerSwarmConfig) []string {
	if len(cfg.LogServices) > 0 {
		out := make([]string, 0, len(cfg.LogServices))
		seen := map[string]struct{}{}
		for _, item := range cfg.LogServices {
			name := strings.TrimSpace(item)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{"app"}
}
