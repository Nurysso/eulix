// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package kbfiletest builds the FlatBuffers files eulix-parser writes, for
// test fixtures across eulix-cli. Only tests import it; eulix-parser is the
// only production writer. Keep the encoders in step with libeulix/src/fb_io.rs.
package kbfiletest

import (
	"encoding/json"
	"os"
	"sort"

	flatbuffers "github.com/google/flatbuffers/go"

	"eulix/internal/fb"
	"eulix/internal/utils"
)

func strVec(b *flatbuffers.Builder, items []string, start func(*flatbuffers.Builder, int) flatbuffers.UOffsetT) flatbuffers.UOffsetT {
	offs := make([]flatbuffers.UOffsetT, len(items))
	for i, s := range items {
		offs[i] = b.CreateString(s)
	}
	start(b, len(offs))
	for i := len(offs) - 1; i >= 0; i-- {
		b.PrependUOffsetT(offs[i])
	}
	return b.EndVector(len(offs))
}

func tableVec(b *flatbuffers.Builder, offs []flatbuffers.UOffsetT, start func(*flatbuffers.Builder, int) flatbuffers.UOffsetT) flatbuffers.UOffsetT {
	start(b, len(offs))
	for i := len(offs) - 1; i >= 0; i-- {
		b.PrependUOffsetT(offs[i])
	}
	return b.EndVector(len(offs))
}

func optString(b *flatbuffers.Builder, s *string) flatbuffers.UOffsetT {
	if s == nil {
		return 0
	}
	return b.CreateString(*s)
}

// EncodeEntryPoints builds a kb_entry_points.fb buffer.
func EncodeEntryPoints(eps []utils.EntryPoint) []byte {
	b := flatbuffers.NewBuilder(1024)
	offs := make([]flatbuffers.UOffsetT, len(eps))
	for i, e := range eps {
		entryType, path := b.CreateString(e.EntryType), optString(b, e.Path)
		function, handler, file := b.CreateString(e.Function), b.CreateString(e.Handler), b.CreateString(e.File)
		var methods flatbuffers.UOffsetT
		if e.Methods != nil {
			methods = strVec(b, e.Methods, fb.EntryPointStartMethodsVector)
		}
		fb.EntryPointStart(b)
		fb.EntryPointAddEntryType(b, entryType)
		if path != 0 {
			fb.EntryPointAddPath(b, path)
		}
		fb.EntryPointAddFunction(b, function)
		fb.EntryPointAddHandler(b, handler)
		fb.EntryPointAddFile(b, file)
		fb.EntryPointAddLine(b, uint64(e.Line))
		if methods != 0 {
			fb.EntryPointAddMethods(b, methods)
		}
		offs[i] = fb.EntryPointEnd(b)
	}
	vec := tableVec(b, offs, fb.EntryPointsStartEntryPointsVector)
	fb.EntryPointsStart(b)
	fb.EntryPointsAddFormatVersion(b, utils.KBFormatVersion)
	fb.EntryPointsAddEntryPoints(b, vec)
	fb.FinishEntryPointsBuffer(b, fb.EntryPointsEnd(b))
	return b.FinishedBytes()
}

// EncodeExternalDeps builds a kb_external_deps.fb buffer.
func EncodeExternalDeps(deps []utils.ExternalDependency) []byte {
	b := flatbuffers.NewBuilder(1024)
	offs := make([]flatbuffers.UOffsetT, len(deps))
	for i, d := range deps {
		name, version, source := b.CreateString(d.Name), optString(b, d.Version), b.CreateString(d.Source)
		usedBy := strVec(b, d.UsedBy, fb.ExternalDependencyStartUsedByVector)
		fb.ExternalDependencyStart(b)
		fb.ExternalDependencyAddName(b, name)
		if version != 0 {
			fb.ExternalDependencyAddVersion(b, version)
		}
		fb.ExternalDependencyAddSource(b, source)
		fb.ExternalDependencyAddUsedBy(b, usedBy)
		fb.ExternalDependencyAddImportCount(b, uint64(d.ImportCount))
		offs[i] = fb.ExternalDependencyEnd(b)
	}
	vec := tableVec(b, offs, fb.ExternalDepsStartExternalDependenciesVector)
	fb.ExternalDepsStart(b)
	fb.ExternalDepsAddFormatVersion(b, utils.KBFormatVersion)
	fb.ExternalDepsAddExternalDependencies(b, vec)
	fb.FinishExternalDepsBuffer(b, fb.ExternalDepsEnd(b))
	return b.FinishedBytes()
}

