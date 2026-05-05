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

const (
	dockerSwarmStackModeServices = "services"
	dockerSwarmStackModeJob      = "job"
)

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
	if len(cfg.Stacks) == 0 {
		return fmt.Errorf("driver_docker_swarm.stacks must contain at least one stack")
	}

	names := map[string]struct{}{}
	for i, stack := range cfg.Stacks {
		prefix := fmt.Sprintf("driver_docker_swarm.stacks[%d]", i)
		if strings.TrimSpace(stack.Name) == "" {
			return fmt.Errorf("%s.name is required", prefix)
		}
		if strings.TrimSpace(stack.ComposeFile) == "" {
			return fmt.Errorf("%s.compose_file is required", prefix)
		}
		if _, seen := names[stack.Name]; seen {
			return fmt.Errorf("duplicate stack name %q", stack.Name)
		}
		names[stack.Name] = struct{}{}

		mode := dockerSwarmStackMode(stack)
		if mode != dockerSwarmStackModeServices && mode != dockerSwarmStackModeJob {
			return fmt.Errorf("%s.mode must be %q or %q", prefix, dockerSwarmStackModeServices, dockerSwarmStackModeJob)
		}
		if stack.WaitTimeoutSeconds < 0 {
			return fmt.Errorf("%s.wait_timeout_seconds must be >= 0", prefix)
		}
	}

	for i, item := range cfg.LogServices {
		prefix := fmt.Sprintf("driver_docker_swarm.log_services[%d]", i)
		if strings.TrimSpace(item.Name) == "" {
			return fmt.Errorf("%s.name is required", prefix)
		}
		if strings.TrimSpace(item.Stack) != "" {
			if _, ok := names[item.Stack]; !ok {
				return fmt.Errorf("%s.stack %q is not present in driver_docker_swarm.stacks", prefix, item.Stack)
			}
		}
	}

	if _, _, err := parsePruneContainersAfter(cfg.PruneContainersAfter); err != nil {
		return err
	}

	return nil
}

func (dockerSwarmDriver) Deploy(ctx context.Context, runner RemoteRunner, cfg Config, remoteServiceDir string, stdout, stderr io.Writer) error {
	swarmCfg := *cfg.DriverDockerSwarm
	appNetwork := swarmCfg.ResolvedAppNetwork(cfg.Name)

	if err := ensureDockerSwarmNetwork(ctx, runner, appNetwork, stdout, stderr); err != nil {
		return err
	}

	for _, stack := range swarmCfg.Stacks {
		mode := dockerSwarmStackMode(stack)
		fmt.Fprintf(stdout, "deploying stack %q (mode=%s)\n", stack.Name, mode)

		deployCommand := fmt.Sprintf(
			"cd %s && GCI_APP_NETWORK=%s docker stack deploy --prune -c %s %s",
			shellQuote(remoteServiceDir),
			shellQuote(appNetwork),
			shellQuote(stack.ComposeFile),
			shellQuote(stack.Name),
		)
		if err := runner.Stream(ctx, deployCommand, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "deploy command failed for stack %q, collecting recent service logs...\n", stack.Name)
			diagCtx, cancel := diagnosticContext()
			defer cancel()
			dumpDockerSwarmStackLogs(diagCtx, runner, stack.Name, 50, stderr)
			return fmt.Errorf("failed to deploy docker stack %q: %w", stack.Name, err)
		}

		if mode == dockerSwarmStackModeServices && swarmCfg.ForceRestartServicesEnabled() {
			if err := forceRestartDockerSwarmStackServices(ctx, runner, stack.Name, stdout, stderr); err != nil {
				fmt.Fprintf(stderr, "forced service restart failed for stack %q, collecting recent service logs...\n", stack.Name)
				diagCtx, cancel := diagnosticContext()
				defer cancel()
				dumpDockerSwarmStackLogs(diagCtx, runner, stack.Name, 50, stderr)
				return err
			}
		}

		if err := waitForDockerSwarmStack(ctx, runner, stack, stdout); err != nil {
			fmt.Fprintf(stderr, "stack %q failed to reach stable state, collecting recent service logs...\n", stack.Name)
			diagCtx, cancel := diagnosticContext()
			defer cancel()
			dumpDockerSwarmStackLogs(diagCtx, runner, stack.Name, 50, stderr)
			return err
		}
	}

	if swarmCfg.PruneImagesEnabled() {
		fmt.Fprintln(stdout, "pruning unused docker images...")
		if err := runner.Stream(ctx, "docker image prune -a -f", stdout, stderr); err != nil {
			return fmt.Errorf("failed to prune docker images: %w", err)
		}
	}

	if cutoff, enabled := swarmCfg.PruneContainersAfterDuration(); enabled && cutoff > 0 {
		for _, stack := range swarmCfg.Stacks {
			if err := pruneDockerSwarmStackContainers(ctx, runner, stack.Name, cutoff, stdout, stderr); err != nil {
				return err
			}
		}
	}

	return nil
}

