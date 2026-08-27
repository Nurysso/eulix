//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package cli provides the command-line interface implementation for EULIX.
/*
This file is responsible for managing all the commands executed/supported
by eulix and serves as a entry point for the project
*/
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"eulix/internal/cache"
	"eulix/internal/checksum"
	"eulix/internal/config"
	"eulix/internal/embeddings"
	"eulix/internal/fixers"
	"eulix/internal/llm"
	"eulix/internal/query"
	"eulix/internal/utils"

	"github.com/spf13/cobra"
)

var (
	force bool
	fix   bool
)

var rootCmd = &cobra.Command{
	Use:   "eulix",
	Short: "eulix - Turn your codebase into a searchable book",
	Long: `eulix is an intelligent CLI tool for understanding and querying your codebase.

Turn your codebase into a searchable book. Ask questions about your code,
get accurate answers using local/cloud ML and LLMs.

eulix is currently in beta.`,
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
}

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Analyze codebase and generate knowledge base",
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := analyzeProject("."); err != nil {
			fmt.Fprintf(os.Stderr, "Analysis failed: %v\n", err)
			os.Exit(1)
		}
	},
}

var checksumCmd = &cobra.Command{
	Use:   "checksum",
	Short: "Creates checksum without running analyze",
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		detector := checksum.HashHound(".")

		currentChecksum, err := detector.Calculate()
		if err != nil {
			return fmt.Errorf("checksum calculation failed: %w", err)
		}

		fmt.Printf("   Found: %d files\n", currentChecksum.TotalFiles)

		if err := detector.Save(currentChecksum); err != nil {
			return fmt.Errorf("failed to save checksum: %w", err)
		}

		return nil
	},
}

var versionCMD = &cobra.Command{
	Use:   "version",
	Short: "Displays version of eulix and eulix_parser, eulix_embed",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("%s: %s\n", utils.AppName, utils.AppVersion)

		homeDir, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "eulix_parser: [Error] failed to get user home directory: %v\n", err)
		} else {
			parserPath := filepath.Join(homeDir, ".Eulix", "bin", "eulix_parser")
			if output, err := runSubCommand(parserPath, "--version"); err != nil {
				fmt.Fprintf(os.Stderr, "eulix_parser: [Error] %v\n", err)
			} else {
				fmt.Printf("%s", output)
			}
		}

		scriptPath, pythonPath, venvEnv, err := embeddings.FindEulixEmbed()
		if err != nil {
			fmt.Fprintf(os.Stderr, "eulix_embed: [Error] %v\n", err)
			return
		}
		proc := exec.Command(pythonPath, scriptPath, "--version")
		proc.Env = venvEnv
		if output, err := proc.Output(); err != nil {
			fmt.Fprintf(os.Stderr, "eulix_embed: [Error] %v\n", err)
		} else {
			fmt.Printf("eulix_embed: %s\n", strings.TrimSpace(string(output)))
		}
	},
}

var embedCMD = &cobra.Command{
	Use:                "embed [flags]",
	Short:              "Run the eulix_embed pipeline (Python venv)",
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		scriptPath, pythonPath, venvEnv, err := embeddings.FindEulixEmbed()
		if err != nil {
			fmt.Fprintf(os.Stderr, "eulix embed: setup failed: %v\n", err)
			os.Exit(1)
		}

		// Build: python3 ~/.Eulix/eulix_embed.py [args...]
		cmdArgs := append([]string{scriptPath}, args...)
		proc := exec.Command(pythonPath, cmdArgs...)
		proc.Env = venvEnv
		proc.Stdin = os.Stdin
		proc.Stdout = os.Stdout
		proc.Stderr = os.Stderr

		if err := proc.Run(); err != nil {
			// Preserve the Python exit code if available
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			fmt.Fprintf(os.Stderr, "eulix embed: run failed: %v\n", err)
			os.Exit(1)
		}
	},
}

// TODO pass through flags to rust bin
//
//nolint:unused
var parserCMD = &cobra.Command{
	Use:                "embed [flags]",
	Short:              "wrapper around eulix_parser",
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		scriptPath, pythonPath, venvEnv, err := embeddings.FindEulixEmbed()
		if err != nil {
			fmt.Fprintf(os.Stderr, "eulix embed: setup failed: %v\n", err)
			os.Exit(1)
		}

		cmdArgs := append([]string{scriptPath}, args...)
		proc := exec.Command(pythonPath, cmdArgs...)
		proc.Env = venvEnv
		proc.Stdin = os.Stdin
		proc.Stdout = os.Stdout
		proc.Stderr = os.Stderr

		if err := proc.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			fmt.Fprintf(os.Stderr, "eulix embed: run failed: %v\n", err)
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

var queryCmd = &cobra.Command{
	Use:   "query [question]",
	Short: "Build context prompt for LLM queries, or answer non‑LLM queries directly",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Please provide a query.")
			return
		}
		userQuery := strings.Join(args, " ")

		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
			return
		}
		eulixDir := ".eulix"
		// checksum check (todo)

		llmClient, err := llm.MouthClient(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "LLM init failed: %v\n", err)
			return
		}
		cacheManager, _ := cache.CacheController(cfg)

		router, err := query.QueryTrafficController(eulixDir, cfg, llmClient, cacheManager)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to init router: %v\n", err)
			return
		}
		defer func() { _ = router.Close() }()

		result, err := router.PromptOrAnswer(userQuery)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return
		}
		if cfg.Project.DebugConfig {
			if err := writeQueryDebugLog(eulixDir, userQuery, result); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to write query debug log: %v\n", err)
			}
		}
		fmt.Println(result)
	},
}

