// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"eulix/internal/config"
	"eulix/internal/utils"
)

const analysisCompleteFile = "analysis-complete.json"

type analysisSettings struct {
	Version int    `json:"version"`
	Model   string `json:"model"`
	Engine  string `json:"engine"`
	Prism   int    `json:"prism"`
}

type analysisArtifact struct {
	Size    int64 `json:"size"`
	ModTime int64 `json:"mod_time"`
}

type analysisCompletion struct {
	Settings  analysisSettings            `json:"settings"`
	Artifacts map[string]analysisArtifact `json:"artifacts"`
}

func pipelineSettings(cfg *config.Config) analysisSettings {
	// These are the output-affecting settings actually passed to subprocesses.
	// Do not persist unrelated configuration (in particular provider API keys).
	return analysisSettings{Version: 1, Model: cfg.Embeddings.Model, Engine: cfg.Embeddings.Engine, Prism: cfg.Parser.PrismV}
}

func analysisArtifacts(dir string) (map[string]analysisArtifact, error) {
	// The parser's checksum and the files consumed by embedding/retrieval.
	names := []string{utils.ChecksumFileName, "kb.json", "kb_index.json", "kb_call_graph.json", "embeddings.bin", "vectors.bin"}
	artifacts := make(map[string]analysisArtifact, len(names))
	for _, name := range names {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("analysis artifact %s: %w", name, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, fmt.Errorf("analysis artifact %s is not a nonempty regular file", name)
		}
		artifacts[name] = analysisArtifact{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
	}
	return artifacts, nil
}

func analysisComplete(dir string, cfg *config.Config) bool {
	data, err := os.ReadFile(filepath.Join(dir, analysisCompleteFile))
	if err != nil {
		return false
	}
	var saved analysisCompletion
	if err := json.Unmarshal(data, &saved); err != nil || saved.Settings != pipelineSettings(cfg) {
		return false
	}
	current, err := analysisArtifacts(dir)
	return err == nil && maps.Equal(saved.Artifacts, current)
}

func saveAnalysisCompletion(dir string, cfg *config.Config) error {
	artifacts, err := analysisArtifacts(dir)
	if err != nil {
		return err
	}
	data, err := json.Marshal(analysisCompletion{Settings: pipelineSettings(cfg), Artifacts: artifacts})
	if err != nil {
		return err
	}
	// Publish atomically only after both subprocesses succeeded. A crash while
	// writing this file leaves no valid marker, so the next analyze retries.
	file, err := os.CreateTemp(dir, ".analysis-complete-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(dir, analysisCompleteFile))
}