func (dockerSwarmDriver) Remove(ctx context.Context, runner RemoteRunner, cfg Config, remoteServiceDir string) error {
	swarmCfg := *cfg.DriverDockerSwarm
	for i := len(swarmCfg.Stacks) - 1; i >= 0; i-- {
		stackName := swarmCfg.Stacks[i].Name

		removeCommand := fmt.Sprintf("docker stack rm %s || true", shellQuote(stackName))
		if _, err := runner.Run(ctx, removeCommand); err != nil {
			return fmt.Errorf("failed to remove docker stack %q: %w", stackName, err)
		}

		waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		ticker := time.NewTicker(2 * time.Second)
		for {
			exists, err := dockerSwarmStackExists(waitCtx, runner, stackName)
			if err != nil {
				ticker.Stop()
				cancel()
				return err
			}
			if !exists {
				break
			}

			select {
			case <-waitCtx.Done():
				ticker.Stop()
				cancel()
				return fmt.Errorf("timed out waiting for docker stack %q to be removed", stackName)
			case <-ticker.C:
			}
		}
		ticker.Stop()
		cancel()
	}

	if swarmCfg.AutoManagesAppNetwork() {
		networkName := swarmCfg.ResolvedAppNetwork(cfg.Name)
		removeNetworkCommand := fmt.Sprintf("docker network rm %s || true", shellQuote(networkName))
		if _, err := runner.Run(ctx, removeNetworkCommand); err != nil {
			return fmt.Errorf("failed to remove app network %q: %w", networkName, err)
		}
	}

	deleteDirCommand := fmt.Sprintf("rm -rf %s", shellQuote(remoteServiceDir))
	if _, err := runner.Run(ctx, deleteDirCommand); err != nil {
		return fmt.Errorf("failed to remove remote service directory %q: %w", remoteServiceDir, err)
	}

	return nil
}

func (dockerSwarmDriver) Logs(ctx context.Context, runner RemoteRunner, cfg Config, lines int, stdout, stderr io.Writer) error {
	swarmCfg := *cfg.DriverDockerSwarm
	services, err := selectedLogServices(ctx, runner, swarmCfg)
	if err != nil {
		return err
	}

	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	for i, service := range services {
		serviceRef := fmt.Sprintf("%s_%s", service.Stack, service.Name)
		command := fmt.Sprintf("docker service logs --tail %d %s", lines, shellQuote(serviceRef))

		if i > 0 {
			fmt.Fprintln(stdout)
		}

		header := fmt.Sprintf("===== %s.%s =====\n", service.Stack, service.Name)
		fmt.Fprint(stdout, header)

		errWriter := &logHeaderOnceWriter{
			header: header,
			w:      stderr,
		}

		if err := runner.Stream(ctx, command, stdout, errWriter); err != nil {
			return fmt.Errorf("failed to fetch docker swarm logs for service %q in stack %q: %w", service.Name, service.Stack, err)
		}
	}

	return nil
}

func (dockerSwarmDriver) LogsStream(ctx context.Context, runner RemoteRunner, cfg Config, lines int, stdout, stderr io.Writer) error {
	swarmCfg := *cfg.DriverDockerSwarm
	services, err := selectedLogServices(ctx, runner, swarmCfg)
	if err != nil {
		return err
	}
	if len(services) != 1 {
		return fmt.Errorf("streaming logs requires exactly one service; set driver_docker_swarm.log_services to one entry or pass --service")
	}

	serviceRef := fmt.Sprintf("%s_%s", services[0].Stack, services[0].Name)
	command := fmt.Sprintf("docker service logs --tail %d --follow %s", lines, shellQuote(serviceRef))
	if err := runner.Stream(ctx, command, stdout, stderr); err != nil {
		return fmt.Errorf("failed to stream docker swarm logs: %w", err)
	}
	return nil
}

