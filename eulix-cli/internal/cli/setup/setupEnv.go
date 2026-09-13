//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer mnae (Nurysso) contact - nurysso [at] proton.me

// package setup contains code related to init flow of eulix

package setup

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

type EnvFile struct {
	path     string
	values   map[string]string
	keyOrder []string
}

func LoadEnvFile(path string) *EnvFile {
	envFile := &EnvFile{path: path, values: make(map[string]string)}
	data, err := os.ReadFile(path)
	if err != nil {
		return envFile
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := parseEnvLine(line)
		if !ok {
			continue
		}
		envFile.values[key] = value
		envFile.keyOrder = append(envFile.keyOrder, key)
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
	if _, exists := envFile.values[key]; !exists {
		envFile.keyOrder = append(envFile.keyOrder, key)
	}
	envFile.values[key] = value
}

func (envFile *EnvFile) HasValues() bool {
	return len(envFile.values) > 0
}

func (envFile *EnvFile) Save() error {
	if !envFile.HasValues() {
		return nil
	}
	var builder strings.Builder
	writtenKeys := make(map[string]bool, len(envFile.values))
	for _, key := range envFile.keyOrder {
		if writtenKeys[key] {
			continue
		}
		writtenKeys[key] = true
		fmt.Fprintf(&builder, "%s=%s\n", key, envFile.values[key])
	}
	writeUnorderedKeys(&builder, envFile.values, writtenKeys)
	return os.WriteFile(envFile.path, []byte(builder.String()), 0600)
}

func writeUnorderedKeys(builder *strings.Builder, values map[string]string, writtenKeys map[string]bool) {
	if len(writtenKeys) == len(values) {
		return
	}
	var unwrittenKeys []string
	for key := range values {
		if !writtenKeys[key] {
			unwrittenKeys = append(unwrittenKeys, key)
		}
	}
	sort.Strings(unwrittenKeys)
	for _, key := range unwrittenKeys {
		fmt.Fprintf(builder, "%s=%s\n", key, values[key])
	}
}
