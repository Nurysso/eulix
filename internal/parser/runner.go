package parser

import (
    "encoding/json"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strings"

    "eulix/internal/config"
)

// ParseStats represents parsing statistics
type ParseStats struct {
    Files     int
    TotalLOC  int
    Functions int
    Classes   int
    Methods   int
}

// RunParser executes the Rust parser binary
func RunParser() error {
    _, err := RunParserWithStats()
    return err
}

func RunEmbed() error {
    err := RunEmbedWithStats()
    return err
}

// RunParserWithStats executes parser and returns statistics
func RunParserWithStats() (*ParseStats, error) {
    cfg := config.Load()

    // Find parser binary
    parserPath := findParserBinary(cfg.Parser.BinaryPath)
    if parserPath == "" {
        return nil, fmt.Errorf("eulix_parser binary not found")
    }

    // Prepare command
    cmd := exec.Command(
        parserPath,
        "--root", ".",
        "--output", ".eulix/knowledge_base.json",
        "--threads", fmt.Sprintf("%d", cfg.Parser.Threads),
    )

    // Capture output
    output, err := cmd.CombinedOutput()
    if err != nil {
        return nil, fmt.Errorf("parser failed: %w\n%s", err, string(output))
    }

    // Parse output to extract stats
    stats := parseStatsFromOutput(string(output))

    return stats, nil
}

// parseStatsFromOutput extracts statistics from parser output
func parseStatsFromOutput(output string) *ParseStats {
    stats := &ParseStats{}

    lines := strings.Split(output, "\n")
    for _, line := range lines {
        line = strings.TrimSpace(line)

        // Parse lines like "│ Files:         40                │"
        if strings.Contains(line, "Files:") {
            fmt.Sscanf(line, "│ Files: %d", &stats.Files)
        } else if strings.Contains(line, "Lines:") {
            fmt.Sscanf(line, "│ Lines: %d", &stats.TotalLOC)
        } else if strings.Contains(line, "Functions:") {
            fmt.Sscanf(line, "│ Functions: %d", &stats.Functions)
        } else if strings.Contains(line, "Classes:") {
            fmt.Sscanf(line, "│ Classes: %d", &stats.Classes)
        } else if strings.Contains(line, "Methods:") {
            fmt.Sscanf(line, "│ Methods: %d", &stats.Methods)
        }
    }

    // Fallback: read from KB if parsing failed
    if stats.Files == 0 {
        if kb, err := loadKBMetadata(); err == nil {
            stats.Files = kb.TotalFiles
            stats.TotalLOC = kb.TotalLOC
            stats.Functions = kb.TotalFunctions
            stats.Classes = kb.TotalClasses
            stats.Methods = kb.TotalMethods
        }
    }

    return stats
}

// loadKBMetadata loads metadata from knowledge base
func loadKBMetadata() (*struct {
    TotalFiles     int `json:"total_files"`
    TotalLOC       int `json:"total_loc"`
    TotalFunctions int `json:"total_functions"`
    TotalClasses   int `json:"total_classes"`
    TotalMethods   int `json:"total_methods"`
}, error) {
    data, err := os.ReadFile(".eulix/knowledge_base.json")
    if err != nil {
        return nil, err
    }

    var kb struct {
        Metadata struct {
            TotalFiles     int `json:"total_files"`
            TotalLOC       int `json:"total_loc"`
            TotalFunctions int `json:"total_functions"`
            TotalClasses   int `json:"total_classes"`
            TotalMethods   int `json:"total_methods"`
        } `json:"metadata"`
    }

    if err := json.Unmarshal(data, &kb); err != nil {
        return nil, err
    }

    return &kb.Metadata, nil
}

// findParserBinary locates the eulix_parser executable
func findParserBinary(configPath string) string {
    // 1. Try config path
    if configPath != "" {
        if _, err := os.Stat(configPath); err == nil {
            return configPath
        }
    }

    // 2. Try in PATH
    if path, err := exec.LookPath("eulix_parser"); err == nil {
        return path
    }

    // 3. Try common locations
    possiblePaths := []string{
        "~/.local/bin/eulix_parser",
        "./eulix_parser",
        "/usr/local/bin/eulix_parser",
        "~/bin/eulix_parser",
    }

    // Add OS-specific extension
    if runtime.GOOS == "windows" {
        for i, p := range possiblePaths {
            possiblePaths[i] = p + ".exe"
        }
    }

    for _, path := range possiblePaths {
        // Expand home directory
        if strings.HasPrefix(path, "~/") {
            home, err := os.UserHomeDir()
            if err == nil {
                path = filepath.Join(home, path[2:])
            }
        }

        absPath, err := filepath.Abs(path)
        if err != nil {
            continue
        }
        if _, err := os.Stat(absPath); err == nil {
            return absPath
        }
    }

    return ""
}

// RunEmbedWithStats executes embedder and returns statistics
func RunEmbedWithStats() error {
    cfg := config.Load()

    // Find embedder binary
    embedPath := findEmbedBinary(cfg.Parser.BinaryPath)
    if embedPath == "" {
        return fmt.Errorf("eulix_embed binary not found")
    }

    // Prepare command
    cmd := exec.Command(
        embedPath,
        "--input", ".eulix/knowledge_base.json",
        "--context", ".eulix/context.json",
        "--embeddings", ".eulix/embeddings.bin",
        "--precompute-embeddings",
    )

    // Capture output
    output, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("embedder failed: %w\n%s", err, string(output))
    }

    return nil
}

// findEmbedBinary locates the eulix_embed executable
func findEmbedBinary(configPath string) string {
    // 1. Try config path
    if configPath != "" {
        if _, err := os.Stat(configPath); err == nil {
            return configPath
        }
    }

    // 2. Try in PATH
    if path, err := exec.LookPath("eulix_embed"); err == nil {
        return path
    }

    // 3. Try common locations
    possiblePaths := []string{
        "~/.local/bin/eulix_embed",
        "./eulix_embed",
        "/usr/local/bin/eulix_embed",
        "~/bin/eulix_embed",
    }

    // Add OS-specific extension
    if runtime.GOOS == "windows" {
        for i, p := range possiblePaths {
            possiblePaths[i] = p + ".exe"
        }
    }

    for _, path := range possiblePaths {
        // Expand home directory
        if strings.HasPrefix(path, "~/") {
            home, err := os.UserHomeDir()
            if err == nil {
                path = filepath.Join(home, path[2:])
            }
        }

        absPath, err := filepath.Abs(path)
        if err != nil {
            continue
        }
        if _, err := os.Stat(absPath); err == nil {
            return absPath
        }
    }

    return ""
}