func encodeMetadata(b *flatbuffers.Builder, m *utils.Metadata) flatbuffers.UOffsetT {
	if m == nil {
		m = &utils.Metadata{}
	}
	version, gitHash, name := b.CreateString(m.Version), b.CreateString(m.GitHash), b.CreateString(m.ProjectName)
	hash, parsedAt := b.CreateString(m.ProjectHash), b.CreateString(m.ParsedAt)
	langs := strVec(b, m.Languages, fb.MetadataStartLanguagesVector)
	fb.MetadataStart(b)
	fb.MetadataAddVersion(b, version)
	fb.MetadataAddGitHash(b, gitHash)
	fb.MetadataAddProjectName(b, name)
	fb.MetadataAddProjectHash(b, hash)
	fb.MetadataAddParsedAt(b, parsedAt)
	fb.MetadataAddLanguages(b, langs)
	fb.MetadataAddTotalFiles(b, uint64(m.TotalFiles))
	fb.MetadataAddTotalLoc(b, uint64(m.TotalLoc))
	fb.MetadataAddTotalFunctions(b, uint64(m.TotalFunctions))
	fb.MetadataAddTotalClasses(b, uint64(m.TotalClasses))
	fb.MetadataAddTotalMethods(b, uint64(m.TotalMethods))
	return fb.MetadataEnd(b)
}

// EncodeMetrics builds a kb_metrics.fb buffer.
func EncodeMetrics(r *utils.MetricsReport) []byte {
	b := flatbuffers.NewBuilder(1024)
	md := encodeMetadata(b, r.Metadata)
	offs := make([]flatbuffers.UOffsetT, len(r.TopComplexFunctions))
	for i, f := range r.TopComplexFunctions {
		name, file := b.CreateString(f.Name), b.CreateString(f.File)
		fb.FunctionMetricStart(b)
		fb.FunctionMetricAddName(b, name)
		fb.FunctionMetricAddFile(b, file)
		fb.FunctionMetricAddComplexity(b, uint64(f.Complexity))
		fb.FunctionMetricAddImportanceScore(b, f.ImportanceScore)
		fb.FunctionMetricAddLineStart(b, uint64(f.LineStart))
		fb.FunctionMetricAddLineEnd(b, uint64(f.LineEnd))
		offs[i] = fb.FunctionMetricEnd(b)
	}
	vec := tableVec(b, offs, fb.MetricsStartTopComplexFunctionsVector)
	fb.MetricsStart(b)
	fb.MetricsAddFormatVersion(b, utils.KBFormatVersion)
	fb.MetricsAddMetadata(b, md)
	fb.MetricsAddTopComplexFunctions(b, vec)
	fb.FinishMetricsBuffer(b, fb.MetricsEnd(b))
	return b.FinishedBytes()
}

// kbPad matches libeulix fb_io::kb_pad: zero bytes up to the next multiple of 8.
func kbPad(n int64) int64 { return (8 - n%8) % 8 }

// WriteFile writes an encoded buffer, for test fixtures.
func WriteFile(path string, buf []byte) error {
	return os.WriteFile(path, buf, 0o644)
}