func (dockerSwarmDriver) Status(ctx context.Context, runner RemoteRunner, cfg Config) (CommandResult, error) {
	swarmCfg := *cfg.DriverDockerSwarm
	output := strings.Builder{}

	for i, stack := range swarmCfg.Stacks {
		command := fmt.Sprintf(
			"docker stack services %s --format '  {{.Name}}  replicas={{.Replicas}}  image={{.Image}}  ports={{.Ports}}'",
			shellQuote(stack.Name),
		)

		result, err := runner.Run(ctx, command)
		if err != nil {
			return CommandResult{}, fmt.Errorf("failed to fetch docker swarm status for stack %q: %w", stack.Name, err)
		}

		if i > 0 {
			output.WriteString("\n")
		}
		output.WriteString(fmt.Sprintf("STACK %s\n", stack.Name))
		if strings.TrimSpace(result.Stdout) == "" {
			output.WriteString("  (no services)\n")
		} else {
			output.WriteString(result.Stdout)
			if !strings.HasSuffix(result.Stdout, "\n") {
				output.WriteString("\n")
			}
		}
	}

	return CommandResult{Stdout: output.String()}, nil
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

func dockerSwarmStackStable(ctx context.Context, runner RemoteRunner, stackName string) (bool, string, error) {
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

func dockerSwarmJobStackCompleted(ctx context.Context, runner RemoteRunner, stackName string) (bool, string, error) {
	servicesCommand := fmt.Sprintf("docker stack services %s --format '{{.Name}}'", shellQuote(stackName))
	servicesResult, err := runner.Run(ctx, servicesCommand)
	if err != nil {
		return false, "", commandError("failed to list docker stack services", err, servicesResult)
	}

	serviceLines := strings.Split(strings.TrimSpace(servicesResult.Stdout), "\n")
	if len(serviceLines) == 1 && serviceLines[0] == "" {
		return false, "no services reported", nil
	}

	pending := make([]string, 0)
	for _, serviceName := range serviceLines {
		serviceName = strings.TrimSpace(serviceName)
		if serviceName == "" {
			continue
		}

		stateCommand := fmt.Sprintf("docker service ps --no-trunc --format '{{.CurrentState}}|{{.Error}}|{{.DesiredState}}' %s | head -n 1", shellQuote(serviceName))
		stateResult, stateErr := runner.Run(ctx, stateCommand)
		if stateErr != nil {
			return false, "", commandError(fmt.Sprintf("failed to inspect docker service tasks for %q", serviceName), stateErr, stateResult)
		}

		line := strings.TrimSpace(stateResult.Stdout)
		if line == "" {
			pending = append(pending, fmt.Sprintf("%s=no task info", serviceName))
			continue
		}

		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			pending = append(pending, fmt.Sprintf("%s=%s", serviceName, line))
			continue
		}

		currentState := strings.ToLower(strings.TrimSpace(parts[0]))
		errorMessage := strings.TrimSpace(parts[1])
		desiredState := strings.ToLower(strings.TrimSpace(parts[2]))

		if strings.HasPrefix(currentState, "complete") && errorMessage == "" {
			continue
		}
		if strings.Contains(currentState, "failed") || strings.HasPrefix(currentState, "rejected") || (errorMessage != "" && !strings.HasPrefix(currentState, "running")) {
			return false, "", fmt.Errorf("job stack %q failed: %s=%s", stackName, serviceName, line)
		}

		pending = append(pending, fmt.Sprintf("%s=%s (desired=%s)", serviceName, currentState, desiredState))
	}

	if len(pending) > 0 {
		return false, strings.Join(pending, ", "), nil
	}

	return true, "complete", nil
}

func waitForDockerSwarmStack(ctx context.Context, runner RemoteRunner, stack DockerSwarmStack, stdout io.Writer) error {
	waitTimeout := dockerSwarmStackTimeout(stack)
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	mode := dockerSwarmStackMode(stack)
	lastState := ""
	for {
		var stable bool
		var state string
		var err error

		switch mode {
		case dockerSwarmStackModeJob:
			stable, state, err = dockerSwarmJobStackCompleted(waitCtx, runner, stack.Name)
		default:
			stable, state, err = dockerSwarmStackStable(waitCtx, runner, stack.Name)
		}
		if err != nil {
			return err
		}
		lastState = state
		if stable {
			fmt.Fprintf(stdout, "stack %q is stable\n", stack.Name)
			return nil
		}
		fmt.Fprintf(stdout, "waiting for stack %q (%s): %s\n", stack.Name, mode, state)

		select {
		case <-waitCtx.Done():
			if lastState == "" {
				lastState = "no services reported"
			}
			return fmt.Errorf("timed out waiting for docker stack %q to become stable (mode=%s, last state: %s)", stack.Name, mode, lastState)
		case <-ticker.C:
		}
	}
}

func diagnosticContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 45*time.Second)
}

