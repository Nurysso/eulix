//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer mnae (Nurysso) contact - nurysso [at] proton.me
/*
Package checksum handles project-level source file hashing and change detection.
It walks the configured project directory, hashes each source file with xxh3,
and produces a combined project hash used by eulix to detect whether
re-analysis is needed.

Ignore rules are read from a .euignore file in the project root (same syntax
as .gitignore). The .eulix/ output directory is always excluded automatically.

Checksum state is persisted at <config.Project.Path>/.eulix/checksum.json.
Run() is the single entry point: it loads config itself, creates the
checksum file on first run, or compares against the stored checksum on
subsequent runs and reports the percentage of the codebase that changed.
*/

package checksum

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/zeebo/xxh3"

	"eulix/internal/config"
)

// FileEntry stores enough metadata per file to allow cheap "did this file
// possibly change" checks (mtime/size) before paying for a full xxh3 hash.
type FileEntry struct {
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"` // unix nanos
}

type Checksum struct {
	ProjectPath     string               `json:"project_path"`
	TotalFiles      int                  `json:"total_files"`
	TotalLines      int                  `json:"total_lines"`
	Hash            string               `json:"hash"`
	Files           map[string]FileEntry `json:"files"`
	LastAnalyzed    time.Time            `json:"last_analyzed"`
	AnalysisVersion string               `json:"analysis_version"`
}

// Result is what Run() returns: the fresh checksum plus a summary of how it
// compares to whatever was previously stored (if anything).
type Result struct {
	Checksum      *Checksum
	FirstRun      bool    // true if there was no existing checksum.json
	ChangedRatio  float64 // fraction (0.0-1.0) of files added/removed/modified
	FilesAdded    int
	FilesDeleted  int
	FilesModified int
}

const analysisVersion = "0.5.3"
const eulixDirName = ".eulix"
const checksumFileName = "checksum.json.zst"
const ignoreFileName = ".euignore"

type Detector struct {
	projectPath    string
	ignorePatterns []string
}

// newDetector builds a Detector for the given project root and loads its
// .euignore patterns. Unexported: callers should go through Run().
func hashHound(projectPath string) *Detector {
	d := &Detector{projectPath: projectPath}
	d.loadIgnorePatterns()
	return d
}

// Run is the high-level entry point. It loads config itself, so callers
// don't pass a directory. If no checksum.json exists yet, it creates one.
// If one exists, it recalculates and compares against the stored version,
// reporting what percentage of the codebase changed.
func Run() (*Result, error) {
	cfg, _ := config.Load()

	projectPath := &cfg.Project.Path
	d := hashHound(*projectPath)

	stored, loadErr := d.Load()
	firstRun := loadErr != nil

	current, err := d.calculate()
	if err != nil {
		return nil, fmt.Errorf("checksum: failed to calculate checksum: %w", err)
	}

	if err := d.Save(current); err != nil {
		return nil, fmt.Errorf("checksum: failed to save checksum file: %w", err)
	}

	if firstRun {
		return &Result{
			Checksum:      current,
			FirstRun:      true,
			ChangedRatio:  1.0,
			FilesAdded:    current.TotalFiles,
			FilesDeleted:  0,
			FilesModified: 0,
		}, nil
	}

	added, deleted, modified, ratio := compare(stored, current)

	return &Result{
		Checksum:      current,
		FirstRun:      false,
		ChangedRatio:  ratio,
		FilesAdded:    added,
		FilesDeleted:  deleted,
		FilesModified: modified,
	}, nil
}

