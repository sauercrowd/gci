package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	servercmd "github.com/sauercrowd/gci/cmd/server"
	"github.com/sauercrowd/gci/service"
	gcissh "github.com/sauercrowd/gci/ssh"
	"github.com/spf13/cobra"
)

const deployConnectTimeout = 30 * time.Second

var deployCmd = &cobra.Command{
	Use:          "deploy [config_file]",
	Short:        "Deploy the current project",
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

		serverName, err := cmd.Flags().GetString("server")
		if err != nil {
			return err
		}
		if serverName == "" {
			serverName = cfg.Server
		}

		srv, err := servercmd.ResolveServer(serverName)
		if err != nil {
			return err
		}

		baseDir := filepath.Dir(configPath)
		target := gcissh.Target{
			User:           srv.User,
			Host:           srv.Host,
			PrivateKeyPath: srv.PrivateKey,
			Timeout:        deployConnectTimeout,
		}
		renderCtx := templateContext{
			ServiceName: cfg.Name,
		}
		if cfg.DriverDockerSwarm != nil {
			renderCtx.AppNetwork = cfg.DriverDockerSwarm.ResolvedAppNetwork(cfg.Name)
		}
		localBuild := strings.TrimSpace(cfg.BuildLocal)
		remoteBuild := strings.TrimSpace(cfg.BuildRemote)
		if localBuild == "" && remoteBuild == "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: neither build_local nor build_remote is configured; skipping build steps")
		}
		if localBuild != "" {
			renderedLocalBuild, err := renderTemplateString("build_local", localBuild, baseDir, renderCtx)
			if err != nil {
				return fmt.Errorf("failed to render build_local template: %w", err)
			}
			if err := runLocalBuild(cmd.Context(), cmd, baseDir, renderedLocalBuild, target, cfg.BuildForwards); err != nil {
				return err
			}
		}

		remoteServiceDir := path.Join(srv.ServiceDir, cfg.Name)
		syncPaths := cfg.ResolvedSyncPaths()
		fmt.Fprintf(cmd.OutOrStdout(), "syncing files to %s:%s...\n", srv.Host, remoteServiceDir)
		if err := gcissh.SyncPaths(cmd.Context(), target, baseDir, syncPaths, cfg.ExcludePatterns, remoteServiceDir); err != nil {
			return err
		}

		if remoteBuild != "" {
			renderedRemoteBuild, err := renderTemplateString("build_remote", remoteBuild, baseDir, renderCtx)
			if err != nil {
				return fmt.Errorf("failed to render build_remote template: %w", err)
			}
			if err := runRemoteBuild(cmd.Context(), cmd, renderedRemoteBuild, target, remoteServiceDir); err != nil {
				return err
			}
		}
		if err := syncRenderedComposeFiles(cmd.Context(), cmd, cfg, target, baseDir, remoteServiceDir, renderCtx); err != nil {
			return err
		}

		driver, err := service.ResolveDriver(cfg)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "running %s deploy actions...\n", driver.Name())
		if err := driver.Deploy(cmd.Context(), newSSHRemoteRunner(target), cfg, remoteServiceDir, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "deployed %q to server %q\n", cfg.Name, srv.Name)
		return nil
	},
}

func init() {
	deployCmd.Flags().String("server", "", "Override server from service config")
	rootCmd.AddCommand(deployCmd)
}

func runLocalBuild(ctx context.Context, cmd *cobra.Command, baseDir, buildCommand string, target gcissh.Target, sshForwards []string) error {
	fmt.Fprintln(cmd.OutOrStdout(), "running local build...")
	if len(sshForwards) > 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "starting SSH forwards for build...")
		for _, forward := range sshForwards {
			fmt.Fprintf(cmd.OutOrStdout(), "  -L %s\n", forward)
		}
		session, err := gcissh.StartLocalForwards(ctx, target, sshForwards)
		if err != nil {
			return fmt.Errorf("failed to start build SSH forwards: %w", err)
		}
		defer session.Close()
	}

	scriptFile, err := os.CreateTemp("", "gci-build-*.sh")
	if err != nil {
		return fmt.Errorf("failed to create temp build script: %w", err)
	}
	scriptPath := scriptFile.Name()
	defer os.Remove(scriptPath)

	// Ensure multi-line build scripts stop at the first failing command.
	localScript := "set -e\n" + buildCommand + "\n"
	if _, err := scriptFile.WriteString(localScript); err != nil {
		_ = scriptFile.Close()
		return fmt.Errorf("failed to write temp build script: %w", err)
	}
	if err := scriptFile.Close(); err != nil {
		return fmt.Errorf("failed to finalize temp build script: %w", err)
	}

	build := exec.Command("bash", scriptPath)
	build.Dir = baseDir
	build.Stdout = cmd.OutOrStdout()
	build.Stderr = cmd.ErrOrStderr()
	build.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := build.Start(); err != nil {
		return fmt.Errorf("failed to start build command: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- build.Wait()
	}()

	var runErr error
	select {
	case runErr = <-waitCh:
	case <-ctx.Done():
		if build.Process != nil {
			_ = syscall.Kill(-build.Process.Pid, syscall.SIGTERM)
			select {
			case <-waitCh:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-build.Process.Pid, syscall.SIGKILL)
				<-waitCh
			}
		}
		return fmt.Errorf("build command canceled: %w", ctx.Err())
	}

	if runErr != nil {
		return fmt.Errorf(
			"local build command failed (check quoting in build_local): %w\nbuild_local:\n%s",
			runErr,
			numberedScript(buildCommand),
		)
	}

	return nil
}

