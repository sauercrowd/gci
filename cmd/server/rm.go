package server

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRmCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "rm <name>",
		Short:        "Remove a server",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			entries, err := load()
			if err != nil {
				return err
			}

			filtered := make([]entry, 0, len(entries))
			removed := false
			for _, server := range entries {
				if entryName(server) == name {
					removed = true
					continue
				}
				filtered = append(filtered, server)
			}

			if !removed {
				return fmt.Errorf("server %q not found", name)
			}

			if err := save(filtered); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "removed server %q\n", name)
			return nil
		},
	}
}