var glaDOSCmd = &cobra.Command{
	Use:   "glados [directory]",
	Short: "Checks for errors in knowledge base and embeddings size",
	Args:  cobra.MaximumNArgs(1),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			if _, err := requireProjectRoot(); err != nil {
				return err
			}
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		eulixDir := ".eulix"
		if len(args) > 0 {
			eulixDir = args[0]
		}

		if err := fixers.GLaDOS(eulixDir); err != nil {
			fmt.Fprintf(os.Stderr, "holy [moooo]... Even Doctor failed\n")
			os.Exit(1)
		}
	},
}

var aspirineCmd = &cobra.Command{
	Use:   "aspirine [directory]",
	Short: "tries to fix embedings.bin and kb MEANT TO BE USED IN TEST",
	Long:  "Tries to fixes corrupted or mismatched embeddings by rebuilding the binary file from JSON",
	Args:  cobra.MaximumNArgs(1),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			if _, err := requireProjectRoot(); err != nil {
				return err
			}
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		eulixDir := ".eulix"
		if len(args) > 0 {
			eulixDir = args[0]
		}

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

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start interactive chat interface",
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		root, err := requireProjectRoot()
		if err != nil {
			return err
		}
		if err := checkInitialized(); err != nil {
			return err
		}
		// Chat needs an actual knowledge base, not just the .eulix skeleton.
		if !hasKnowledgeBase(root) {
			return fmt.Errorf("no knowledge base found at %s\nRun 'eulix analyze' to generate it", getKnowledgeBasePath(root))
		}
		return nil
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
	Short: "Initialize eulix in the current directory",
	Run: func(cmd *cobra.Command, args []string) {
		var targets []string
		if fix && !force {
			state := checkInitState()
			targets = state.missing()
		}

		if err := initializeProject(force, targets); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

//  history command group

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Browse query history",
	Long:  "List, inspect, and delete recorded query/response turns.",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	// Default action with no sub-command: launch TUI (or list if --no-tui).
	Run: func(cmd *cobra.Command, args []string) {
		noTUI, _ := cmd.Flags().GetBool("no-tui")
		if noTUI {
			if err := HistoryList(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}
		if err := HistoryView(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

var historyListCmd = &cobra.Command{
	Use:   "list",
	Short: "Print all history entries (plain text)",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := HistoryList(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

var historyShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Print full detail for one history entry",
	Args:  cobra.ExactArgs(1),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := HistoryShow(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

var historyViewCmd = &cobra.Command{
	Use:   "view",
	Short: "Launch the interactive TUI history viewer",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := HistoryView(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

var historyDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a history entry by ID",
	Args:  cobra.ExactArgs(1),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := HistoryDelete(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

var historyClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Delete all history entries",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := HistoryClear(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

//  cache command group (kept for backwards compat, thin wrappers)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Alias for 'eulix history' (kept for backwards compatibility)",
	Long:  "The cache command is depriciated. Use 'eulix history' going forward.",
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	setupFlags()
	setupHistoryCommands()
	setupCacheCommands()
	registerCommands()
}

func setupFlags() {
	// init flags
	initCmd.Flags().BoolVarP(&force, "force", "f", false, "Reset and overwrite all eulix files")
	initCmd.Flags().BoolVar(&fix, "fix", false, "Create only missing components, keep existing ones")

	// aspirine flags
	aspirineCmd.Flags().Bool("no-backup", false, "Don't backup existing embeddings.bin")
	aspirineCmd.Flags().Bool("force", false, "Force rebuild even if validations fail")

	// history flags
	historyCmd.Flags().Bool("no-tui", false, "Use plain-text output instead of the TUI")
}

func setupHistoryCommands() {
	historyCmd.AddCommand(
		historyListCmd,
		historyShowCmd,
		historyViewCmd,
		historyDeleteCmd,
		historyClearCmd,
	)
}

func setupCacheCommands() {
	cacheCmd.AddCommand()
}

func registerCommands() {
	rootCmd.AddCommand(
		versionCMD,
		checksumCmd,
		initCmd,
		analyzeCmd,
		chatCmd,
		queryCmd,
		configCmd,
		glaDOSCmd,
		aspirineCmd,
		historyCmd,
		cacheCmd,
		embedCMD,
	)
}
