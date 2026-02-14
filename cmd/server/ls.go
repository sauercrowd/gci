package server

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newLsCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "ls",
		Short:        "List servers",
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
			fmt.Fprintln(tw, "NAME\tHOST\tUSER\tPRIVATE KEY\tSERVICE DIR")
			for _, server := range entries {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", entryName(server), server.Host, server.User, server.PrivateKey, entryServiceDir(server))
			}
			return tw.Flush()
		},
	}
}
