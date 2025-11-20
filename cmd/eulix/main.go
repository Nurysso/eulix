package main

import (
	"fmt"
	"os"

	"eulix/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// package main

// import (
//     "fmt"
//     "os"
//     "strings"
//     "github.com/spf13/cobra"
//     "eulix/internal/checksum"
//     "eulix/internal/config"
//     "eulix/internal/parser"
//     "eulix/internal/cli"
// )

// const (
//     Version = "0.5.0"
// )

// var (
//     rootCmd = &cobra.Command{
//         Use:   "eulix",
//         Short: "AI-powered codebase exploration",
//         // Long:  "Eulix helps you understand and explore codebases using AI",
//         Version: Version,
//     }

//     initCmd = &cobra.Command{
//         Use:   "init",
//         Short: "Initialize Eulix in current directory",
//         Run:   runInit,
//     }

//     parseCmd = &cobra.Command{
//         Use:   "parse",
//         Short: "Parse codebase and generate knowledge base, Embeddings",
//         Run:   runParse,
//     }

//     askCmd = &cobra.Command{
//         Use:   "ask [query]",
//         Short: "Ask questions about your codebase",
//         Args:  cobra.MinimumNArgs(1),
//         Run:   runAsk,
//     }

//     chatCmd = &cobra.Command{
//         Use:   "chat",
//         Short: "Start interactive chat session",
//         Run:   runChat,
//     }

//     historyCmd = &cobra.Command{
//         Use:   "history",
//         Short: "Show query history",
//         Run:   runHistory,
//     }

//     configCmd = &cobra.Command{
//         Use:   "config",
//         Short: "Manage configuration",
//     }

//     configSetCmd = &cobra.Command{
//         Use:   "set [key] [value]",
//         Short: "Set configuration value",
//         Args:  cobra.ExactArgs(2),
//         Run:   runConfigSet,
//     }

//     configShowCmd = &cobra.Command{
//         Use:   "show",
//         Short: "Show current configuration",
//         Run:   runConfigShow,
//     }

//     cacheCmd = &cobra.Command{
//         Use:   "cache",
//         Short: "Manage cache",
//     }

//     cacheStatsCmd = &cobra.Command{
//         Use:   "stats",
//         Short: "Show cache statistics",
//         Run:   runCacheStats,
//     }

//     cacheClearCmd = &cobra.Command{
//         Use:   "clear",
//         Short: "Clear cache",
//         Run:   runCacheClear,
//     }

//     versionCmd = &cobra.Command{
//         Use:   "version",
//         Short: "Show version information",
//         Run:   runVersion,
//     }
// )

// func init() {
//     // Add subcommands
//     rootCmd.AddCommand(initCmd)
//     rootCmd.AddCommand(parseCmd)
//     rootCmd.AddCommand(askCmd)
//     rootCmd.AddCommand(chatCmd)
//     rootCmd.AddCommand(historyCmd)
//     rootCmd.AddCommand(configCmd)
//     rootCmd.AddCommand(cacheCmd)
//     rootCmd.AddCommand(versionCmd)

//     configCmd.AddCommand(configSetCmd)
//     configCmd.AddCommand(configShowCmd)

//     cacheCmd.AddCommand(cacheStatsCmd)
//     cacheCmd.AddCommand(cacheClearCmd)

//     // Flags
//     parseCmd.Flags().BoolP("force", "f", false, "Force re-parse even if checksum matches")
//     parseCmd.Flags().Bool("ui", false, "Use interactive UI for parsing")
//     askCmd.Flags().BoolP("verbose", "v", false, "Verbose output")
//     chatCmd.Flags().BoolP("verbose", "v", false, "Verbose output")
// }

// func main() {
//     if err := rootCmd.Execute(); err != nil {
//         fmt.Fprintln(os.Stderr, err)
//         os.Exit(1)
//     }
// }

// func runInit(cmd *cobra.Command, args []string) {
//     if err := cli.RunInit(); err != nil {
//         fmt.Fprintf(os.Stderr, "⤫ Init failed: %v\n", err)
//         os.Exit(1)
//     }
// }

// func runParse(cmd *cobra.Command, args []string) {
//     force, _ := cmd.Flags().GetBool("force")
//     useUI, _ := cmd.Flags().GetBool("ui")

//     // Check if already initialized
//     if !config.IsInitialized() {
//         fmt.Println("⤫ Not initialized. Run: eulix init")
//         os.Exit(1)
//     }

//     // Check if re-parse needed
//     if !force {
//         needsReparse, err := checksum.NeedsReparse()
//         if err != nil {
//             fmt.Fprintf(os.Stderr, ":(  Checksum check failed: %v\n", err)
//         } else if !needsReparse {
//             fmt.Println("✓ Knowledge base is up to date")
//             return
//         }
//     }

//     // Use UI or simple output based on flag
//     if useUI {
//         // Run parse with UI
//         if err := cli.RunParse(); err != nil {
//             fmt.Fprintf(os.Stderr, "⤫ Parse failed: %v\n", err)
//             os.Exit(1)
//         }

//         // Run embeddings after UI parse completes
//         fmt.Println("\nGenerating embeddings...")
//         if err := parser.RunEmbed(); err != nil {
//             fmt.Fprintf(os.Stderr, "⤫ Failed to create embeddings: %v\n", err)
//             os.Exit(1)
//         }
//         fmt.Println("✓ Embeddings complete!")
//     } else {
//         fmt.Println("Parsing codebase...")

//         if err := parser.RunParser(); err != nil {
//             fmt.Fprintf(os.Stderr, "⤫ Parse failed: %v\n", err)
//             os.Exit(1)
//         }

//         fmt.Println("✓ Parse complete!")

//         fmt.Println("Generating embeddings...")
//         if err := parser.RunEmbed(); err != nil {
//             fmt.Fprintf(os.Stderr, "⤫ Failed to create embeddings: %v\n", err)
//             os.Exit(1)
//         }

//         fmt.Println("✓ Embeddings complete!")
//     }

//     // Save new checksum
//     if err := checksum.SaveChecksum(); err != nil {
//         fmt.Fprintf(os.Stderr, ":(  Failed to save checksum: %v\n", err)
//     }
// }

// func runAsk(cmd *cobra.Command, args []string) {
//     queryText := strings.Join(args, " ")
//     verbose, _ := cmd.Flags().GetBool("verbose")

//     // Check if initialized
//     if !config.IsInitialized() {
//         fmt.Println("⤫ Not initialized. Run: eulix init")
//         os.Exit(1)
//     }

//     // Auto-parse if needed
//     needsReparse, err := checksum.NeedsReparse()
//     if err != nil {
//         fmt.Fprintf(os.Stderr, ":(  Checksum check failed: %v\n", err)
//     } else if needsReparse {
//         fmt.Println("Codebase changed, re-parsing...")
//         if err := parser.RunParser(); err != nil {
//             fmt.Fprintf(os.Stderr, "⤫ Parse failed: %v\n", err)
//             os.Exit(1)
//         }

//         fmt.Println("Generating embeddings...")
//         if err := parser.RunEmbed(); err != nil {
//             fmt.Fprintf(os.Stderr, "⤫ Failed to create embeddings: %v\n", err)
//             os.Exit(1)
//         }
//         checksum.SaveChecksum()
//     }

//     // Run query with Bubbletea UI
//     if err := cli.RunAsk(queryText, verbose); err != nil {
//         fmt.Fprintf(os.Stderr, "⤫ Query failed: %v\n", err)
//         os.Exit(1)
//     }
// }

// func runChat(cmd *cobra.Command, args []string) {
//     verbose, _ := cmd.Flags().GetBool("verbose")

//     // Check if initialized
//     if !config.IsInitialized() {
//         fmt.Println("⤫ Not initialized. Run: eulix init")
//         os.Exit(1)
//     }

//     // Auto-parse if needed
//     needsReparse, err := checksum.NeedsReparse()
//     if err != nil {
//         fmt.Fprintf(os.Stderr, ":(  Checksum check failed: %v\n", err)
//     } else if needsReparse {
//         fmt.Println("Codebase changed, re-parsing...")
//         if err := parser.RunParser(); err != nil {
//             fmt.Fprintf(os.Stderr, "⤫ Parse failed: %v\n", err)
//             os.Exit(1)
//         }

//         fmt.Println("Generating embeddings...")
//         if err := parser.RunEmbed(); err != nil {
//             fmt.Fprintf(os.Stderr, "⤫ Failed to create embeddings: %v\n", err)
//             os.Exit(1)
//         }
//         checksum.SaveChecksum()
//     }

//     // Run chat with Bubbletea UI
//     if err := cli.RunChat(verbose); err != nil {
//         fmt.Fprintf(os.Stderr, "⤫ Chat failed: %v\n", err)
//         os.Exit(1)
//     }
// }

// func runHistory(cmd *cobra.Command, args []string) {
//     if err := cli.RunHistory(); err != nil {
//         fmt.Fprintf(os.Stderr, "⤫ History failed: %v\n", err)
//         os.Exit(1)
//     }
// }

// func runConfigSet(cmd *cobra.Command, args []string) {
//     key := args[0]
//     value := args[1]

//     if err := config.Set(key, value); err != nil {
//         fmt.Fprintf(os.Stderr, "⤫ Failed to set config: %v\n", err)
//         os.Exit(1)
//     }

//     fmt.Printf("✓ Set %s = %s\n", key, value)
// }

// func runConfigShow(cmd *cobra.Command, args []string) {
//     cfg := config.Load()
//     fmt.Println("Current configuration:")
//     fmt.Printf("├─ LLM Provider: %s\n", cfg.LLM.Provider)
//     fmt.Printf("├─ Model: %s\n", cfg.LLM.Model)
//     fmt.Printf("├─ Temperature: %.1f\n", cfg.LLM.Temperature)
//     fmt.Printf("├─ Parser Binary: %s\n", cfg.Parser.BinaryPath)
//     fmt.Printf("├─ Threads: %d\n", cfg.Parser.Threads)
//     fmt.Printf("└─ Cache Enabled: %v\n", cfg.Cache.Enabled)
// }

// func runCacheStats(cmd *cobra.Command, args []string) {
//     if err := cli.RunCacheStats(); err != nil {
//         fmt.Fprintf(os.Stderr, "⤫ Cache stats failed: %v\n", err)
//         os.Exit(1)
//     }
// }

// func runCacheClear(cmd *cobra.Command, args []string) {
//     if err := cli.RunCacheClear(); err != nil {
//         fmt.Fprintf(os.Stderr, "⤫ Cache clear failed: %v\n", err)
//         os.Exit(1)
//     }
// }

// func runVersion(cmd *cobra.Command, args []string) {
//     fmt.Printf("Eulix version %s\n", Version)
// }
