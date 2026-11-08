// Package config parses gci.toml, the per-project deployment description.
package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// DefaultNixpkgs pins the package set used when gci.toml does not set one.
const DefaultNixpkgs = "github:NixOS/nixpkgs/08d3a97586f6b6343b3b5e799980773629432610" // nixos-26.05

const DefaultFileName = "gci.toml"

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Config struct {
	App      string              `toml:"app" json:"app"`
	Server   string              `toml:"server,omitempty" json:"server,omitempty"`
	Nixpkgs  string              `toml:"nixpkgs,omitempty" json:"nixpkgs"`
	Services map[string]*Service `toml:"services" json:"services"`
}

type Service struct {
	// Packages are Nix expressions evaluated with nixpkgs in scope,
	// e.g. "postgresql_16" or "python3.withPackages (p: [p.psycopg])".
	Packages  []string          `toml:"packages" json:"packages"`
	Command   string            `toml:"command" json:"command"`
	Setup     string            `toml:"setup,omitempty" json:"setup,omitempty"`
	User      string            `toml:"user,omitempty" json:"user"`
	Env       map[string]string `toml:"env,omitempty" json:"env,omitempty"`
	Port      int               `toml:"port,omitempty" json:"port,omitempty"`
	Health    string            `toml:"health,omitempty" json:"health,omitempty"`
	Volumes   map[string]string `toml:"volumes,omitempty" json:"volumes,omitempty"`
	DependsOn []string          `toml:"depends_on,omitempty" json:"depends_on,omitempty"`
	Routes    []Route           `toml:"routes,omitempty" json:"routes,omitempty"`
	CPU       string            `toml:"cpu,omitempty" json:"cpu,omitempty"`
	Memory    string            `toml:"memory,omitempty" json:"memory,omitempty"`
	// HealthTimeout is in seconds.
	HealthTimeout int `toml:"health_timeout,omitempty" json:"health_timeout"`
}

type Route struct {
	Host   string `toml:"host,omitempty" json:"host"`
	Prefix string `toml:"prefix,omitempty" json:"prefix"`
}

// Stateful services own volumes; they are replaced stop-then-start instead of blue/green.
func (s *Service) Stateful() bool { return len(s.Volumes) > 0 }

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	dec := toml.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Nixpkgs == "" {
		c.Nixpkgs = DefaultNixpkgs
	}
	for _, svc := range c.Services {
		if svc.User == "" {
			svc.User = "gci"
		}
		if svc.HealthTimeout == 0 {
			svc.HealthTimeout = 120
		}
		for i := range svc.Routes {
			if svc.Routes[i].Host == "" {
				svc.Routes[i].Host = "*"
			}
			if svc.Routes[i].Prefix == "" {
				svc.Routes[i].Prefix = "/"
			}
		}
	}
}

func (c *Config) Validate() error {
	if err := ValidateAppName(c.App); err != nil {
		return err
	}
	if len(c.Services) == 0 {
		return fmt.Errorf("at least one [services.<name>] is required")
	}
	volumeOwner := map[string]string{}
	for name, svc := range c.Services {
		if !nameRe.MatchString(name) {
			return fmt.Errorf("service %q must match %s", name, nameRe)
		}
		if strings.TrimSpace(svc.Command) == "" {
			return fmt.Errorf("service %q: command is required", name)
		}
		for _, dep := range svc.DependsOn {
			if _, ok := c.Services[dep]; !ok {
				return fmt.Errorf("service %q depends on unknown service %q", name, dep)
			}
		}
		for env := range svc.Env {
			if !envNameRe.MatchString(env) {
				return fmt.Errorf("service %q: environment variable %q is invalid", name, env)
			}
		}
		if len(svc.Routes) > 0 && svc.Port == 0 {
			return fmt.Errorf("service %q: routes require port", name)
		}
		if svc.Health != "" && svc.Port == 0 {
			return fmt.Errorf("service %q: health requires port", name)
		}
		if svc.Health != "" && !strings.HasPrefix(svc.Health, "/") {
			return fmt.Errorf("service %q: health must be an HTTP path starting with /", name)
		}
		for _, r := range svc.Routes {
			if !strings.HasPrefix(r.Prefix, "/") {
				return fmt.Errorf("service %q: route prefix %q must start with /", name, r.Prefix)
			}
		}
		for vol, p := range svc.Volumes {
			if !nameRe.MatchString(vol) {
				return fmt.Errorf("service %q: volume %q must match %s", name, vol, nameRe)
			}
			if !strings.HasPrefix(p, "/") {
				return fmt.Errorf("service %q: volume %q path must be absolute", name, vol)
			}
			if other, ok := volumeOwner[vol]; ok {
				return fmt.Errorf("volume %q used by both %q and %q", vol, other, name)
			}
			volumeOwner[vol] = name
		}
	}
	_, err := c.Order()
	return err
}

func ValidateAppName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("app %q must match %s", name, nameRe)
	}
	return nil
}

// Order returns service names topologically sorted by depends_on (ties broken by name).
func (c *Config) Order() ([]string, error) {
	names := make([]string, 0, len(c.Services))
	for n := range c.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(string, []string) error
	visit = func(n string, path []string) error {
		switch state[n] {
		case 1:
			return fmt.Errorf("dependency cycle: %s", strings.Join(append(path, n), " -> "))
		case 2:
			return nil
		}
		state[n] = 1
		deps := append([]string(nil), c.Services[n].DependsOn...)
		sort.Strings(deps)
		for _, d := range deps {
			if err := visit(d, append(path, n)); err != nil {
				return err
			}
		}
		state[n] = 2
		out = append(out, n)
		return nil
	}
	for _, n := range names {
		if err := visit(n, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}
