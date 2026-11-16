// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package checksum handles project-level source file hashing and change detection.
/*
It walks the configured project directory, hashes each source file's size and
mtime with xxh3 (matching the Rust parser, which never reads contents here),
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
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/zeebo/xxh3"

	"eulix/internal/config"
	"eulix/internal/utils"
)

// FileEntry stores per-file metadata; Hash is MetaHash(Size, ModTime).
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

// Result is what Run() returns: the fresh checksum plus a summary of how it
// compares to whatever was previously stored (if anything).
type Result struct {
	Checksum      *Checksum // current state of the project (nil on first run)
	ChangedRatio  float64
	FilesAdded    int
	FilesDeleted  int
	FilesModified int
	FirstRun      bool
}

type Detector struct {
	projectPath    string
	checksumDir    string
	ignorePatterns []string
	exts           map[string]bool

	nestedMu sync.Mutex
	nested   map[string][]string // dir (slash, root-relative) → its own .euignore patterns
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

// Track supported source extensions as well as those present in the baseline.
func extsFromBaseline(stored *Checksum) map[string]bool {
	// Match the "all" filter in eulix-parser/src/parse/file.rs and the selected
	// extensions in libeulix/src/grammar_files/language.rs. Keep both in sync.
	set := map[string]bool{
		".py": true, ".pyw": true, ".pyi": true,
		".js": true, ".mjs": true, ".cjs": true,
		".ts": true, ".mts": true, ".cts": true,
		".go": true, ".rs": true, ".java": true,
		".c": true, ".h": true, ".cpp": true, ".cc": true, ".cxx": true,
		".hpp": true, ".hxx": true, ".h++": true, ".c++": true,
	}
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

	user := readIgnoreFile(filepath.Join(d.projectPath, utils.EuignorePath))
	d.ignorePatterns = append(user, defaults...)
}

// readIgnoreFile returns the patterns in an ignore file, skipping blank lines
// and #-comments. The file is read whole: a bufio.Scanner stops at a 64 KiB
// line and would silently drop every rule after it, which the Rust walker
// still applies.
func readIgnoreFile(name string) []string {
	data, err := os.ReadFile(name)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			// stderr: under 'eulix mcp', stdout carries the protocol.
			fmt.Fprintf(os.Stderr, "reading %s: %v\n", name, err)
		}
		return nil
	}
	var patterns []string
	for _, line := range strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			patterns = append(patterns, line)
		}
	}
	return patterns
}

// IgnoreMatcher reports whether a path under projectPath is excluded by the
// project's .euignore or the default ignored directories, the same rules the
// checksum scan uses. It reads .euignore once, so build one per walk.
func IgnoreMatcher(projectPath string) func(path string) bool {
	return newDetector(projectPath).shouldIgnore
}

// ServableMatcher reports whether a repo-relative path listed in the knowledge
// base may still be read and served: it stays inside projectPath, has no
// hidden component (the parser never indexes those), and the current
// .euignore rules do not exclude it. The KB cannot be taken on trust: it can
// predate the rules, or come inside a cloned repository. Index entries repeat
// files, so each answer is remembered. Not safe for concurrent use.
func ServableMatcher(projectPath string) func(rel string) bool {
	ignored := IgnoreMatcher(projectPath)
	check := func(rel string) bool {
		rel = filepath.FromSlash(rel)
		if !filepath.IsLocal(rel) {
			return false
		}
		path := projectPath
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			path = filepath.Join(path, part)
			if strings.HasPrefix(part, ".") || hiddenPath(path) {
				return false
			}
		}
		return !ignored(filepath.Join(projectPath, rel))
	}
	seen := make(map[string]bool)
	return func(rel string) bool {
		ok, known := seen[rel]
		if !known {
			ok = check(rel)
			seen[rel] = ok
		}
		return ok
	}
}

// ReadRegularNoSymlinks opens every path component relative to a pinned root.
// Comparing each opened handle with its lstat result closes the race where a
// regular component is replaced by a symlink between validation and use.
func ReadRegularNoSymlinks(root *os.Root, filePath string) ([]byte, error) {
	clean := filepath.Clean(filepath.FromSlash(filePath))
	if clean == "." || !filepath.IsLocal(clean) {
		return nil, fmt.Errorf("path is not local: %s", filePath)
	}
	parts := strings.Split(clean, string(filepath.Separator))
	current := root
	owned := false
	defer func() {
		if owned {
			_ = current.Close()
		}
	}()
	for _, part := range parts[:len(parts)-1] {
		before, err := current.Lstat(part)
		if err != nil {
			return nil, err
		}
		if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
			return nil, fmt.Errorf("path component is not a real directory: %s", part)
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			return nil, err
		}
		after, err := next.Stat(".")
		if err != nil || !os.SameFile(before, after) {
			_ = next.Close()
			return nil, fmt.Errorf("path component changed while opening: %s", part)
		}
		if owned {
			_ = current.Close()
		}
		current = next
		owned = true
	}

	name := parts[len(parts)-1]
	before, err := current.Lstat(name)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a regular file: %s", filePath)
	}
	file, err := current.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() {
		return nil, fmt.Errorf("file changed while opening: %s", filePath)
	}
	return io.ReadAll(file)
}

func (d *Detector) shouldIgnore(path string) bool {
	relPath, err := filepath.Rel(d.projectPath, path)
	if err != nil {
		return false
	}

	// Normalize to forward slashes for consistent matching
	relPath = filepath.ToSlash(relPath)
	if relPath == "." || relPath == ".." || strings.HasPrefix(relPath, "../") {
		return false
	}

	parts := strings.Split(relPath, "/")
	if matchesAny(d.ignorePatterns, parts) {
		return true
	}
	// As with nested .gitignore files, a .euignore below the root applies to
	// its own directory, with paths relative to it. The Rust walker reads them.
	for i := 1; i < len(parts); i++ {
		if matchesAny(d.nestedPatterns(parts[:i]), parts[i:]) {
			return true
		}
	}
	return false
}

// nestedPatterns returns the patterns of the .euignore inside the directory
// dirParts (root-relative), reading each one once.
func (d *Detector) nestedPatterns(dirParts []string) []string {
	dir := strings.Join(dirParts, "/")
	d.nestedMu.Lock()
	defer d.nestedMu.Unlock()
	if patterns, ok := d.nested[dir]; ok {
		return patterns
	}
	if d.nested == nil {
		d.nested = make(map[string][]string)
	}
	patterns := readIgnoreFile(filepath.Join(d.projectPath, filepath.FromSlash(dir), utils.EuignorePath))
	d.nested[dir] = patterns
	return patterns
}

// matchesAny reports whether any gitignore-style pattern excludes the path
// given as slash-separated parts.
func matchesAny(patterns, parts []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "./")
		// Dir-only "x/" also matches a file named x: hiding more is the safe side.
		pattern = strings.TrimSuffix(pattern, "/")
		// A leading or inner slash anchors the pattern to the ignore file's directory.
		anchored := strings.Contains(pattern, "/")
		pattern = strings.TrimPrefix(pattern, "/")
		if pattern != "" && matchIgnorePattern(pattern, parts, anchored) {
			return true
		}
	}
	return false
}

// matchIgnorePattern reports whether pattern matches the path or one of its
// parent directories: as with .gitignore, excluding a directory excludes
// everything below it. Anchored patterns match the path from the top;
// the others match any single name in it.
func matchIgnorePattern(pattern string, parts []string, anchored bool) bool {
	prefix := ""
	for i, part := range parts {
		if i > 0 {
			prefix += "/"
		}
		prefix += part
		target := part
		if anchored {
			target = prefix
		}
		if matchGlob(pattern, target) {
			return true
		}
	}
	return false
}

// matchGlob matches a slash-separated target against a gitignore glob.
// path.Match keeps "*" from crossing "/" on every OS; "**" needs a regexp.
func matchGlob(pattern, target string) bool {
	if strings.Contains(pattern, "**") {
		ok, err := regexpMatch(globstarRegexp(pattern), target)
		return ok && err == nil
	}
	ok, _ := path.Match(pattern, target)
	return ok
}

// globstarRegexp converts a gitignore glob containing "**" into an anchored
// regexp. A leading "**/" is any run of directories, "/**/" is zero or more
// directories, a trailing "/**" is everything below, and any other "**" is a
// plain "*".
func globstarRegexp(pattern string) string {
	var re strings.Builder
	re.WriteString("^")
	for i := 0; i < len(pattern); {
		rest := pattern[i:]
		switch {
		case i == 0 && strings.HasPrefix(rest, "**/"):
			re.WriteString("(?:.*/)?")
			i += 3
		case strings.HasPrefix(rest, "/**/"):
			re.WriteString("/(?:.*/)?")
			i += 4
		case rest == "/**":
			re.WriteString("(?:/.*)?")
			i += 3
		case strings.HasPrefix(rest, "**"):
			re.WriteString("[^/]*")
			i += 2
		case rest[0] == '*':
			re.WriteString("[^/]*")
			i++
		case rest[0] == '?':
			re.WriteString("[^/]")
			i++
		case rest[0] == '[' && strings.IndexByte(rest, ']') > 1:
			class := rest[1:strings.IndexByte(rest, ']')]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			re.WriteString("[" + class + "]")
			i += len(class) + 2
		case rest[0] == '\\' && len(rest) > 1:
			re.WriteString(regexp.QuoteMeta(rest[1:2]))
			i += 2
		default:
			re.WriteString(regexp.QuoteMeta(rest[:1]))
			i++
		}
	}
	re.WriteString("$")
	return re.String()
}

