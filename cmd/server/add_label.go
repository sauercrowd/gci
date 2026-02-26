package server

import (
	"context"
	"fmt"

	gcissh "github.com/sauercrowd/gci/ssh"
	"github.com/spf13/cobra"
)

func newAddLabelCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "add-label <server> <key=value>",
		Short:        "Add a Docker Swarm node label on a server",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := ResolveServer(args[0])
			if err != nil {
				return err
			}

			key, value, err := parseLabelPair(args[1])
			if err != nil {
				return err
			}

			target := gcissh.Target{
				User:           srv.User,
				Host:           srv.Host,
				PrivateKeyPath: srv.PrivateKey,
				Timeout:        connectTimeout,
			}

			updateCtx, cancel := context.WithTimeout(cmd.Context(), connectTimeout*2)
			defer cancel()

			if err := addNodeLabel(updateCtx, target, key, value); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "added label %q to server %q\n", key, srv.Name)
			return nil
		},
	}
}