// EncodeCallGraph builds a kb_call_graph.fb buffer with nodes and edges only.
func EncodeCallGraph(ref *utils.CallGraphRef) []byte {
	b := flatbuffers.NewBuilder(1024)
	nodes := make([]flatbuffers.UOffsetT, len(ref.Nodes))
	for i, n := range ref.Nodes {
		id, typ, file := b.CreateSharedString(n.ID), b.CreateSharedString(n.NodeType), b.CreateSharedString(n.File)
		fb.CallGraphNodeStart(b)
		fb.CallGraphNodeAddId(b, id)
		fb.CallGraphNodeAddNodeType(b, typ)
		fb.CallGraphNodeAddFile(b, file)
		fb.CallGraphNodeAddIsEntryPoint(b, n.IsEntryPoint)
		fb.CallGraphNodeAddCallCountEstimate(b, uint64(n.CallCountEstimate))
		nodes[i] = fb.CallGraphNodeEnd(b)
	}
	nodeVec := tableVec(b, nodes, fb.CallGraphStartNodesVector)
	edges := make([]flatbuffers.UOffsetT, len(ref.Edges))
	for i, e := range ref.Edges {
		from, to, typ := b.CreateSharedString(e.From), b.CreateSharedString(e.To), b.CreateSharedString(e.EdgeType)
		fb.CallGraphEdgeStart(b)
		fb.CallGraphEdgeAddFrom(b, from)
		fb.CallGraphEdgeAddTo(b, to)
		fb.CallGraphEdgeAddEdgeType(b, typ)
		fb.CallGraphEdgeAddConditional(b, e.Conditional)
		fb.CallGraphEdgeAddCallSiteLine(b, uint64(e.CallSiteLine))
		edges[i] = fb.CallGraphEdgeEnd(b)
	}
	edgeVec := tableVec(b, edges, fb.CallGraphStartEdgesVector)
	fb.CallGraphStart(b)
	fb.CallGraphAddFormatVersion(b, utils.KBFormatVersion)
	fb.CallGraphAddNodes(b, nodeVec)
	fb.CallGraphAddEdges(b, edgeVec)
	fb.FinishCallGraphBuffer(b, fb.CallGraphEnd(b))
	return b.FinishedBytes()
}

// EncodeCallGraphJSON encodes a call graph given in the old JSON shape, so
// tests can keep readable fixtures. It panics on invalid JSON.
func EncodeCallGraphJSON(js string) []byte {
	var ref utils.CallGraphRef
	if err := json.Unmarshal([]byte(js), &ref); err != nil {
		panic("kbfile.EncodeCallGraphJSON: " + err.Error())
	}
	return EncodeCallGraph(&ref)
}

func encodeStringLists(b *flatbuffers.Builder, m map[string][]string, start func(*flatbuffers.Builder, int) flatbuffers.UOffsetT) flatbuffers.UOffsetT {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys) // StringList vectors are sorted by key, as the parser writes them
	offs := make([]flatbuffers.UOffsetT, len(keys))
	for i, k := range keys {
		vals := make([]flatbuffers.UOffsetT, len(m[k]))
		for j, v := range m[k] {
			vals[j] = b.CreateSharedString(v)
		}
		fb.StringListStartValuesVector(b, len(vals))
		for j := len(vals) - 1; j >= 0; j-- {
			b.PrependUOffsetT(vals[j])
		}
		values := b.EndVector(len(vals))
		key := b.CreateSharedString(k)
		fb.StringListStart(b)
		fb.StringListAddKey(b, key)
		fb.StringListAddValues(b, values)
		offs[i] = fb.StringListEnd(b)
	}
	return tableVec(b, offs, start)
}

// EncodeIndex builds a kb_index.fb buffer.
func EncodeIndex(ix *utils.Indices) []byte {
	b := flatbuffers.NewBuilder(1024)
	byName := encodeStringLists(b, ix.FunctionsByName, fb.IndicesStartFunctionsByNameVector)
	calling := encodeStringLists(b, ix.FunctionsCalling, fb.IndicesStartFunctionsCallingVector)
	byTag := encodeStringLists(b, ix.FunctionsByTag, fb.IndicesStartFunctionsByTagVector)
	types := encodeStringLists(b, ix.TypesByName, fb.IndicesStartTypesByNameVector)
	files := encodeStringLists(b, ix.FilesByCategory, fb.IndicesStartFilesByCategoryVector)
	fb.IndicesStart(b)
	fb.IndicesAddFormatVersion(b, utils.KBFormatVersion)
	fb.IndicesAddFunctionsByName(b, byName)
	fb.IndicesAddFunctionsCalling(b, calling)
	fb.IndicesAddFunctionsByTag(b, byTag)
	fb.IndicesAddTypesByName(b, types)
	fb.IndicesAddFilesByCategory(b, files)
	fb.FinishIndicesBuffer(b, fb.IndicesEnd(b))
	return b.FinishedBytes()
}

