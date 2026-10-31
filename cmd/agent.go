package cmd

import "github.com/spf13/cobra"

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:    "agent",
		Hidden: true,
		Short:  "Run a host-side GCI operation",
	})
}
