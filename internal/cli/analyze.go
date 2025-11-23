package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"eulix/internal/checksum"
	"eulix/internal/config"
)

func analyzeProject(projectPath string) error {
	startTime := time.Now()

	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	eulixDir := filepath.Join(projectPath, ".eulix")

	// Calculate checksum
	// fmt.Println("Calculating checksum...")
	detector := checksum.HashHound(projectPath)
	currentChecksum, err := detector.Calculate()
	if err != nil {
		return fmt.Errorf("checksum calculation failed: %w", err)
	}
	// fmt.Printf("   Found: %d files\n", currentChecksum.TotalFiles)
	// fmt.Println()

	// Runs parser
	fmt.Println("Parsing codebase...")
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
	fmt.Println("✓ Parser completed")
	fmt.Println()


	// Generate embeddings
	fmt.Println("Generating embeddings...")
	embeddingsPath := filepath.Join(eulixDir)

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

	// Step 6: Save checksum
	fmt.Println("Saving checksum...")
	if err := detector.Save(currentChecksum); err != nil {
		return fmt.Errorf("failed to save checksum: %w", err)
	}
	fmt.Println("   ✓ Checksum saved")
	fmt.Println()

	duration := time.Since(startTime)
	fmt.Printf("Took %s\n", duration.Round(time.Second))
	// fmt.Println("═══════════════════════════════════════")
	fmt.Println()
	fmt.Println("Run 'eulix chat' to start querying your codebase!")

	return nil
}
