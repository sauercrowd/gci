package host

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const stateRoot = "/var/lib/gci"

type HostConfig struct {
	EnvoyIP     string `json:"envoy_ip"`
	VIPRange    string `json:"vip_range"`
	StoragePool string `json:"storage_pool"`
}

func hostConfigPath() string { return filepath.Join(stateRoot, "host.json") }
func envoyDir() string       { return filepath.Join(stateRoot, "envoy") }

func (h *HostConfig) save() error {
	raw, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(hostConfigPath(), raw, 0o644)
}

func loadHostConfig() (*HostConfig, error) {
	raw, err := os.ReadFile(hostConfigPath())
	if err != nil {
		return nil, fmt.Errorf("read %s (re-run `gci bootstrap`): %w", hostConfigPath(), err)
	}
	hc := &HostConfig{}
	if err := json.Unmarshal(raw, hc); err != nil {
		return nil, err
	}
	return hc, nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path))
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func lock() (func(), error) {
	if err := os.MkdirAll(stateRoot, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(stateRoot, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
