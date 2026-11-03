// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"eulix/internal/config"
	"eulix/internal/utils"
)

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFindProjectRoot(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	mkdirs(t, filepath.Join(root, utils.EulixDir), deep)

	if got, err := findProjectRoot(deep); err != nil || got != root {
		t.Fatalf("from a subdirectory: got (%q, %v), want %q", got, err, root)
	}

	// .euignore alone also marks a project, and the nearest marker wins.
	touch(t, filepath.Join(root, "a", utils.EuignorePath))
	if got, _ := findProjectRoot(deep); got != filepath.Join(root, "a") {
		t.Fatalf("nearest marker should win, got %q", got)
	}

	if _, err := findProjectRoot(t.TempDir()); err == nil || !strings.Contains(err.Error(), "eulix init") {
		t.Fatalf("outside a project: want an error pointing at 'eulix init', got %v", err)
	}
}

func TestIsInitializedNeedsBothMarkers(t *testing.T) {
	root := t.TempDir()
	if isInitialized("") || isInitialized(root) {
		t.Fatal("empty root or bare directory reported as initialized")
	}
	mkdirs(t, filepath.Join(root, utils.EulixDir))
	if isInitialized(root) {
		t.Fatal(".eulix without .euignore reported as initialized")
	}
	touch(t, filepath.Join(root, utils.EuignorePath))
	if !isInitialized(root) || hasKnowledgeBase(root) {
		t.Fatal("want initialized with no knowledge base yet")
	}
	touch(t, getKnowledgeBasePath(root))
	if !hasKnowledgeBase(root) {
		t.Fatal("kb.json not detected")
	}
}

func TestInitStateMissing(t *testing.T) {
	t.Chdir(t.TempDir())
	if s := checkInitState(); s.fullyInitialized() ||
		// these keys are the ones initializeProject's shouldWrite understands
		!slices.Equal(s.missingTargets(), []string{"config", "dir", "euignore"}) ||
		len(s.missingDescriptions()) != 3 {
		t.Fatalf("empty dir: %+v targets=%v", s, s.missingTargets())
	}

	touch(t, utils.ConfigPath)
	mkdirs(t, utils.EulixDir)
	if s := checkInitState(); !slices.Equal(s.missingTargets(), []string{"euignore"}) ||
		!slices.Equal(s.missingDescriptions(), []string{utils.EuignorePath + " (ignore patterns)"}) {
		t.Fatalf("only .euignore missing: targets=%v descriptions=%v", s.missingTargets(), s.missingDescriptions())
	}

	touch(t, utils.EuignorePath)
	if s := checkInitState(); !s.fullyInitialized() || s.missingTargets() != nil {
		t.Fatalf("all present: %+v", s)
	}
}

func TestCheckEmbeddingsFiles(t *testing.T) {
	dir := t.TempDir()
	if got := checkEmbeddingsFiles(dir); len(got) != 5 {
		t.Fatalf("empty dir: want 5 missing, got %q", got)
	}
	for _, f := range []string{"kb.json", "kb_index.json", "kb_call_graph.json", "embeddings.bin"} {
		touch(t, filepath.Join(dir, f))
	}
	if got := checkEmbeddingsFiles(dir); len(got) != 1 || !strings.Contains(got[0], "vectors.bin") {
		t.Fatalf("want only vectors.bin missing, got %q", got)
	}
}

func TestParseID(t *testing.T) {
	if id, err := parseID(" 42\n"); err != nil || id != 42 {
		t.Fatalf("parseID(\" 42\\n\") = (%d, %v), want 42", id, err)
	}
	for _, bad := range []string{"", "-1", "abc", "1.5"} {
		if _, err := parseID(bad); err == nil {
			t.Errorf("parseID(%q) should fail", bad)
		}
	}
}

func TestWriteConfigRoundTrips(t *testing.T) {
	dst := filepath.Join(t.TempDir(), utils.ConfigPath)
	want := config.DefaultConfig()
	want.LLM.Provider, want.LLM.Model, want.Parser.Threads = "openrouter", "some/model", 3
	if err := writeConfig(want, dst); err != nil {
		t.Fatal(err)
	}

	var got config.Config
	if _, err := toml.DecodeFile(dst, &got); err != nil {
		t.Fatalf("written eulix.toml does not parse: %v", err)
	}
	if !reflect.DeepEqual(&got, want) {
		t.Fatalf("config changed after write+read:\ngot  %+v\nwant %+v", got, *want)
	}
}
