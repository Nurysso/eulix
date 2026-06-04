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
	"time"

	"eulix/internal/cache"
	"eulix/internal/checksum"
	"eulix/internal/config"
	"eulix/internal/fixers"
	"eulix/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

const (
	AppName    = "Eulix"
	AppVersion = "v0.6.3"
)

var (
	force bool
	fix   bool
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

var checksumCmd = &cobra.Command{
	Use:   "checksum",
	Short: "Creates checksum without running analyze",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		detector := checksum.HashHound(".")
		currentChecksum, err := detector.Calculate()
		if err != nil {
			// return fmt.Errorf("checksum calculation failed: %w", err)
		}
		fmt.Printf("   Found: %d files\n", currentChecksum.TotalFiles)
		if err := detector.Save(currentChecksum); err != nil {
			// return fmt.Errorf("failed to save checksum: %w", err)
		}
		os.Exit(1)
	},
}

var versionCMD = &cobra.Command{
	Use:   "version",
	Short: "Displays version of eulix and eulix_parser, eulix_embed",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("%s: %s \n", AppName, AppVersion)

		components := []string{"eulix_parser", "eulix_embed"}

		for _, bin := range components {
			output, err := runSubCommand(bin, "--version")
			if err != nil {
				fmt.Printf("%s: [Error] %v\n", bin, err)
				continue
			}
			fmt.Printf("%s: %s", bin, output)
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
	Short: "Initialize eulix in the current directory",
	Run: func(cmd *cobra.Command, args []string) {
		// --fix: create only missing components, never overwrite existing ones
		var targets []string
		if fix && !force {
			state := checkInitState()
			targets = state.missing() // passes the human-readable names — adapt if needed
		}

		if err := initializeProject(force, targets); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

// Cache command group

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Manage cache entries",
	Long:  `View, manage, and interact with cached query responses`,
}

var cacheListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all cache entries",
	Long:  "Display all cached queries and their metadata",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		mgr, err := initCacheManager()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize cache: %v\n", err)
			os.Exit(1)
		}
		defer mgr.Close()

		entries, err := mgr.ListAll()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list cache entries: %v\n", err)
			os.Exit(1)
		}

		if len(entries) == 0 {
			fmt.Println("No cache entries found.")
			return
		}

		verbose, _ := cmd.Flags().GetBool("verbose")

		fmt.Printf("Found %d cache entries:\n\n", len(entries))
		for i, entry := range entries {
			fmt.Printf("[%d] Query Hash: %s\n", i+1, entry.QueryHash)
			fmt.Printf("    Created: %s\n", entry.CreatedAt.Format(time.RFC3339))
			fmt.Printf("    Expires: %s\n", entry.ExpiresAt.Format(time.RFC3339))

			if time.Now().After(entry.ExpiresAt) {
				fmt.Printf("    Status: EXPIRED\n")
			} else {
				fmt.Printf("    Status: Valid\n")
			}

			if verbose {
				fmt.Printf("    Query: %s\n", truncateString(entry.Query, 80))
				fmt.Printf("    Response: %s\n", truncateString(entry.Response, 100))
				fmt.Printf("    Checksum: %s\n", entry.ChecksumHash[:12])
			}
			fmt.Println()
		}
	},
}

var cacheStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show cache statistics",
	Long:  "Display statistics about cache usage and storage",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		mgr, err := initCacheManager()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize cache: %v\n", err)
			os.Exit(1)
		}
		defer mgr.Close()

		stats, err := mgr.GetStats()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get cache stats: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Cache Statistics:")

		if total, ok := stats["sql_total_entries"].(int); ok {
			fmt.Printf("SQL Total Entries: %d\n", total)
		}
		if valid, ok := stats["sql_valid_entries"].(int); ok {
			fmt.Printf("SQL Valid Entries: %d\n", valid)
		}
		if connected, ok := stats["redis_connected"].(bool); ok && connected {
			fmt.Println("Redis: Connected")
		}
	},
}

var cacheClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear all cache entries",
	Long:  "Remove all cached queries and responses",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		force, _ := cmd.Flags().GetBool("force")

		if !force {
			fmt.Print("Are you sure you want to clear all cache entries? (y/N): ")
			var response string
			fmt.Scanln(&response)
			if strings.ToLower(response) != "y" {
				fmt.Println("Operation cancelled.")
				return
			}
		}

		mgr, err := initCacheManager()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize cache: %v\n", err)
			os.Exit(1)
		}
		defer mgr.Close()

		entries, err := mgr.ListAll()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list entries: %v\n", err)
			os.Exit(1)
		}

		deleted := 0
		for _, entry := range entries {
			if err := mgr.Delete(entry.QueryHash); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to delete entry %s: %v\n", entry.QueryHash, err)
			} else {
				deleted++
			}
		}

		fmt.Printf("Successfully cleared %d cache entries.\n", deleted)
	},
}

var cacheDeleteCmd = &cobra.Command{
	Use:   "delete <query-hash>",
	Short: "Delete a specific cache entry",
	Long:  "Remove a cache entry by its query hash",
	Args:  cobra.ExactArgs(1),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		queryHash := args[0]

		mgr, err := initCacheManager()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize cache: %v\n", err)
			os.Exit(1)
		}
		defer mgr.Close()

		if err := mgr.Delete(queryHash); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to delete entry: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Successfully deleted cache entry: %s\n", queryHash)
	},
}

var cacheCleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove expired cache entries",
	Long:  "Clean up cache by removing all expired entries",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		mgr, err := initCacheManager()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize cache: %v\n", err)
			os.Exit(1)
		}
		defer mgr.Close()

		if err := mgr.CleanExpired(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to clean expired entries: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Successfully cleaned expired cache entries.")
	},
}

// var cacheHistoryCmd = &cobra.Command{
// 	Use:   "history",
// 	Short: "View cache entry history in detail",
// 	Long:  "Display detailed view of cached queries with full content",
// 	PreRunE: func(cmd *cobra.Command, args []string) error {
// 		return checkInitialized()
// 	},
// 	Run: func(cmd *cobra.Command, args []string) {
// 		runHistoryCommand(cmd)
// 	},
// }

// history command (launches TUI)
var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "View query history interactively",
	Long:  "Launch an interactive TUI to browse your cached query history",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		useTUI, _ := cmd.Flags().GetBool("tui")
		noTUI, _ := cmd.Flags().GetBool("no-tui")

		// Default to TUI unless --no-tui is specified
		if !noTUI || useTUI {
			runHistoryTUI(cmd)
		} else {
			runHistoryCommand(cmd)
		}
	},
}

func runHistoryCommand(cmd *cobra.Command) {
	mgr, err := initCacheManager()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize cache: %v\n", err)
		os.Exit(1)
	}
	defer mgr.Close()

	entries, err := mgr.ListAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load history: %v\n", err)
		os.Exit(1)
	}

	if len(entries) == 0 {
		fmt.Println("No history found. Your question history is empty.")
		return
	}

	fmt.Printf("Query History (%d entries):\n", len(entries))
	fmt.Println(strings.Repeat("=", 80))

	for i, entry := range entries {
		fmt.Printf("\n[Entry %d]\n", i+1)
		fmt.Printf("Hash: %s\n", entry.QueryHash)
		fmt.Printf("Created: %s\n", entry.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("Expires: %s\n", entry.ExpiresAt.Format("2006-01-02 15:04:05"))

		if time.Now().After(entry.ExpiresAt) {
			fmt.Printf("Status: EXPIRED \n")
		} else {
			remaining := time.Until(entry.ExpiresAt)
			fmt.Printf("Status: Valid (expires in %v)\n", remaining.Round(time.Minute))
		}

		fmt.Printf("\nQuery:\n%s\n", wrapText(entry.Query, 76))
		fmt.Printf("\nResponse:\n%s\n", wrapText(entry.Response, 76))
		fmt.Println(strings.Repeat("-", 80))
	}
}