func dumpDockerSwarmStackLogs(ctx context.Context, runner RemoteRunner, stackName string, lines int, out io.Writer) {
	fmt.Fprintf(out, "----- stack %s logs (last %d lines per service) -----\n", stackName, lines)

	servicesCommand := fmt.Sprintf("docker stack services %s --format '{{.Name}}'", shellQuote(stackName))
	servicesResult, err := runner.Run(ctx, servicesCommand)
	if err != nil {
		fmt.Fprintf(out, "failed to list services for stack %q: %v\n", stackName, err)
		if strings.TrimSpace(servicesResult.Stderr) != "" {
			fmt.Fprintf(out, "stderr:\n%s\n", strings.TrimSpace(servicesResult.Stderr))
		}
		if strings.TrimSpace(servicesResult.Stdout) != "" {
			fmt.Fprintf(out, "stdout:\n%s\n", strings.TrimSpace(servicesResult.Stdout))
		}
		return
	}

	services := strings.Split(strings.TrimSpace(servicesResult.Stdout), "\n")
	if len(services) == 1 && strings.TrimSpace(services[0]) == "" {
		fmt.Fprintf(out, "no services found for stack %q\n", stackName)
		return
	}

	for _, serviceName := range services {
		serviceName = strings.TrimSpace(serviceName)
		if serviceName == "" {
			continue
		}
		fmt.Fprintf(out, "===== %s =====\n", serviceName)
		logCommand := fmt.Sprintf("docker service logs --tail %d %s", lines, shellQuote(serviceName))
		logResult, logErr := runner.Run(ctx, logCommand)
		if logErr != nil {
			fmt.Fprintf(out, "failed to fetch logs for %q: %v\n", serviceName, logErr)
		}
		if strings.TrimSpace(logResult.Stdout) != "" {
			fmt.Fprintln(out, strings.TrimSpace(logResult.Stdout))
		}
		if strings.TrimSpace(logResult.Stderr) != "" {
			fmt.Fprintln(out, strings.TrimSpace(logResult.Stderr))
		}
	}
}

func dockerSwarmStackMode(stack DockerSwarmStack) string {
	mode := strings.ToLower(strings.TrimSpace(stack.Mode))
	if mode == "" {
		return dockerSwarmStackModeServices
	}
	return mode
}

func dockerSwarmStackTimeout(stack DockerSwarmStack) time.Duration {
	if stack.WaitTimeoutSeconds > 0 {
		return time.Duration(stack.WaitTimeoutSeconds) * time.Second
	}
	if dockerSwarmStackMode(stack) == dockerSwarmStackModeJob {
		return 10 * time.Minute
	}
	return 5 * time.Minute
}

func ensureDockerSwarmNetwork(ctx context.Context, runner RemoteRunner, networkName string, stdout, stderr io.Writer) error {
	fmt.Fprintf(stdout, "ensuring app network %q exists\n", networkName)
	command := fmt.Sprintf("docker network inspect %s >/dev/null 2>&1 || docker network create --driver overlay --attachable %s", shellQuote(networkName), shellQuote(networkName))
	if err := runner.Stream(ctx, command, stdout, stderr); err != nil {
		return fmt.Errorf("failed to ensure app network %q exists: %w", networkName, err)
	}
	return nil
}