func runRemoteBuild(ctx context.Context, cmd *cobra.Command, buildCommand string, target gcissh.Target, remoteServiceDir string) error {
	fmt.Fprintln(cmd.OutOrStdout(), "running remote build...")
	// Ensure multi-line build scripts stop at the first failing command.
	script := fmt.Sprintf("set -e\ncd %s\n%s\n", shellQuote(remoteServiceDir), buildCommand)
	if err := gcissh.RunCommandStream(ctx, target, script, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
		return fmt.Errorf(
			"remote build command failed (check quoting in build_remote): %w\nbuild_remote:\n%s",
			err,
			numberedScript(buildCommand),
		)
	}
	return nil
}

func numberedScript(script string) string {
	lines := strings.Split(script, "\n")
	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, "%2d | %s\n", i+1, line)
	}
	return b.String()
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func resolveServiceConfigPath(explicitPath string) (string, error) {
	if explicitPath != "" {
		return filepath.Abs(explicitPath)
	}

	defaultPath := "gci.toml"
	_, err := os.Stat(defaultPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no service config file found at %q; pass one explicitly", defaultPath)
		}
		return "", fmt.Errorf("failed to stat %q: %w", defaultPath, err)
	}
	return filepath.Abs(defaultPath)
}

func syncRenderedComposeFiles(
	ctx context.Context,
	cmd *cobra.Command,
	cfg service.Config,
	target gcissh.Target,
	baseDir string,
	remoteServiceDir string,
	renderCtx templateContext,
) error {
	if cfg.DriverDockerSwarm == nil {
		return nil
	}

	composePaths := make([]string, 0, len(cfg.DriverDockerSwarm.Stacks))
	seen := map[string]struct{}{}
	for _, stack := range cfg.DriverDockerSwarm.Stacks {
		composePath := strings.TrimSpace(stack.ComposeFile)
		if composePath == "" {
			continue
		}
		if filepath.IsAbs(composePath) {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping automatic render for absolute compose path %q\n", composePath)
			continue
		}
		if _, ok := seen[composePath]; ok {
			continue
		}
		seen[composePath] = struct{}{}
		composePaths = append(composePaths, composePath)
	}
	if len(composePaths) == 0 {
		return nil
	}

	tempDir, err := os.MkdirTemp("", "gci-compose-render-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory for rendered compose files: %w", err)
	}
	defer os.RemoveAll(tempDir)

	for _, composePath := range composePaths {
		sourcePath := filepath.Join(baseDir, filepath.FromSlash(composePath))
		content, err := os.ReadFile(sourcePath)
		if err != nil {
			return fmt.Errorf("failed to read compose file %q: %w", composePath, err)
		}

		rendered, err := renderTemplateString(composePath, string(content), baseDir, renderCtx)
		if err != nil {
			return fmt.Errorf("failed to render compose file %q: %w", composePath, err)
		}

		renderedPath := filepath.Join(tempDir, filepath.FromSlash(composePath))
		if err := os.MkdirAll(filepath.Dir(renderedPath), 0o755); err != nil {
			return fmt.Errorf("failed to create temp directory for %q: %w", composePath, err)
		}
		if err := os.WriteFile(renderedPath, []byte(rendered), 0o644); err != nil {
			return fmt.Errorf("failed to write rendered compose file %q: %w", composePath, err)
		}
	}

	fmt.Fprintln(cmd.OutOrStdout(), "syncing rendered compose files...")
	if err := gcissh.SyncPaths(ctx, target, tempDir, composePaths, nil, remoteServiceDir); err != nil {
		return fmt.Errorf("failed to sync rendered compose files: %w", err)
	}
	return nil
}
