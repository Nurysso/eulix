// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

/*
Package kbfile reads the FlatBuffers files eulix-parser writes into .eulix/
and converts them into the structures in package utils, so the rest of
eulix-cli is independent of the on-disk format.

The Go FlatBuffers runtime has no verifier, so every reader checks the file
identifier and format version first and converts a panic from a truncated or
corrupt buffer into an error that tells the user to re-run `eulix analyze`.
See docs/Architecture/Binary-Knowledge-Base.md.
*/
package kbfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"eulix/internal/fb"
	"eulix/internal/query/retrieval/mmap"
	"eulix/internal/utils"
)

// File names of the parser outputs, relative to .eulix/.
const (
	EntryPointsFile  = "kb_entry_points.fb"
	ExternalDepsFile = "kb_external_deps.fb"
	MetricsFile      = "kb_metrics.fb"
	SummaryFile      = "kb_summary.fb"
	PatternsFile     = "kb_patterns.fb"
	CallGraphFile    = "kb_call_graph.fb"
	IndexFile        = "kb_index.fb"
)

// ErrStale reports a file written by another eulix version.
var ErrStale = errors.New("knowledge base was written by a different eulix version; run 'eulix analyze'")

// decode runs fn over the file at path after checking its identifier and
// format version. Large files are memory-mapped for the duration of fn, so fn
// must copy out everything it keeps. Any panic inside fn (a corrupt buffer)
// becomes an error.
func decode(path, kind string, hasID func([]byte) bool, version func([]byte) uint32, fn func([]byte)) error {
	err := mmap.WithFileBytes(path, func(buf []byte) error {
		return checked(buf, path, kind, hasID, version, fn)
	})
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return err
}

// checked runs fn over buf, the contents of path, after checking its
// identifier and format version, and turns a panic inside fn into an error.
func checked(buf []byte, path, kind string, hasID func([]byte) bool, version func([]byte) uint32, fn func([]byte)) (err error) {
	if len(buf) < 8 || !hasID(buf) {
		return fmt.Errorf("%s is not a eulix %s file: %w", filepath.Base(path), kind, ErrStale)
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s is corrupt (%v); run 'eulix analyze'", filepath.Base(path), r)
		}
	}()
	if v := version(buf); v != utils.KBFormatVersion {
		return fmt.Errorf("%s has format version %d, want %d: %w", filepath.Base(path), v, utils.KBFormatVersion, ErrStale)
	}
	fn(buf)
	return nil
}

func str(b []byte) string { return string(b) }

func optStr(b []byte) *string {
	if b == nil {
		return nil
	}
	s := string(b)
	return &s
}

// strings copies a FlatBuffers string vector out of the buffer.
func copyStrings(n int, at func(int) []byte) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = string(at(i))
	}
	return out
}

// ReadEntryPoints reads kb_entry_points.fb.
func ReadEntryPoints(path string) ([]utils.EntryPoint, error) {
	var out []utils.EntryPoint
	err := decode(path, "entry points", fb.EntryPointsBufferHasIdentifier,
		func(b []byte) uint32 { return fb.GetRootAsEntryPoints(b, 0).FormatVersion() },
		func(b []byte) {
			root := fb.GetRootAsEntryPoints(b, 0)
			out = make([]utils.EntryPoint, root.EntryPointsLength())
			var e fb.EntryPoint
			for i := range out {
				root.EntryPoints(&e, i)
				var methods []string // nil when absent, as JSON null decoded before
				if n := e.MethodsLength(); n > 0 {
					methods = copyStrings(n, e.Methods)
				}
				out[i] = utils.EntryPoint{
					EntryType: str(e.EntryType()),
					Path:      optStr(e.Path()),
					Function:  str(e.Function()),
					Handler:   str(e.Handler()),
					File:      str(e.File()),
					Line:      int(e.Line()),
					Methods:   methods,
				}
			}
		})
	return out, err
}

// ReadExternalDeps reads kb_external_deps.fb.
func ReadExternalDeps(path string) ([]utils.ExternalDependency, error) {
	var out []utils.ExternalDependency
	err := decode(path, "external deps", fb.ExternalDepsBufferHasIdentifier,
		func(b []byte) uint32 { return fb.GetRootAsExternalDeps(b, 0).FormatVersion() },
		func(b []byte) {
			root := fb.GetRootAsExternalDeps(b, 0)
			out = make([]utils.ExternalDependency, root.ExternalDependenciesLength())
			var d fb.ExternalDependency
			for i := range out {
				root.ExternalDependencies(&d, i)
				out[i] = utils.ExternalDependency{
					Name:        str(d.Name()),
					Version:     optStr(d.Version()),
					Source:      str(d.Source()),
					UsedBy:      copyStrings(d.UsedByLength(), d.UsedBy),
					ImportCount: int(d.ImportCount()),
				}
			}
		})
	return out, err
}

func metadata(m *fb.Metadata) *utils.Metadata {
	if m == nil {
		return &utils.Metadata{}
	}
	return &utils.Metadata{
		Version:        str(m.Version()),
		GitHash:        str(m.GitHash()),
		ProjectName:    str(m.ProjectName()),
		ProjectHash:    str(m.ProjectHash()),
		ParsedAt:       str(m.ParsedAt()),
		Languages:      copyStrings(m.LanguagesLength(), m.Languages),
		TotalFiles:     int(m.TotalFiles()),
		TotalLoc:       int(m.TotalLoc()),
		TotalFunctions: int(m.TotalFunctions()),
		TotalClasses:   int(m.TotalClasses()),
		TotalMethods:   int(m.TotalMethods()),
	}
}

