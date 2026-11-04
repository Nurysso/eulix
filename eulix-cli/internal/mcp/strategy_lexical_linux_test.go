// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLexicalSearchUsesBytesReadDuringWalk(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a_target.go")
	barrier := filepath.Join(root, "b_barrier.go")
	outside := filepath.Join(t.TempDir(), "secret.go")
	if err := os.WriteFile(target, []byte("// marker SAFE_SNAPSHOT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(barrier, []byte(strings.Repeat("x", maxLexFileBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("// marker RACE_SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		name := filepath.Join(root, fmt.Sprintf("c_%03d.go", i))
		if err := os.WriteFile(name, []byte("package filler\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		t.Skipf("inotify unavailable: %v", err)
	}
	defer func() { _ = unix.Close(fd) }()
	if _, err := unix.InotifyAddWatch(fd, barrier, unix.IN_OPEN); err != nil {
		t.Skipf("inotify watch unavailable: %v", err)
	}

	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, err := lexicalSearch(context.Background(), root, "marker", nil, DefaultStrategyConfig())
		done <- result{text: text, err: err}
	}()

	opened := make(chan error, 1)
	go func() {
		buf := make([]byte, unix.SizeofInotifyEvent+unix.PathMax)
		_, err := unix.Read(fd, buf)
		opened <- err
	}()
	select {
	case err := <-opened:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lexical walk did not reach the barrier file")
	}

	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	res := <-done
	if res.err != nil {
		t.Fatal(res.err)
	}
	if strings.Contains(res.text, "RACE_SECRET") || !strings.Contains(res.text, "SAFE_SNAPSHOT") {
		t.Fatalf("lexical result did not preserve the bytes read during the walk:\n%s", res.text)
	}
}
