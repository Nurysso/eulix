//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package query provides the context window builder and query routing for Eulix's
RAG (Retrieval-Augmented Generation) system.

This file orchestrates knowledge base artifact loading and indexing:
  - kb.json: full codebase structure (functions, classes, methods with signatures,
    line ranges, complexity scores, call graphs)
  - kb_index.json: lightweight symbol index (function/type names and locations)
  - kb_call_graph.json: inter-procedural call relationships
  - embeddings.bin: vector embeddings for all chunks (binary format v2/v3)
  - vectors.bin: embedding→chunk ID map

Memory strategy:
  - Chunks are lazy-loaded (Content field omitted) for corpora > 50k chunks,
    with content hydrated on-demand from KB structures during keyword search.
  - For smaller corpora, content is inlined immediately for fast scanning.
  - Boilerplate detection runs after full chunk population so that
    document-frequency counts reflect the real corpus, not a partial scan.
    Common tokens (ctx, err, i, j) are then filtered from the symbol index
    and inverted index to reduce noise in retrieval scoring.

Boilerplate filtering is applied in three places:
 1. Symbol index construction (step 5): skip boilerplate symbols
 2. Inverted index construction (step 6): tokeniser checks cb.isBoilerplateSymbol
 3. Caller code: simBetween, any custom scorers should call cb.isBoilerplateSymbol

File format notes:
  - embeddings.bin / vectors.bin: [4B magic][4B version][4B+str model][4B count][4B dim]
    followed by count entries of [4B+str id][dim*4B float32 vector]
  - kb_call_graph.json: flat map of function ID → {Calls: [], CalledBy: []}

See:
  - boilerplate.go: corpus-driven BoilerplateDetector implementation
  - context_builder.go: ContextBuilder initialization and lifecycle
  - context_search.go: retrieval and scoring pipeline
*/
package query

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"eulix/internal/types"
)

const (
	ivfBuildThreshold = 10_000
	invIdxThreshold   = 5_000
	lazyContentLimit  = 50_000
	ivfNClusters      = 256
	ivfKMeansIter     = 20

	// Boilerplate detector tuning.
	// dfThresholdDefault is the fraction of chunks a symbol must appear in
	// to be suppressed. 0.30 is the recommended starting point — raise it
	// if legitimate short identifiers are being filtered, lower it if too
	// many common helpers are leaking through.
	dfThresholdDefault = 0.30
	// bpMinChunks is the minimum corpus size before statistics are trusted.
	bpMinChunks = 50
)

func (cb *ContextBuilder) loadKnowledgeBase() error {
	data, err := os.ReadFile(filepath.Join(cb.eulixDir, "kb.json"))
	if err != nil {
		return fmt.Errorf("kb.json not found: %w", err)
	}
	var kb types.KnowledgeBase
	if err := json.Unmarshal(data, &kb); err != nil {
		return fmt.Errorf("failed to parse kb.json: %w", err)
	}
	cb.kbData = &kb
	return nil
}

