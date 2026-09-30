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

Checksum state is persisted at <config.Project.Path>/.eulix/checksum.json.zst
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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/zeebo/xxh3"

	"eulix/internal/config"
	"eulix/internal/utils"
)

// FileEntry stores enough metadata per file to allow cheap "did this file
// possibly change" checks (mtime/size) before paying for a full xxh3 hash.
type FileEntry struct {
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"` // unix nanos
}

type DirEntry struct {
	Hash    string   `json:"hash"`
	ModTime int64    `json:"mod_time"`
	Files   []string `json:"files"`
	Dirs    []string `json:"dirs"`
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

// // Result is what Run() returns: the fresh checksum plus a summary of how it
// // compares to whatever was previously stored (if anything).
type Result struct {
	Checksum      *Checksum // current state of the project (nil on first run)
	FirstRun      bool
	ChangedRatio  float64
	FilesAdded    int
	FilesDeleted  int
	FilesModified int
}

type Detector struct {
	projectPath    string
	checksumDir    string
	ignorePatterns []string
	exts           map[string]bool
}

// newDetector builds a Detector for the given project root and loads its
// .euignore patterns. Unexported: callers should go through Run().
func newDetector(projectPath string) *Detector {
	d := &Detector{projectPath: projectPath}
	d.loadIgnorePatterns()
	return d
}

// Run is the high-level entry point. It loads config itself, so callers
// don't pass a directory. If no checksum.json.zst exists yet, it creates one.
// If one exists, it recalculates and compares against the stored version,
// reporting what percentage of the codebase changed.
func Run() (*Result, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("checksum: failed to load config: %w", err)
	}
	projectPath := cfg.Project.Path

	d := newDetector(projectPath)
	// TODO: point this at the directory of the Rust --output if it isn't .eulix
	d.checksumDir = filepath.Join(projectPath, utils.EulixDir)

	stored, err := d.Load()
	if err != nil {
		// No (or unreadable) baseline from the Rust parser: treat as full change.
		return &Result{FirstRun: true, ChangedRatio: 1.0}, nil
	}
	d.exts = extsFromBaseline(stored)

	current, err := d.scan(stored)
	if err != nil {
		return nil, fmt.Errorf("checksum: failed to scan project: %w", err)
	}

	added, deleted, modified, ratio := compare(stored, current)
	return &Result{
		Checksum:      current,
		ChangedRatio:  ratio,
		FilesAdded:    added,
		FilesDeleted:  deleted,
		FilesModified: modified,
	}, nil
}

func (d *Detector) Load() (*Checksum, error) {
	raw, err := os.ReadFile(filepath.Join(d.checksumDir, utils.ChecksumFileName))
	if err != nil {
		return nil, err
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	data, err := dec.DecodeAll(raw, nil)
	if err != nil {
		return nil, fmt.Errorf("checksum: failed to decompress: %w", err)
	}
	var c Checksum
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("checksum: invalid json: %w", err)
	}
	return &c, nil
}

// Only track the extensions Rust actually tracked.
func extsFromBaseline(stored *Checksum) map[string]bool {
	set := make(map[string]bool)
	for p := range stored.Files {
		base := filepath.Base(p)
		// A leading-dot-only basename (".gitignore") is not an extension.
		// Require at least one non-dot char before the final dot.
		ext := filepath.Ext(p)
		if ext == "" || ext == base {
			continue
		}
		set[ext] = true
	}
	return set
}

// loadIgnorePatterns reads .euignore file and loads patterns
func (d *Detector) loadIgnorePatterns() {
	// Keep identical to `ignored_dirs` in the Rust walker.
	defaults := []string{
		".git/", ".eulix/", "__pycache__/", ".venv/", "venv/", "env/", ".env/",
		"node_modules/", ".pytest_cache/", ".mypy_cache/", ".tox/", "dist/",
		"build/", ".eggs/", ".ipynb_checkpoints/", "target/", "*.egg-info/",
	}

	var user []string
	if f, err := os.Open(filepath.Join(d.projectPath, utils.EuignorePath)); err == nil {
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			user = append(user, line)
		}
	}
	d.ignorePatterns = append(user, defaults...)
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

func (d *Detector) scan(stored *Checksum) (*Checksum, error) {
	files := make(map[string]FileEntry)

	err := filepath.WalkDir(d.projectPath, func(path string, e fs.DirEntry, err error) error {
		if path == d.projectPath {
			return err
		}
		skip := func() error {
			if e != nil && e.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if err != nil {
			return skip()
		}
		// Rust's walker skips hidden entries and doesn't follow symlinks.
		if strings.HasPrefix(e.Name(), ".") || e.Type()&fs.ModeSymlink != 0 {
			return skip()
		}
		if d.shouldIgnore(path) {
			return skip()
		}
		if e.IsDir() || !d.exts[filepath.Ext(e.Name())] {
			return nil
		}

		info, err := e.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(d.projectPath, path)
		if err != nil {
			return nil
		}

		entry := FileEntry{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		if old, ok := stored.Files[rel]; ok && old.Size == entry.Size && old.ModTime == entry.ModTime {
			entry.Hash = old.Hash // unchanged, no need to read the file
		} else {
			h, err := hashFile(path)
			if err != nil {
				return nil
			}
			entry.Hash = h
		}
		files[rel] = entry
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &Checksum{
		ProjectPath:  d.projectPath,
		TotalFiles:   len(files),
		Files:        files,
		LastAnalyzed: time.Now(),
	}, nil
}

func (d *Detector) eulixDir() string {
	return filepath.Join(d.projectPath, utils.EulixDir)
}

func (d *Detector) Save(checksum *Checksum) error {
	dir := d.eulixDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	checksumPath := filepath.Join(dir, utils.ChecksumFileName)
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
// nolint: unused
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
	for file, entry := range current.Files {
		old, ok := stored.Files[file]
		switch {
		case !ok:
			added++
		case old.Hash != entry.Hash:
			modified++
		}
	}
	for file := range stored.Files {
		if _, ok := current.Files[file]; !ok {
			deleted++
		}
	}

	denom := max(len(stored.Files), len(current.Files))
	if denom == 0 {
		return added, deleted, modified, 0
	}
	ratio = float64(added+deleted+modified) / float64(denom)
	if ratio > 1 {
		ratio = 1
	}
	return
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := xxh3.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%016x", h.Sum64()), nil
}
