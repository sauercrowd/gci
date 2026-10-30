package host

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const healthCommandAttemptTimeout = 10 * time.Second
const maxHealthCommandOutput = 2 * 1024

func waitTCP(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s not reachable after %s: %w", addr, timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

type probe func() error

func tcpProbe(addr string) probe {
	return func() error {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return err
		}
		return conn.Close()
	}
}

func httpProbe(url string) probe {
	client := &http.Client{Timeout: 3 * time.Second}
	return func() error {
		resp, err := client.Get(url)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("GET %s: %s", url, resp.Status)
		}
		return nil
	}
}

func unitActiveProbe(ctx context.Context, container, unit string) probe {
	return func() error {
		out, _ := incus(ctx, "exec", container, "--", "systemctl", "is-active", unit)
		if strings.TrimSpace(out) != "active" {
			return fmt.Errorf("unit %s is %q", unit, out)
		}
		return nil
	}
}

// waitHealthy polls p until it succeeds, the timeout expires, or the unit
// keeps crash-looping.
func waitHealthy(ctx context.Context, container, unit string, p probe, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	return waitHealthyUntil(ctx, container, unit, p, deadline, timeout)
}

func waitHealthyUntil(ctx context.Context, container, unit string, p probe, deadline time.Time, timeout time.Duration) error {
	var last error
	for time.Now().Before(deadline) {
		if last = p(); last == nil {
			if time.Now().Before(deadline) {
				return nil
			}
			break
		}
		if !time.Now().Before(deadline) {
			break
		}
		out, _ := incus(ctx, "exec", container, "--", "systemctl", "show", "-p", "NRestarts", "--value", unit)
		if n, _ := strconv.Atoi(strings.TrimSpace(out)); n >= 5 {
			return fmt.Errorf("%s restarted %d times: %v", unit, n, last)
		}
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded && !time.Now().Before(deadline) {
				break
			}
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("not healthy after %s: %v", timeout, last)
}

func healthScript(command string) string {
	return "#!/bin/sh\nset -e\n" + command + "\n"
}

func waitHealthCommand(ctx context.Context, container, user string, hasSource bool, deadline time.Time, timeout time.Duration, secrets map[string]string) error {
	var lastErr error
	var lastOutput string
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		attemptTimeout := healthCommandAttemptTimeout
		if remaining < attemptTimeout {
			attemptTimeout = remaining
		}
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		output, err := runHealthCommand(attemptCtx, container, user, hasSource)
		ctxErr := ctx.Err()
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		lastOutput = sanitizeHealthOutput(output, secrets)
		if ctxErr != nil && time.Now().Before(deadline) {
			return fmt.Errorf("health command: %w%s", ctxErr, healthOutputDetail(lastOutput))
		}
		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			if time.Now().Before(deadline) {
				return fmt.Errorf("health command: %w%s", ctx.Err(), healthOutputDetail(lastOutput))
			}
		case <-time.After(time.Second):
		}
	}
	if lastErr == nil {
		lastErr = context.DeadlineExceeded
	}
	return fmt.Errorf("health command timed out after %s (last attempt: %v)%s", timeout, lastErr, healthOutputDetail(lastOutput))
}

func runHealthCommand(ctx context.Context, container, user string, hasSource bool) (string, error) {
	args := []string{"exec", container, "--", "systemd-run", "--wait", "--pipe", "--quiet", "--collect",
		"-p", "User=" + user, "-p", "EnvironmentFile=/etc/gci/service.env"}
	if hasSource {
		args = append(args, "-p", "WorkingDirectory=/app")
	}
	args = append(args, "/bin/sh", "/etc/gci/health.sh")
	cmd := exec.CommandContext(ctx, "incus", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}

func sanitizeHealthOutput(output string, secrets map[string]string) string {
	values := make([]string, 0, len(secrets))
	for _, value := range secrets {
		if value != "" {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, value := range values {
		output = strings.ReplaceAll(output, value, "[REDACTED]")
	}
	output = strings.TrimSpace(output)
	if len(output) > maxHealthCommandOutput {
		output = output[:maxHealthCommandOutput] + "…"
	}
	return output
}

func healthOutputDetail(output string) string {
	if output == "" {
		return ""
	}
	return "; last output: " + output
}
