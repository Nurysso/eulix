// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package kbfile

import (
	"fmt"
	"os"
	"path/filepath"

	flatbuffers "github.com/google/flatbuffers/go"

	"eulix/internal/fb"
	"eulix/internal/utils"
)

// ViewerCallGraph returns kb_call_graph.fb for vizEulize, which decodes it in
// the browser. keep reports whether a repo-relative file may be shown; nil
// keeps everything. When every node's file and every categorized file is
// kept, the file's bytes are returned unchanged. Otherwise the graph is
// rewritten without those nodes, the edges that touch them, their tags and
// the dropped files' categories, so the browser never receives their names.
func ViewerCallGraph(path string, keep func(file string) bool) ([]byte, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	out := buf
	err = checked(buf, path, "call graph", fb.CallGraphBufferHasIdentifier,
		func(b []byte) uint32 { return fb.GetRootAsCallGraph(b, 0).FormatVersion() },
		func(b []byte) {
			if keep != nil {
				out = trimCallGraph(fb.GetRootAsCallGraph(b, 0), keep)
			}
		})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// trimCallGraph returns g's own bytes when keep hides nothing, and a rewritten
// graph otherwise. Tags and categories stay sorted, since the order is kept.
func trimCallGraph(g *fb.CallGraph, keep func(string) bool) []byte {
	var n fb.CallGraphNode
	hidden := make(map[string]bool)
	for i := range g.NodesLength() {
		g.Nodes(&n, i)
		if !keep(string(n.File())) {
			hidden[string(n.Id())] = true
		}
	}
	var c fb.FileCategory
	dropCategory := false
	for i := range g.FileCategoriesLength() {
		g.FileCategories(&c, i)
		if !keep(string(c.Key())) {
			dropCategory = true
			break
		}
	}
	if len(hidden) == 0 && !dropCategory {
		return g.Table().Bytes
	}

	// The parser stores each distinct string once; keep it that way.
	b := flatbuffers.NewBuilder(len(g.Table().Bytes) / 2)
	str := func(s []byte) flatbuffers.UOffsetT { return b.CreateSharedString(string(s)) }
	vec := func(offs []flatbuffers.UOffsetT, start func(*flatbuffers.Builder, int) flatbuffers.UOffsetT) flatbuffers.UOffsetT {
		start(b, len(offs))
		for i := len(offs) - 1; i >= 0; i-- {
			b.PrependUOffsetT(offs[i])
		}
		return b.EndVector(len(offs))
	}

	var nodes []flatbuffers.UOffsetT
	for i := range g.NodesLength() {
		g.Nodes(&n, i)
		if hidden[string(n.Id())] {
			continue
		}
		id, typ, file := str(n.Id()), str(n.NodeType()), str(n.File())
		fb.CallGraphNodeStart(b)
		fb.CallGraphNodeAddId(b, id)
		fb.CallGraphNodeAddNodeType(b, typ)
		fb.CallGraphNodeAddFile(b, file)
		fb.CallGraphNodeAddIsEntryPoint(b, n.IsEntryPoint())
		fb.CallGraphNodeAddCallCountEstimate(b, n.CallCountEstimate())
		nodes = append(nodes, fb.CallGraphNodeEnd(b))
	}
	nodeVec := vec(nodes, fb.CallGraphStartNodesVector)

	var e fb.CallGraphEdge
	var edges []flatbuffers.UOffsetT
	for i := range g.EdgesLength() {
		g.Edges(&e, i)
		if hidden[string(e.From())] || hidden[string(e.To())] {
			continue
		}
		from, to, typ := str(e.From()), str(e.To()), str(e.EdgeType())
		fb.CallGraphEdgeStart(b)
		fb.CallGraphEdgeAddFrom(b, from)
		fb.CallGraphEdgeAddTo(b, to)
		fb.CallGraphEdgeAddEdgeType(b, typ)
		fb.CallGraphEdgeAddConditional(b, e.Conditional())
		fb.CallGraphEdgeAddCallSiteLine(b, e.CallSiteLine())
		edges = append(edges, fb.CallGraphEdgeEnd(b))
	}
	edgeVec := vec(edges, fb.CallGraphStartEdgesVector)

	var s fb.StringList
	var tags []flatbuffers.UOffsetT
	for i := range g.TagsLength() {
		g.Tags(&s, i)
		if hidden[string(s.Key())] {
			continue
		}
		vals := make([]flatbuffers.UOffsetT, s.ValuesLength())
		for j := range vals {
			vals[j] = str(s.Values(j))
		}
		valVec := vec(vals, fb.StringListStartValuesVector)
		key := str(s.Key())
		fb.StringListStart(b)
		fb.StringListAddKey(b, key)
		fb.StringListAddValues(b, valVec)
		tags = append(tags, fb.StringListEnd(b))
	}
	tagVec := vec(tags, fb.CallGraphStartTagsVector)

	var cats []flatbuffers.UOffsetT
	for i := range g.FileCategoriesLength() {
		g.FileCategories(&c, i)
		if !keep(string(c.Key())) {
			continue
		}
		key, cat := str(c.Key()), str(c.Category())
		fb.FileCategoryStart(b)
		fb.FileCategoryAddKey(b, key)
		fb.FileCategoryAddCategory(b, cat)
		cats = append(cats, fb.FileCategoryEnd(b))
	}
	catVec := vec(cats, fb.CallGraphStartFileCategoriesVector)

	fb.CallGraphStart(b)
	fb.CallGraphAddFormatVersion(b, utils.KBFormatVersion)
	fb.CallGraphAddNodes(b, nodeVec)
	fb.CallGraphAddEdges(b, edgeVec)
	fb.CallGraphAddTags(b, tagVec)
	fb.CallGraphAddFileCategories(b, catVec)
	fb.FinishCallGraphBuffer(b, fb.CallGraphEnd(b))
	return b.FinishedBytes()
}
