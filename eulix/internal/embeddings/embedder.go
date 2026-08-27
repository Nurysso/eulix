//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package embeddings provides the command-line interface implementation for EULIX.

/*
This file is responsible for the eulix_embed related operations
except the analyze command.

VectorWeaver starts eulix_embed in "serve" mode.  The mode is Set by
config.Project.EmbedIs:
  - "script" → venv Python + $HOME/.Eulix/eulix_embed/main.py  (default)
  - "bin"    → embedded eulix_embed binary extracted from the eulix executable
*/

package embeddings

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sync"
	"time"
)

// Embedder holds a long-lived eulix_embed serve subprocess.
// The process keeps the model loaded between calls, so each
// EmbedQueryBinary call costs only the encode() time (~5-50ms) rather
// than a full startup (~3-5s).
//
// Protocol: newline-delimited JSON on stdin/stdout.
//
//	Send:    {"query": "text"}\n
//	Receive: {"embedding": [f32,...], "dimension": N, "model": "..."}\n
//	Error:   {"error": "message"}\n
type Embedder struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	enc    *json.Encoder
	mu     sync.Mutex
	model  string
	dim    int
}

// serveReadyMsg is what the serve command writes to stdout as its first line
// when the model is loaded and ready.
type serveReadyMsg struct {
	Ready bool   `json:"ready"`
	Model string `json:"model"`
	Dim   int    `json:"dim"`
}

// serveRequest is what we send to the process on stdin.
type serveRequest struct {
	Query string `json:"query"`
}

// serveResponse is what the process writes to stdout per request.
type serveResponse struct {
	Embedding []float32 `json:"embedding"`
	Dimension int       `json:"dimension"`
	Model     string    `json:"model"`
	Error     string    `json:"error"`
}

// VectorWeaver starts eulix_embed in serve mode and waits for the model to
// finish loading.  Which backend is launched depends on cfg.Project.Embedis:
//   - "script" → Python venv interpreter running eulix_embed/main.py
//   - "bin"    → embedded eulix_embed binary extracted from the executable
//
// The subprocess stays alive for the lifetime of the returned Embedder;
// call Close() when done.
func VectorWeaver(model string) (*Embedder, error) {
	var cmd *exec.Cmd
	scriptPath, pythonPath, venvEnv, err := FindEulixEmbed()
	if err != nil {
		return nil, err
	}
	cmd = exec.Command(pythonPath, scriptPath, "serve", "-m", model)
	cmd.Env = venvEnv

	// Pipe stdin so we can send JSON requests.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	// Pipe stdout so we can read JSON responses.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	cmd.Stderr = nil // swap to os.Stderr to debug

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start eulix_embed serve: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	// Increase scanner buffer for large embedding responses.
	// 768-dim = ~25 KB JSON; 3072-dim = ~100 KB JSON. 4 MB is safe.
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	e := &Embedder{
		cmd:    cmd,
		stdin:  stdin,
		stdout: scanner,
		enc:    json.NewEncoder(stdin),
		model:  model,
	}

	// Block until the ready signal arrives or we time out.
	if err := e.waitReady(); err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			return nil, fmt.Errorf("embed process failed to start: %w (also failed to kill process: %v)", err, killErr)
		}
		return nil, fmt.Errorf("embed process failed to start: %w", err)
	}

	return e, nil
}

// waitReady reads the first JSON line from the subprocess stdout and checks
// that it is the ready signal.
func (e *Embedder) waitReady() error {
	done := make(chan error, 1)

	go func() {
		for e.stdout.Scan() {
			line := e.stdout.Bytes()
			if len(line) == 0 || line[0] != '{' {
				// Skip human-readable startup banners (e.g. "Using ONNX-RUNTIME")
				continue
			}

			var msg serveReadyMsg
			if err := json.Unmarshal(line, &msg); err != nil {
				// It starts with '{' but isn't the ready message — skip it too.
				continue
			}
			if !msg.Ready {
				done <- fmt.Errorf("embed process sent non-ready first message: %s", line)
				return
			}
			e.dim = msg.Dim
			done <- nil
			return
		}

		err := e.stdout.Err()
		if err == nil {
			err = fmt.Errorf("subprocess stdout closed before ready signal")
		}
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(60 * time.Second):
		return fmt.Errorf("timed out after 60s waiting for embed process ready signal")
	}
}

// EmbedQueryBinary embeds a single query string and returns the float32 vector.
// Thread-safe via mutex; only one request in flight at a time.
func (e *Embedder) EmbedQueryBinary(query string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	req := serveRequest{Query: query}
	if err := e.enc.Encode(req); err != nil {
		return nil, fmt.Errorf("write to embed process: %w", err)
	}

	if !e.stdout.Scan() {
		err := e.stdout.Err()
		if err == nil {
			err = fmt.Errorf("embed process stdout closed unexpectedly")
		}
		return nil, fmt.Errorf("read from embed process: %w", err)
	}

	var resp serveResponse
	if err := json.Unmarshal(e.stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parse embed response: %w\nraw: %s", err, e.stdout.Bytes())
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("embed process error: %s", resp.Error)
	}
	if len(resp.Embedding) == 0 {
		return nil, fmt.Errorf("embed process returned empty embedding")
	}

	e.dim = resp.Dimension
	return resp.Embedding, nil
}

// Close sends a clean shutdown to the subprocess and waits for it to exit.
func (e *Embedder) Close() error {
	_ = e.enc.Encode(map[string]bool{"shutdown": true})
	_ = e.stdin.Close()
	return e.cmd.Wait()
}

// Embed generates an embedding vector for the given text.
func (e *Embedder) Embed(text string) ([]float32, error) {
	return e.EmbedQueryBinary(text)
}

// GetDimension returns the embedding dimension (0 until ready signal received).
func (e *Embedder) GetDimension() int { return e.dim }

// GetModel returns the model name.
func (e *Embedder) GetModel() string { return e.model }

// BatchEmbed embeds multiple texts sequentially.
func (e *Embedder) BatchEmbed(texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := e.Embed(text)
		if err != nil {
			return nil, fmt.Errorf("embed text %d: %w", i, err)
		}
		result[i] = emb
	}
	return result, nil
}

// BatchEmbedBatch sends all texts in a single request to the subprocess,
// which is more efficient than calling Embed in a loop for large batches.
func (e *Embedder) BatchEmbedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	type batchRequest struct {
		Queries []string `json:"queries"`
	}
	type batchResponse struct {
		Embeddings [][]float32 `json:"embeddings"`
		Dimension  int         `json:"dimension"`
		Model      string      `json:"model"`
		Error      string      `json:"error"`
	}

	if err := e.enc.Encode(batchRequest{Queries: texts}); err != nil {
		return nil, fmt.Errorf("write batch request: %w", err)
	}

	if !e.stdout.Scan() {
		err := e.stdout.Err()
		if err == nil {
			err = fmt.Errorf("stdout closed")
		}
		return nil, fmt.Errorf("read batch response: %w", err)
	}

	var resp batchResponse
	if err := json.Unmarshal(e.stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parse batch response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("batch embed error: %s", resp.Error)
	}
	return resp.Embeddings, nil
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
			return fmt.Errorf("embedding not deterministic at index %d: %f vs %f", i, emb1[i], emb2[i])
		}
	}
	return nil
}

// GetModelInfo returns metadata about the current embedder configuration.
func (e *Embedder) GetModelInfo() map[string]interface{} {
	return map[string]interface{}{
		"model":     e.model,
		"dimension": e.dim,
	}
}
