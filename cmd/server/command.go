package server

import "github.com/spf13/cobra"

func NewCommand() *cobra.Command {
	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Manage server entries",
	}

	serverCmd.AddCommand(newAddCommand())
	serverCmd.AddCommand(newAddLabelCommand())
	serverCmd.AddCommand(newExecCommand())
	serverCmd.AddCommand(newLsCommand())
	serverCmd.AddCommand(newRmCommand())
	serverCmd.AddCommand(newRemoveLabelCommand())
	serverCmd.AddCommand(newStatusCommand())

	return serverCmd
}
