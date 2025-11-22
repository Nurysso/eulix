package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"eulix/internal/fixers"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "eulix",
	Short: "Eulix - AI-powered code assistant",
	Long:  `Eulix is an intelligent CLI tool for understanding and querying your codebase.`,
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
}

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Analyze codebase and generate knowledge base",
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := analyzeProject("."); err != nil {
			fmt.Fprintf(os.Stderr, "Analysis failed: %v\n", err)
			os.Exit(1)
		}
	},
}
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage eulix configuration",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Configuration management coming soon!")
	},
}

var glaDOSCmd = &cobra.Command{
    Use:   "glados [directory]",
    Short: "Checks for errors in knowledge base and embeddings size",
    Args:  cobra.MaximumNArgs(1),
    Run: func(cmd *cobra.Command, args []string) {
        eulixDir := ".eulix"
        if len(args) > 0 {
            eulixDir = args[0]
        }

        if err := fixers.GLaDOS(eulixDir); err != nil {
            fmt.Fprintf(os.Stderr, "holy 🐄... Even Doctor failed\n")
            os.Exit(1)
        }
    },
}

var aspirineCmd = &cobra.Command{
    Use:   "aspirine [directory]",
    Short: "Rebuild embeddings.bin from embeddings.json",
    Long:  "Fixes corrupted or mismatched embeddings by rebuilding the binary file from JSON",
    Args:  cobra.MaximumNArgs(1),
    Run: func(cmd *cobra.Command, args []string) {
        eulixDir := ".eulix"
        if len(args) > 0 {
            eulixDir = args[0]
        }

        // Get flags
        noBackup, _ := cmd.Flags().GetBool("no-backup")
        force, _ := cmd.Flags().GetBool("force")

        opts := fixers.AspirineOptions{
            NoBackup: noBackup,
            Force:    force,
        }

        if err := fixers.Aspirine(eulixDir, opts); err != nil {
            fmt.Fprintf(os.Stderr, "Failed to rebuild embeddings: %v\n", err)
            os.Exit(1)
        }
    },
}

func init() {
    aspirineCmd.Flags().Bool("no-backup", false, "Don't backup existing embeddings.bin")
    aspirineCmd.Flags().Bool("force", false, "Force rebuild even if validations fail")

    rootCmd.AddCommand(aspirineCmd)
}

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start interactive chat interface",
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := startChat(); err != nil {
			fmt.Fprintf(os.Stderr, "Chat failed: %v\n", err)
			os.Exit(1)
		}
	},
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

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Disable default help command
	rootCmd.SetHelpCommand(&cobra.Command{
		Use:    "no-help",
		Hidden: true,
	})

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(analyzeCmd)
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(glaDOSCmd)
	rootCmd.AddCommand(aspirineCmd)
}

// checkInitialized verifies that eulix has been initialized in the current directory
func checkInitialized() error {
	// Check for .eulix directory
	eulixDir := ".eulix"
	if _, err := os.Stat(eulixDir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "\nEulix not initialized in this directory\n\n")
		fmt.Fprintf(os.Stderr, "Please run: eulix init\n\n")
		os.Exit(1)
	}

	// Check for .euignore file
	euignorePath := ".euignore"
	if _, err := os.Stat(euignorePath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "\n.euignore file missing\n\n")
		fmt.Fprintf(os.Stderr, "Please run: eulix init, or create a .euignore file similar to .gitignore\n\n")
		os.Exit(1)
	}

	// Check for eulix.toml (required for configuration)
	configPath := "eulix.toml"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "\neulix.toml configuration file missing\n\n")
		fmt.Fprintf(os.Stderr, "This file is required for eulix to run properly.\n")
		fmt.Fprintf(os.Stderr, "Please run: eulix init\n\n")
		os.Exit(1)
	}

	return nil
}

// isInitialized checks if eulix has been initialized (helper function)
func isInitialized() bool {
	eulixDir := ".eulix"
	euignorePath := ".euignore"

	_, eulixErr := os.Stat(eulixDir)
	_, euignoreErr := os.Stat(euignorePath)

	return eulixErr == nil && euignoreErr == nil
}

// getKnowledgeBasePath returns the path to the knowledge base files
func getKnowledgeBasePath() string {
	return filepath.Join(".eulix", "kb.json")
}

// hasKnowledgeBase checks if the knowledge base has been generated
func hasKnowledgeBase() bool {
	kbPath := getKnowledgeBasePath()
	_, err := os.Stat(kbPath)
	return err == nil
}
