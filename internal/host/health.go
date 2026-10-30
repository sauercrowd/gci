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
	"sync"
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
	attempt := 0
	for time.Now().Before(deadline) {
		attempt++
		remaining := time.Until(deadline)
		attemptTimeout := healthCommandAttemptTimeout
		if remaining < attemptTimeout {
			attemptTimeout = remaining
		}
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		output, err := runHealthCommand(attemptCtx, container, user, hasSource, attempt, attemptTimeout)
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

func runHealthCommand(ctx context.Context, container, user string, hasSource bool, attempt int, attemptTimeout time.Duration) (string, error) {
	args, unit := healthCommandArgs(container, user, hasSource, attempt, attemptTimeout)
	output := &cappedHealthWriter{limit: maxHealthCommandOutput + maxSecretSize}
	cmd := exec.CommandContext(ctx, "incus", args...)
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	if err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = incus(stopCtx, "exec", container, "--", "systemctl", "stop", unit)
		cancel()
	}
	return output.String(), err
}

func healthCommandArgs(container, user string, hasSource bool, attempt int, attemptTimeout time.Duration) ([]string, string) {
	unit := fmt.Sprintf("gci-health-%d", attempt)
	runtimeSeconds := int64(attemptTimeout / time.Second)
	if attemptTimeout%time.Second != 0 {
		runtimeSeconds++
	}
	if runtimeSeconds < 1 {
		runtimeSeconds = 1
	}
	args := []string{"exec", container, "--", "systemd-run", "--unit", unit, "--wait", "--pipe", "--quiet", "--collect",
		"-p", "RuntimeMaxSec=" + strconv.FormatInt(runtimeSeconds, 10),
		"-p", "User=" + user, "-p", "EnvironmentFile=/etc/gci/service.env"}
	if hasSource {
		args = append(args, "-p", "WorkingDirectory=/app")
	}
	args = append(args, "/bin/sh", "/etc/gci/health.sh")
	return args, unit
}

type cappedHealthWriter struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
}

func (w *cappedHealthWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.limit - w.buf.Len()
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		_, _ = w.buf.Write(p[:remaining])
	}
	return len(p), nil
}

func (w *cappedHealthWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func sanitizeHealthOutput(output string, secrets map[string]string) string {
	values := make([]string, 0, len(secrets))
	for _, value := range secrets {
		if value != "" {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	var sanitized strings.Builder
	i := 0
	for i < len(output) && i < maxHealthCommandOutput {
		matched := ""
		for _, value := range values {
			if strings.HasPrefix(output[i:], value) {
				matched = value
				break
			}
		}
		if matched != "" {
			sanitized.WriteString("[REDACTED]")
			i += len(matched)
		} else {
			sanitized.WriteByte(output[i])
			i++
		}
	}
	if i < len(output) {
		sanitized.WriteString("…")
	}
	return strings.TrimSpace(sanitized.String())
}

func healthOutputDetail(output string) string {
	if output == "" {
		return ""
	}
	return "; last output: " + output
}
