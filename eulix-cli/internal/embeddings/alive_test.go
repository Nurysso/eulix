// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package embeddings

import (
	"io"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestIsAliveTracksTheProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The fake server blocks on stdin, so it runs until the test ends it.
	cmd := exec.Command(exe, "main.py", "server", "-m", "ok", "-e", "onnx")
	cmd.Env = append(os.Environ(), fakeEnv+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	go func() { _, _ = io.Copy(io.Discard, stdout) }()

	e := &Embedder{cmd: cmd}
	if !e.IsAlive() {
		t.Fatal("IsAlive = false for a running process")
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// Dead but not reaped. Unix keeps a zombie here, which still takes signals.
		deadline := time.Now().Add(5 * time.Second)
		for e.IsAlive() {
			if time.Now().After(deadline) {
				t.Fatal("IsAlive = true for a killed process that is not reaped")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	_ = cmd.Wait()
	if e.IsAlive() {
		t.Fatal("IsAlive = true after the process exited")
	}
}
