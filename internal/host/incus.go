package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	networkName = "gci0"
	profileName = "gci"
	baseImage   = "gci-base"
	legacyPool  = "default"
	envoyName   = "gci-envoy"
	nixStore    = "/nix/store"
)

type instance struct {
	Name   string            `json:"name"`
	Status string            `json:"status"`
	Config map[string]string `json:"config"`
	State  *struct {
		Network map[string]struct {
			Addresses []struct {
				Family  string `json:"family"`
				Address string `json:"address"`
				Scope   string `json:"scope"`
			} `json:"addresses"`
		} `json:"network"`
	} `json:"state"`
}

func (i instance) IPv4() string {
	if i.State == nil {
		return ""
	}
	for _, a := range i.State.Network["eth0"].Addresses {
		if a.Family == "inet" && a.Scope == "global" {
			return a.Address
		}
	}
	return ""
}

func incus(ctx context.Context, args ...string) (string, error) {
	return run(ctx, "incus", args...)
}

func listInstances(ctx context.Context, filters ...string) ([]instance, error) {
	args := append([]string{"list", "--format", "json"}, filters...)
	out, err := incus(ctx, args...)
	if err != nil {
		return nil, err
	}
	var list []instance
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, fmt.Errorf("parse incus list: %w", err)
	}
	return list, nil
}

func getInstance(ctx context.Context, name string) (*instance, error) {
	list, err := listInstances(ctx, "^"+name+"$")
	if err != nil {
		return nil, err
	}
	for _, i := range list {
		if i.Name == name {
			return &i, nil
		}
	}
	return nil, nil
}

func unsetInstanceConfig(ctx context.Context, name, key string) error {
	inst, err := getInstance(ctx, name)
	if err != nil {
		return err
	}
	if inst == nil || inst.Config[key] == "" {
		return nil
	}
	_, err = incus(ctx, "config", "unset", name, key)
	return err
}

func waitIPv4(ctx context.Context, name string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		inst, err := getInstance(ctx, name)
		if err != nil {
			return "", err
		}
		if inst != nil {
			if ip := inst.IPv4(); ip != "" {
				return ip, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("%s: no IPv4 address after %s", name, timeout)
}

// waitSystemd blocks until systemd inside the container finished booting.
func waitSystemd(ctx context.Context, name string) {
	_, _ = incus(ctx, "exec", name, "--", "systemctl", "is-system-running", "--wait")
}

func pushFile(ctx context.Context, name, path, content string, mode string) error {
	_, err := runInput(ctx, strings.NewReader(content), "incus", "file", "push", "--create-dirs", "--mode", mode, "-", name+path)
	return err
}

func exists(ctx context.Context, args ...string) bool {
	_, err := incus(ctx, args...)
	return err == nil
}

func deleteInstance(ctx context.Context, name string) error {
	_, err := incus(ctx, "delete", "--force", name)
	return err
}
