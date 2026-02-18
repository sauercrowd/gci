package cmd

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
)

//go:embed agents.md
var llmDocs string

var docsCmd = &cobra.Command{
	Use:     "agents.md",
	Aliases: []string{"docs"},
	Short:   "Read this if you are an LLM",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprint(cmd.OutOrStdout(), llmDocs)
	},
}

func init() {
	rootCmd.AddCommand(docsCmd)
}