// TUI implementation for history
func runHistoryTUI(cmd *cobra.Command) {
	mgr, err := initCacheManager()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize cache: %v\n", err)
		os.Exit(1)
	}
	defer mgr.Close()

	entries, err := mgr.ListAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load history: %v\n", err)
		os.Exit(1)
	}

	if len(entries) == 0 {
		fmt.Println("No history found. Your question history is empty.")
		return
	}

	// Launch the TUI
	model := tui.HistoryView(entries, mgr)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	setupFlags()
	setupCacheCommands()
	disableDefaultHelp()
	registerCommands()
}

func setupFlags() {
	// init flags
	initCmd.Flags().BoolVarP(&force, "force", "f", false, "Reset and overwrite all eulix files")
	initCmd.Flags().BoolVar(&fix, "fix", false, "Create only missing components, keep existing ones")

	// Aspirine flags
	aspirineCmd.Flags().Bool("no-backup", false, "Don't backup existing embeddings.bin")
	aspirineCmd.Flags().Bool("force", false, "Force rebuild even if validations fail")

	// Cache flags
	cacheListCmd.Flags().BoolP("verbose", "v", false, "Show detailed information")
	cacheClearCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")

	// History flags
	historyCmd.Flags().Bool("tui", false, "Force interactive TUI mode (default)")
	historyCmd.Flags().Bool("no-tui", false, "Use text output instead of TUI")
}

func setupCacheCommands() {
	cacheCmd.AddCommand(
		cacheListCmd,
		cacheStatsCmd,
		cacheClearCmd,
		cacheDeleteCmd,
		cacheCleanCmd,
	)
}

func disableDefaultHelp() {
	rootCmd.SetHelpCommand(&cobra.Command{
		Use:    "no-help",
		Hidden: true,
	})
}

func registerCommands() {
	rootCmd.AddCommand(
		versionCMD,
		checksumCmd,
		initCmd,
		analyzeCmd,
		chatCmd,
		configCmd,
		glaDOSCmd,
		aspirineCmd,
		cacheCmd,
		historyCmd,
	)
}

// Helper functions

func runSubCommand(name string, arg string) (string, error) {
	cmd := exec.Command(name, arg)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func checkInitialized() error {
	state := checkInitState()

	if state.fullyInitialized() {
		return nil
	}

	missing := state.missing()
	fmt.Fprintf(os.Stderr, "\nEulix is not fully initialized. Missing:\n")
	for _, m := range missing {
		fmt.Fprintf(os.Stderr, "  - %s\n", m)
	}
	fmt.Fprintf(os.Stderr, "\nRun 'eulix init --fix' to restore missing files, or 'eulix init --force' to reset.\n\n")
	os.Exit(1)

	return nil // unreachable, satisfies compiler
}

func isInitialized() bool {
	eulixDir := ".eulix"
	euignorePath := ".euignore"

	_, eulixErr := os.Stat(eulixDir)
	_, euignoreErr := os.Stat(euignorePath)

	return eulixErr == nil && euignoreErr == nil
}

func getKnowledgeBasePath() string {
	return filepath.Join(".eulix", "kb.json")
}

func hasKnowledgeBase() bool {
	kbPath := getKnowledgeBasePath()
	_, err := os.Stat(kbPath)
	return err == nil
}

// initCacheManager initializes and returns a cache manager
func initCacheManager() (*cache.Manager, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	if !cfg.Cache.Redis.Enabled && !cfg.Cache.SQL.Enabled {
		return nil, fmt.Errorf("cache is not enabled in configuration")
	}

	mgr, err := cache.CacheController(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cache manager: %w", err)
	}

	return mgr, nil
}

// truncateString truncates a string to maxLen characters
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// wrapText wraps text at the specified width
func wrapText(text string, width int) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}

	var lines []string
	var currentLine string

	for _, word := range words {
		if len(currentLine)+len(word)+1 > width {
			if currentLine != "" {
				lines = append(lines, currentLine)
				currentLine = word
			} else {
				lines = append(lines, word)
			}
		} else {
			if currentLine != "" {
				currentLine += " " + word
			} else {
				currentLine = word
			}
		}
	}

	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return strings.Join(lines, "\n")
}
