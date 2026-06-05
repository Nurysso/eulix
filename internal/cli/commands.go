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
	"eulix/internal/fixers"

	"github.com/spf13/cobra"
)

const (
	AppName    = "Eulix"
	AppVersion = "v0.6.5"
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
	PreRunE: func(cmd *cobra.Command, args []string) error {
		// glados accepts an explicit directory; only auto-discover when none given.
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
			return fmt.Errorf("no knowledge base found at %s\nRun 'eulix analyze' to generate it.", getKnowledgeBasePath(root))
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
	// init intentionally does NOT call requireProjectRoot — it bootstraps
	// whatever directory the user is sitting in.
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
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		// Delegate to history.go — CacheHistory prints the plain-text list.
		if err := CacheHistory(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

var cacheStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show cache statistics",
	Long:  "Display statistics about cache usage and storage",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := CacheStats(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

var cacheClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear all cache entries",
	Long:  "Remove all cached queries and responses",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		// CacheClear handles its own --force / confirmation prompt.
		if clearForce, _ := cmd.Flags().GetBool("force"); clearForce {
			// Skip the interactive prompt by bypassing stdin check.
			// CacheClear always prompts, so handle force here.
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
			return
		}
		if err := CacheClear(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

var cacheDeleteCmd = &cobra.Command{
	Use:   "delete <query-hash>",
	Short: "Delete a specific cache entry",
	Long:  "Remove a cache entry by its query hash",
	Args:  cobra.ExactArgs(1),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := CacheDelete(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

var cacheCleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove expired cache entries",
	Long:  "Clean up cache by removing all expired entries",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := CacheCleanup(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

// history command (launches TUI)
var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "View query history interactively",
	Long:  "Launch an interactive TUI to browse your cached query history",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if _, err := requireProjectRoot(); err != nil {
			return err
		}
		return checkInitialized()
	},
	Run: func(cmd *cobra.Command, args []string) {
		noTUI, _ := cmd.Flags().GetBool("no-tui")
		if noTUI {
			if err := CacheHistory(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}
		if err := CacheView(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
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

// Project-root discovery + initialization helpers

// projectMarkerFiles lists the files/directories that mark a directory as
// the root of an eulix project. Either one is enough.
var projectMarkerFiles = []string{".eulix", ".euignore"}

// findProjectRoot walks up from startDir looking for any project marker. It
// returns the absolute path of the directory that contains one, or an error
// if the search reaches the filesystem root without a hit.
func findProjectRoot(startDir string) (string, error) {
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}

	dir := abs
	for {
		for _, marker := range projectMarkerFiles {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf(
				"not inside an eulix project (no %s found in %s or any parent directory)\n"+
					"Run 'eulix init' in your project root, or 'cd' into an initialized project",
				strings.Join(projectMarkerFiles, " or "), abs,
			)
		}
		dir = parent
	}
}

// requireProjectRoot discovers the project root (walking up parent dirs as
// needed) and chdirs into it so all subsequent code that uses relative
// paths like ".eulix" or ".euignore" keeps working. It returns the absolute
// path of the root.
func requireProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get cwd: %w", err)
	}

	root, err := findProjectRoot(cwd)
	if err != nil {
		return "", err
	}

	if root != cwd {
		if err := os.Chdir(root); err != nil {
			return "", fmt.Errorf("found project root at %s but failed to chdir: %w", root, err)
		}
	}
	return root, nil
}

// isInitialized reports whether the given project root has been initialized.
// A root is considered initialized when both the .eulix directory and the
// .euignore file exist there.
func isInitialized(root string) bool {
	if root == "" {
		return false
	}
	_, eulixErr := os.Stat(filepath.Join(root, ".eulix"))
	_, euignoreErr := os.Stat(filepath.Join(root, ".euignore"))
	return eulixErr == nil && euignoreErr == nil
}

// getKnowledgeBasePath returns the absolute path to the knowledge base file
// inside the given project root.
func getKnowledgeBasePath(root string) string {
	if root == "" {
		// Best-effort fallback for callers that don't yet have a root.
		cwd, _ := os.Getwd()
		root = cwd
	}
	return filepath.Join(root, ".eulix", "kb.json")
}

// hasKnowledgeBase reports whether the knowledge base file exists inside
// the given project root.
func hasKnowledgeBase(root string) bool {
	kbPath := getKnowledgeBasePath(root)
	_, err := os.Stat(kbPath)
	return err == nil
}

// Initialization check

// checkInitialized returns nil if the current directory is a fully
// initialized eulix project, or a descriptive error otherwise. It is
// intended to be used from a command's PreRunE.
func checkInitialized() error {
	// Quick check: do both marker files exist in cwd? After
	// requireProjectRoot has run, cwd is the project root.
	if !isInitialized(".") {
		return fmt.Errorf(
			"\nEulix is not fully initialized in the current directory.\n" +
				"Run 'eulix init' to set it up, or 'cd' into a project that has been initialized.\n",
		)
	}

	// Deeper check: are the internal components the user actually needs
	// (kb, embeddings, ...) present?
	state := checkInitState()
	if state.fullyInitialized() {
		return nil
	}

	missing := state.missing()
	var b strings.Builder
	b.WriteString("\nEulix is not fully initialized. Missing:\n")
	for _, m := range missing {
		b.WriteString("  - " + m + "\n")
	}
	b.WriteString("\nRun 'eulix init --fix' to restore missing files, or 'eulix init --force' to reset.\n\n")
	return fmt.Errorf("%s", b.String())
}

// Misc helpers

func runSubCommand(name string, arg string) (string, error) {
	cmd := exec.Command(name, arg)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
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