// EncodeIndexJSON encodes an index given in the old kb_index.json shape
// ({"indices": {...}}), so tests can keep readable fixtures. It panics on
// invalid JSON.
func EncodeIndexJSON(js string) []byte {
	var ref utils.IndexDataRef
	if err := json.Unmarshal([]byte(js), &ref); err != nil {
		panic("kbfile.EncodeIndexJSON: " + err.Error())
	}
	if ref.Indices == nil {
		ref.Indices = &utils.Indices{}
	}
	return EncodeIndex(ref.Indices)
}

func encodeFunction(b *flatbuffers.Builder, f *utils.KBFunction) flatbuffers.UOffsetT {
	params := make([]flatbuffers.UOffsetT, len(f.Params))
	for i, p := range f.Params {
		name, typ, def := b.CreateString(p.Name), b.CreateString(p.TypeAnnotation), optString(b, p.DefaultValue)
		fb.ParameterStart(b)
		fb.ParameterAddName(b, name)
		fb.ParameterAddTypeAnnotation(b, typ)
		if def != 0 {
			fb.ParameterAddDefaultValue(b, def)
		}
		params[i] = fb.ParameterEnd(b)
	}
	pv := tableVec(b, params, fb.FunctionStartParamsVector)
	id, name, sig := b.CreateString(f.ID), b.CreateString(f.Name), b.CreateString(f.Signature)
	ret, doc := b.CreateString(f.ReturnType), b.CreateString(f.Docstring)
	fb.FunctionStart(b)
	fb.FunctionAddId(b, id)
	fb.FunctionAddName(b, name)
	fb.FunctionAddSignature(b, sig)
	fb.FunctionAddParams(b, pv)
	fb.FunctionAddReturnType(b, ret)
	fb.FunctionAddDocstring(b, doc)
	fb.FunctionAddLineStart(b, uint64(f.LineStart))
	fb.FunctionAddLineEnd(b, uint64(f.LineEnd))
	return fb.FunctionEnd(b)
}

func encodeFunctions(b *flatbuffers.Builder, fs []utils.KBFunction, start func(*flatbuffers.Builder, int) flatbuffers.UOffsetT) flatbuffers.UOffsetT {
	offs := make([]flatbuffers.UOffsetT, len(fs))
	for i := range fs {
		offs[i] = encodeFunction(b, &fs[i])
	}
	return tableVec(b, offs, start)
}

