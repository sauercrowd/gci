package cmd

import "github.com/sauercrowd/gci/cmd/server"

func init() {
	rootCmd.AddCommand(server.NewCommand())
}