// loadChunksFromKB builds cb.chunks, cb.symbolIndex, cb.invertedIdx, and
// cb.boilerplate from kb.json + kb_index.json — without touching embeddings.json.
//
// Memory strategy:
//   - For corpora with > lazyContentLimit chunks, Content is left empty and
//     hydrated on demand from the KB structures.
//   - For smaller corpora content is inlined immediately so keyword search
//     has something to scan.
//
// Boilerplate detection runs after the chunk slice is fully populated, so the
// document-frequency counts are computed over the real corpus rather than a
// partial scan. The detector is then available to simBetween, the inverted
// index, and any other scorer that calls cb.isBoilerplateSymbol.
//
// See:
//   - boilerplate.go: NewBoilerplateDetector and isBoilerplateSymbol
//   - context_kb.go: buildChunkFromKBFunction, buildChunkFromKBClass helpers
func (cb *ContextBuilder) loadChunksFromKB() error {
	// 1. Load kb_index.json (lightweight: just names + locations)
	idxData, err := os.ReadFile(filepath.Join(cb.eulixDir, "kb_index.json"))
	if err != nil {
		return fmt.Errorf("kb_index.json not found: %w", err)
	}
	var kbIdx KBIndex
	if err := json.Unmarshal(idxData, &kbIdx); err != nil {
		return fmt.Errorf("failed to parse kb_index.json: %w", err)
	}

	// 2. Load kb.json for full content
	kbData, err := os.ReadFile(filepath.Join(cb.eulixDir, "kb.json"))
	if err != nil {
		return fmt.Errorf("kb.json not found: %w", err)
	}
	var kb types.KnowledgeBase
	if err := json.Unmarshal(kbData, &kb); err != nil {
		return fmt.Errorf("failed to parse kb.json: %w", err)
	}
	cb.kbData = &kb

	// 3. Build chunk slice
	total := 0
	for _, fs := range kb.Structure {
		total += len(fs.Functions) + len(fs.Classes)
		for _, cls := range fs.Classes {
			total += len(cls.Methods)
		}
	}

	cb.lazyContent = total > lazyContentLimit
	cb.chunks = make([]Chunk, 0, total)

	for filePath, fs := range kb.Structure {
		for _, fn := range fs.Functions {
			c := cb.buildChunkFromKBFunction(fn, filePath)
			if cb.lazyContent {
				c.Content = ""
			}
			cb.chunks = append(cb.chunks, c)
		}
		for _, cls := range fs.Classes {
			cc := cb.buildChunkFromKBClass(cls, filePath)
			if cb.lazyContent {
				cc.Content = ""
			}
			cb.chunks = append(cb.chunks, cc)
			for _, m := range cls.Methods {
				mc := cb.buildChunkFromKBFunction(m, filePath)
				if cb.lazyContent {
					mc.Content = ""
				}
				cb.chunks = append(cb.chunks, mc)
			}
		}
	}

	// 4. Boilerplate detector
	// Built here, over the complete chunk corpus, so that document-frequency
	// counts are accurate. Everything downstream (symbol index, inverted index,
	// simBetween) can call cb.isBoilerplateSymbol to skip noisy tokens.
	cb.boilerplate = NewBoilerplateDetector(cb.chunks, dfThresholdDefault, bpMinChunks)
	cb.debugLog.Log("Boilerplate detector: %d symbols filtered (threshold=%.2f, corpus=%d) top: %v",
		len(cb.boilerplate.boilerplate),
		dfThresholdDefault,
		len(cb.chunks),
		cb.boilerplate.TopBoilerplate(5),
	)

	// 5. Symbol index (boilerplate-filtered)
	// Skipping boilerplate symbols here halves the index size on typical Go/Python
	// codebases (ctx, err, ok, i, j are in nearly every chunk).
	cb.symbolIndex = make(map[string][]int, len(cb.chunks)*2)
	for i, c := range cb.chunks {
		for _, sym := range c.Symbols {
			if cb.isBoilerplateSymbol(sym) {
				continue
			}
			cb.symbolIndex[sym] = append(cb.symbolIndex[sym], i)
		}
	}

	// 6. Inverted index (large corpora only)
	// buildInvertedIndexFromKB is expected to call cb.isBoilerplateSymbol
	// internally when tokenising chunk content — see its implementation.
	if len(cb.chunks) > invIdxThreshold {
		cb.invertedIdx = cb.buildInvertedIndexFromKB(&kb)
	}

	cb.kbIdx = &kbIdx
	cb.callSites = buildCallSiteIndex(cb.chunks)
	cb.debugLog.Log("Built call-site index with %d entries", len(cb.callSites))

	return nil
}

// loadEmbeddings reads embeddings.bin (EULX magic, version 2/3).
// Supports both legacy (v2, no embedding IDs) and current (v3, with IDs) formats.
//
// Format: [4B magic][4B version][4B+str model_name][4B count][4B dim]
// then for each entry: [4B+str id (v3 only)][dim*4B float32 vector]
//
// Builds an IVF (Inverted File) index if the embedding count exceeds
// ivfBuildThreshold, enabling approximate nearest-neighbor search without
// comparing against all vectors. See context_vectorIVF.go for index construction.
//
// See:
//   - context_vectorIVF.go: buildIVFIndex and vectorSearchIVF
//   - context_search.go: vectorSearch integration point
func (cb *ContextBuilder) loadEmbeddings() error {
	data, err := os.ReadFile(filepath.Join(cb.eulixDir, "embeddings.bin"))
	if err != nil {
		return fmt.Errorf("embeddings.bin not found: %w", err)
	}
	if len(data) < 8 {
		return fmt.Errorf("invalid embeddings file: too short (%d bytes)", len(data))
	}

	off := 0
	if string(data[off:off+4]) != MagicBytes {
		return fmt.Errorf("wrong magic bytes in embeddings.bin: %q", data[off:off+4])
	}
	off += 4

	version := binary.LittleEndian.Uint32(data[off : off+4])
	off += 4
	if version != 2 && version != 3 {
		return fmt.Errorf("unsupported embeddings.bin version: %d", version)
	}
	if version != BinaryVersion {
		return fmt.Errorf("version mismatch: expected %d, got %d", BinaryVersion, version)
	}

	_, off, err = readStr(data, off)
	if err != nil {
		return fmt.Errorf("reading model name: %w", err)
	}

	if off+8 > len(data) {
		return fmt.Errorf("invalid embeddings file: truncated header")
	}
	numEmb := int(binary.LittleEndian.Uint32(data[off : off+4]))
	dim := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
	off += 8

	if dim != cb.config.Embeddings.Dimension {
		return fmt.Errorf("dimension mismatch: expected %d, got %d", cb.config.Embeddings.Dimension, dim)
	}

	cb.embeddings = make([][]float32, numEmb)
	ids := make([]string, numEmb)

	for i := 0; i < numEmb; i++ {
		if version >= 3 {
			ids[i], off, err = readStr(data, off)
			if err != nil {
				return fmt.Errorf("reading id at index %d: %w", i, err)
			}
		} else {
			ids[i] = fmt.Sprintf("embedding_%d", i)
		}
		if off+dim*4 > len(data) {
			return fmt.Errorf("unexpected EOF reading vector at index %d", i)
		}
		emb := make([]float32, dim)
		for j := range emb {
			emb[j] = math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4]))
			off += 4
		}
		cb.embeddings[i] = emb
	}

	if numEmb > ivfBuildThreshold {
		cb.ivfIndex = buildIVFIndex(cb.embeddings, ivfNClusters, ivfKMeansIter)
	}
	return nil
}

