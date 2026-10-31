// Package config parses gci.toml, the per-project deployment description.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// DefaultNixpkgs pins the package set used when gci.toml does not set one.
const DefaultNixpkgs = "github:NixOS/nixpkgs/08d3a97586f6b6343b3b5e799980773629432610" // nixos-26.05

const DefaultFileName = "gci.toml"

type Config struct {
	App      string              `toml:"app" json:"app"`
	Server   string              `toml:"server,omitempty" json:"server,omitempty"`
	Nixpkgs  string              `toml:"nixpkgs,omitempty" json:"nixpkgs"`
	Services map[string]*Service `toml:"services" json:"services"`
}

type Service struct {
	// Packages are Nix expressions evaluated with nixpkgs in scope,
	// e.g. "postgresql_16" or "python3.withPackages (p: [p.psycopg])".
	Packages []string          `toml:"packages" json:"packages"`
	Command  string            `toml:"command" json:"command"`
	Setup    string            `toml:"setup,omitempty" json:"setup,omitempty"`
	User     string            `toml:"user,omitempty" json:"user"`
	Env      map[string]string `toml:"env,omitempty" json:"env,omitempty"`
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
	if cfg.Nixpkgs == "" {
		cfg.Nixpkgs = DefaultNixpkgs
	}
	for _, svc := range cfg.Services {
		if svc.User == "" {
			svc.User = "gci"
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if !nameRe.MatchString(c.App) {
		return fmt.Errorf("app %q must match %s", c.App, nameRe)
	}
	if len(c.Services) == 0 {
		return fmt.Errorf("at least one [services.<name>] is required")
	}
	for name, svc := range c.Services {
		if !nameRe.MatchString(name) {
			return fmt.Errorf("service %q must match %s", name, nameRe)
		}
		if strings.TrimSpace(svc.Command) == "" {
			return fmt.Errorf("service %q: command is required", name)
		}
		for env := range svc.Env {
			if !envNameRe.MatchString(env) {
				return fmt.Errorf("service %q: environment variable %q is invalid", name, env)
			}
		}
	}
	return nil
}

func ValidateAppName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("app %q must match %s", name, nameRe)
	}
	return nil
}
