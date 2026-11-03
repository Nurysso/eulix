// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package embeddings

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fakeEnv = "EULIX_FAKE_EMBED"

func TestMain(m *testing.M) {
	if os.Getenv(fakeEnv) == "1" {
		os.Exit(fakePython(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakePython stands in for the venv interpreter running eulix_embed's
// server command (eulix-embed/server/server.py), speaking the same protocol.
// The -m model argument picks the behaviour.
func fakePython(args []string) int {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("Python 3.11.9")
		return 0
	}
	mode := args[3] // main.py server -m <mode> -e <engine>
	out := json.NewEncoder(os.Stdout)
	fmt.Fprintln(os.Stderr, "starting server mode | model="+mode)
	switch mode {
	case "fail":
		_ = out.Encode(map[string]any{"ready": false, "error": "model load failed: boom"})
		return 1
	case "crash":
		fmt.Fprintln(os.Stderr, "ImportError: No module named 'torch'")
		return 1
	}
	fmt.Println("human-readable banner") // must be skipped while waiting for ready
	_ = out.Encode(map[string]any{"ready": true, "model": mode, "dim": 3})

	served := 0
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		var req struct {
			Query    *string `json:"query"`
			Shutdown bool    `json:"shutdown"`
		}
		_ = json.Unmarshal(in.Bytes(), &req)
		switch {
		case req.Shutdown:
			_ = out.Encode(map[string]string{"shutdown": "ok"})
			fmt.Fprintf(os.Stderr, "exiting | requests_served=%d\n", served)
			return 0
		case mode == "hang":
			time.Sleep(time.Hour)
		case req.Query == nil || *req.Query == "":
			_ = out.Encode(map[string]string{"error": `"query" is empty`})
		default:
			served++
			_ = out.Encode(map[string]any{"embedding": []float32{float32(len(*req.Query)), 0, 0}, "dimension": 3, "model": mode})
		}
	}
	return 0
}

func writeExec(t *testing.T, path, script string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeInstall makes ~/.Eulix look installed, with a venv python3 that
// re-runs this test binary as fakePython.
func fakeInstall(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake venv interpreter is a shell script")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(fakeEnv, "1")
	writeExec(t, filepath.Join(home, ".Eulix", "eulix_embed", "main.py"), "")
	writeExec(t, filepath.Join(home, ".Eulix", ".venv", "bin", "python3"), fmt.Sprintf("exec '%s' \"$@\"", exe))
}

func TestEmbedderRoundTrip(t *testing.T) {
	fakeInstall(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	e, err := VectorWeaver("ok", "onnx", false)
	if err != nil {
		t.Fatal(err)
	}
	if e.GetDimension() != 3 {
		t.Fatalf("dimension from the ready signal = %d, want 3", e.GetDimension())
	}
	for _, q := range []string{"abcd", "ab"} {
		emb, err := e.EmbedQueryBinary(q)
		if err != nil || len(emb) != 3 || emb[0] != float32(len(q)) {
			t.Fatalf("EmbedQueryBinary(%q) = (%v, %v), want this query's own vector", q, emb, err)
		}
	}
	if _, err := e.EmbedQueryBinary(""); err == nil || !strings.Contains(err.Error(), "query") {
		t.Fatalf("server error should surface, got %v", err)
	}
	if emb, err := e.EmbedQueryBinary("abc"); err != nil || emb[0] != 3 {
		t.Fatalf("a request after an error answer is out of step: (%v, %v)", emb, err)
	}

	if err := e.Close(); err != nil {
		t.Fatalf("clean shutdown: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := e.EmbedQueryBinary("x"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("query after Close: %v", err)
	}
	// The server's last stderr line is written as it exits; Close must not cut it off.
	if tail := e.stderrTail.String(); !strings.Contains(tail, "requests_served=3") {
		t.Fatalf("stderr tail lost the server's exit line:\n%s", tail)
	}
	if strings.Contains(logs.String(), "scan error") {
		t.Fatalf("Close logged a spurious stderr error: %s", logs.String())
	}
}

func TestEmbedderStartupFailures(t *testing.T) {
	fakeInstall(t)
	for mode, want := range map[string]string{
		"fail":  "model load failed: boom",              // structured reason on stdout
		"crash": "ImportError: No module named 'torch'", // only stderr to go on
	} {
		if _, err := VectorWeaver(mode, "onnx", false); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want error containing %q, got %v", mode, want, err)
		}
	}
}

func TestEmbedderTimeoutKillsServer(t *testing.T) {
	fakeInstall(t)
	e, err := VectorWeaver("hang", "onnx", false)
	if err != nil {
		t.Fatal(err)
	}
	e.requestTimeout = 200 * time.Millisecond

	start := time.Now()
	if _, err := e.EmbedQueryBinary("q"); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want a timeout error, got %v", err)
	}
	if _, err := e.EmbedQueryBinary("q"); err == nil {
		t.Fatal("the killed server must not answer later queries")
	}
	_ = e.Close() // reports the kill; it must just not hang
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout path took %s", time.Since(start))
	}
}

func TestStderrTailKeepsLastLines(t *testing.T) {
	var tail stderrTail
	for i := range stderrTailSize + 10 {
		tail.add(fmt.Sprint(i))
	}
	lines := strings.Split(tail.String(), "\n")
	if len(lines) != stderrTailSize || lines[0] != "10" || lines[len(lines)-1] != fmt.Sprint(stderrTailSize+9) {
		t.Fatalf("tail kept %d lines, %s..%s", len(lines), lines[0], lines[len(lines)-1])
	}
}