// loadIgnorePatterns reads .euignore file and loads patterns
func (d *Detector) loadIgnorePatterns() {
	// Default patterns that are always applied (can't be overridden)
	defaultPatterns := []string{
		"node_modules/",
		".eulix/",
		"target/",
		"dist/",
		"build/",
		"out/",
		"bin/",
		"obj/",
		"__pycache__/",
		".venv/",
		"venv/",
		".git/",
		".idea/",
		".vscode/",
		".DS_Store",
		"Thumbs.db",
	}

	// Load user patterns from .euignore
	userPatterns := []string{}
	ignorePath := filepath.Join(d.projectPath, ignoreFileName)
	file, err := os.Open(ignorePath)
	if err == nil {
		defer func() {
			_ = file.Close()
		}()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			userPatterns = append(userPatterns, line)
		}

		if err := scanner.Err(); err != nil {
			fmt.Printf("warning: error reading %s: %v\n", ignorePath, err)
		}
	}

	// Combine: user patterns first (they take priority), then defaults
	d.ignorePatterns = append(userPatterns, defaultPatterns...)
}

func (d *Detector) shouldIgnore(path string) bool {
	relPath, err := filepath.Rel(d.projectPath, path)
	if err != nil {
		return false
	}

	// Normalize to forward slashes for consistent matching
	relPath = filepath.ToSlash(relPath)
	pathParts := strings.Split(relPath, "/")

	for _, pattern := range d.ignorePatterns {
		pattern = filepath.ToSlash(pattern)

		// Handle directory patterns
		if strings.HasSuffix(pattern, "/") {
			dirPattern := strings.TrimSuffix(pattern, "/")

			// Check if any path component matches
			for _, part := range pathParts {
				if matched, _ := filepath.Match(dirPattern, part); matched {
					return true
				}
			}

			// Check if path starts with this directory
			if relPath == dirPattern || strings.HasPrefix(relPath, dirPattern+"/") {
				return true
			}
		} else {
			// File pattern - check full path and each component
			if matched, _ := filepath.Match(pattern, relPath); matched {
				return true
			}

			for _, part := range pathParts {
				if matched, _ := filepath.Match(pattern, part); matched {
					return true
				}
			}
		}
	}

	return false
}

// calculate walks the project and hashes every source file. It's a full
// recompute — used both for first-run creation and for producing the
// "current" snapshot to diff against a stored checksum.
func (d *Detector) calculate() (*Checksum, error) {
	files := make(map[string]FileEntry)
	totalLines := 0
	totalFiles := 0

	err := filepath.Walk(d.projectPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if d.shouldIgnore(path) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}
		if base := filepath.Base(path); len(base) > 0 && base[0] == '.' {
			return nil
		}

		ext := filepath.Ext(path)
		if !isSourceFile(ext) {
			return nil
		}

		hash, lines, err := hashFile(path)
		if err != nil {
			return nil
		}

		relPath, _ := filepath.Rel(d.projectPath, path)
		files[relPath] = FileEntry{
			Hash:    hash,
			Size:    info.Size(),
			ModTime: info.ModTime().UnixNano(),
		}
		totalLines += lines
		totalFiles++

		return nil
	})

	if err != nil {
		return nil, err
	}

	projectHash := combinedHash(files)

	return &Checksum{
		ProjectPath:     d.projectPath,
		TotalFiles:      totalFiles,
		TotalLines:      totalLines,
		Hash:            projectHash,
		Files:           files,
		LastAnalyzed:    time.Now(),
		AnalysisVersion: analysisVersion,
	}, nil
}

// combinedHash folds all per-file hashes into a single deterministic
// project-level hash. Paths are sorted first since map iteration order in
// Go is randomized — without sorting, the same file contents could produce
// a different combined hash from run to run.
func combinedHash(files map[string]FileEntry) string {
	relPaths := make([]string, 0, len(files))
	for p := range files {
		relPaths = append(relPaths, p)
	}
	sort.Strings(relPaths)

	hasher := xxh3.New()
	for _, p := range relPaths {
		_, _ = hasher.WriteString(p)
		_, _ = hasher.WriteString(files[p].Hash)
	}
	return fmt.Sprintf("%016x", hasher.Sum64())
}

