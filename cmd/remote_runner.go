package cmd

import (
	"context"
	"io"

	"github.com/sauercrowd/gci/service"
	gcissh "github.com/sauercrowd/gci/ssh"
)

type sshRemoteRunner struct {
	target gcissh.Target
}

func newSSHRemoteRunner(target gcissh.Target) service.RemoteRunner {
	return sshRemoteRunner{target: target}
}

func (r sshRemoteRunner) Run(ctx context.Context, command string) (service.CommandResult, error) {
	result, err := gcissh.RunCommand(ctx, r.target, command)
	return service.CommandResult{
		Stdout: result.Stdout,
		Stderr: result.Stderr,
	}, err
}

func (r sshRemoteRunner) Stream(ctx context.Context, command string, stdout, stderr io.Writer) error {
	return gcissh.RunCommandStream(ctx, r.target, command, stdout, stderr)
}
