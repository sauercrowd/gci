package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	gcissh "github.com/sauercrowd/gci/ssh"
)

type Config struct {
	Name              string             `toml:"name"`
	Server            string             `toml:"server,omitempty"`
	BuildLocal        string             `toml:"build_local,omitempty"`
	BuildRemote       string             `toml:"build_remote,omitempty"`
	BuildForwards     []string           `toml:"build_forwards,omitempty"`
	SyncPaths         []string           `toml:"sync_paths,omitempty"`
	ExcludePatterns   []string           `toml:"exclude_patterns,omitempty"`
	DriverDockerSwarm *DockerSwarmConfig `toml:"driver_docker_swarm,omitempty"`
}

type DockerSwarmConfig struct {
	AppNetwork           string                  `toml:"app_network,omitempty"`
	LogServices          []DockerSwarmLogService `toml:"log_services,omitempty"`
	Stacks               []DockerSwarmStack      `toml:"stacks"`
	PruneImages          *bool                   `toml:"prune_images,omitempty"`
	PruneContainersAfter *string                 `toml:"prune_containers_after,omitempty"`
}

type DockerSwarmLogService struct {
	Stack string `toml:"stack,omitempty"`
	Name  string `toml:"name"`
}

type DockerSwarmStack struct {
	Name               string `toml:"name"`
	ComposeFile        string `toml:"compose_file"`
	Mode               string `toml:"mode,omitempty"`
	WaitTimeoutSeconds int    `toml:"wait_timeout_seconds,omitempty"`
}

const (
	defaultDockerSwarmContainerPruneAfter = 24 * time.Hour
	pruneContainersDisableLiteral         = "none"
)

var defaultDockerSwarmContainerPruneAfterLiteral = FormatDurationLiteral(defaultDockerSwarmContainerPruneAfter)

func (c DockerSwarmConfig) PruneImagesEnabled() bool {
	return c.PruneImages == nil || *c.PruneImages
}

func (c DockerSwarmConfig) PruneContainersAfterDuration() (time.Duration, bool) {
	duration, enabled, err := parsePruneContainersAfter(c.PruneContainersAfter)
	if err != nil {
		panic(err)
	}
	return duration, enabled
}

func (c DockerSwarmConfig) ResolvedPruneContainersAfterLiteral() string {
	if c.PruneContainersAfter == nil || strings.TrimSpace(*c.PruneContainersAfter) == "" {
		return defaultDockerSwarmContainerPruneAfterLiteral
	}
	literal := strings.TrimSpace(*c.PruneContainersAfter)
	if strings.EqualFold(literal, pruneContainersDisableLiteral) {
		return pruneContainersDisableLiteral
	}
	return literal
}

func (c DockerSwarmConfig) ResolvedAppNetwork(appName string) string {
	configured := strings.TrimSpace(c.AppNetwork)
	if configured == "" || strings.EqualFold(configured, "auto") {
		return deterministicDockerSwarmNetwork(appName)
	}
	return configured
}

func (c DockerSwarmConfig) AutoManagesAppNetwork() bool {
	configured := strings.TrimSpace(c.AppNetwork)
	return configured == "" || strings.EqualFold(configured, "auto")
}

func deterministicDockerSwarmNetwork(appName string) string {
	normalized := strings.ToLower(strings.TrimSpace(appName))
	if normalized == "" {
		normalized = "app"
	}

	cleaned := strings.Builder{}
	lastUnderscore := false
	for _, r := range normalized {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlnum {
			cleaned.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			cleaned.WriteRune('_')
			lastUnderscore = true
		}
	}

	value := strings.Trim(cleaned.String(), "_")
	if value == "" {
		value = "app"
	}
	return "gci_net_" + value
}

func parsePruneContainersAfter(value *string) (time.Duration, bool, error) {
	literal := defaultDockerSwarmContainerPruneAfterLiteral
	if value != nil && strings.TrimSpace(*value) != "" {
		literal = strings.TrimSpace(*value)
	}

	if strings.EqualFold(literal, pruneContainersDisableLiteral) {
		return 0, false, nil
	}

	dur, err := time.ParseDuration(literal)
	if err != nil {
		return 0, false, fmt.Errorf("driver_docker_swarm.prune_containers_after must be a valid duration or %q: %w", pruneContainersDisableLiteral, err)
	}
	if dur <= 0 {
		return 0, false, fmt.Errorf("driver_docker_swarm.prune_containers_after must be greater than 0 or %q", pruneContainersDisableLiteral)
	}

	return dur, true, nil
}

func FormatDurationLiteral(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	d = d.Round(time.Second)
	if d%(time.Hour) == 0 {
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	}
	if d%(time.Minute) == 0 {
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	}
	return d.String()
}

func NewDefaultConfig(serviceName, serverName string) Config {
	pruneImages := true
	pruneContainersAfter := defaultDockerSwarmContainerPruneAfterLiteral

	return Config{
		Name:       serviceName,
		Server:     serverName,
		BuildLocal: "go build ./...",
		ExcludePatterns: []string{
			".git",
			"node_modules",
			"__pycache__",
			"*.pyc",
		},
		DriverDockerSwarm: &DockerSwarmConfig{
			AppNetwork: "auto",
			Stacks: []DockerSwarmStack{
				{
					Name:        serviceName,
					ComposeFile: "docker-compose.prod.yaml",
					Mode:        "services",
				},
			},
			PruneImages:          &pruneImages,
			PruneContainersAfter: &pruneContainersAfter,
		},
	}
}

func (c Config) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}

	if strings.TrimSpace(c.BuildLocal) == "" && strings.TrimSpace(c.BuildRemote) == "" {
		return fmt.Errorf("at least one of build_local or build_remote is required")
	}
	for _, spec := range c.BuildForwards {
		if _, err := gcissh.ParseForwardSpec(spec); err != nil {
			return fmt.Errorf("invalid build_forwards entry %q: %w", spec, err)
		}
	}

	driver, err := ResolveDriver(c)
	if err != nil {
		return err
	}

	return driver.Validate(c)
}

func (c Config) ResolvedSyncPaths() []string {
	paths := make([]string, 0, len(c.SyncPaths)+1)
	seen := map[string]struct{}{}

	add := func(path string) {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			return
		}
		if _, ok := seen[trimmed]; ok {
			return
		}
		seen[trimmed] = struct{}{}
		paths = append(paths, trimmed)
	}

	for _, path := range c.SyncPaths {
		add(path)
	}

	if c.DriverDockerSwarm != nil {
		for _, stack := range c.DriverDockerSwarm.Stacks {
			add(stack.ComposeFile)
		}
	}

	return paths
}

func WriteConfigFile(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid service configuration: %w", err)
	}

	encoded, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to encode service config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create directory for %q: %w", path, err)
	}

	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("failed to write %q: %w", path, err)
	}

	return nil
}

func ReadConfigFile(path string) (Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("failed to read %q: %w", path, err)
	}

	var cfg Config
	if err := toml.Unmarshal(content, &cfg); err != nil {
		return Config{}, fmt.Errorf("failed to parse %q: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid service configuration in %q: %w", path, err)
	}

	return cfg, nil
}
