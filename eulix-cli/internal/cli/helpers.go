// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package cli provides the command-line interface implementation for EULIX.

/*
This file is have helpers for cli stuff
*/

package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	a "eulix/internal/assets"
	"eulix/internal/checksum"
	"eulix/internal/utils"
)

// windowsFileUrl turns absolute windows path into a percent-encoded file://
//
//	URL. UNC paths put the server in the host position.
func windowsFileURL(path string) string {
	slashed := strings.ReplaceAll(path, `\`, "/")
	if rest, ok := strings.CutPrefix(slashed, "//"); ok {
		host, tail, _ := strings.Cut(rest, "/")
		return (&url.URL{Scheme: "file", Host: host, Path: "/" + tail}).String()
	}
	return (&url.URL{Scheme: "file", Path: "/" + slashed}).String()
}

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
			destination = windowsFileURL(targetPath)
		}
		// Not 'cmd /c start': cmd.exe re-parses its command line, so an unquoted
		// & ^ | or % in the target would split it or run as a second command.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", destination)

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

// servableCallGraphJSON returns a call graph without hidden or ignored files.
// It keeps the JSON generic because the viewer reads fields the router does not.
func servableCallGraphJSON(path, projectPath string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var g struct {
		Nodes          []map[string]any `json:"nodes"`
		Edges          []map[string]any `json:"edges"`
		Tags           map[string]any   `json:"tags"`
		FileCategories map[string]any   `json:"file_categories"`
	}
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, err
	}

	servable := checksum.ServableMatcher(projectPath)
	removed := make(map[any]bool)
	g.Nodes = slices.DeleteFunc(g.Nodes, func(n map[string]any) bool {
		file, _ := n["file"].(string)
		if servable(file) {
			return false
		}
		removed[n["id"]] = true
		return true
	})
	g.Edges = slices.DeleteFunc(g.Edges, func(e map[string]any) bool { return removed[e["from"]] || removed[e["to"]] })
	maps.DeleteFunc(g.Tags, func(id string, _ any) bool { return removed[id] })
	maps.DeleteFunc(g.FileCategories, func(file string, _ any) bool { return !servable(file) })
	return json.Marshal(g)
}

func writeQueryDebugLog(eulixDir, query, result string) error {
	f, err := utils.OpenEulixFile(eulixDir, filepath.Join("debug", "query-debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY)
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

// hasProjectMarker reports whether dir itself holds a project marker.
func hasProjectMarker(dir string) bool {
	for _, marker := range projectMarkerFiles {
		info, err := os.Stat(filepath.Join(dir, marker))
		if err == nil && !isGlobalEulixDir(info) {
			return true
		}
	}
	return false
}

// isGlobalEulixDir reports whether info is the global install directory. On
// case-insensitive filesystems (NTFS, default APFS) ~/.Eulix also answers to
// ~/.eulix, which must not make the home directory look like a project.
func isGlobalEulixDir(info os.FileInfo) bool {
	root, err := a.EulixRoot()
	if err != nil {
		return false
	}
	global, err := os.Stat(root)
	return err == nil && os.SameFile(info, global)
}

func findProjectRoot(startDir string) (string, error) {
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}

	dir := abs
	for {
		if hasProjectMarker(dir) {
			return dir, nil
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
