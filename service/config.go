package service

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
	gcissh "github.com/sauercrowd/gci/ssh"
)

type Config struct {
	Name              string             `toml:"name"`
	Server            string             `toml:"server,omitempty"`
	BuildCommand      string             `toml:"build_command"`
	BuildForwards     []string           `toml:"build_forwards,omitempty"`
	SyncPaths         []string           `toml:"sync_paths"`
	ExcludePatterns   []string           `toml:"exclude_patterns,omitempty"`
	DriverDockerSwarm *DockerSwarmConfig `toml:"driver_docker_swarm,omitempty"`
}

type DockerSwarmConfig struct {
	StackName        string   `toml:"stack_name"`
	LogServices      []string `toml:"log_services,omitempty"`
	ComposeFile      string   `toml:"compose_file"`
	MigrationService string   `toml:"migration_service,omitempty"`
	MigrationStrict  bool     `toml:"migration_strict,omitempty"`
	PruneImages      *bool    `toml:"prune_images,omitempty"`
}

func (c DockerSwarmConfig) PruneImagesEnabled() bool {
	return c.PruneImages == nil || *c.PruneImages
}

func NewDefaultConfig(serviceName, serverName string) Config {
	pruneImages := true

	return Config{
		Name:         serviceName,
		Server:       serverName,
		BuildCommand: "go build ./...",
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
			StackName:        serviceName,
			LogServices:      []string{"app", "migrate"},
			ComposeFile:      "docker-compose.prod.yaml",
			MigrationService: "migrate",
			PruneImages:      &pruneImages,
		},
	}
}

func (c Config) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.BuildCommand == "" {
		return fmt.Errorf("build_command is required")
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
