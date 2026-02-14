package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	gcissh "github.com/sauercrowd/gci/ssh"
)

type entry struct {
	Name       string `toml:"name"`
	User       string `toml:"user"`
	Host       string `toml:"host"`
	PrivateKey string `toml:"private_key"`
	ServiceDir string `toml:"service_dir,omitempty"`
}

type file struct {
	Servers []entry `toml:"servers"`
}

func filePath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}

	return filepath.Join(homeDir, ".gci", "servers.toml"), nil
}

func load() ([]entry, error) {
	path, err := filePath()
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []entry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	if len(strings.TrimSpace(string(content))) == 0 {
		return []entry{}, nil
	}

	var data file
	if err := toml.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	return data.Servers, nil
}

func save(entries []entry) error {
	path, err := filePath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data := file{Servers: entries}
	encoded, err := toml.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to encode server config: %w", err)
	}

	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}

	return nil
}

func expandUserPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" {
		return os.UserHomeDir()
	}
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}

	return filepath.Join(homeDir, path[2:]), nil
}

func entryName(server entry) string {
	if server.Name != "" {
		return server.Name
	}
	return server.Host
}

func entryTarget(server entry) gcissh.Target {
	return gcissh.Target{
		User:           server.User,
		Host:           server.Host,
		PrivateKeyPath: server.PrivateKey,
	}
}

func entryServiceDir(server entry) string {
	if server.ServiceDir != "" {
		return server.ServiceDir
	}
	return defaultServiceDir
}