// loadVectorMap reads vectors.bin to build the id→embedding-index map.
// Maps embedding IDs (from kb_index.json or manually assigned) to their
// positions in the embeddings slice for fast lookup during scoring.
//
// Format: [4B magic][4B version][4B+str model_name][4B count][4B dim]
// then for each entry: [4B+str id][dim*4B float32 vector (skipped)]
//
// See:
//   - context_search.go: vectorSearch uses vectorMap to locate embeddings by chunk ID
//   - context_kb.go: chunk ID assignment during KB loading
func (cb *ContextBuilder) loadVectorMap() error {
	data, err := os.ReadFile(filepath.Join(cb.eulixDir, "vectors.bin"))
	if err != nil {
		return fmt.Errorf("vectors.bin not found: %w", err)
	}
	if len(data) < 8 {
		return fmt.Errorf("invalid vectors file: too short (%d bytes)", len(data))
	}

	off := 0
	if string(data[off:off+4]) != VectorMagic {
		return fmt.Errorf("wrong magic bytes in vectors.bin: %q", data[off:off+4])
	}
	off += 4

	version := binary.LittleEndian.Uint32(data[off : off+4])
	off += 4
	if version != VectorVersion {
		return fmt.Errorf("vectors.bin version mismatch: expected %d, got %d", VectorVersion, version)
	}

	_, off, err = readStr(data, off)
	if err != nil {
		return fmt.Errorf("reading model name: %w", err)
	}

	if off+8 > len(data) {
		return fmt.Errorf("invalid vectors file: truncated header")
	}
	count := int(binary.LittleEndian.Uint32(data[off : off+4]))
	dim := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
	off += 8

	cb.vectorMap = make(map[string]int, count)
	for i := 0; i < count; i++ {
		var id string
		if version >= 2 {
			id, off, err = readStr(data, off)
			if err != nil {
				return fmt.Errorf("reading id at index %d: %w", i, err)
			}
		} else {
			id = fmt.Sprintf("vec_%d", i)
		}
		if off+dim*4 > len(data) {
			return fmt.Errorf("unexpected EOF skipping vector at index %d", i)
		}
		cb.vectorMap[id] = i
		off += dim * 4
	}
	return nil
}

// loadCallGraph loads kb_call_graph.json to build the inter-procedural call graph.
// Maps each function to its callers and callees, enabling graph-based expansion
// during context assembly. Gracefully skips if call graph is unavailable.
//
// See:
//   - context_graph.go: expandGraph uses call graph to trace related code
//   - context_search.go: integration with multi-strategy search pipeline
func (cb *ContextBuilder) loadCallGraph() {
	data, err := os.ReadFile(filepath.Join(cb.eulixDir, "kb_call_graph.json"))
	if err != nil {
		cb.hasCallGraph = false
		return
	}
	var gd CallGraphData
	if err := json.Unmarshal(data, &gd); err != nil {
		cb.hasCallGraph = false
		return
	}
	cb.callGraph = make(map[string][]Relationship, len(gd.Functions))
	for fn, fd := range gd.Functions {
		rels := make([]Relationship, 0, len(fd.Calls)+len(fd.CalledBy))
		for _, c := range fd.Calls {
			rels = append(rels, Relationship{Type: "calls", Target: c, Distance: 1})
		}
		for _, c := range fd.CalledBy {
			rels = append(rels, Relationship{Type: "called_by", Target: c, Distance: 1})
		}
		cb.callGraph[fn] = rels
	}
	cb.hasCallGraph = len(cb.callGraph) > 0
}
