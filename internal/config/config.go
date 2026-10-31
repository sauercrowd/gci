// Package config parses gci.toml, the per-project deployment description.
package config

import (
	"errors"
	"fmt"
	"net"
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

var defaultInstallInputs = []string{
	"package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml",
	"yarn.lock", ".npmrc", "pyproject.toml", "uv.lock", "requirements.txt",
	"poetry.lock", "Pipfile", "Pipfile.lock",
}

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
	InstallInputs []string          `toml:"install_inputs,omitempty" json:"install_inputs,omitempty"`
	Build         string            `toml:"build,omitempty" json:"build,omitempty"`
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
	TLS    string `toml:"tls,omitempty" json:"tls,omitempty"`
	Cert   string `toml:"cert,omitempty" json:"cert,omitempty"`
	Key    string `toml:"key,omitempty" json:"key,omitempty"`
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
		if svc.Install != "" && len(svc.InstallInputs) == 0 && svc.Source != "" {
			for _, candidate := range defaultInstallInputs {
				if _, err := os.Stat(filepath.Join(svc.Source, candidate)); err == nil {
					svc.InstallInputs = append(svc.InstallInputs, candidate)
				}
			}
		}
		if svc.Install != "" && len(svc.InstallInputs) == 0 {
			return nil, errors.New("install_inputs: no known lockfile/manifest found; set install_inputs")
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
			if svc.Routes[i].TLS == "" {
				svc.Routes[i].TLS = DefaultTLSMode(svc.Routes[i].Host)
			}
		}
	}
}

func (c *Config) Validate() error {
	c.applyDefaults()
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
		if (svc.Install != "" || len(svc.InstallInputs) > 0 || svc.Build != "") && svc.Source == "" {
			return fmt.Errorf("service %q: install, install_inputs, and build require source", name)
		}
		for _, input := range svc.InstallInputs {
			if err := ValidateInstallInput(input); err != nil {
				return fmt.Errorf("service %q: install_inputs: %w", name, err)
			}
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
			switch r.TLS {
			case "acme", "self-signed", "custom", "off":
			default:
				return fmt.Errorf("service %q: route %s has unknown tls mode %q", name, r.Host, r.TLS)
			}
			if r.TLS == "acme" && acmeUnsupportedHost(r.Host) {
				return fmt.Errorf("service %q: ACME TLS cannot be used for route host %q (wildcard, localhost, and IP hosts are not supported)", name, r.Host)
			}
			if r.TLS == "custom" {
				if r.Cert == "" || r.Key == "" {
					return fmt.Errorf("service %q: route %s with tls=custom requires both cert and key secret names", name, r.Host)
				}
				if err := ValidateSecretName(r.Cert); err != nil {
					return fmt.Errorf("service %q: route %s cert: %w", name, r.Host, err)
				}
				if err := ValidateSecretName(r.Key); err != nil {
					return fmt.Errorf("service %q: route %s key: %w", name, r.Host, err)
				}
			} else if r.Cert != "" || r.Key != "" {
				return fmt.Errorf("service %q: route %s cert and key are only valid with tls=custom", name, r.Host)
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
	routeOwners := map[string]string{}
	hostTLS := map[string]Route{}
	for _, name := range sortedServiceNames(c.Services) {
		for _, r := range c.Services[name].Routes {
			routeKey := r.Host + "\x00" + r.Prefix
			if owner, ok := routeOwners[routeKey]; ok && owner != name {
				return fmt.Errorf("route %s%s is already used by app %q service %q", r.Host, r.Prefix, c.App, owner)
			}
			routeOwners[routeKey] = name
			if previous, ok := hostTLS[r.Host]; ok && !sameTLSSettings(previous, r) {
				return fmt.Errorf("route host %q has inconsistent tls, cert, or key settings", r.Host)
			}
			hostTLS[r.Host] = r
		}
	}
	_, err := c.Order()
	return err
}

func DefaultTLSMode(host string) string {
	lower := strings.ToLower(host)
	if strings.Contains(host, "*") || lower == "localhost" || strings.HasSuffix(lower, ".localhost") || net.ParseIP(strings.Trim(host, "[]")) != nil {
		return "self-signed"
	}
	return "acme"
}

func acmeUnsupportedHost(host string) bool {
	return strings.Contains(host, "*") || strings.EqualFold(host, "localhost") ||
		strings.HasSuffix(strings.ToLower(host), ".localhost") || net.ParseIP(strings.Trim(host, "[]")) != nil
}

func sameTLSSettings(a, b Route) bool {
	return a.TLS == b.TLS && a.Cert == b.Cert && a.Key == b.Key
}

func sortedServiceNames(services map[string]*Service) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func ValidateAppName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("app %q must match %s", name, nameRe)
	}
	return nil
}

func ValidateInstallInput(input string) error {
	if input == "" || filepath.IsAbs(input) {
		return fmt.Errorf("%q must be a non-empty source-relative path", input)
	}
	for _, part := range strings.Split(filepath.ToSlash(input), "/") {
		if part == ".." {
			return fmt.Errorf("%q must not contain ..", input)
		}
	}
	if strings.ContainsAny(input, "*?[]") {
		return fmt.Errorf("%q must not contain globs", input)
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
