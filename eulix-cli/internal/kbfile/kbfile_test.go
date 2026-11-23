// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package kbfile

// The files in testdata/ were written by eulix_parser --emit-json over a
// two-file fixture (a Flask route plus a Go main). Each test decodes the .fb
// with the Go reader and checks it against the JSON copy the Rust side wrote,
// so it fails if the two languages disagree about the format. To regenerate,
// run eulix_parser -r <fixture> -o <dir>/kb.json --emit-json and copy the
// kb_<name>.fb/.json pairs here.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"eulix/internal/kbfile/kbfiletest"
	"eulix/internal/utils"
)

func readCopy(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}

func TestReadEntryPointsMatchesJSONCopy(t *testing.T) {
	got, err := ReadEntryPoints(filepath.Join("testdata", EntryPointsFile))
	if err != nil {
		t.Fatal(err)
	}
	var want utils.EntryPointsRef
	readCopy(t, "kb_entry_points.json", &want)
	if len(got) != len(want.EntryPoints) || len(got) == 0 {
		t.Fatalf("got %d entry points, want %d", len(got), len(want.EntryPoints))
	}
	for i := range got {
		g, w := got[i], want.EntryPoints[i]
		if g.EntryType != w.EntryType || g.Function != w.Function || g.File != w.File || g.Line != w.Line ||
			(g.Path == nil) != (w.Path == nil) || (g.Path != nil && *g.Path != *w.Path) ||
			len(g.Methods) != len(w.Methods) {
			t.Fatalf("entry point %d: got %+v, want %+v", i, g, w)
		}
	}
	if got[0].Path == nil || *got[0].Path != "/users" || len(got[0].Methods) != 2 {
		t.Fatalf("route entry point lost its path or methods: %+v", got[0])
	}
}

func TestReadExternalDepsMatchesJSONCopy(t *testing.T) {
	got, err := ReadExternalDeps(filepath.Join("testdata", ExternalDepsFile))
	if err != nil {
		t.Fatal(err)
	}
	var want utils.ExternalDependencyRef
	readCopy(t, "kb_external_deps.json", &want)
	if len(got) != len(want.ExternalDependencies) || len(got) == 0 {
		t.Fatalf("got %d deps, want %d", len(got), len(want.ExternalDependencies))
	}
	for i := range got {
		g, w := got[i], want.ExternalDependencies[i]
		if g.Name != w.Name || g.Source != w.Source || g.ImportCount != w.ImportCount || len(g.UsedBy) != len(w.UsedBy) {
			t.Fatalf("dep %d: got %+v, want %+v", i, g, w)
		}
	}
}

func TestReadMetricsMatchesJSONCopy(t *testing.T) {
	got, err := ReadMetrics(filepath.Join("testdata", MetricsFile))
	if err != nil {
		t.Fatal(err)
	}
	var want utils.MetricsReport
	readCopy(t, "kb_metrics.json", &want)
	if got.Metadata.ProjectName == "" || got.Metadata.ProjectName != want.Metadata.ProjectName ||
		got.Metadata.TotalLoc != want.Metadata.TotalLoc || len(got.Metadata.Languages) != len(want.Metadata.Languages) {
		t.Fatalf("metadata: got %+v, want %+v", got.Metadata, want.Metadata)
	}
	if len(got.TopComplexFunctions) != len(want.TopComplexFunctions) {
		t.Fatalf("got %d functions, want %d", len(got.TopComplexFunctions), len(want.TopComplexFunctions))
	}
	for i, g := range got.TopComplexFunctions {
		w := want.TopComplexFunctions[i]
		if g.Name != w.Name || g.Complexity != w.Complexity || g.ImportanceScore != w.ImportanceScore || g.LineStart != w.LineStart {
			t.Fatalf("function %d: got %+v, want %+v", i, g, w)
		}
	}
}

func TestReadPatternsMatchesJSONCopy(t *testing.T) {
	got, err := ReadPatterns(filepath.Join("testdata", PatternsFile))
	if err != nil {
		t.Fatal(err)
	}
	var want utils.PatternsRef
	readCopy(t, "kb_patterns.json", &want)
	if got.NamingConvention != want.Patterns.NamingConvention || got.StructureType != want.Patterns.StructureType {
		t.Fatalf("got %+v, want %+v", got, want.Patterns)
	}
}

