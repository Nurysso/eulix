//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package cli provides the command-line interface implementation for EULIX.
/*
This file is responsible for running the Chat command that launches tui
and checks for necessary file required by context window creation
*/
package cli

import (
	"bufio"
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
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
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
		"vectors.bin":        "vector index (binary)",
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
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	eulixDir := ".eulix"
	kbPath := filepath.Join(eulixDir, "kb.json")
	if _, err := os.Stat(kbPath); os.IsNotExist(err) {
		return fmt.Errorf("knowledge base not found. Run 'eulix analyze' first")
	}

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
		fmt.Println()
	}

	cacheManager, err := cache.CacheController(cfg)
	if err != nil {
		fmt.Printf("Warning: History database unavailable: %v\n", err)
	} else if cacheManager != nil {
		defer cacheManager.Close()
	}
	llmClient, err := llm.MouthClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize LLM: %w", err)
	}

	// Initialize query router (embeddings will be lazy-loaded)
	fmt.Println("#####===---,,..This BETA VERSION..,,---===######")
	fmt.Println("Initializing query system...")
	router, err := query.QueryTrafficController(eulixDir, cfg, llmClient, cacheManager)
	if err != nil {
		return fmt.Errorf("failed to initialize query router: %w", err)
	}
	defer func() { _ = router.Close() }() // Clean up embeddings if they were initialized

	router.SetCurrentChecksum(current.Hash)

	printSystemDiagnostics(eulixDir)

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
	kbPath := filepath.Join(eulixDir, "kb.json")
	if data, err := os.ReadFile(kbPath); err == nil {
		chunkCount := strings.Count(string(data), `"id":`)
		if chunkCount > 0 {
			fmt.Printf("Loaded %d code chunks\n", chunkCount)
		}
	}

	embPath := filepath.Join(eulixDir, "embeddings.bin")
	if info, err := os.Stat(embPath); err == nil {
		sizeMB := float64(info.Size()) / (1024 * 1024)
		fmt.Printf("Embeddings file: %.2f MB\n", sizeMB)
	}

	fmt.Println()
}
