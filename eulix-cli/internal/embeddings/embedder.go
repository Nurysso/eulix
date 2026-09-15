//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package embeddings provides the command-line interface implementation for EULIX.

/*
This file is responsible for the eulix_embed related operations
except the analyze command.

VectorWeaver starts eulix_embed in "server" mode.  The mode is Set by
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
	"log"
	"math"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const stderrTailSize = 50
const defaultRequestTimeout = 30 * time.Second

type stderrTail struct {
	mu    sync.Mutex
	lines []string
}

// Embedder holds a long-lived eulix_embed server subprocess.
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
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	stdout         *bufio.Scanner
	enc            *json.Encoder
	mu             sync.Mutex
	model          string
	dim            int
	closed         bool
	requestTimeout time.Duration
	stderrTail     *stderrTail
	debug          bool
}

// serverReadyMsg is what the server command writes to stdout as its first line
// when the model is loaded and ready.
type serveReadyMsg struct {
	Ready bool   `json:"ready"`
	Model string `json:"model"`
	Dim   int    `json:"dim"`
	Error string `json:"error"`
}

// serveRequest is what we send to the process on stdin.
type serverRequest struct {
	Query string `json:"query"`
}

// serveResponse is what the process writes to stdout per request.
type serveResponse struct {
	Embedding []float32 `json:"embedding"`
	Dimension int       `json:"dimension"`
	Model     string    `json:"model"`
	Error     string    `json:"error"`
}

// VectorWeaver starts eulix_embed in server mode. debug, when true, streams
// the subprocess's stderr live to the parent's logger; otherwise it's only
// kept in a rolling buffer for diagnosing startup/crash failures.
func VectorWeaver(model string, debug bool) (*Embedder, error) {
	scriptPath, pythonPath, venvEnv, err := FindEulixEmbed()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(pythonPath, scriptPath, "server", "-m", model)
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

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	tail := &stderrTail{}
	go streamStderr(model, stderr, tail, debug)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start eulix_embed server: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024) // Increase scanner buffer for large embedding responses.

	e := &Embedder{
		cmd:            cmd,
		stdin:          stdin,
		stdout:         scanner,
		enc:            json.NewEncoder(stdin),
		model:          model,
		requestTimeout: defaultRequestTimeout,
		stderrTail:     tail,
		debug:          debug,
	}

	// Block until the ready signal arrives or we time out
	if err := e.waitReady(); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		if diag := tail.String(); diag != "" {
			return nil, fmt.Errorf("embed process failed to start: %w\nrecent stderr:\n%s", err, diag)
		}
		return nil, fmt.Errorf("embed process failed to start: %w", err)
	}

	return e, nil
}

// streamStderr consumes the subprocess's stderr into a rolling tail buffer
// for crash diagnostics, and — when debug is true — also streams it live
// to the parent's logger for troubleshooting.
func streamStderr(model string, r io.Reader, tail *stderrTail, debug bool) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		tail.add(line)
		if debug {
			log.Printf("[eulix_embed:%s] %s", model, line)
		}
	}
}

func (t *stderrTail) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if len(t.lines) > stderrTailSize {
		t.lines = t.lines[len(t.lines)-stderrTailSize:]
	}
}

func (t *stderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

// waitReady reads the first JSON line from the subprocess stdout and checks
// that it is the ready signal.
func (e *Embedder) waitReady() error {
	done := make(chan error, 1)

	go func() {
		for e.stdout.Scan() {
			line := e.stdout.Bytes()
			if len(line) == 0 || line[0] != '{' {
				continue // human-readable startup banner
			}

			var msg serveReadyMsg
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			if !msg.Ready {
				reason := msg.Error
				if reason == "" {
					reason = string(line)
				}
				done <- fmt.Errorf("embed process reported startup failure: %s", reason)
				return
			}
			e.mu.Lock()
			e.dim = msg.Dim
			e.mu.Unlock()
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

// roundTrip writes one request line and reads one response line, bounded by
// requestTimeout. The scanner read happens on a goroutine because
// bufio.Scanner has no native deadline support.
func (e *Embedder) roundTrip(req any) ([]byte, error) {
	if e.closed {
		return nil, fmt.Errorf("embedder is closed")
	}
	if err := e.enc.Encode(req); err != nil {
		return nil, fmt.Errorf("write to embed process: %w", err)
	}

	type result struct {
		line []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		if !e.stdout.Scan() {
			err := e.stdout.Err()
			if err == nil {
				err = fmt.Errorf("embed process stdout closed unexpectedly")
			}
			if diag := e.stderrTail.String(); diag != "" {
				err = fmt.Errorf("%w\nrecent stderr:\n%s", err, diag)
			}
			done <- result{nil, err}
			return
		}
		// Bytes() is only valid until the next Scan — copy it out.
		line := append([]byte(nil), e.stdout.Bytes()...)
		done <- result{line, nil}
	}()

	select {
	case r := <-done:
		return r.line, r.err
	case <-time.After(e.requestTimeout):
		// The subprocess is unresponsive (hung/deadlocked). We can't safely
		// keep using this pipe, so kill it — the caller should treat this
		// Embedder as dead and recreate it.
		_ = e.cmd.Process.Kill()
		return nil, fmt.Errorf("embed process timed out after %s (process killed)", e.requestTimeout)
	}
}

// EmbedQueryBinary embeds a single query string and returns the float32 vector.
// Thread-safe via mutex; only one request in flight at a time.
func (e *Embedder) EmbedQueryBinary(query string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	line, err := e.roundTrip(serverRequest{Query: query})
	if err != nil {
		return nil, err
	}

	var resp serveResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("parse embed response: %w\nraw: %s", err, line)
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

// Close sends a clean shutdown to the subprocess and waits for it to exit,
// forcibly killing it if it doesn't within the grace period.
func (e *Embedder) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	_ = e.enc.Encode(map[string]bool{"shutdown": true})
	_ = e.stdin.Close()
	e.mu.Unlock()

	waitDone := make(chan error, 1)
	go func() { waitDone <- e.cmd.Wait() }()

	select {
	case err := <-waitDone:
		return err
	case <-time.After(5 * time.Second):
		_ = e.cmd.Process.Kill()
		return <-waitDone
	}
}

// Embed generates an embedding vector for the given text.
func (e *Embedder) Embed(text string) ([]float32, error) {
	return e.EmbedQueryBinary(text)
}

// GetDimension returns the embedding dimension (0 until ready signal received).
func (e *Embedder) GetDimension() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dim
}

// GetModel returns the model name.
func (e *Embedder) GetModel() string { return e.model }

// IsAlive reports whether the subprocess is still running. Use this after
// a timeout/error to decide whether to recreate the Embedder.
func (e *Embedder) IsAlive() bool {
	if e.cmd.ProcessState != nil {
		return false // already reaped, process exited
	}
	// Signal(0) checks liveness without actually sending a signal.
	return e.cmd.Process.Signal(syscall.Signal(0)) == nil
}

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

	line, err := e.roundTrip(batchRequest{Queries: texts})
	if err != nil {
		return nil, err
	}

	var resp batchResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("parse batch response: %w\nraw: %s", err, line)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("batch embed error: %s", resp.Error)
	}
	if len(resp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("batch response count mismatch: sent %d, got %d", len(texts), len(resp.Embeddings))
	}

	e.dim = resp.Dimension
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
		"dimension": e.GetDimension(),
	}
}
