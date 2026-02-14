package service

import (
	"context"
	"fmt"
	"io"
)

type Driver interface {
	Name() string
	Validate(cfg Config) error
	Deploy(ctx context.Context, runner RemoteRunner, cfg Config, remoteServiceDir string, stdout, stderr io.Writer) error
	Remove(ctx context.Context, runner RemoteRunner, cfg Config, remoteServiceDir string) error
	Logs(ctx context.Context, runner RemoteRunner, cfg Config, lines int) (CommandResult, error)
	LogsStream(ctx context.Context, runner RemoteRunner, cfg Config, lines int, stdout, stderr io.Writer) error
	Status(ctx context.Context, runner RemoteRunner, cfg Config) (CommandResult, error)
	Doctor(ctx context.Context, runner RemoteRunner, cfg Config) error
}

type CommandResult struct {
	Stdout string
	Stderr string
}

type RemoteRunner interface {
	Run(ctx context.Context, command string) (CommandResult, error)
	Stream(ctx context.Context, command string, stdout, stderr io.Writer) error
}

func ResolveDriver(cfg Config) (Driver, error) {
	count := 0
	var resolved Driver

	if cfg.DriverDockerSwarm != nil {
		count++
		resolved = dockerSwarmDriver{}
	}

	if count == 0 {
		return nil, fmt.Errorf("driver configuration is required (e.g. driver_docker_swarm)")
	}
	if count > 1 {
		return nil, fmt.Errorf("multiple driver configurations provided; exactly one driver is allowed")
	}

	return resolved, nil
}
