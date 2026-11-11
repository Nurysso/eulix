// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package setup contains code related to init flow of eulix
package setup

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// EnvFile edits a .env in place: Save rewrites only the lines of keys that
// were Set to a new value, so comments, blank lines and other keys survive.
type EnvFile struct {
	path    string
	values  map[string]string
	lines   []string        // the file as loaded, without line endings
	updated map[string]bool // keys Set to a new value since loading
	added   []string        // keys not in the file, in Set order
	changed bool
}

func LoadEnvFile(path string) *EnvFile {
	envFile := &EnvFile{path: path, values: make(map[string]string), updated: make(map[string]bool)}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return envFile
	}
	envFile.lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	for _, line := range envFile.lines {
		if key, value, ok := parseEnvLine(line); ok {
			envFile.values[key] = value
		}
	}
	return envFile
}

func parseEnvLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		value = value[1 : len(value)-1]
	}
	return key, value, true
}

func (envFile *EnvFile) Set(key, value string) {
	if old, exists := envFile.values[key]; exists && old == value {
		return
	}
	if _, exists := envFile.values[key]; !exists {
		envFile.added = append(envFile.added, key)
	}
	envFile.values[key] = value
	envFile.updated[key] = true
	envFile.changed = true
}

func (envFile *EnvFile) HasValues() bool {
	return len(envFile.values) > 0
}

func (envFile *EnvFile) Changed() bool {
	return envFile.changed
}

func (envFile *EnvFile) Save() error {
	if !envFile.changed {
		return nil
	}
	// // Explicitly secure permissions even if the file pre-existed
	encoded := make(map[string]string, len(envFile.updated))
	for key := range envFile.updated {
		value, err := encodeEnvValue(key, envFile.values[key])
		if err != nil {
			return err
		}
		encoded[key] = value
	}
	// Lock the file down before any secret reaches it, including a file that
	// already existed. If that fails, the keys must not be written.
	_ = os.Chmod(envFile.path, 0o600) // a read-only (0400) .env the user owns must stay savable
	file, err := os.OpenFile(envFile.path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := restrictToOwner(envFile.path); err != nil {
		return fmt.Errorf("restrict permissions on %s: %w", envFile.path, err)
	}

	var builder strings.Builder
	for _, line := range envFile.lines {
		if key, _, ok := parseEnvLine(line); ok && envFile.updated[key] {
			line = key + "=" + encoded[key]
		}
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	for _, key := range envFile.added {
		fmt.Fprintf(&builder, "%s=%s\n", key, encoded[key])
	}
	return os.WriteFile(envFile.path, []byte(builder.String()), 0o600)
}

// encodeEnvValue writes value so godotenv reads back exactly the same thing.
// Errors name the key only: the value may be a secret.
func encodeEnvValue(key, value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("value for %s contains a line break", key)
	}
	// Unquoted, " #" starts a comment and outer spaces are dropped.
	if !strings.Contains(value, " #") && value == strings.TrimSpace(value) &&
		!strings.HasPrefix(value, `"`) && !strings.HasPrefix(value, "'") {
		return value, nil
	}
	if strings.Contains(value, "'") {
		return "", fmt.Errorf("value for %s cannot be stored in .env: it needs quoting but contains a single quote", key)
	}
	quoted := "'" + value + "'"
	// godotenv reads a trailing "\'" as an escaped quote and then drops the
	// whole file, so refuse anything that does not read back unchanged.
	if got, err := godotenv.Unmarshal(key + "=" + quoted); err != nil || got[key] != value {
		return "", fmt.Errorf("value for %s cannot be stored in .env: it does not read back unchanged when quoted", key)
	}
	return quoted, nil
}
