package checksum

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	// "fmt"
	"io"
	"os"
	"path/filepath"
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
	projectPath string
}

func NewDetector(projectPath string) *Detector {
	return &Detector{projectPath: projectPath}
}

func (d *Detector) Calculate() (*Checksum, error) {
	fileHashes := make(map[string]string)
	totalLines := 0
	totalFiles := 0

	err := filepath.Walk(d.projectPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
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
		AnalysisVersion: "1.0.0",
	}, nil
}

func (d *Detector) Save(checksum *Checksum) error {
	checksumPath := filepath.Join(d.projectPath, ".eulix", "checksum.json")
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
		".java": true,
		".c":    true,
		".cpp":  true,
		".h":    true,
		".rs":   true,
	}
	return sourceExts[ext]
}
