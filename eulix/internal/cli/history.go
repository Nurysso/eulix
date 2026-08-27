//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package cli provides the command-line interface implementation for EULIX.
/*
	This file provides CLI commands for the history log — listing, viewing,
	and deleting recorded query/response turns. The old cache-oriented
	commands (stats, clear, cleanup, test) have been removed because the
	underlying store is now a plain append-only log with no TTL, checksums,
	or cache-hit logic.
*/

package cli

import (
	"fmt"
	"strconv"
	"strings"

	"eulix/internal/cache"
	"eulix/internal/config"
	"eulix/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// historyManager opens the history database, returning (manager, closer, error).
// The caller must invoke closer() when done — even if manager is nil.
func historyManager() (*cache.Manager, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, func() {}, fmt.Errorf("failed to load config: %w", err)
	}

	m, err := cache.CacheController(cfg)
	if err != nil {
		return nil, func() {}, fmt.Errorf("[history.go] failed to open history database: %w", err)
	}

	closer := func() { _ = m.Close() }
	return m, closer, nil
}

// HistoryList prints every stored entry, newest first (plain text).
func HistoryList() error {
	m, close, err := historyManager()
	if err != nil {
		return err
	}
	defer close()

	if m == nil {
		fmt.Println("History is disabled. Set cache.enable = true in eulix.toml.")
		return nil
	}

	entries, err := m.ListAll()
	if err != nil {
		return fmt.Errorf("failed to list history: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println(":( No history entries found.")
		return nil
	}

	fmt.Printf(" Query History (%d entries)\n", len(entries))
	fmt.Println(strings.Repeat("─", 40))

	for _, entry := range entries {
		query := entry.Query
		if len(query) > 60 {
			query = query[:57] + "..."
		}

		reasoningMark := " "
		if entry.Reasoning != "" {
			reasoningMark = "◈"
		}

		fmt.Printf("\n%s [%d] %s\n", reasoningMark, entry.ID, query)
		fmt.Printf("    Logged: %s\n", entry.CreatedAt.Format("2006-01-02 15:04:05"))

		answerPreview := entry.Answer
		if len(answerPreview) > 80 {
			answerPreview = answerPreview[:77] + "..."
		}
		fmt.Printf("    Answer: %s\n", answerPreview)
	}

	fmt.Println("\n Use 'eulix history view' for the interactive viewer.")
	return nil
}

// HistoryShow prints the full detail for a single entry identified by its
// numeric ID (as shown in HistoryList / the TUI).
func HistoryShow(idStr string) error {
	id, err := parseID(idStr)
	if err != nil {
		return err
	}

	m, close, err := historyManager()
	if err != nil {
		return err
	}
	defer close()

	if m == nil {
		fmt.Println("History is disabled. Set cache.enable = true in eulix.toml.")
		return nil
	}

	entry, found, err := m.Get(id)
	if err != nil {
		return fmt.Errorf("failed to read history entry: %w", err)
	}
	if !found {
		return fmt.Errorf("no history entry with ID %d", id)
	}

	fmt.Printf("ID:     %d\n", entry.ID)
	fmt.Printf("Logged: %s\n\n", entry.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("Query\n%s\n\n", strings.Repeat("─", 40))
	fmt.Println(entry.Query)

	if entry.Reasoning != "" {
		fmt.Printf("\nReasoning\n%s\n", strings.Repeat("─", 40))
		fmt.Println(entry.Reasoning)
	}

	fmt.Printf("\nAnswer\n%s\n", strings.Repeat("─", 40))
	fmt.Println(entry.Answer)

	return nil
}

// HistoryView launches the interactive Bubble Tea history viewer.
func HistoryView() error {
	m, close, err := historyManager()
	if err != nil {
		return err
	}
	defer close()

	if m == nil {
		fmt.Println("History is disabled. Set cache.enable = true in eulix.toml.")
		return nil
	}

	entries, err := m.ListAll()
	if err != nil {
		return fmt.Errorf("failed to list history: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println(":( No history entries found.")
		return nil
	}

	model := tui.HistoryView(entries, m)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

// HistoryDelete removes a single entry by its numeric ID, with confirmation.
func HistoryDelete(idStr string) error {
	id, err := parseID(idStr)
	if err != nil {
		return err
	}

	m, close, err := historyManager()
	if err != nil {
		return err
	}
	defer close()

	if m == nil {
		fmt.Println("History is disabled. Set cache.enable = true in eulix.toml.")
		return nil
	}

	entry, found, err := m.Get(id)
	if err != nil {
		return fmt.Errorf("failed to read history entry: %w", err)
	}
	if !found {
		return fmt.Errorf("no history entry with ID %d", id)
	}

	query := entry.Query
	if len(query) > 60 {
		query = query[:57] + "..."
	}

	fmt.Printf("[!]  Deleting history entry:\n")
	fmt.Printf("      ID:     %d\n", entry.ID)
	fmt.Printf("      Logged: %s\n", entry.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("      Query:  %s\n\n", query)
	fmt.Print("[!]  Continue? [y/N]: ")

	var response string
	_, _ = fmt.Scanln(&response)
	if response != "y" && response != "yes" {
		fmt.Println("Cancelled.")
		return nil
	}

	if err := m.Delete(entry.ID); err != nil {
		return fmt.Errorf("failed to delete history entry: %w", err)
	}

	fmt.Println("✓ History entry deleted.")
	return nil
}

// HistoryClear removes every stored entry after confirmation.
func HistoryClear() error {
	m, close, err := historyManager()
	if err != nil {
		return err
	}
	defer close()

	if m == nil {
		fmt.Println("History is disabled. Set cache.enable = true in eulix.toml.")
		return nil
	}

	fmt.Print("[!]  This will delete all history entries. Continue? [y/N]: ")
	var response string
	_, _ = fmt.Scanln(&response)
	if response != "y" && response != "yes" {
		fmt.Println("Cancelled.")
		return nil
	}

	if err := m.Clear(); err != nil {
		return fmt.Errorf("failed to clear history: %w", err)
	}

	fmt.Println("✓ History cleared.")
	return nil
}

func parseID(s string) (uint64, error) {
	id, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid ID %q: must be a positive integer", s)
	}
	return id, nil
}
