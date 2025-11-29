package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"eulix/internal/cache"
	"eulix/internal/checksum"
	"eulix/internal/config"
	"eulix/internal/llm"
	"eulix/internal/query"
	"eulix/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// printStatusMessage prints a formatted status message with consistent spacing
func printStatusMessage(primaryMsg string, additionalLines ...string) {
    printStatusMessageWithIcon(" ", primaryMsg, additionalLines...)
}

func printStatusMessageWithIcon(icon, primaryMsg string, additionalLines ...string) {
    fmt.Printf("%s %s\n", icon, primaryMsg)
    for _, line := range additionalLines {
        fmt.Printf("  %s\n", line)
    }
}

// promptConfirm asks for user confirmation
func promptConfirm(question string) bool {
	fmt.Printf("%s [y/N]: ", question)
	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}

// checkEmbeddingsFiles verifies all required files exist
func checkEmbeddingsFiles(eulixDir string) []string {
	var missing []string

	requiredFiles := map[string]string{
		"kb.json":            "Knowledge base",
		"kb_index.json":      "KB index",
		"kb_call_graph.json": "Call graph",
		"embeddings.bin":     "Embeddings (binary)",
	}

	for file, desc := range requiredFiles {
		path := filepath.Join(eulixDir, file)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			missing = append(missing, fmt.Sprintf("  • %s (%s)", desc, file))
		}
	}

	return missing
}

func startChat() error {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Check KB files
	eulixDir := ".eulix"
	kbPath := filepath.Join(eulixDir, "kb.json")
	if _, err := os.Stat(kbPath); os.IsNotExist(err) {
		return fmt.Errorf("knowledge base not found. Run 'eulix analyze' first")
	}

	// Check for all required files
	missing := checkEmbeddingsFiles(eulixDir)
	if len(missing) > 0 {
		printStatusMessage(
			":(",
			"Missing required files:",
		)
		for _, m := range missing {
			fmt.Println(m)
		}
		fmt.Println()
		printStatusMessage(
			"[TIP]",
			"Run 'eulix analyze' to generate all required files",
		)
		return fmt.Errorf("missing required files")
	}

	// Validate checksum
	detector := checksum.HashHound(".")
	stored, err := detector.Load()
	if err != nil {
		printStatusMessage("No checksum found.",
			"Run 'eulix analyze' to generate one.",
		)
		return fmt.Errorf("checksum required")
	}

	current, err := detector.Calculate()
	if err != nil {
		return fmt.Errorf("failed to calculate checksum: %w", err)
	}

	changePercent := detector.CompareChecksums(stored, current)

	if changePercent > 0.30 {
		printStatusMessage(fmt.Sprintf("Codebase changed %.1f%%", changePercent*100),
			"Knowledge base is significantly stale.",
			"Run 'eulix analyze' to update.",
		)
		return fmt.Errorf("analysis required")
	} else if changePercent > 0.10 {
		printStatusMessage(fmt.Sprintf("Codebase changed %.1f%%", changePercent*100),
			"Consider running 'eulix analyze' to update.",
		)
		if !promptConfirm("Continue anyway?") {
			return nil
		}
		fmt.Println() // Add spacing after user response
	}

	// Initialize cache with checksum
	var cacheManager *cache.Manager
	if cfg.Cache.Redis.Enabled || cfg.Cache.SQL.Enabled {
		cacheManager, err = cache.CacheController(cfg)
		if err != nil {
			printStatusMessage(fmt.Sprintf("Cache initialization failed: %v", err),
				"Caching disabled, continuing...",
			)
		} else {
			defer cacheManager.Close()

			// Clean expired entries on startup
			if err := cacheManager.CleanExpired(); err != nil {
				fmt.Printf("Failed to clean expired cache: %v\n", err)
			}

			// Invalidate old cache entries if checksum changed
			if changePercent > 0 {
				if err := cacheManager.InvalidateByChecksum(current.Hash); err != nil {
					fmt.Printf("Failed to invalidate old cache: %v\n", err)
				} else {
					printStatusMessage("Cache invalidated due to codebase changes",
					)
				}
			}

			// Show cache stats
			if stats, err := cacheManager.GetStats(); err == nil {
				if sqlEntries, ok := stats["sql_valid_entries"].(int); ok && sqlEntries > 0 {
					fmt.Printf("Cache: %d valid entries\n", sqlEntries)
				}
			}
		}
	}

	// Initialize LLM client
	llmClient, err := llm.MouthClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize LLM: %w", err)
	}

	// Initialize query router (embeddings will be lazy-loaded)
	fmt.Println("Initializing query system...")
	router, err := query.QueryTrafficController(eulixDir, cfg, llmClient, cacheManager)
	if err != nil {
		return fmt.Errorf("failed to initialize query router: %w", err)
	}
	defer router.Close() // Clean up embeddings if they were initialized

	// Store current checksum in router for cache operations
	router.SetCurrentChecksum(current.Hash)

	// Diagnostic info
	printSystemDiagnostics(eulixDir)

	// Start TUI
	fmt.Println("Starting chat interface...")
	fmt.Println()

	model := tui.MainModel(router, cfg, cacheManager)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

func printSystemDiagnostics(eulixDir string) {
	// Count chunks in kb.json
	kbPath := filepath.Join(eulixDir, "kb.json")
	if data, err := os.ReadFile(kbPath); err == nil {
		// Quick count of chunks without full parsing
		chunkCount := strings.Count(string(data), `"id":`)
		if chunkCount > 0 {
			fmt.Printf("Loaded %d code chunks\n", chunkCount)
		}
	}

	// Check embeddings file size
	embPath := filepath.Join(eulixDir, "embeddings.bin")
	if info, err := os.Stat(embPath); err == nil {
		sizeMB := float64(info.Size()) / (1024 * 1024)
		fmt.Printf("Embeddings file: %.2f MB\n", sizeMB)
	}

	fmt.Println()
}
