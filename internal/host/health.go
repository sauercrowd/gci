package host

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

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
	var last error
	for time.Now().Before(deadline) {
		if last = p(); last == nil {
			return nil
		}
		out, _ := incus(ctx, "exec", container, "--", "systemctl", "show", "-p", "NRestarts", "--value", unit)
		if n, _ := strconv.Atoi(strings.TrimSpace(out)); n >= 5 {
			return fmt.Errorf("%s restarted %d times: %v", unit, n, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("not healthy after %s: %v", timeout, last)
}
