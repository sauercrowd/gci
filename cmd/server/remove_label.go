package server

import (
	"context"
	"fmt"
	"strings"

	gcissh "github.com/sauercrowd/gci/ssh"
	"github.com/spf13/cobra"
)

func newRemoveLabelCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "remove-label <server> <key>",
		Short:        "Remove a Docker Swarm node label from a server",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := ResolveServer(args[0])
			if err != nil {
				return err
			}

			key := strings.TrimSpace(args[1])
			if key == "" {
				return fmt.Errorf("label key must not be empty")
			}

			target := gcissh.Target{
				User:           srv.User,
				Host:           srv.Host,
				PrivateKeyPath: srv.PrivateKey,
				Timeout:        connectTimeout,
			}

			updateCtx, cancel := context.WithTimeout(cmd.Context(), connectTimeout*2)
			defer cancel()

			if err := removeNodeLabel(updateCtx, target, key); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "removed label %q from server %q\n", key, srv.Name)
			return nil
		},
	}
}
