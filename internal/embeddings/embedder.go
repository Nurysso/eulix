//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package embeddings provides the command-line interface implementation for EULIX.

/*
This file is responsible for the eulix_embed related operations
except the analyze command.
*/

package embeddings

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Embedder wraps the eulix_embed.py Python script for embedding generation.
// This ensures consistent embeddings with the KB generation pipeline.
type Embedder struct {
	scriptPath string
	pythonPath string
	venvEnv    []string
	model      string
	backend    string
	dimension  int
}

// QueryEmbedder is an alias for Embedder to maintain compatibility.
type QueryEmbedder = Embedder

// QueryEmbeddingResult represents the JSON output from eulix_embed.
type QueryEmbeddingResult struct {
	Query     string    `json:"query"`
	Model     string    `json:"model"`
	Dimension int       `json:"dimension"`
	Embedding []float32 `json:"embedding"`
}

// getVenvPython validates the venv at the given path and returns the Python
// interpreter path and a venv-activated environment slice.
// Mirrors the logic in cli/analyze.go so both callers behave identically.
func getVenvPython(venvPath string) (string, []string, error) {
	pythonPath := filepath.Join(venvPath, "bin", "python")
	if _, err := os.Stat(pythonPath); err != nil {
		return "", nil, fmt.Errorf("venv python not found at %s: %w", pythonPath, err)
	}

	out, err := exec.Command(pythonPath, "--version").Output()
	if err != nil {
		return "", nil, fmt.Errorf("failed to check python version: %w", err)
	}
	ver := strings.TrimSpace(string(out))
	if !strings.HasPrefix(ver, "Python 3.10") && !strings.HasPrefix(ver, "Python 3.11") {
		return "", nil, fmt.Errorf("unsupported Python version: %s (want 3.10 or 3.11)", ver)
	}

	// Prepend venv bin to PATH so transitive imports resolve correctly.
	venvBin := filepath.Join(venvPath, "bin")
	env := os.Environ()
	newEnv := make([]string, 0, len(env)+1)
	foundPath := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			newEnv = append(newEnv, "PATH="+venvBin+string(os.PathListSeparator)+e[5:])
			foundPath = true
		} else {
			newEnv = append(newEnv, e)
		}
	}
	if !foundPath {
		newEnv = append(newEnv, "PATH="+venvBin)
	}
	return pythonPath, newEnv, nil
}

// findEulixEmbed locates eulix_embed.py and resolves a usable Python interpreter
// from the canonical venv at ~/.Eulix/.venv.
func findEulixEmbed() (scriptPath string, pythonPath string, venvEnv []string, err error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", "", nil, fmt.Errorf("cannot determine home directory: %w", err)
	}

	script := filepath.Join(homeDir, ".Eulix", "eulix_embed.py")
	if _, err := os.Stat(script); err != nil {
		return "", "", nil, fmt.Errorf("eulix_embed.py not found (expected at %s)", script)
	}

	venvPath := filepath.Join(homeDir, ".Eulix", ".venv")
	python, env, err := getVenvPython(venvPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("found embed script but no usable Python: %w", err)
	}

	return script, python, env, nil
}

// VectorWeaver creates a new query embedder, locating the embed script and
// venv interpreter automatically.
func VectorWeaver(model string) (*Embedder, error) {
	scriptPath, pythonPath, venvEnv, err := findEulixEmbed()
	if err != nil {
		return nil, err
	}
	return &Embedder{
		scriptPath: scriptPath,
		pythonPath: pythonPath,
		venvEnv:    venvEnv,
		model:      model,
		dimension:  0,
	}, nil
}

// Embed generates an embedding vector for the given text.
// Uses binary output for performance; call EmbedQuery for JSON (debug) output.
func (e *Embedder) Embed(text string) ([]float32, error) {
	return e.EmbedQueryBinary(text)
}

// EmbedQuery generates an embedding using JSON output (for debugging).
func (e *Embedder) EmbedQuery(query string) ([]float32, error) {
	cmd := exec.Command(
		e.pythonPath, e.scriptPath,
		"query",
		"-q", query,
		"-m", e.model,
		"-f", "json",
	)
	cmd.Env = e.venvEnv

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("eulix_embed failed: %w\nstderr: %s", err, stderr.String())
	}

	var result QueryEmbeddingResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("failed to parse embedding result: %w", err)
	}

	if e.dimension != result.Dimension {
		e.dimension = result.Dimension
	}
	return result.Embedding, nil
}

// EmbedQueryBinary generates an embedding using binary output (faster, recommended).
func (e *Embedder) EmbedQueryBinary(query string) ([]float32, error) {
	cmd := exec.Command(
		e.pythonPath, e.scriptPath,
		"query",
		"-q", query,
		"-m", e.model,
		"-f", "binary",
	)
	cmd.Env = e.venvEnv

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("eulix_embed failed: %w\nstderr: %s", err, stderr.String())
	}

	data := stdout.Bytes()
	if len(data) < 4 {
		return nil, fmt.Errorf("invalid binary output: too short")
	}

	dimension := binary.LittleEndian.Uint32(data[0:4])
	expectedSize := 4 + int(dimension)*4
	if len(data) != expectedSize {
		return nil, fmt.Errorf("invalid binary output: expected %d bytes, got %d", expectedSize, len(data))
	}

	if e.dimension != int(dimension) {
		e.dimension = int(dimension)
	}

	embedding := make([]float32, dimension)
	offset := 4
	for i := range embedding {
		bits := binary.LittleEndian.Uint32(data[offset : offset+4])
		embedding[i] = math.Float32frombits(bits)
		offset += 4
	}
	return embedding, nil
}

// GetDimension returns the embedding dimension (0 until first Embed call).
func (e *Embedder) GetDimension() int { return e.dimension }

// GetModel returns the model name.
func (e *Embedder) GetModel() string { return e.model }

// Close cleans up resources (no-op for command-based embedder).
func (e *Embedder) Close() error { return nil }

// BatchEmbed generates embeddings for multiple texts in order.
func (e *Embedder) BatchEmbed(texts []string) ([][]float32, error) {
	embeddings := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := e.Embed(text)
		if err != nil {
			return nil, fmt.Errorf("failed to embed text %d: %w", i, err)
		}
		embeddings[i] = emb
	}
	return embeddings, nil
}

// VerifyConsistency checks that the embedder produces deterministic results.
func (e *Embedder) VerifyConsistency(testText string) error {
	emb1, err := e.Embed(testText)
	if err != nil {
		return fmt.Errorf("first embedding failed: %w", err)
	}
	emb2, err := e.Embed(testText)
	if err != nil {
		return fmt.Errorf("second embedding failed: %w", err)
	}
	if len(emb1) != len(emb2) {
		return fmt.Errorf("dimension mismatch: %d vs %d", len(emb1), len(emb2))
	}
	for i := range emb1 {
		if math.Abs(float64(emb1[i]-emb2[i])) > 1e-6 {
			return fmt.Errorf("embeddings not consistent at index %d: %f vs %f", i, emb1[i], emb2[i])
		}
	}
	return nil
}

// GetModelInfo returns metadata about the current embedder configuration.
func (e *Embedder) GetModelInfo() map[string]interface{} {
	return map[string]interface{}{
		"model":     e.model,
		"backend":   e.backend,
		"dimension": e.dimension,
		"script":    e.scriptPath,
		"python":    e.pythonPath,
	}
}