var (
	regexCache   = make(map[string]*regexp.Regexp)
	regexCacheMu sync.RWMutex
)

func regexpMatch(pattern, s string) (bool, error) {
	regexCacheMu.RLock()
	re, ok := regexCache[pattern]
	regexCacheMu.RUnlock()
	if !ok {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return false, err
		}
		regexCacheMu.Lock()
		regexCache[pattern] = re
		regexCacheMu.Unlock()
	}
	return re.MatchString(s), nil
}

func slashKeys(m map[string]FileEntry) map[string]FileEntry {
	out := make(map[string]FileEntry, len(m))
	for k, v := range m {
		out[filepath.ToSlash(k)] = v
	}
	return out
}

func (d *Detector) scan(_ *Checksum) (*Checksum, error) {
	files := make(map[string]FileEntry)

	root := d.projectPath
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if p == root {
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
		if strings.HasPrefix(e.Name(), ".") || HiddenByAttr(e) || e.Type()&fs.ModeSymlink != 0 {
			return skip()
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return skip()
		}
		if d.shouldIgnore(filepath.Join(d.projectPath, rel)) {
			return skip()
		}
		if e.IsDir() || !d.exts[filepath.Ext(e.Name())] {
			return nil
		}

		info, err := e.Info()
		if err != nil {
			return nil
		}

		key := filepath.ToSlash(rel)
		entry := FileEntry{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		entry.Hash = MetaHash(entry.Size, entry.ModTime)
		files[key] = entry
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
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

	return os.WriteFile(checksumPath, compressed, 0o644)
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
	storedFiles := slashKeys(stored.Files)
	for file, entry := range current.Files {
		old, ok := storedFiles[file]
		switch {
		case !ok:
			added++
		case old.Hash != entry.Hash:
			modified++
		}
	}
	for file := range storedFiles {
		if _, ok := current.Files[file]; !ok {
			deleted++
		}
	}

	denom := max(len(storedFiles), len(current.Files))
	if denom == 0 {
		return added, deleted, modified, 0
	}
	ratio = float64(added+deleted+modified) / float64(denom)
	if ratio > 1 {
		ratio = 1
	}
	return
}

// MetaHash must match the Rust FileWalker: xxh3 of size then mtime, both little-endian.
func MetaHash(size, modTime int64) string {
	var b [16]byte
	binary.LittleEndian.PutUint64(b[:8], uint64(size))
	binary.LittleEndian.PutUint64(b[8:], uint64(modTime))
	return fmt.Sprintf("%016x", xxh3.Hash(b[:]))
}
