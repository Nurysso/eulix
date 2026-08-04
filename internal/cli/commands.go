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
	"eulix/internal/embeddings"
	"eulix/internal/fixers"
	"eulix/internal/llm"
	"eulix/internal/query"

	// "eulix/internal/llm"

	"github.com/spf13/cobra"
)

const (
	AppName    = "eulix"
	AppVersion = "v0.7.4"
)

var (
	force bool
	fix   bool
)

var rootCmd = &cobra.Command{
	Use:   "eulix",
	Short: "eulix [Beta] - Turn your codebase into a searchable book",
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
		fmt.Printf("%s: %s\n", AppName, AppVersion)

		// eulix_parser native binary, call directly
		if output, err := runSubCommand("eulix", "--version"); err != nil {
			fmt.Fprintf(os.Stderr, "eulix_parser: [Error] %v\n", err)
		} else {
			fmt.Printf("%s", output)
		}

		// eulix_embed Python script, must go through the venv
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
	Use:   "embed [flags]",
	Short: "Run the eulix_embed pipeline (Python venv)",
	// Pass-through flags forwarded to eulix_embed.py
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

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage eulix configuration",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Configuration management coming soon!")
	},
}

// cmd/query.go (or wherever the command is defined)

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

		// Load config, LLM client, cache, etc. (same as startChat)
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
		defer router.Close()

		// Get either the prompt or the direct answer
		// answer from non llm queries and prompt for llm based queries
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
	// disableDefaultHelp()
	registerCommands()
}

// writeQueryDebugLog appends the final query output to a query-debug log
// file under eulixDir, mirroring llm.go's llmdebug.log format.
func writeQueryDebugLog(eulixDir, query, result string) error {
	if err := os.MkdirAll(eulixDir, 0755); err != nil {
		return fmt.Errorf("failed to create debug directory: %w", err)
	}

	logFile := filepath.Join(eulixDir, "debug", "query-debug.log")
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open debug log file: %w", err)
	}
	defer f.Close()

	entry := fmt.Sprintf("\n%s\n=== QUERY DEBUG ENTRY ===\nTimestamp: %s\n\n=== QUERY ===\n%s\n=== END QUERY ===\n\n=== OUTPUT ===\n%s\n=== END OUTPUT ===\n%s\n",
		strings.Repeat("=", 80),
		time.Now().Format("2006-01-02 15:04:05"),
		query,
		result,
		strings.Repeat("=", 80))

	_, err = f.WriteString(entry)
	return err
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

// func disableDefaultHelp() {
// 	rootCmd.SetHelpCommand(&cobra.Command{
// 		Use:    "no-help",
// 		Hidden: true,
// 	})
// }

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
		cacheCmd,
		historyCmd,
		embedCMD,
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
