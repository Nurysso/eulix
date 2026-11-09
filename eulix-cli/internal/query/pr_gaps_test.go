// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package query

// Regression tests for the gaps found while reviewing the symlink and stale
// index fixes. Numbers match the review. Run them with: go test -run TestPRGap

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"testing"

	"eulix/internal/utils"
)

// #3: C++ names contain "::" themselves (the parser writes
// func_Foo::bar::src/a.cpp), so the file is what follows the last separator.
func TestPRGap3FilterHandlesQualifiedCallerNames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".euignore"), "secret/\n")
	index := &utils.Indices{FunctionsCalling: map[string][]string{"baz": {
		"func_plain::secret/a.cpp",
		"func_Vault::unlock::secret/a.cpp",
		"func_Vault::open::.hidden/b.cpp",
		"func_Foo::bar::src/a.cpp",
		"func_ok::src/a.cpp",
	}}}

	filterServableIndex(index, dir)

	want := []string{"func_Foo::bar::src/a.cpp", "func_ok::src/a.cpp"}
	if got := index.FunctionsCalling["baz"]; !slices.Equal(got, want) {
		t.Fatalf("callers kept = %q, want %q", got, want)
	}
}

// #6: the filter runs on every router build and most callers share a file, so
// each file should be checked once. Allocations stand in for time here: they
// do not depend on how fast the machine is.
func TestPRGap6FilterChecksEachFileOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".euignore"), "secret/\n*.gen.go\nvendor/**\n")
	const callers, files = 200_000, 1_000
	index := &utils.Indices{FunctionsCalling: make(map[string][]string)}
	for i := range callers {
		callee := fmt.Sprintf("callee%d", i%50)
		caller := fmt.Sprintf("func_c%d::src/pkg%d/file%d.go", i, i%files, i%files)
		index.FunctionsCalling[callee] = append(index.FunctionsCalling[callee], caller)
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	filterServableIndex(index, dir)
	runtime.ReadMemStats(&after)

	if allocs := after.Mallocs - before.Mallocs; allocs > callers {
		t.Fatalf("filtering %d callers from %d files made %d allocations; check each file once",
			callers, files, allocs)
	}
}

// #7: a path that fails partway must not leave the opened folder behind.
func TestPRGap7FailedReadLeavesNoOpenHandles(t *testing.T) {
	const fdDir = "/proc/self/fd"
	if _, err := os.ReadDir(fdDir); err != nil {
		t.Skipf("cannot count open files here: %v", err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	// A garbage collection could close the leaked handles and hide the leak.
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	countOpen := func() int {
		entries, err := os.ReadDir(fdDir)
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}

	const reads = 50
	before := countOpen()
	for range reads {
		if _, err := extractSignature(root, "a/missing/x.go", 1); err == nil {
			t.Fatal("reading a missing file should fail")
		}
	}
	if leaked := countOpen() - before; leaked > 0 {
		t.Fatalf("%d handles left open after %d failed reads", leaked, reads)
	}
}
