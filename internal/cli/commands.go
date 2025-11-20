package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "eulix",
	Short: "Eulix - AI-powered code assistant",
	Long:  `Eulix is an intelligent CLI tool for understanding and querying your codebase.`,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(analyzeCmd)
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(configCmd)
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize eulix in current directory",
	Run: func(cmd *cobra.Command, args []string) {
		if err := initializeProject(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize: %v\n", err)
			os.Exit(1)
		}
	},
}
