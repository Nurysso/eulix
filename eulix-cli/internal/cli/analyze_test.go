// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	"eulix/internal/checksum"
	"eulix/internal/config"
	"eulix/internal/utils"
)

// Model the parser's real ordering: artifacts and checksum are written before
// embedding can fail. No installed parser, Python environment or model is used.
type analysisFixture struct {
	t     *testing.T
	root  string
	calls int
	fail  bool
}

func newAnalysisFixture(t *testing.T) *analysisFixture {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	f := &analysisFixture{t: t, root: root}
	f.write("app.go", "package main\nfunc main() {}\n")
	f.configure("model-a", "onnx", 2)
	return f
}

func (f *analysisFixture) write(name, value string) {
	f.t.Helper()
	path := filepath.Join(f.root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *analysisFixture) configure(model, engine string, prism int) {
	f.t.Helper()
	f.write("eulix.toml", fmt.Sprintf("[embeddings]\nmodel = %q\nengine = %q\n[parser]\nprismVersion = %d\n", model, engine, prism))
}

func (f *analysisFixture) pipeline(_ string, _ *config.Config) error {
	f.t.Helper()
	f.calls++
	for _, name := range []string{"kb.json", "kb_index.json", "kb_call_graph.json"} {
		f.write(filepath.Join(utils.EulixDir, name), "{\"complete\":true}\n")
	}
	info, err := os.Stat(filepath.Join(f.root, "app.go"))
	if err != nil {
		f.t.Fatal(err)
	}
	baseline := checksum.Checksum{Files: map[string]checksum.FileEntry{
		"app.go": {Hash: checksum.MetaHash(info.Size(), info.ModTime().UnixNano()), Size: info.Size(), ModTime: info.ModTime().UnixNano()},
	}}
	data, err := json.Marshal(baseline)
	if err != nil {
		f.t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		f.t.Fatal(err)
	}
	f.write(filepath.Join(utils.EulixDir, utils.ChecksumFileName), string(encoder.EncodeAll(data, nil)))
	if err := encoder.Close(); err != nil {
		f.t.Fatal(err)
	}
	if f.fail {
		return errors.New("embedding generation failed")
	}
	for _, name := range []string{"embeddings.bin", "vectors.bin"} {
		f.write(filepath.Join(utils.EulixDir, name), "complete embedding output")
	}
	return nil
}

func (f *analysisFixture) run() error {
	return analyzeWithPipeline(f.root, f.pipeline)
}

func TestAnalyzeRetriesAfterEmbeddingFailure(t *testing.T) {
	f := newAnalysisFixture(t)
	f.fail = true
	if err := f.run(); err == nil {
		t.Fatal("expected embedding failure")
	}
	f.fail = false
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	if f.calls != 2 {
		t.Fatalf("pipeline calls = %d, want retry after failure", f.calls)
	}
	if _, err := os.Stat(filepath.Join(f.root, utils.EulixDir, "embeddings.bin")); err != nil {
		t.Fatal(err)
	}
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	if f.calls != 2 {
		t.Fatal("completed unchanged analysis should skip")
	}
}

func TestAnalyzeSkipsAfterSuccessfulPipeline(t *testing.T) {
	f := newAnalysisFixture(t)
	for range 2 {
		if err := f.run(); err != nil {
			t.Fatal(err)
		}
	}
	if f.calls != 1 {
		t.Fatalf("pipeline calls = %d, want 1", f.calls)
	}
}

func TestAnalyzeFailureInvalidatesPreviousSuccess(t *testing.T) {
	f := newAnalysisFixture(t)
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	f.write("app.go", "package main\nfunc main() { println(42) }\n")
	f.fail = true
	if err := f.run(); err == nil {
		t.Fatal("expected embedding failure")
	}
	f.fail = false
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	if f.calls != 3 {
		t.Fatalf("pipeline calls = %d, want retry despite old artifacts", f.calls)
	}
}

func TestAnalyzeRerunsWhenPipelineSettingsChange(t *testing.T) {
	f := newAnalysisFixture(t)
	for i, cfg := range []struct {
		model, engine string
		prism         int
	}{
		{"model-a", "onnx", 2}, {"model-b", "onnx", 2}, {"model-b", "torch", 2}, {"model-b", "torch", 1},
	} {
		f.configure(cfg.model, cfg.engine, cfg.prism)
		if err := f.run(); err != nil {
			t.Fatal(err)
		}
		if f.calls != i+1 {
			t.Fatalf("pipeline calls = %d after settings change %d", f.calls, i)
		}
	}
}

func TestAnalyzeRerunsWithMissingOrTruncatedArtifacts(t *testing.T) {
	for _, name := range []string{"kb.json", "kb_index.json", "kb_call_graph.json", "embeddings.bin", "vectors.bin"} {
		t.Run(name, func(t *testing.T) {
			f := newAnalysisFixture(t)
			if err := f.run(); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(f.root, utils.EulixDir, name)); err != nil {
				t.Fatal(err)
			}
			if err := f.run(); err != nil {
				t.Fatal(err)
			}
			if f.calls != 2 {
				t.Fatal("missing artifact should force analysis")
			}
			f.write(filepath.Join(utils.EulixDir, name), "x")
			if err := f.run(); err != nil {
				t.Fatal(err)
			}
			if f.calls != 3 {
				t.Fatal("truncated nonempty artifact should force analysis")
			}
		})
	}
}

func TestAnalyzeDoesNotRecordIncompletePipeline(t *testing.T) {
	f := newAnalysisFixture(t)
	incomplete := func(path string, cfg *config.Config) error {
		if err := f.pipeline(path, cfg); err != nil {
			return err
		}
		return os.Remove(filepath.Join(f.root, utils.EulixDir, "vectors.bin"))
	}
	if err := analyzeWithPipeline(f.root, incomplete); err == nil {
		t.Fatal("successful subprocesses without required artifacts must not complete analysis")
	}
	if _, err := os.Stat(filepath.Join(f.root, utils.EulixDir, analysisCompleteFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected completion marker after missing output: %v", err)
	}
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	if f.calls != 2 {
		t.Fatal("missing output must cause retry")
	}
}

func TestAnalyzeRerunsWithoutValidCompletionMarker(t *testing.T) {
	for _, state := range []string{"legacy", "malformed"} {
		t.Run(state, func(t *testing.T) {
			f := newAnalysisFixture(t)
			if err := f.run(); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(utils.EulixDir, analysisCompleteFile)
			if state == "legacy" {
				if err := os.Remove(filepath.Join(f.root, marker)); err != nil {
					t.Fatal(err)
				}
			} else {
				f.write(marker, "{partial")
			}
			if err := f.run(); err != nil {
				t.Fatal(err)
			}
			if f.calls != 2 {
				t.Fatal("untrusted completion state must cause analysis")
			}
		})
	}
}
