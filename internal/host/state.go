package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/sauercrowd/gci/internal/config"
)

const stateRoot = "/var/lib/gci"

type AppState struct {
	App      string                   `json:"app"`
	Services map[string]*ServiceState `json:"services"`
	Releases map[string][]*Release    `json:"releases,omitempty"`
}

type ServiceState struct {
	Container string            `json:"container"`
	IP        string            `json:"ip"`
	Rev       string            `json:"rev"`
	Env       string            `json:"env"`
	Release   string            `json:"release,omitempty"`
	VIP       string            `json:"vip,omitempty"`
	HTTP      bool              `json:"http,omitempty"`
	Port      int               `json:"port,omitempty"`
	Routes    []config.Route    `json:"routes,omitempty"`
	Volumes   map[string]string `json:"volumes,omitempty"`
}

type Release struct {
	Key      string    `json:"key"`
	Instance string    `json:"instance"`
	Deps     string    `json:"deps,omitempty"`
	Env      string    `json:"env"`
	Source   string    `json:"source"`
	Created  time.Time `json:"created"`
}

// HostConfig is written by bootstrap and read by every later agent call.
type HostConfig struct {
	EnvoyIP     string `json:"envoy_ip"`
	VIPRange    string `json:"vip_range"`
	StoragePool string `json:"storage_pool"`
}

func hostConfigPath() string { return filepath.Join(stateRoot, "host.json") }

func loadHostConfig() (*HostConfig, error) {
	raw, err := os.ReadFile(hostConfigPath())
	if err != nil {
		return nil, fmt.Errorf("read %s (re-run `gci bootstrap`): %w", hostConfigPath(), err)
	}
	hc := &HostConfig{}
	if err := json.Unmarshal(raw, hc); err != nil {
		return nil, err
	}
	if hc.StoragePool == "" {
		return nil, fmt.Errorf("host was bootstrapped before release snapshots; re-run `gci bootstrap`")
	}
	return hc, nil
}

func (h *HostConfig) save() error {
	raw, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(hostConfigPath(), raw, 0o644)
}

// allocateVIP returns the lowest address in the VIP range not used by any
// service on this host. st is the in-memory state of the app being deployed.
func allocateVIP(hc *HostConfig, st *AppState) (string, error) {
	prefix, err := netip.ParsePrefix(hc.VIPRange)
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	all, err := loadAllStates()
	if err != nil {
		return "", err
	}
	for _, s := range append(all, st) {
		if s.App == st.App && s != st {
			continue
		}
		for _, svc := range s.Services {
			used[svc.VIP] = true
		}
	}
	for a := prefix.Masked().Addr().Next(); prefix.Contains(a); a = a.Next() {
		if !used[a.String()] && prefix.Contains(a.Next()) {
			return a.String(), nil
		}
	}
	return "", fmt.Errorf("VIP range %s exhausted", hc.VIPRange)
}

func appDir(app string) string    { return filepath.Join(stateRoot, "apps", app) }
func statePath(app string) string { return filepath.Join(appDir(app), "state.json") }
func envoyDir() string            { return filepath.Join(stateRoot, "envoy") }
func releaseRoot(app, svc, key string) string {
	return filepath.Join(appDir(app), "releases", svc, key[:12])
}
func sourceDir(app, svc, hash string) string {
	return filepath.Join(appDir(app), "sources", svc, hash)
}

func loadState(app string) (*AppState, error) {
	st := &AppState{App: app, Services: map[string]*ServiceState{}}
	raw, err := os.ReadFile(statePath(app))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", statePath(app), err)
	}
	if st.Services == nil {
		st.Services = map[string]*ServiceState{}
	}
	return st, nil
}

func (s *AppState) save() error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(statePath(s.App), raw, 0o644)
}

func loadAllStates() ([]*AppState, error) {
	entries, err := os.ReadDir(filepath.Join(stateRoot, "apps"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*AppState
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(statePath(e.Name())); err != nil {
			continue
		}
		st, err := loadState(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].App < out[j].App })
	return out, nil
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

// lock serialises agent operations on the host.
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
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
