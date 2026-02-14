package server

import (
	"context"
	"fmt"
	"text/tabwriter"

	gcissh "github.com/sauercrowd/gci/ssh"
	"github.com/spf13/cobra"
)

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "status",
		Short:        "Check SSH reachability for configured servers",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := load()
			if err != nil {
				return err
			}

			if len(entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no servers configured")
				return nil
			}

			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tHOST\tSTATUS\tDETAIL")
			for _, server := range entries {
				checkCtx, cancel := context.WithTimeout(cmd.Context(), connectTimeout)
				err := gcissh.CheckReachable(checkCtx, entryTarget(server))
				cancel()

				status := "reachable"
				detail := "-"
				if err != nil {
					status = "unreachable"
					detail = err.Error()
				}

				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", entryName(server), server.Host, status, detail)
			}

			return tw.Flush()
		},
	}
}
