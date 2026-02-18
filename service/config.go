package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	gcissh "github.com/sauercrowd/gci/ssh"
)

type Config struct {
	Name              string             `toml:"name"`
	Server            string             `toml:"server,omitempty"`
	BuildLocal        string             `toml:"build_local,omitempty"`
	BuildRemote       string             `toml:"build_remote,omitempty"`
	BuildForwards     []string           `toml:"build_forwards,omitempty"`
	SyncPaths         []string           `toml:"sync_paths"`
	ExcludePatterns   []string           `toml:"exclude_patterns,omitempty"`
	DriverDockerSwarm *DockerSwarmConfig `toml:"driver_docker_swarm,omitempty"`
}

type DockerSwarmConfig struct {
	AppNetwork  string             `toml:"app_network,omitempty"`
	LogStack    string             `toml:"log_stack,omitempty"`
	LogServices []string           `toml:"log_services,omitempty"`
	Stacks      []DockerSwarmStack `toml:"stacks"`
	PruneImages *bool              `toml:"prune_images,omitempty"`
}

type DockerSwarmStack struct {
	Name               string `toml:"name"`
	ComposeFile        string `toml:"compose_file"`
	Mode               string `toml:"mode,omitempty"`
	WaitTimeoutSeconds int    `toml:"wait_timeout_seconds,omitempty"`
}

func (c DockerSwarmConfig) PruneImagesEnabled() bool {
	return c.PruneImages == nil || *c.PruneImages
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

func NewDefaultConfig(serviceName, serverName string) Config {
	pruneImages := true

	return Config{
		Name:       serviceName,
		Server:     serverName,
		BuildLocal: "go build ./...",
		SyncPaths: []string{
			".",
		},
		ExcludePatterns: []string{
			".git",
			"node_modules",
			"__pycache__",
			"*.pyc",
		},
		DriverDockerSwarm: &DockerSwarmConfig{
			AppNetwork:  "auto",
			LogServices: []string{"app"},
			Stacks: []DockerSwarmStack{
				{
					Name:        serviceName,
					ComposeFile: "docker-compose.prod.yaml",
					Mode:        "services",
				},
			},
			PruneImages: &pruneImages,
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
	if len(c.SyncPaths) == 0 {
		return fmt.Errorf("at least one sync path is required")
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
