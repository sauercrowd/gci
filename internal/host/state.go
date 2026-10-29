package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/sauercrowd/gci/internal/config"
)

const stateRoot = "/var/lib/gci"

type AppState struct {
	App      string                   `json:"app"`
	Services map[string]*ServiceState `json:"services"`
}

type ServiceState struct {
	Container string            `json:"container"`
	IP        string            `json:"ip"`
	Rev       string            `json:"rev"`
	Env       string            `json:"env"`
	Port      int               `json:"port,omitempty"`
	Routes    []config.Route    `json:"routes,omitempty"`
	Volumes   map[string]string `json:"volumes,omitempty"`
}

func appDir(app string) string    { return filepath.Join(stateRoot, "apps", app) }
func statePath(app string) string { return filepath.Join(appDir(app), "state.json") }
func envoyDir() string            { return filepath.Join(stateRoot, "envoy") }
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
