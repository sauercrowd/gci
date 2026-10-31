// Package config parses gci.toml, the per-project deployment description.
package config

import (
	"fmt"
	"os"
	"path/filepath"
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
var secretNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

type Config struct {
	App      string              `toml:"app" json:"app"`
	Server   string              `toml:"server,omitempty" json:"server,omitempty"`
	Nixpkgs  string              `toml:"nixpkgs,omitempty" json:"nixpkgs"`
	Services map[string]*Service `toml:"services" json:"services"`
}

type Service struct {
	// Packages are Nix expressions evaluated with nixpkgs in scope,
	// e.g. "postgresql_16" or "python3.withPackages (p: [p.psycopg])".
	Packages []string `toml:"packages" json:"packages"`
	// Source is a local directory shipped to the host and snapshotted at /app.
	Source        string            `toml:"source,omitempty" json:"source,omitempty"`
	Install       string            `toml:"install,omitempty" json:"install,omitempty"`
	Build         string            `toml:"build,omitempty" json:"build,omitempty"`
	Reuse         string            `toml:"reuse,omitempty" json:"reuse,omitempty"`
	Migrate       string            `toml:"migrate,omitempty" json:"migrate,omitempty"`
	Command       string            `toml:"command" json:"command"`
	Setup         string            `toml:"setup,omitempty" json:"setup,omitempty"`
	User          string            `toml:"user,omitempty" json:"user"`
	Env           map[string]string `toml:"env,omitempty" json:"env,omitempty"`
	Secrets       map[string]string `toml:"secrets,omitempty" json:"secrets,omitempty"`
	Port          int               `toml:"port,omitempty" json:"port,omitempty"`
	Health        string            `toml:"health,omitempty" json:"health,omitempty"`
	HealthCommand string            `toml:"health_command,omitempty" json:"health_command,omitempty"`
	Volumes       map[string]string `toml:"volumes,omitempty" json:"volumes,omitempty"`
	DependsOn     []string          `toml:"depends_on,omitempty" json:"depends_on,omitempty"`
	Routes        []Route           `toml:"routes,omitempty" json:"routes,omitempty"`
	CPU           string            `toml:"cpu,omitempty" json:"cpu,omitempty"`
	Memory        string            `toml:"memory,omitempty" json:"memory,omitempty"`
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
	base := filepath.Dir(path)
	for _, svc := range cfg.Services {
		if svc.Source != "" && !filepath.IsAbs(svc.Source) {
			svc.Source = filepath.Join(base, svc.Source)
		}
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
		if svc.Reuse == "" {
			svc.Reuse = "dependencies"
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
		if svc.Reuse != "dependencies" && svc.Reuse != "build" && svc.Reuse != "nothing" {
			return fmt.Errorf("service %q: reuse must be dependencies, build, or nothing", name)
		}
		if (svc.Install != "" || svc.Build != "") && svc.Source == "" {
			return fmt.Errorf("service %q: install and build require source", name)
		}
		if strings.TrimSpace(svc.Migrate) == "" && svc.Migrate != "" {
			return fmt.Errorf("service %q: migrate must not be blank", name)
		}
		if svc.Migrate != "" && len(svc.Volumes) > 0 {
			return fmt.Errorf("service %q: migrate cannot be used with volumes", name)
		}
		for env := range svc.Env {
			if !envNameRe.MatchString(env) {
				return fmt.Errorf("service %q: environment variable %q is invalid", name, env)
			}
		}
		for env, secret := range svc.Secrets {
			if !envNameRe.MatchString(env) {
				return fmt.Errorf("service %q: secret environment variable %q is invalid", name, env)
			}
			if !secretNameRe.MatchString(secret) {
				return fmt.Errorf("service %q: secret name %q is invalid", name, secret)
			}
			if _, ok := svc.Env[env]; ok {
				return fmt.Errorf("service %q: secret environment variable %q is also defined in env", name, env)
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
		if svc.HealthCommand != "" && strings.TrimSpace(svc.HealthCommand) == "" {
			return fmt.Errorf("service %q: health_command must not be blank", name)
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

func ValidateSecretName(name string) error {
	if !secretNameRe.MatchString(name) {
		return fmt.Errorf("secret name %q must match %s", name, secretNameRe)
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
