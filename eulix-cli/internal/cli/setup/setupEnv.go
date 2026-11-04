// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package setup contains code related to init flow of eulix
package setup

import (
	"fmt"
	"os"
	"strings"
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
	// Explicitly secure permissions even if the file pre-existed
	_ = os.Chmod(envFile.path, 0o600)

	var builder strings.Builder
	for _, line := range envFile.lines {
		if key, _, ok := parseEnvLine(line); ok && envFile.updated[key] {
			line = key + "=" + envFile.values[key]
		}
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	for _, key := range envFile.added {
		fmt.Fprintf(&builder, "%s=%s\n", key, envFile.values[key])
	}
	return os.WriteFile(envFile.path, []byte(builder.String()), 0o600)
}
