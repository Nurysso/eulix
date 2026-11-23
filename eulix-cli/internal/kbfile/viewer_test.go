// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package kbfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"eulix/internal/fb"
)

func TestViewerCallGraphKeepsFileWhenNothingIsHidden(t *testing.T) {
	path := filepath.Join("testdata", CallGraphFile)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, keep := range map[string]func(string) bool{
		"no filter": nil,
		"all kept":  func(string) bool { return true },
	} {
		got, err := ViewerCallGraph(path, keep)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: the file was rewritten although nothing was hidden", name)
		}
	}
}

func TestViewerCallGraphDropsHiddenFiles(t *testing.T) {
	got, err := ViewerCallGraph(filepath.Join("testdata", CallGraphFile), func(file string) bool { return file != "app/server.py" })
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("server.py")) {
		t.Fatal("the trimmed graph still names the hidden file")
	}
	if !fb.CallGraphBufferHasIdentifier(got) {
		t.Fatal("the trimmed graph lost its file identifier")
	}
	g := fb.GetRootAsCallGraph(got, 0)
	if v := g.FormatVersion(); v != 1 {
		t.Errorf("format version = %d", v)
	}

	var n fb.CallGraphNode
	var ids []string
	for i := range g.NodesLength() {
		g.Nodes(&n, i)
		ids = append(ids, string(n.Id()))
		if string(n.Id()) == "func_helper::app/tool.go" && (n.CallCountEstimate() != 1 || string(n.NodeType()) != "function") {
			t.Errorf("node fields changed: count %d, type %q", n.CallCountEstimate(), n.NodeType())
		}
	}
	if len(ids) != 2 || ids[0] != "func_helper::app/tool.go" || ids[1] != "func_main::app/tool.go" {
		t.Errorf("nodes = %v", ids)
	}

	var e fb.CallGraphEdge
	if g.EdgesLength() != 1 || !g.Edges(&e, 0) || string(e.From()) != "func_main::app/tool.go" || e.CallSiteLine() != 15 {
		t.Errorf("edge between two kept nodes was not kept as written")
	}

	var s fb.StringList
	var tagged []string
	for i := range g.TagsLength() {
		g.Tags(&s, i)
		tagged = append(tagged, string(s.Key())+"="+string(s.Values(0)))
	}
	if len(tagged) != 2 || tagged[0] != "func_helper::app/tool.go=utility" || tagged[1] != "func_main::app/tool.go=entry-point" {
		t.Errorf("tags = %v", tagged)
	}

	var c fb.FileCategory
	if g.FileCategoriesLength() != 1 || !g.FileCategories(&c, 0) || string(c.Key()) != "app/tool.go" || string(c.Category()) != "Other" {
		t.Errorf("file categories were not trimmed to the kept file")
	}
}

func TestViewerCallGraphRejectsOtherFiles(t *testing.T) {
	if _, err := ViewerCallGraph(filepath.Join("testdata", IndexFile), nil); err == nil {
		t.Error("an index file was accepted as a call graph")
	}
	if _, err := ViewerCallGraph(filepath.Join(t.TempDir(), CallGraphFile), nil); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file error = %v, want one that reports the file is missing", err)
	}
}