// ReadMetrics reads kb_metrics.fb.
func ReadMetrics(path string) (*utils.MetricsReport, error) {
	var out *utils.MetricsReport
	err := decode(path, "metrics", fb.MetricsBufferHasIdentifier,
		func(b []byte) uint32 { return fb.GetRootAsMetrics(b, 0).FormatVersion() },
		func(b []byte) {
			root := fb.GetRootAsMetrics(b, 0)
			fns := make([]utils.FunctionMetric, root.TopComplexFunctionsLength())
			var f fb.FunctionMetric
			for i := range fns {
				root.TopComplexFunctions(&f, i)
				fns[i] = utils.FunctionMetric{
					Name:            str(f.Name()),
					File:            str(f.File()),
					Complexity:      int(f.Complexity()),
					LineStart:       int(f.LineStart()),
					LineEnd:         int(f.LineEnd()),
					ImportanceScore: f.ImportanceScore(),
				}
			}
			out = &utils.MetricsReport{Metadata: metadata(root.Metadata(nil)), TopComplexFunctions: fns}
		})
	return out, err
}

// ReadPatterns reads kb_patterns.fb.
func ReadPatterns(path string) (*utils.PatternInfo, error) {
	var out *utils.PatternInfo
	err := decode(path, "patterns", fb.PatternsBufferHasIdentifier,
		func(b []byte) uint32 { return fb.GetRootAsPatterns(b, 0).FormatVersion() },
		func(b []byte) {
			out = patternInfo(fb.GetRootAsPatterns(b, 0).Patterns(nil))
		})
	return out, err
}

func patternInfo(p *fb.PatternInfo) *utils.PatternInfo {
	if p == nil {
		return &utils.PatternInfo{}
	}
	return &utils.PatternInfo{
		NamingConvention:  str(p.NamingConvention()),
		StructureType:     str(p.StructureType()),
		ArchitectureStyle: optStr(p.ArchitectureStyle()),
	}
}

// ReadCallGraph reads kb_call_graph.fb. Only nodes and edges are returned;
// nothing in eulix-cli reads the tags or file categories.
func ReadCallGraph(path string) (*utils.CallGraphRef, error) {
	var out *utils.CallGraphRef
	err := decode(path, "call graph", fb.CallGraphBufferHasIdentifier,
		func(b []byte) uint32 { return fb.GetRootAsCallGraph(b, 0).FormatVersion() },
		func(b []byte) {
			root := fb.GetRootAsCallGraph(b, 0)
			// Ids, files and types repeat across nodes and edges; the file stores
			// each once, so intern them to keep one Go string per distinct value.
			s := newInterner(root.NodesLength())
			nodes := make([]utils.CallGraphNode, root.NodesLength())
			var n fb.CallGraphNode
			for i := range nodes {
				root.Nodes(&n, i)
				nodes[i] = utils.CallGraphNode{
					ID:                s(n.Id()),
					NodeType:          s(n.NodeType()),
					File:              s(n.File()),
					CallCountEstimate: int(n.CallCountEstimate()),
					IsEntryPoint:      n.IsEntryPoint(),
				}
			}
			edges := make([]utils.CallGraphEdge, root.EdgesLength())
			var e fb.CallGraphEdge
			for i := range edges {
				root.Edges(&e, i)
				edges[i] = utils.CallGraphEdge{
					From:         s(e.From()),
					To:           s(e.To()),
					EdgeType:     s(e.EdgeType()),
					CallSiteLine: int(e.CallSiteLine()),
					Conditional:  e.Conditional(),
				}
			}
			out = &utils.CallGraphRef{Nodes: nodes, Edges: edges}
		})
	return out, err
}

// newInterner returns a function that copies a FlatBuffers string out of the
// buffer, reusing one Go string per distinct value. The outputs store each
// distinct string once; interning keeps that saving in memory too.
func newInterner(sizeHint int) func([]byte) string {
	seen := make(map[string]string, sizeHint)
	return func(raw []byte) string {
		if v, ok := seen[string(raw)]; ok { // the lookup does not allocate
			return v
		}
		v := string(raw)
		seen[v] = v
		return v
	}
}

// stringListMap converts a vector of StringList entries into a map.
func stringListMap(n int, at func(*fb.StringList, int) bool, s func([]byte) string) map[string][]string {
	out := make(map[string][]string, n)
	var e fb.StringList
	for i := 0; i < n; i++ {
		at(&e, i)
		vals := make([]string, e.ValuesLength())
		for j := range vals {
			vals[j] = s(e.Values(j))
		}
		out[s(e.Key())] = vals
	}
	return out
}

// ReadIndex reads kb_index.fb.
func ReadIndex(path string) (*utils.Indices, error) {
	var out *utils.Indices
	err := decode(path, "index", fb.IndicesBufferHasIdentifier,
		func(b []byte) uint32 { return fb.GetRootAsIndices(b, 0).FormatVersion() },
		func(b []byte) {
			ix := fb.GetRootAsIndices(b, 0)
			s := newInterner(ix.FunctionsByNameLength())
			out = &utils.Indices{
				FunctionsByName:  stringListMap(ix.FunctionsByNameLength(), ix.FunctionsByName, s),
				FunctionsCalling: stringListMap(ix.FunctionsCallingLength(), ix.FunctionsCalling, s),
				FunctionsByTag:   stringListMap(ix.FunctionsByTagLength(), ix.FunctionsByTag, s),
				TypesByName:      stringListMap(ix.TypesByNameLength(), ix.TypesByName, s),
				FilesByCategory:  stringListMap(ix.FilesByCategoryLength(), ix.FilesByCategory, s),
			}
		})
	return out, err
}