func forceRestartDockerSwarmStackServices(ctx context.Context, runner RemoteRunner, stackName string, stdout, stderr io.Writer) error {
	serviceNames, err := dockerSwarmStackServiceNames(ctx, runner, stackName)
	if err != nil {
		return err
	}
	if len(serviceNames) == 0 {
		fmt.Fprintf(stdout, "stack %q has no services to restart\n", stackName)
		return nil
	}

	fmt.Fprintf(stdout, "force restarting services for stack %q...\n", stackName)
	for _, serviceName := range serviceNames {
		serviceRef := fmt.Sprintf("%s_%s", stackName, serviceName)
		fmt.Fprintf(stdout, "force restarting service %q\n", serviceRef)
		command := fmt.Sprintf("docker service update --force %s", shellQuote(serviceRef))
		if err := runner.Stream(ctx, command, stdout, stderr); err != nil {
			return fmt.Errorf("failed to force restart docker service %q in stack %q: %w", serviceName, stackName, err)
		}
	}

	return nil
}

func pruneDockerSwarmStackContainers(ctx context.Context, runner RemoteRunner, stackName string, olderThan time.Duration, stdout, stderr io.Writer) error {
	if olderThan <= 0 {
		return nil
	}

	dur := dockerDurationLiteral(olderThan)
	fmt.Fprintf(stdout, "pruning stopped containers for stack %q older than %s...\n", stackName, dur)

	labelFilter := fmt.Sprintf("label=com.docker.stack.namespace=%s", stackName)
	untilFilter := fmt.Sprintf("until=%s", dur)
	command := fmt.Sprintf(
		"docker container prune -f --filter %s --filter %s",
		shellQuote(labelFilter),
		shellQuote(untilFilter),
	)
	if err := runner.Stream(ctx, command, stdout, stderr); err != nil {
		return fmt.Errorf("failed to prune containers for stack %q: %w", stackName, err)
	}
	return nil
}

func dockerDurationLiteral(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	d = d.Round(time.Second)
	if d%(time.Hour) == 0 {
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	}
	if d%(time.Minute) == 0 {
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	}
	return d.String()
}

func defaultDockerSwarmLogStack(cfg DockerSwarmConfig) string {
	return cfg.Stacks[len(cfg.Stacks)-1].Name
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

type logHeaderOnceWriter struct {
	header string
	w      io.Writer
	wrote  bool
}

func (l *logHeaderOnceWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if l.w == nil {
		l.w = io.Discard
	}
	if !l.wrote {
		if _, err := fmt.Fprint(l.w, l.header); err != nil {
			return 0, err
		}
		l.wrote = true
	}
	return l.w.Write(p)
}

func selectedLogServices(ctx context.Context, runner RemoteRunner, cfg DockerSwarmConfig) ([]DockerSwarmLogService, error) {
	if len(cfg.LogServices) > 0 {
		out := make([]DockerSwarmLogService, 0, len(cfg.LogServices))
		seen := map[string]struct{}{}
		defaultStack := defaultDockerSwarmLogStack(cfg)
		for _, item := range cfg.LogServices {
			name := strings.TrimSpace(item.Name)
			if name == "" {
				continue
			}
			stack := strings.TrimSpace(item.Stack)
			if stack == "" {
				stack = defaultStack
			}
			key := stack + "\x00" + name
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, DockerSwarmLogService{Stack: stack, Name: name})
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	stack := defaultDockerSwarmLogStack(cfg)
	serviceNames, err := dockerSwarmStackServiceNames(ctx, runner, stack)
	if err != nil {
		return nil, err
	}
	out := make([]DockerSwarmLogService, 0, len(serviceNames))
	for _, name := range serviceNames {
		out = append(out, DockerSwarmLogService{Stack: stack, Name: name})
	}
	return out, nil
}

func dockerSwarmStackServiceNames(ctx context.Context, runner RemoteRunner, stackName string) ([]string, error) {
	command := fmt.Sprintf("docker stack services %s --format '{{.Name}}'", shellQuote(stackName))
	result, err := runner.Run(ctx, command)
	if err != nil {
		return nil, commandError(fmt.Sprintf("failed to list docker stack services for stack %q", stackName), err, result)
	}

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	out := make([]string, 0, len(lines))
	seen := map[string]struct{}{}
	prefix := stackName + "_"
	for _, line := range lines {
		fullName := strings.TrimSpace(line)
		if fullName == "" {
			continue
		}
		name := strings.TrimPrefix(fullName, prefix)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no services found for stack %q", stackName)
	}
	return out, nil
}
