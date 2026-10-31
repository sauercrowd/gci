package host

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/sauercrowd/gci/internal/config"
)

const migrationTimeout = 10 * time.Minute

func runMigration(ctx context.Context, w io.Writer, hc *HostConfig, cfg *config.Config, name, env string, prepared preparedService, rev string) (retErr error) {
	svc := cfg.Services[name]
	key := rev
	if len(key) > 7 {
		key = key[:7]
	}
	container := fmt.Sprintf("gci-%s-%s-m%s", cfg.App, name, key)
	if err := deleteExistingInstance(ctx, container); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := deleteExistingInstance(cleanupCtx, container); err != nil {
			if retErr == nil {
				retErr = fmt.Errorf("delete migration instance %s: %w", container, err)
			} else {
				retErr = fmt.Errorf("%w (also failed to delete migration instance %s: %v)", retErr, container, err)
			}
		}
	}()

	var args []string
	if prepared.release != nil {
		args = []string{"copy", prepared.release.Instance, container, "-s", hc.StoragePool,
			"-c", "user.gci.role=migration"}
	} else {
		args = []string{"init", baseImage, container, "--profile", profileName, "-s", hc.StoragePool,
			"-c", "user.gci.app=" + cfg.App, "-c", "user.gci.service=" + name, "-c", "user.gci.role=migration"}
	}
	if svc.CPU != "" {
		args = append(args, "-c", "limits.cpu="+svc.CPU)
	}
	if svc.Memory != "" {
		args = append(args, "-c", "limits.memory="+svc.Memory)
	}
	if _, err := incus(ctx, args...); err != nil {
		return err
	}
	if prepared.release != nil {
		if err := unsetInstanceConfig(ctx, container, "user.gci.key"); err != nil {
			return err
		}
	}
	if _, err := incus(ctx, "start", container); err != nil {
		return err
	}
	if _, err := waitIPv4(ctx, container, 60*time.Second); err != nil {
		return err
	}
	waitSystemd(ctx, container)
	if _, err := incus(ctx, "exec", container, "--", "mkdir", "-p", "/app"); err != nil {
		return err
	}
	if err := pushFile(ctx, container, "/etc/systemd/resolved.conf.d/gci.conf",
		"[Resolve]\nDomains="+cfg.App+".internal\n", "0644"); err != nil {
		return err
	}
	_, _ = incus(ctx, "exec", container, "--", "systemctl", "try-restart", "systemd-resolved")
	if err := pushFile(ctx, container, "/etc/gci/service.env",
		renderEnvFile(serviceEnvVars(cfg.App, name, svc, env, prepared.secrets)), "0600"); err != nil {
		return err
	}

	return execMigration(ctx, w, container, name, svc, rev, prepared.secrets)
}

func execMigration(ctx context.Context, w io.Writer, container, name string, svc *config.Service, rev string, secrets map[string]string) error {
	args, unit := migrationCommandArgs(container, svc.User, rev, svc.Migrate)
	migrationCtx, cancel := context.WithTimeout(ctx, migrationTimeout)
	output := &cappedHealthWriter{limit: maxHealthCommandOutput + maxSecretSize}
	cmd := exec.CommandContext(migrationCtx, "incus", args...)
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	ctxErr := migrationCtx.Err()
	cancel()
	sanitized := sanitizeHealthOutput(output.String(), secrets)
	if sanitized != "" {
		fmt.Fprintf(w, "==> %s: migration output\n%s\n", name, sanitized)
	}
	if err != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = incus(stopCtx, "exec", container, "--", "systemctl", "stop", unit)
		stopCancel()
		if ctxErr == context.DeadlineExceeded {
			return fmt.Errorf("migration command timed out after %s%s", migrationTimeout, healthOutputDetail(sanitized))
		}
		return fmt.Errorf("migration command: %w%s", err, healthOutputDetail(sanitized))
	}
	return nil
}

func migrationCommandArgs(container, user, revision, command string) ([]string, string) {
	key := revision
	if len(key) > 7 {
		key = key[:7]
	}
	unit := "gci-migrate-" + key
	args := []string{"exec", container, "--", "systemd-run", "--unit", unit, "--wait", "--pipe", "--quiet", "--collect",
		"-p", "RuntimeMaxSec=600",
		"-p", "User=" + user, "-p", "EnvironmentFile=/etc/gci/service.env", "-p", "WorkingDirectory=/app",
		"/bin/sh", "-e", "-c", command}
	return args, unit
}
