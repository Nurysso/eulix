// internal/cli/chat.go

package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"eulix/internal/cache"
	"eulix/internal/checksum"
	"eulix/internal/config"
	"eulix/internal/llm"
	"eulix/internal/query"
	"eulix/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start interactive chat interface",
	Run: func(cmd *cobra.Command, args []string) {
		if err := startChat(); err != nil {
			fmt.Fprintf(os.Stderr, "Chat failed: %v\n", err)
			os.Exit(1)
		}
	},
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

	// Validate checksum
	detector := checksum.NewDetector(".")
	stored, err := detector.Load()
	if err != nil {
		fmt.Println("⚠️  No checksum found. Run 'eulix analyze' to generate one.")
		fmt.Println()
	} else {
		current, _ := detector.Calculate()
		changePercent := detector.CompareChecksums(stored, current)

		if changePercent > 0.30 {
			fmt.Printf("❌ Codebase changed %.1f%%\n", changePercent*100)
			fmt.Println("Knowledge base is significantly stale.")
			fmt.Println("Run 'eulix analyze' to update.")
			fmt.Println()
			return fmt.Errorf("analysis required")
		} else if changePercent > 0.10 {
			fmt.Printf("⚠️  Codebase changed %.1f%%\n", changePercent*100)
			fmt.Println("Consider running 'eulix analyze' to update.")
			fmt.Print("Continue anyway? [y/N]: ")

			var response string
			fmt.Scanln(&response)
			if response != "y" && response != "Y" {
				return nil
			}
		}
	}

	// Initialize cache
	var cacheManager *cache.Manager
	if cfg.Cache.Redis.Enabled {
		cacheManager, err = cache.NewManager(cfg)
		if err != nil {
			fmt.Printf("⚠️  Redis unavailable: %v\n", err)
			fmt.Println("Caching disabled, continuing...")
		}
	}

	// Initialize LLM client
	llmClient, err := llm.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize LLM: %w", err)
	}

	// Initialize query router
	router, err := query.NewRouter(eulixDir, cfg, llmClient, cacheManager)
	if err != nil {
		return fmt.Errorf("failed to initialize query router: %w", err)
	}

	// Start TUI
	model := tui.NewModel(router, cfg)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}
