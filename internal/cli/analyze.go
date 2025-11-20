package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"eulix/internal/checksum"
	"eulix/internal/config"

	"github.com/spf13/cobra"
)

var analyzeCmd = &cobra.Command{
	Use:   "analyze [path]",
	Short: "Analyze codebase and generate knowledge base",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		path := "."
		if len(args) > 0 {
			path = args[0]
		}
		if err := analyzeProject(path); err != nil {
			fmt.Fprintf(os.Stderr, "Analysis failed: %v\n", err)
			os.Exit(1)
		}
	},
}

func analyzeProject(projectPath string) error {
	startTime := time.Now()

	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	eulixDir := filepath.Join(projectPath, ".eulix")

	fmt.Println("🔍 Starting analysis...")
	fmt.Println()

	// Step 1: Calculate checksum
	fmt.Println("Calculating checksum...")
	detector := checksum.NewDetector(projectPath)
	currentChecksum, err := detector.Calculate()
	if err != nil {
		return fmt.Errorf("checksum calculation failed: %w", err)
	}
	fmt.Printf("   Found: %d files\n", currentChecksum.TotalFiles)
	fmt.Println()

	// Step 2: Run parser (it will create kb.json, index.json, call_graph.json, summary.json)
	fmt.Println("Parsing codebase...")

	// Parser creates files in the same directory as the output file
	// So if we specify .eulix/kb.json, it should create:
	// - .eulix/kb.json
	// - .eulix/index.json
	// - .eulix/call_graph.json
	// - .eulix/summary.json
	kbPath := filepath.Join(eulixDir, "kb.json")

	parserCmd := exec.Command("eulix_parser",
		"--root", projectPath,
		"-o", kbPath,
		"--threads", fmt.Sprintf("%d", cfg.Parser.Threads),
	)
	parserCmd.Stdout = os.Stdout
	parserCmd.Stderr = os.Stderr

	if err := parserCmd.Run(); err != nil {
		return fmt.Errorf("parser failed: %w", err)
	}
	fmt.Println("   ✓ Parser completed")
	fmt.Println()

	// Step 3: Check what files the parser actually created
	fmt.Println("Checking parser outputs...")

	// Parser creates files with kb_ prefix
	indexPath := filepath.Join(eulixDir, "kb_index.json")
	callGraphPath := filepath.Join(eulixDir, "kb_call_graph.json")
	summaryPath := filepath.Join(eulixDir, "kb_summary.json")

	// Verify all required files exist
	requiredFiles := map[string]string{
		"kb.json":            kbPath,
		"kb_index.json":      indexPath,
		"kb_call_graph.json": callGraphPath,
		"kb_summary.json":    summaryPath,
	}

	for name, path := range requiredFiles {
		if info, err := os.Stat(path); err != nil {
			return fmt.Errorf("parser did not generate %s: %w", name, err)
		} else if info.Size() == 0 {
			fmt.Printf("   ⚠️  Warning: %s is empty\n", name)
		}
	}
	fmt.Println("   ✓ All parser outputs verified")
	fmt.Println()

	// Step 4: Generate embeddings
	fmt.Println("Generating embeddings...")
	embeddingsPath := filepath.Join(eulixDir, "embeddings")

	embedCmd := exec.Command("eulix_embed",
		"-k", kbPath,
		"-o", embeddingsPath,
		"-m", cfg.Embeddings.Model,
	)
	embedCmd.Stdout = os.Stdout
	embedCmd.Stderr = os.Stderr

	if err := embedCmd.Run(); err != nil {
		return fmt.Errorf("embedding generation failed: %w", err)
	}
	fmt.Println("   ✓ Embeddings completed")
	fmt.Println()

	// Step 5: Save checksum
	fmt.Println("Saving checksum...")
	if err := detector.Save(currentChecksum); err != nil {
		return fmt.Errorf("failed to save checksum: %w", err)
	}
	fmt.Println("   ✓ Checksum saved")
	fmt.Println()

	duration := time.Since(startTime)

	fmt.Println("═══════════════════════════════════════")
	fmt.Println(" Analysis Complete!")
	fmt.Println("═══════════════════════════════════════")
	fmt.Printf("Files:      %d\n", currentChecksum.TotalFiles)
	fmt.Printf("Lines:      %d\n", currentChecksum.TotalLines)
	fmt.Printf("Time:       %s\n", duration.Round(time.Second))
	fmt.Println("═══════════════════════════════════════")
	fmt.Println()
	fmt.Println("Run 'eulix chat' to start querying your codebase!")

	return nil
}