func (d *Detector) eulixDir() string {
	return filepath.Join(d.projectPath, eulixDirName)
}

func (d *Detector) Save(checksum *Checksum) error {
	dir := d.eulixDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	checksumPath := filepath.Join(dir, checksumFileName)
	data, err := json.Marshal(checksum)
	if err != nil {
		return err
	}

	compressed, err := compressZstd(data)
	if err != nil {
		return fmt.Errorf("checksum: failed to compress checksum data: %w", err)
	}

	return os.WriteFile(checksumPath, compressed, 0644)
}

func (d *Detector) Load() (*Checksum, error) {
	checksumPath := filepath.Join(d.eulixDir(), checksumFileName)
	raw, err := os.ReadFile(checksumPath)
	if err != nil {
		return nil, err
	}

	data, err := decompressZstd(raw)
	if err != nil {
		return nil, fmt.Errorf("checksum: failed to decompress checksum data: %w", err)
	}

	var checksum Checksum
	if err := json.Unmarshal(data, &checksum); err != nil {
		return nil, err
	}

	return &checksum, nil
}

// compressZstd compresses data using zstd at the default compression level.
// The checksum file is written and read frequently on every Run(), so we
// favor default speed over the slower "best compression" levels.
func compressZstd(data []byte) ([]byte, error) {
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = encoder.Close()
	}()

	return encoder.EncodeAll(data, make([]byte, 0, len(data))), nil
}

// decompressZstd reverses compressZstd.
func decompressZstd(data []byte) ([]byte, error) {
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer decoder.Close()

	return decoder.DecodeAll(data, nil)
}

// compare diffs stored vs current checksums and returns counts plus the
// changed ratio (added+deleted+modified over the larger of the two file
// counts, so both wholesale deletions and wholesale additions register
// correctly instead of being able to exceed 1.0 or hide against a stale
// denominator).
func compare(stored, current *Checksum) (added, deleted, modified int, ratio float64) {
	if stored == nil || current == nil {
		return 0, 0, 0, 1.0
	}

	for file, entry := range current.Files {
		storedEntry, exists := stored.Files[file]
		if !exists {
			added++
			continue
		}
		// Fast path: identical size+mtime means almost certainly unchanged,
		// skip trusting the hash comparison to a cheap metadata check first.
		if storedEntry.Size == entry.Size && storedEntry.ModTime == entry.ModTime {
			continue
		}
		if storedEntry.Hash != entry.Hash {
			modified++
		}
	}

	for file := range stored.Files {
		if _, exists := current.Files[file]; !exists {
			deleted++
		}
	}

	denom := stored.TotalFiles
	if current.TotalFiles > denom {
		denom = current.TotalFiles
	}
	if denom == 0 {
		return added, deleted, modified, 0.0
	}

	totalChanges := added + deleted + modified
	ratio = float64(totalChanges) / float64(denom)
	if ratio > 1.0 {
		ratio = 1.0
	}
	return added, deleted, modified, ratio
}

func hashFile(path string) (string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		_ = f.Close()
	}()

	hasher := xxh3.New()
	lines := 0
	buf := make([]byte, 64*1024) // larger buffer = fewer syscalls, faster

	for {
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = hasher.Write(buf[:n])
			for i := 0; i < n; i++ {
				if buf[i] == '\n' {
					lines++
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, err
		}
	}

	return fmt.Sprintf("%016x", hasher.Sum64()), lines, nil
}

func isSourceFile(ext string) bool {
	sourceExts := map[string]bool{
		".go":    true,
		".py":    true,
		".js":    true,
		".ts":    true,
		".tsx":   true,
		".jsx":   true,
		".java":  true,
		".c":     true,
		".cpp":   true,
		".h":     true,
		".hpp":   true,
		".rs":    true,
		".rb":    true,
		".php":   true,
		".cs":    true,
		".swift": true,
		".kt":    true,
		".scala": true,
	}
	return sourceExts[ext]
}
