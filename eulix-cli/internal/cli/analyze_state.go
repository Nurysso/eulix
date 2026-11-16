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
	"eulix/internal/query/retrieval"
	"eulix/internal/utils"
)

const analysisCompleteFile = "analysis-complete.json"

type analysisSettings struct {
	Version   int    `json:"version"`
	Retrieval string `json:"retrieval"`
	Model     string `json:"model"`
	Engine    string `json:"engine"`
	Prism     int    `json:"prism"`
}

// needsEmbeddings reports whether the configured retrieval engine reads the
// embedding artifacts. An unknown engine falls back to true so analyze still
// produces everything; the query side reports the bad config value.
func needsEmbeddings(cfg *config.Config) bool {
	eng, err := retrieval.ResolveEngine(cfg.Project.Engine)
	return err != nil || eng.UsesSemantic()
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
	// Embedding settings only matter when embeddings are generated, so an
	// ehnas project is not re-analyzed when the embedding model changes.
	if !needsEmbeddings(cfg) {
		return analysisSettings{Version: 2, Retrieval: "ehnas", Prism: cfg.Parser.PrismV}
	}
	return analysisSettings{Version: 2, Retrieval: "etiya", Model: cfg.Embeddings.Model, Engine: cfg.Embeddings.Engine, Prism: cfg.Parser.PrismV}
}

func analysisArtifacts(dir string, cfg *config.Config) (map[string]analysisArtifact, error) {
	// The parser's checksum and the files consumed by embedding/retrieval.
	names := []string{utils.ChecksumFileName, "kb.json", "kb_index.json", "kb_call_graph.json"}
	if needsEmbeddings(cfg) {
		names = append(names, "embeddings.bin", "vectors.bin")
	}
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
	current, err := analysisArtifacts(dir, cfg)
	return err == nil && maps.Equal(saved.Artifacts, current)
}

func saveAnalysisCompletion(dir string, cfg *config.Config) error {
	artifacts, err := analysisArtifacts(dir, cfg)
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