// EncodeFileRecord builds one kb.fb FileRecord buffer.
func EncodeFileRecord(fd *utils.FileData) []byte {
	b := flatbuffers.NewBuilder(1024)
	funcs := encodeFunctions(b, fd.Functions, fb.FileRecordStartFunctionsVector)
	classes := make([]flatbuffers.UOffsetT, len(fd.Classes))
	for i := range fd.Classes {
		c := &fd.Classes[i]
		methods := encodeFunctions(b, c.Methods, fb.ClassStartMethodsVector)
		attrs := make([]flatbuffers.UOffsetT, len(c.Attributes))
		for j, a := range c.Attributes {
			name, typ, val := b.CreateString(a.Name), b.CreateString(a.TypeAnnotation), optString(b, a.Value)
			fb.AttributeStart(b)
			fb.AttributeAddName(b, name)
			fb.AttributeAddTypeAnnotation(b, typ)
			if val != 0 {
				fb.AttributeAddValue(b, val)
			}
			attrs[j] = fb.AttributeEnd(b)
		}
		av := tableVec(b, attrs, fb.ClassStartAttributesVector)
		bases := strVec(b, c.Bases, fb.ClassStartBasesVector)
		decos := strVec(b, c.Decorators, fb.ClassStartDecoratorsVector)
		id, name, doc := b.CreateString(c.ID), b.CreateString(c.Name), b.CreateString(c.Docstring)
		fb.ClassStart(b)
		fb.ClassAddId(b, id)
		fb.ClassAddName(b, name)
		fb.ClassAddBases(b, bases)
		fb.ClassAddDocstring(b, doc)
		fb.ClassAddLineStart(b, uint64(c.LineStart))
		fb.ClassAddLineEnd(b, uint64(c.LineEnd))
		fb.ClassAddMethods(b, methods)
		fb.ClassAddAttributes(b, av)
		fb.ClassAddDecorators(b, decos)
		classes[i] = fb.ClassEnd(b)
	}
	cv := tableVec(b, classes, fb.FileRecordStartClassesVector)
	todos := make([]flatbuffers.UOffsetT, len(fd.Todos))
	for i, t := range fd.Todos {
		text, prio := b.CreateString(t.Text), b.CreateString(t.Priority)
		fb.TodoStart(b)
		fb.TodoAddLine(b, uint64(t.Line))
		fb.TodoAddText(b, text)
		fb.TodoAddPriority(b, prio)
		todos[i] = fb.TodoEnd(b)
	}
	tv := tableVec(b, todos, fb.FileRecordStartTodosVector)
	lang := b.CreateString(fd.Language)
	fb.FileRecordStart(b)
	fb.FileRecordAddLanguage(b, lang)
	fb.FileRecordAddLoc(b, uint64(fd.Loc))
	fb.FileRecordAddFunctions(b, funcs)
	fb.FileRecordAddClasses(b, cv)
	fb.FileRecordAddTodos(b, tv)
	fb.FinishFileRecordBuffer(b, fb.FileRecordEnd(b))
	return b.FinishedBytes()
}

// EncodeKB builds a whole kb.fb file: header, padding and one record per
// file in path order, as eulix-parser writes it.
func EncodeKB(kb *utils.KnowledgeBaseSimplifiedRef) []byte {
	paths := make([]string, 0, len(kb.Structure))
	for p := range kb.Structure {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	records := make([][]byte, len(paths))
	for i, p := range paths {
		fd := kb.Structure[p]
		records[i] = EncodeFileRecord(&fd)
	}

	b := flatbuffers.NewBuilder(1024)
	entries := make([]flatbuffers.UOffsetT, len(paths))
	var offset int64
	for i, p := range paths {
		path := b.CreateString(p)
		fb.FileEntryStart(b)
		fb.FileEntryAddPath(b, path)
		fb.FileEntryAddOffset(b, uint64(offset))
		fb.FileEntryAddLength(b, uint32(len(records[i])))
		entries[i] = fb.FileEntryEnd(b)
		offset += int64(len(records[i])) + kbPad(int64(len(records[i])))
	}
	files := tableVec(b, entries, fb.KbHeaderStartFilesVector)
	md := encodeMetadata(b, kb.Metadata)
	fb.KbHeaderStart(b)
	fb.KbHeaderAddFormatVersion(b, utils.KBFormatVersion)
	fb.KbHeaderAddMetadata(b, md)
	fb.KbHeaderAddFiles(b, files)
	fb.FinishSizePrefixedKbHeaderBuffer(b, fb.KbHeaderEnd(b))

	out := append([]byte(nil), b.FinishedBytes()...)
	out = append(out, make([]byte, kbPad(int64(len(out))))...)
	for _, rec := range records {
		out = append(out, rec...)
		out = append(out, make([]byte, kbPad(int64(len(rec))))...)
	}
	return out
}

// EncodeKBJSON encodes a knowledge base given in the old kb.json shape
// ({"metadata": ..., "structure": {path: file}}), so tests can keep readable
// fixtures. It panics on invalid JSON.
func EncodeKBJSON(js string) []byte {
	var kb utils.KnowledgeBaseSimplifiedRef
	if err := json.Unmarshal([]byte(js), &kb); err != nil {
		panic("kbfile.EncodeKBJSON: " + err.Error())
	}
	return EncodeKB(&kb)
}
