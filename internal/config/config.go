package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const DefaultNixpkgs = "github:NixOS/nixpkgs/08d3a97586f6b6343b3b5e799980773629432610"

type Config struct {
	App      string              `toml:"app"`
	Server   string              `toml:"server,omitempty"`
	Nixpkgs  string              `toml:"nixpkgs,omitempty"`
	Services map[string]*Service `toml:"services"`
}

type Service struct {
	Packages []string `toml:"packages"`
	Command  string   `toml:"command"`
	Setup    string   `toml:"setup,omitempty"`
	User     string   `toml:"user,omitempty"`
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)

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
	if !namePattern.MatchString(c.App) {
		return fmt.Errorf("app %q must match %s", c.App, namePattern)
	}
	if len(c.Services) == 0 {
		return fmt.Errorf("at least one [services.<name>] is required")
	}
	for name, svc := range c.Services {
		if !namePattern.MatchString(name) {
			return fmt.Errorf("service %q must match %s", name, namePattern)
		}
		if strings.TrimSpace(svc.Command) == "" {
			return fmt.Errorf("service %q: command is required", name)
		}
	}
	return nil
}

func ValidateAppName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("app %q must match %s", name, namePattern)
	}
	return nil
}
