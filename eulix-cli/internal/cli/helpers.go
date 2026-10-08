//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package cli provides the command-line interface implementation for EULIX.

/*
This file is have helpers for cli stuff
*/

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"eulix/internal/utils"
)

// openInBrowser launches the specified path or URL in the system's default browser.
// It handles platform-specific quirks, absolute path resolution, and non-blocking execution.
func openInBrowser(target string) error {
	var cmd *exec.Cmd

	// Determine if the target is an HTTP/HTTPS URL or a local file path
	isURL := strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
	targetPath := target

	if !isURL {
		// Resolve relative paths (e.g. .eulix/...) to absolute paths
		absPath, err := filepath.Abs(target)
		if err != nil {
			return fmt.Errorf("failed to resolve absolute path for %s: %w", target, err)
		}
		targetPath = absPath
	}

	switch runtime.GOOS {
	case "windows":
		// Properly format local file paths as file:// URIs while leaving web URLs untouched
		destination := targetPath
		if !isURL {
			destination = "file:///" + filepath.ToSlash(targetPath)
		}
		// 'cmd /c start "" <url|file>' is resilient against spaces and quotes in paths
		cmd = exec.Command("cmd", "/c", "start", "", destination)

	case "darwin":
		cmd = exec.Command("open", targetPath)

	default: // Linux, FreeBSD, OpenBSD, etc.
		// Check for xdg-open to prevent cryptic crashes on headless servers / Docker
		if _, err := exec.LookPath("xdg-open"); err != nil {
			return fmt.Errorf("xdg-open not found in $PATH (headless environment?); please open manually: %s", targetPath)
		}
		cmd = exec.Command("xdg-open", targetPath)
	}

	// Detach standard I/O streams so child browser processes don't hold terminal handles
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	// Start running without blocking or waiting for browser process exit
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start browser command for %s: %w", targetPath, err)
	}

	return nil
}

func writeQueryDebugLog(eulixDir, query, result string) error {
	logFile := filepath.Join(eulixDir, "debug", "query-debug.log")
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		return fmt.Errorf("failed to create debug directory: %w", err)
	}

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open debug log file: %w", err)
	}
	defer func() { _ = f.Close() }()

	entry := fmt.Sprintf("\n%s\n=== QUERY DEBUG ENTRY ===\nTimestamp: %s\n\n=== QUERY ===\n%s\n=== END QUERY ===\n\n=== OUTPUT ===\n%s\n=== END OUTPUT ===\n%s\n",
		strings.Repeat("=", 80),
		time.Now().Format("2006-01-02 15:04:05"),
		query,
		result,
		strings.Repeat("=", 80))

	_, err = f.WriteString(entry)
	return err
}

func runSubCommand(name string, arg string) (string, error) {
	cmd := exec.Command(name, arg)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

var projectMarkerFiles = []string{utils.EulixDir, ".euignore"}

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

func isInitialized(root string) bool {
	if root == "" {
		return false
	}
	_, eulixErr := os.Stat(filepath.Join(root, ".eulix"))
	_, euignoreErr := os.Stat(filepath.Join(root, ".euignore"))
	return eulixErr == nil && euignoreErr == nil
}

func getKnowledgeBasePath(root string) string {
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	return filepath.Join(root, ".eulix", "kb.json")
}

func hasKnowledgeBase(root string) bool {
	_, err := os.Stat(getKnowledgeBasePath(root))
	return err == nil
}

func checkInitialized() error {
	if !isInitialized(".") {
		return fmt.Errorf(
			"\nEulix is not fully initialized in the current directory\n" +
				"Run 'eulix init' to set it up, or 'cd' into a project that has been initialized",
		)
	}
	state := checkInitState()
	if state.fullyInitialized() {
		return nil
	}

	missing := state.missingDescriptions()
	var b strings.Builder
	b.WriteString("\nEulix is not fully initialized. Missing:\n")
	for _, m := range missing {
		fmt.Fprintf(&b, "  - %s\n", m)
	}
	b.WriteString("\nRun 'eulix init fix' to restore missing files, or 'eulix init force' to reset.\n\n")
	return fmt.Errorf("%s", b.String())
}
