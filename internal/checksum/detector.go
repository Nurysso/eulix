package checksum

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Checksum struct {
	ProjectPath     string            `json:"project_path"`
	TotalFiles      int               `json:"total_files"`
	TotalLines      int               `json:"total_lines"`
	Hash            string            `json:"hash"`
	FileHashes      map[string]string `json:"file_hashes"`
	LastAnalyzed    time.Time         `json:"last_analyzed"`
	AnalysisVersion string            `json:"analysis_version"`
}

type Detector struct {
	projectPath   string
	ignorePatterns []string
}

func HashHound(projectPath string) *Detector {
	d := &Detector{projectPath: projectPath}
	d.loadIgnorePatterns()
	return d
}

// loadIgnorePatterns reads .euignore file and loads patterns
func (d *Detector) loadIgnorePatterns() {
	ignorePath := filepath.Join(d.projectPath, ".euignore")
	file, err := os.Open(ignorePath)
	if err != nil {
		// .euignore doesn't exist, that's okay
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		d.ignorePatterns = append(d.ignorePatterns, line)
	}
}

// shouldIgnore checks if a path should be ignored
func (d *Detector) shouldIgnore(path string) bool {
	relPath, err := filepath.Rel(d.projectPath, path)
	if err != nil {
		return false
	}

	// Always ignore .eulix directory
	if strings.HasPrefix(relPath, ".eulix") || strings.Contains(relPath, string(filepath.Separator)+".eulix") {
		return true
	}

	// Check against ignore patterns
	for _, pattern := range d.ignorePatterns {
		matched, err := filepath.Match(pattern, relPath)
		if err == nil && matched {
			return true
		}

		// Also check if pattern matches any component of the path
		pathParts := strings.Split(relPath, string(filepath.Separator))
		for _, part := range pathParts {
			matched, err := filepath.Match(pattern, part)
			if err == nil && matched {
				return true
			}
		}

		// Check for prefix match (directory patterns)
		if strings.HasSuffix(pattern, "/") {
			if strings.HasPrefix(relPath, strings.TrimSuffix(pattern, "/")) {
				return true
			}
		}

		// Check for exact match or prefix match
		if strings.HasPrefix(relPath, pattern) {
			return true
		}
	}

	return false
}

func (d *Detector) Calculate() (*Checksum, error) {
	fileHashes := make(map[string]string)
	totalLines := 0
	totalFiles := 0

	err := filepath.Walk(d.projectPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Check if path should be ignored
		if d.shouldIgnore(path) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip directories and hidden files
		if info.IsDir() || filepath.Base(path)[0] == '.' {
			return nil
		}

		// Skip non-source files
		ext := filepath.Ext(path)
		if !isSourceFile(ext) {
			return nil
		}

		// Calculate file hash
		hash, lines, err := hashFile(path)
		if err != nil {
			return nil // Skip files we can't read
		}

		relPath, _ := filepath.Rel(d.projectPath, path)
		fileHashes[relPath] = hash
		totalLines += lines
		totalFiles++

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Calculate project hash
	h := sha256.New()
	for _, hash := range fileHashes {
		h.Write([]byte(hash))
	}
	projectHash := hex.EncodeToString(h.Sum(nil))

	return &Checksum{
		ProjectPath:     d.projectPath,
		TotalFiles:      totalFiles,
		TotalLines:      totalLines,
		Hash:            projectHash,
		FileHashes:      fileHashes,
		LastAnalyzed:    time.Now(),
		AnalysisVersion: "0.5.3",
	}, nil
}

func (d *Detector) Save(checksum *Checksum) error {
	eulixDir := filepath.Join(d.projectPath, ".eulix")
	if err := os.MkdirAll(eulixDir, 0755); err != nil {
		return err
	}

	checksumPath := filepath.Join(eulixDir, "checksum.json")
	data, err := json.MarshalIndent(checksum, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(checksumPath, data, 0644)
}

func (d *Detector) Load() (*Checksum, error) {
	checksumPath := filepath.Join(d.projectPath, ".eulix", "checksum.json")
	data, err := os.ReadFile(checksumPath)
	if err != nil {
		return nil, err
	}

	var checksum Checksum
	if err := json.Unmarshal(data, &checksum); err != nil {
		return nil, err
	}

	return &checksum, nil
}

func (d *Detector) CompareChecksums(stored, current *Checksum) float64 {
	if stored == nil || current == nil {
		return 1.0
	}

	added := 0
	deleted := 0
	modified := 0

	// Count changes
	for file := range current.FileHashes {
		if storedHash, exists := stored.FileHashes[file]; !exists {
			added++
		} else if storedHash != current.FileHashes[file] {
			modified++
		}
	}

	for file := range stored.FileHashes {
		if _, exists := current.FileHashes[file]; !exists {
			deleted++
		}
	}

	totalChanges := added + deleted + modified
	if stored.TotalFiles == 0 {
		return 1.0
	}

	return float64(totalChanges) / float64(stored.TotalFiles)
}

func hashFile(path string) (string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	lines := 0
	buf := make([]byte, 4096)

	for {
		n, err := f.Read(buf)
		if err != nil && err != io.EOF {
			return "", 0, err
		}
		if n == 0 {
			break
		}

		h.Write(buf[:n])

		// Count newlines
		for i := 0; i < n; i++ {
			if buf[i] == '\n' {
				lines++
			}
		}
	}

	return hex.EncodeToString(h.Sum(nil)), lines, nil
}

func isSourceFile(ext string) bool {
	sourceExts := map[string]bool{
		".go":   true,
		".py":   true,
		".js":   true,
		".ts":   true,
		".tsx":  true,
		".jsx":  true,
		".java": true,
		".c":    true,
		".cpp":  true,
		".h":    true,
		".hpp":  true,
		".rs":   true,
		".rb":   true,
		".php":  true,
		".cs":   true,
		".swift": true,
		".kt":   true,
		".scala": true,
	}
	return sourceExts[ext]
}
