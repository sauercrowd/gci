package server

import (
	"fmt"
	"os"
	"strings"

	gcissh "github.com/sauercrowd/gci/ssh"
	"github.com/spf13/cobra"
)

func newExecCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "exec <server> [command] [args...]",
		Short:        "Execute a command on a server over SSH",
		SilenceUsage: true,
		Args:         cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := ResolveServer(args[0])
			if err != nil {
				return err
			}

			target := gcissh.Target{
				User:           srv.User,
				Host:           srv.Host,
				PrivateKeyPath: srv.PrivateKey,
				Timeout:        connectTimeout,
			}

			remoteCommand := defaultRemoteShellCommand()
			if len(args) > 1 {
				remoteCommand = joinRemoteCommandArgs(args[1:])
			}

			if err := gcissh.RunCommandTerminal(cmd.Context(), target, remoteCommand, os.Stdin, os.Stdout, os.Stderr); err != nil {
				if len(args) > 1 {
					return fmt.Errorf("failed to execute command on server %q: %w", srv.Name, err)
				}
				return fmt.Errorf("failed to open shell on server %q: %w", srv.Name, err)
			}
			return nil
		},
	}
}

func defaultRemoteShellCommand() string {
	return `exec ${SHELL:-/bin/sh} -l`
}

func joinRemoteCommandArgs(args []string) string {
	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i] = shellQuote(arg)
	}
	return strings.Join(parts, " ")
}