func TestWrongFileIsStale(t *testing.T) {
	_, err := ReadEntryPoints(filepath.Join("testdata", PatternsFile))
	if !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	old := filepath.Join(t.TempDir(), EntryPointsFile)
	if err := os.WriteFile(old, []byte(`{"entry_points":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEntryPoints(old); !errors.Is(err, ErrStale) {
		t.Fatalf("old JSON contents: err = %v, want ErrStale", err)
	}
}

func TestCorruptFileIsAnError(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", ExternalDepsFile))
	if err != nil {
		t.Fatal(err)
	}
	cut := filepath.Join(t.TempDir(), ExternalDepsFile)
	if err := os.WriteFile(cut, data[:len(data)/3], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadExternalDeps(cut); err == nil {
		t.Fatal("truncated file decoded without error")
	}
}

func TestGoEncodersRoundTrip(t *testing.T) {
	dir := t.TempDir()
	v := "2.0"
	deps := []utils.ExternalDependency{{Name: "x", Version: &v, Source: "external", UsedBy: []string{"a.go"}, ImportCount: 1}, {Name: "os", Source: "stdlib", UsedBy: []string{}}}
	p := filepath.Join(dir, ExternalDepsFile)
	if err := kbfiletest.WriteFile(p, kbfiletest.EncodeExternalDeps(deps)); err != nil {
		t.Fatal(err)
	}
	got, err := ReadExternalDeps(p)
	if err != nil || len(got) != 2 || *got[0].Version != "2.0" || got[1].Version != nil || got[0].UsedBy[0] != "a.go" {
		t.Fatalf("got %+v, %v", got, err)
	}
	route := "/x"
	eps := []utils.EntryPoint{{EntryType: "api_endpoint", Path: &route, Function: "f", File: "a.py", Line: 3, Methods: []string{"GET"}}}
	p = filepath.Join(dir, EntryPointsFile)
	if err := kbfiletest.WriteFile(p, kbfiletest.EncodeEntryPoints(eps)); err != nil {
		t.Fatal(err)
	}
	gotEP, err := ReadEntryPoints(p)
	if err != nil || len(gotEP) != 1 || *gotEP[0].Path != "/x" || gotEP[0].Methods[0] != "GET" {
		t.Fatalf("got %+v, %v", gotEP, err)
	}
	rep := &utils.MetricsReport{Metadata: &utils.Metadata{ProjectName: "p", Languages: []string{"go"}}, TopComplexFunctions: []utils.FunctionMetric{{Name: "f", Complexity: 4, ImportanceScore: 0.5}}}
	p = filepath.Join(dir, MetricsFile)
	if err := kbfiletest.WriteFile(p, kbfiletest.EncodeMetrics(rep)); err != nil {
		t.Fatal(err)
	}
	gotM, err := ReadMetrics(p)
	if err != nil || gotM.Metadata.ProjectName != "p" || gotM.TopComplexFunctions[0].Complexity != 4 {
		t.Fatalf("got %+v, %v", gotM, err)
	}
}

func TestReadCallGraphMatchesJSONCopy(t *testing.T) {
	got, err := ReadCallGraph(filepath.Join("testdata", CallGraphFile))
	if err != nil {
		t.Fatal(err)
	}
	var want utils.CallGraphRef
	readCopy(t, "kb_call_graph.json", &want)
	if len(got.Nodes) == 0 || len(got.Edges) == 0 {
		t.Fatalf("fixture graph is empty: %d nodes, %d edges", len(got.Nodes), len(got.Edges))
	}
	if !reflect.DeepEqual(got.Nodes, want.Nodes) {
		t.Fatalf("nodes differ:\n got %+v\nwant %+v", got.Nodes, want.Nodes)
	}
	if !reflect.DeepEqual(got.Edges, want.Edges) {
		t.Fatalf("edges differ:\n got %+v\nwant %+v", got.Edges, want.Edges)
	}
}

func TestReadIndexMatchesJSONCopy(t *testing.T) {
	got, err := ReadIndex(filepath.Join("testdata", IndexFile))
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		Key    string   `json:"key"`
		Values []string `json:"values"`
	}
	var copyJSON map[string]json.RawMessage
	readCopy(t, "kb_index.json", &copyJSON)
	toMap := func(field string) map[string][]string {
		var es []entry
		if err := json.Unmarshal(copyJSON[field], &es); err != nil {
			t.Fatal(err)
		}
		m := make(map[string][]string, len(es))
		for _, e := range es {
			m[e.Key] = e.Values
		}
		return m
	}
	for field, gotMap := range map[string]map[string][]string{
		"functions_by_name": got.FunctionsByName,
		"functions_calling": got.FunctionsCalling,
		"functions_by_tag":  got.FunctionsByTag,
		"types_by_name":     got.TypesByName,
		"files_by_category": got.FilesByCategory,
	} {
		if want := toMap(field); !reflect.DeepEqual(gotMap, want) {
			t.Errorf("%s:\n got %v\nwant %v", field, gotMap, want)
		}
	}
	if len(got.FunctionsByName) == 0 {
		t.Fatal("fixture index has no functions")
	}
}

func TestEncodeIndexRoundTrip(t *testing.T) {
	in := &utils.Indices{
		FunctionsByName: map[string][]string{"b": {"x.go:2"}, "a": {"x.go:1", "y.go:3"}},
		FilesByCategory: map[string][]string{"core": {"x.go"}},
	}
	p := filepath.Join(t.TempDir(), IndexFile)
	if err := kbfiletest.WriteFile(p, kbfiletest.EncodeIndex(in)); err != nil {
		t.Fatal(err)
	}
	got, err := ReadIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.FunctionsByName, in.FunctionsByName) || !reflect.DeepEqual(got.FilesByCategory, in.FilesByCategory) ||
		len(got.TypesByName) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestWalkKBMatchesJSONCopy(t *testing.T) {
	var copyJSON struct {
		Metadata *utils.Metadata `json:"metadata"`
		Files    []struct {
			Path   string         `json:"path"`
			Record utils.FileData `json:"record"`
		} `json:"files"`
	}
	readCopy(t, "kb.json", &copyJSON)
	var meta *utils.Metadata
	var paths []string
	err := WalkKB(filepath.Join("testdata", KBFile), func(m *utils.Metadata) { meta = m }, func(p string, fd *utils.FileData) error {
		i := len(paths)
		paths = append(paths, p)
		want := copyJSON.Files[i]
		if p != want.Path || !reflect.DeepEqual(*fd, want.Record) {
			t.Errorf("file %d:\n got %s %+v\nwant %s %+v", i, p, *fd, want.Path, want.Record)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil || meta.ProjectName != copyJSON.Metadata.ProjectName || len(paths) != len(copyJSON.Files) || len(paths) == 0 {
		t.Fatalf("meta %+v, %d files, want %d", meta, len(paths), len(copyJSON.Files))
	}
}

func TestEncodeKBRoundTrip(t *testing.T) {
	def := "1"
	in := &utils.KnowledgeBaseSimplifiedRef{
		Metadata: &utils.Metadata{ProjectName: "p", Languages: []string{"python"}, TotalFiles: 2},
		Structure: map[string]utils.FileData{
			"b.py": {Language: "python", Loc: 3, Functions: []utils.KBFunction{{ID: "func_f::b.py", Name: "f", Params: []utils.Parameter{{Name: "x", DefaultValue: &def}}, LineStart: 1, LineEnd: 2}},
				Classes: []utils.KBClass{}, Todos: []utils.Todo{}},
			"a.py": {Language: "python", Loc: 1, Functions: []utils.KBFunction{},
				Classes: []utils.KBClass{{ID: "class_A::a.py", Name: "A", Bases: []string{}, Methods: []utils.KBFunction{}, Attributes: []utils.Attribute{{Name: "n"}}, Decorators: []string{}}},
				Todos:   []utils.Todo{{Line: 1, Text: "TODO", Priority: "low"}}},
		},
	}
	p := filepath.Join(t.TempDir(), KBFile)
	if err := kbfiletest.WriteFile(p, kbfiletest.EncodeKB(in)); err != nil {
		t.Fatal(err)
	}
	got, err := ReadKB(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.ProjectName != "p" || !reflect.DeepEqual(got.Structure, in.Structure) {
		t.Fatalf("got %+v", got.Structure)
	}
}

func TestWalkKBRejectsOldAndTruncatedFiles(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.fb")
	if err := os.WriteFile(old, []byte(`{"metadata":{},"structure":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	noop := func(string, *utils.FileData) error { return nil }
	if err := WalkKB(old, nil, noop); !errors.Is(err, ErrStale) {
		t.Fatalf("old JSON: err = %v, want ErrStale", err)
	}
	data, err := os.ReadFile(filepath.Join("testdata", KBFile))
	if err != nil {
		t.Fatal(err)
	}
	cut := filepath.Join(dir, "cut.fb")
	if err := os.WriteFile(cut, data[:len(data)-9], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WalkKB(cut, nil, noop); err == nil {
		t.Fatal("truncated kb.fb walked without error")
	}
}
