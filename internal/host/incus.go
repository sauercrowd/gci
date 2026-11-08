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

type volumeInfo struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
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

func volumeOwnerClaim(name, app, owner string) (bool, error) {
	if owner != "" && owner != app {
		return false, fmt.Errorf("volume %s belongs to app %s", name, owner)
	}
	return owner == "", nil
}

func volumeInfoAt(ctx context.Context, pool, name string) (volumeInfo, error) {
	out, err := incus(ctx, "query", fmt.Sprintf("/1.0/storage-pools/%s/volumes/custom/%s", pool, name))
	if err != nil {
		return volumeInfo{}, err
	}
	var volume volumeInfo
	if err := json.Unmarshal([]byte(out), &volume); err != nil {
		return volumeInfo{}, fmt.Errorf("parse volume %s: %w", name, err)
	}
	return volume, nil
}

func claimVolume(ctx context.Context, pool, name, app string) error {
	volume, err := volumeInfoAt(ctx, pool, name)
	if err != nil {
		return fmt.Errorf("inspect volume %s: %w", name, err)
	}
	claim, err := volumeOwnerClaim(name, app, volume.Config["user.gci.app"])
	if err != nil {
		return err
	}
	if !claim {
		return nil
	}
	if _, err := incus(ctx, "storage", "volume", "set", pool, name, "user.gci.app="+app); err != nil {
		return fmt.Errorf("claim volume %s: %w", name, err)
	}
	return nil
}

func setVolumeConfig(ctx context.Context, pool, name, key, value string) error {
	_, err := incus(ctx, "storage", "volume", "set", pool, name, key+"="+value)
	return err
}

func listCustomVolumes(ctx context.Context, pool string) ([]volumeInfo, error) {
	out, err := incus(ctx, "storage", "volume", "list", pool, "--format", "json")
	var volumes []volumeInfo
	if err == nil {
		err = json.Unmarshal([]byte(out), &volumes)
		if err == nil {
			return customVolumes(volumes), nil
		}
	}
	out, queryErr := incus(ctx, "query", fmt.Sprintf("/1.0/storage-pools/%s/volumes?recursion=1", pool))
	if queryErr != nil {
		return nil, fmt.Errorf("list volumes in pool %s (%v); query fallback: %w", pool, err, queryErr)
	}
	if err := json.Unmarshal([]byte(out), &volumes); err != nil {
		return nil, fmt.Errorf("parse volumes in pool %s: %w", pool, err)
	}
	return customVolumes(volumes), nil
}

func customVolumes(volumes []volumeInfo) []volumeInfo {
	custom := make([]volumeInfo, 0, len(volumes))
	for _, volume := range volumes {
		if volume.Type == "custom" {
			custom = append(custom, volume)
		}
	}
	return custom
}

func volumePool(ctx context.Context, hc *HostConfig, name, app string) (string, error) {
	claimAndTag := func(pool string) error {
		if err := claimVolume(ctx, pool, name, app); err != nil {
			return err
		}
		return setVolumeConfig(ctx, pool, name, "user.gci.role", "data")
	}
	if exists(ctx, "storage", "volume", "show", hc.StoragePool, name) {
		return hc.StoragePool, claimAndTag(hc.StoragePool)
	}
	if hc.StoragePool != legacyPool && exists(ctx, "storage", "volume", "show", legacyPool, name) {
		return legacyPool, claimAndTag(legacyPool)
	}
	if _, err := incus(ctx, "storage", "volume", "create", hc.StoragePool, name); err != nil {
		return "", err
	}
	if err := claimAndTag(hc.StoragePool); err != nil {
		return "", err
	}
	return hc.StoragePool, nil
}

func deleteInstance(ctx context.Context, name string) error {
	_, err := incus(ctx, "delete", "--force", name)
	return err
}
