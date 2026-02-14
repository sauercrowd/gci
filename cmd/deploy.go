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
		if localBuild := strings.TrimSpace(cfg.BuildLocal); localBuild != "" {
			if err := runLocalBuild(cmd.Context(), cmd, baseDir, localBuild, target, cfg.BuildForwards); err != nil {
				return err
			}
		}

		remoteServiceDir := path.Join(srv.ServiceDir, cfg.Name)
		fmt.Fprintf(cmd.OutOrStdout(), "syncing files to %s:%s...\n", srv.Host, remoteServiceDir)
		if err := gcissh.SyncPaths(cmd.Context(), target, baseDir, cfg.SyncPaths, cfg.ExcludePatterns, remoteServiceDir); err != nil {
			return err
		}

		if remoteBuild := strings.TrimSpace(cfg.BuildRemote); remoteBuild != "" {
			if err := runRemoteBuild(cmd.Context(), cmd, remoteBuild, target, remoteServiceDir); err != nil {
				return err
			}
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

	if _, err := scriptFile.WriteString(buildCommand + "\n"); err != nil {
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
	script := fmt.Sprintf("cd %s\n%s\n", shellQuote(remoteServiceDir), buildCommand)
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
