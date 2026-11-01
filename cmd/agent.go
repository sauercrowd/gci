package cmd

import "github.com/spf13/cobra"

func init() {
	agent := &cobra.Command{Use: "agent", Short: "Host-side commands (invoked by the CLI)", Hidden: true}
	rootCmd.AddCommand(agent)
}
