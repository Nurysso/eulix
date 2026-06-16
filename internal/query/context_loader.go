// Copyright (C) 2026 Dawood Khan
// SPDX-License-Identifier: GPL-3.0-or-later

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

JSON loading:
  - All JSON files are decoded via decodeJSONFile, which streams through a
    buffered json.Decoder rather than materialising the whole file as a []byte.
    On a 500 MB kb.json this roughly halves peak RSS because the raw bytes and
    parsed struct no longer coexist in memory. For GB-scale corpora the
    difference is significant. The binary .bin files (embeddings, vectors) still
    use os.ReadFile because their parsers require direct []byte index arithmetic.

Boilerplate filtering is applied in three places:
 1. Symbol index construction (step 5): skip boilerplate symbols
 2. Inverted index construction (step 6): tokeniser checks cb.isBoilerplateSymbol
 3. Caller code: simBetween, any custom scorers should call cb.isBoilerplateSymbol

File format notes:
  - embeddings.bin / vectors.bin: [4B magic][4B version][4B+str model][4B count][4B dim]
    followed by count entries of [4B+str id][dim*4B float32 vector]
  - kb_call_graph.json: flat map of function ID → {Calls: [], CalledBy: []}

Standalone routing helpers (loadKBIndex, loadCallGraph) are provided
for the query-classification path, which operates outside the ContextBuilder
lifecycle. They share the same streaming-decode logic as the ContextBuilder
methods.

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
	"runtime"
	"time"

	"eulix/internal/types"
)

const (
	ivfBuildThreshold = 50_000
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

	// jsonBufSize is the I/O read-buffer for the streaming JSON decoder.
	// 4 MiB reduces syscall frequency on large kb.json files without adding
	// meaningful resident-memory pressure.
	jsonBufSize = 1 << 20 // 1 MiB
)

// logFileLoad wraps a file load operation with timing + RSS delta logging.
// Usage:
//
//	done := cb.logFileLoad("kb.json")
//	err := decodeJSONFile(path, &kb)
//	done(err)
func (cb *ContextBuilder) logFileLoad(name string) func(error) {
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	t0 := time.Now()
	cb.debugLog.Log("[LOAD] %-30s opening  (heap: %d MB)",
		name, memBefore.HeapInuse/1024/1024)

	return func(err error) {
		var memAfter runtime.MemStats
		runtime.ReadMemStats(&memAfter)
		elapsed := time.Since(t0)
		heapDelta := int64(memAfter.HeapInuse) - int64(memBefore.HeapInuse)
		if err != nil {
			cb.debugLog.Log("[LOAD] %-30s FAILED   (%v) elapsed=%v",
				name, err, elapsed)
		} else {
			cb.debugLog.Log("[LOAD] %-30s done     elapsed=%-10v heap_delta=+%d MB total_heap=%d MB",
				name, elapsed,
				heapDelta/1024/1024,
				memAfter.HeapInuse/1024/1024,
			)
		}
	}
}

// decodeJSONFile opens path and streams its content through a buffered
// json.Decoder into v. Using a streaming decoder rather than os.ReadFile +
// json.Unmarshal avoids holding both the raw file bytes and the parsed struct
// in memory simultaneously. For a 500 MB kb.json this saves ~500 MB of
// transient allocations; for GB-scale corpora the saving is proportionally
// larger.

//  Standalone routing loaders
// Used by the query-classification path, which does not have access to a
// ContextBuilder. All three functions are thin wrappers over decodeJSONFile,
// so they benefit from the same streaming-decode behaviour as the ContextBuilder
// methods below.

// loadKBIndex loads the lightweight symbol index for routing purposes.
func loadKBIndex(eulixDir string) (*KBIndex, error) {
	var idx KBIndex
	if err := decodeJSONFile(filepath.Join(eulixDir, "kb_index.json"), &idx); err != nil {
		return nil, fmt.Errorf("kb_index.json: %w", err)
	}
	return &idx, nil
}

// loadCallGraph loads the raw call-graph file for routing purposes and returns
// the routing-layer *CallGraph type.
//
// Note: the ContextBuilder method of the same name (cb *ContextBuilder).loadCallGraph
// reads the same file but produces the richer internal representation
// (map[string][]Relationship). In Go a package-level function and a method may
// share a name without conflict, so both coexist here intentionally.
func loadCallGraph(eulixDir string) (*CallGraph, error) {
	var g CallGraph
	if err := decodeJSONFile(filepath.Join(eulixDir, "kb_call_graph.json"), &g); err != nil {
		return nil, fmt.Errorf("kb_call_graph.json: %w", err)
	}
	return &g, nil
}

//  ContextBuilder loaders

func (cb *ContextBuilder) loadKnowledgeBase() error {
	done := cb.logFileLoad("kb.json")
	var kb types.KnowledgeBaseRef
	err := decodeJSONFile(filepath.Join(cb.eulixDir, "kb.json"), &kb)
	done(err)
	if err != nil {
		return fmt.Errorf("kb.json: %w", err)
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
	// 1 kb_index.json
	done := cb.logFileLoad("kb_index.json")
	var kbIdx KBIndex
	err := decodeJSONFile(filepath.Join(cb.eulixDir, "kb_index.json"), &kbIdx)
	done(err)
	if err != nil {
		return fmt.Errorf("kb_index.json: %w", err)
	}

	// 2. kb.json
	done = cb.logFileLoad("kb.json")
	var kb types.KnowledgeBaseRef
	err = decodeJSONFile(filepath.Join(cb.eulixDir, "kb.json"), &kb)
	done(err)
	if err != nil {
		return fmt.Errorf("kb.json: %w", err)
	}

	// 3. Build chunk slice + hydrate index simultaneously
	total := 0
	for _, fs := range kb.Structure {
		total += len(fs.Functions) + len(fs.Classes)
		for _, cls := range fs.Classes {
			total += len(cls.Methods)
		}
	}

	cb.lazyContent = total > lazyContentLimit
	cb.chunks = make([]Chunk, 0, total)
	cb.hydrateIdx = make(map[string]map[[2]int]func() string, len(kb.Structure))

	for filePath, fs := range kb.Structure {
		fileIdx := make(map[[2]int]func() string)

		// Functions
		for _, fn := range fs.Functions {
			c := cb.buildChunkFromKBFunction(fn, filePath)
			if cb.lazyContent {
				// Store the content builder BEFORE clearing Content
				fnCopy := fn
				fileIdx[[2]int{c.StartLine, c.EndLine}] = func() string {
					return cb.buildChunkFromKBFunction(fnCopy, filePath).Content
				}
				c.Content = ""
			}
			cb.chunks = append(cb.chunks, c)
		}

		// Classes
		for _, cls := range fs.Classes {
			cc := cb.buildChunkFromKBClass(cls, filePath)
			if cb.lazyContent {
				clsCopy := cls
				fileIdx[[2]int{cc.StartLine, cc.EndLine}] = func() string {
					return cb.buildChunkFromKBClass(clsCopy, filePath).Content
				}
				cc.Content = ""
			}
			cb.chunks = append(cb.chunks, cc)

			// Methods
			for _, m := range cls.Methods {
				mc := cb.buildChunkFromKBFunction(m, filePath)
				if cb.lazyContent {
					mCopy := m
					fileIdx[[2]int{mc.StartLine, mc.EndLine}] = func() string {
						return cb.buildChunkFromKBFunction(mCopy, filePath).Content
					}
					mc.Content = ""
				}
				cb.chunks = append(cb.chunks, mc)
			}
		}

		cb.hydrateIdx[filePath] = fileIdx
	}

	// 4. Boilerplate detector
	// cb.boilerplate = NewBoilerplateDetector(cb.chunks, dfThresholdDefault, bpMinChunks)
	cb.buildBoilerplateFromKB(&kb)
	cb.debugLog.Log("Boilerplate detector: %d symbols filtered (threshold=%.2f, corpus=%d) top: %v",
		len(cb.boilerplate.boilerplate),
		dfThresholdDefault,
		len(cb.chunks),
		cb.boilerplate.TopBoilerplate(5),
	)

	// 5. Symbol index (boilerplate-filtered)
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
	if len(cb.chunks) > invIdxThreshold {
		cb.invertedIdx = cb.buildInvertedIndexFromKB(&kb)
	}

	cb.kbIdx = &kbIdx
	cb.hasKB = cb.kbData != nil
	kb = types.KnowledgeBaseRef{}
	runtime.GC()
	cb.debugLog.Log("Freed kb.json memory, current heap: %d MB",
		getHeapAlloc()/1024/1024)
	return nil
}

func (cb *ContextBuilder) streamKBChunks() error {
	path := filepath.Join(cb.eulixDir, "kb.json")

	r, cleanup, err := openForSequentialRead(path)
	if err != nil {
		return err
	}
	defer cleanup()

	// encoding/json required here: sonic's decoder doesn't implement
	// Token()/More() streaming use stdlib for the token walk.
	dec := json.NewDecoder(r)

	if err := jsonSkipToKey(dec, "structure"); err != nil {
		return fmt.Errorf("finding 'structure' key: %w", err)
	}
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("reading structure opening brace: %w", err)
	}

	cb.chunks = make([]Chunk, 0, 320_000)
	cb.hydrateIdx = make(map[string]map[[2]int]func() string, 40_000)

	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("reading file path token: %w", err)
		}
		filePath, ok := tok.(string)
		if !ok {
			return fmt.Errorf("expected string key, got %T", tok)
		}

		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("reading FileData for %s: %w", filePath, err)
		}

		var fs types.FileData
		if err := sonicCopy.Unmarshal(raw, &fs); err != nil {
			return fmt.Errorf("unmarshaling FileData for %s: %w", filePath, err)
		}

		cb.addChunksFromFile(filePath, &fs)
	}

	cb.lazyContent = len(cb.chunks) > lazyContentLimit
	return nil
}

// loadEmbeddings reads embeddings.bin (EULX magic, version 2/3).
// Supports both legacy (v2, no embedding IDs) and current (v3, with IDs) formats.
//
// This loader intentionally uses os.ReadFile rather than the streaming JSON
// helper: the binary parser requires direct []byte index arithmetic (off+4,
// off+dim*4 slicing) that is not compatible with an io.Reader-based approach.
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
	done := cb.logFileLoad("embeddings.bin")
	data, err := os.ReadFile(filepath.Join(cb.eulixDir, "embeddings.bin"))
	done(err)
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
	if version != 3 && version != 4 {
		return fmt.Errorf("unsupported embeddings.bin version: %d", version)
	}

	// Read model name
	modelName, n, err := readStr(data, off)
	if err != nil {
		return fmt.Errorf("reading model name: %w", err)
	}
	off = n
	_ = modelName // could validate against config

	if off+8 > len(data) {
		return fmt.Errorf("invalid embeddings file: truncated header")
	}
	numEmb := int(binary.LittleEndian.Uint32(data[off : off+4]))
	dim := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
	off += 8

	// Check for quantization flag (version 4 only)
	quantized := false
	if version == 4 {
		if off >= len(data) {
			return fmt.Errorf("missing quantization flag")
		}
		quantized = data[off] == 1
		off++ // Skip quantization flag
	}

	if dim == 0 {
		return fmt.Errorf("embeddings.bin has dim=0: file is corrupt or was written by an incompatible version, please regenerate")
	}

	// Validate header
	if err := validateEmbeddingsHeader(numEmb, dim, quantized, len(data)); err != nil {
		return err
	}

	cb.embeddings = make([][]float32, numEmb)
	// ids := make([]string, numEmb)

	var embID string
	for i := 0; i < numEmb; i++ {
		// Read ID (present in version >=3)
		embID, off, err = readStr(data, off) // _ is replacement for ids
		_ = embID
		if err != nil {
			return fmt.Errorf("reading id at index %d: %w", i, err)
		}

		emb := make([]float32, dim)
		if quantized {
			// SQ8 format: 4-byte scale + dim bytes of int8
			if off+4 > len(data) {
				return fmt.Errorf("unexpected EOF reading scale at index %d", i)
			}
			scale := math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4]))
			off += 4

			if off+dim > len(data) {
				return fmt.Errorf("unexpected EOF reading quantized vector at index %d (need %d bytes, have %d)", i, dim, len(data)-off)
			}

			// Dequantize int8 -> float32
			for j := 0; j < dim; j++ {
				emb[j] = float32(int8(data[off+j])) * scale
			}
			off += dim
			cb.embeddings[i] = emb
		} else {
			// Float32 format: dim * 4 bytes
			if off+dim*4 > len(data) {
				return fmt.Errorf("unexpected EOF reading vector at index %d", i)
			}
			for j := 0; j < dim; j++ {
				emb[j] = math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4]))
				off += 4
			}
			cb.embeddings[i] = emb
		}
	}

	// Build IVF index if needed
	if numEmb > ivfBuildThreshold {
		go func() {
			idx := buildIVFIndex(cb.embeddings, ivfNClusters, ivfKMeansIter)
			cb.mu.Lock()
			cb.ivfIndex = idx
			cb.mu.Unlock()
			cb.debugLog.Log("IVF index built: %d clusters", ivfNClusters)
		}()
		cb.debugLog.Log("IVF build started in background (%d embeddings)", numEmb)
	}
	return nil
}

func validateEmbeddingsHeader(numEmb, dim int, quantized bool, fileLen int) error {
	// Calculate min entry size
	var minEntryBytes int
	if quantized {
		// SQ8: 4B id-len + id bytes + 4B scale + dim bytes
		minEntryBytes = 4 + 1 + 4 + dim // minimum 1-char ID
	} else {
		// Float32: 4B id-len + id bytes + dim*4 bytes
		minEntryBytes = 4 + 1 + dim*4 // minimum 1-char ID
	}
	maxPossible := fileLen / minEntryBytes
	if numEmb > maxPossible+1 { // +1 for rounding
		format := "float32"
		if quantized {
			format = "SQ8 int8"
		}
		return fmt.Errorf(
			"embeddings.bin: numEmb=%d is impossible given file size %d bytes, dim=%d, format=%s (max possible ~%d); file is likely corrupt — regenerate with eulix-embed",
			numEmb, fileLen, dim, format, maxPossible,
		)
	}
	if dim > 8192 {
		return fmt.Errorf("embeddings.bin: dim=%d exceeds sanity limit 8192", dim)
	}
	if numEmb > 10_000_000 {
		return fmt.Errorf("embeddings.bin: numEmb=%d exceeds sanity limit 10M", numEmb)
	}
	return nil
}

// loadVectorMap reads vectors.bin to build the id→embedding-index map.
// Maps embedding IDs (from kb_index.json or manually assigned) to their
// positions in the embeddings slice for fast lookup during scoring.
//
// Like loadEmbeddings, this loader uses os.ReadFile because the binary format
// requires direct []byte index arithmetic; streaming is not applicable here.
//
// Format: [4B magic][4B version][4B+str model_name][4B count][4B dim]
// then for each entry: [4B+str id][dim*4B float32 vector (skipped)]
//
// See:
//   - context_search.go: vectorSearch uses vectorMap to locate embeddings by chunk ID
//   - context_kb.go: chunk ID assignment during KB loading
func (cb *ContextBuilder) loadVectorMap() error {
	done := cb.logFileLoad("vectors.bin")
	data, err := os.ReadFile(filepath.Join(cb.eulixDir, "vectors.bin"))
	done(err)
	if err != nil {
		return fmt.Errorf("vectors.bin not found: %w", err)
	}

	const minHeader = 4 + 4 + 4 + 1 + 4 // magic+version+model_len_prefix+1char+count
	if len(data) < minHeader {
		return fmt.Errorf("vectors.bin too short: %d bytes", len(data))
	}

	off := 0

	// Magic
	if string(data[off:off+4]) != MagicBytes {
		return fmt.Errorf("wrong magic in vectors.bin: %q", data[off:off+4])
	}
	off += 4

	// Version
	version := binary.LittleEndian.Uint32(data[off : off+4])
	off += 4
	cb.debugLog.Log("vectors.bin: version=%d (expected=%d)", version, BinaryVersion)
	if version != BinaryVersion {
		return fmt.Errorf("vectors.bin version mismatch: expected %d, got %d", BinaryVersion, version)
	}

	// Model name — validate the length prefix before reading
	if off+4 > len(data) {
		return fmt.Errorf("vectors.bin: truncated before model name length")
	}
	modelNameLen := int(binary.LittleEndian.Uint32(data[off : off+4]))
	cb.debugLog.Log("vectors.bin: model name length=%d", modelNameLen)
	if modelNameLen > 512 {
		return fmt.Errorf("vectors.bin: model name length=%d is implausible, file may be corrupt (off=%d)", modelNameLen, off)
	}
	off += 4
	if off+modelNameLen > len(data) {
		return fmt.Errorf("vectors.bin: truncated reading model name (need %d bytes at off=%d, have %d)", modelNameLen, off, len(data))
	}
	modelName := string(data[off : off+modelNameLen])
	off += modelNameLen
	cb.debugLog.Log("vectors.bin: model=%q", modelName)

	// Count
	if off+4 > len(data) {
		return fmt.Errorf("vectors.bin: truncated before count field")
	}
	count := int(binary.LittleEndian.Uint32(data[off : off+4]))
	off += 4
	cb.debugLog.Log("vectors.bin: count=%d, bytes remaining=%d", count, len(data)-off)

	// Sanity: each entry is at minimum 4 (len prefix) + 1 (id char) = 5 bytes
	if count > (len(data)-off)/5 {
		return fmt.Errorf("vectors.bin: count=%d impossible given %d bytes remaining, file is corrupt", count, len(data)-off)
	}
	if count > 10_000_000 {
		return fmt.Errorf("vectors.bin: count=%d exceeds sanity limit", count)
	}

	cb.vectorMap = make(map[string]int, count)
	for i := 0; i < count; i++ {
		if off+4 > len(data) {
			return fmt.Errorf("vectors.bin: truncated reading id length at index %d (off=%d)", i, off)
		}
		idLen := int(binary.LittleEndian.Uint32(data[off : off+4]))
		off += 4
		if idLen == 0 || idLen > 1024 {
			return fmt.Errorf("vectors.bin: implausible id length=%d at index %d (off=%d)", idLen, i, off)
		}
		if off+idLen > len(data) {
			return fmt.Errorf("vectors.bin: truncated reading id at index %d (need %d bytes, have %d)", i, idLen, len(data)-off)
		}
		id := string(data[off : off+idLen])
		off += idLen
		cb.vectorMap[id] = i
	}

	return nil
}

// loadCallGraph loads kb_call_graph.json to build the inter-procedural call
// graph. Maps each function to its callers and callees, enabling graph-based
// expansion during context assembly. Gracefully skips if the file is absent or
// malformed.
//
// Note: a package-level loadCallGraph(eulixDir string) function also exists for
// the routing path; it returns the raw *CallGraph type rather than the
// map[string][]Relationship representation built here. In Go a method and a
// package-level function may share a name without conflict; both are intentional.
func (cb *ContextBuilder) loadAndIndexCallGraph() {
	done := cb.logFileLoad("kb_call_graph.json")
	var cg types.CallGraphRef
	err := decodeJSONFile(filepath.Join(cb.eulixDir, "kb_call_graph.json"), &cg)
	done(err)
	if err != nil {
		cb.hasCallGraph = false
		cb.debugLog.Log("Call graph not loaded: %v", err)
		return
	}

	// log a sample to verify format alignment
	if len(cg.Edges) > 0 {
		cb.debugLog.Log("Call graph: %d nodes, %d edges", len(cg.Nodes), len(cg.Edges))
		cb.debugLog.Log("Edge sample: from=%q to=%q type=%q",
			cg.Edges[0].From, cg.Edges[0].To, cg.Edges[0].EdgeType)
	}
	if len(cb.chunks) > 0 {
		cb.debugLog.Log("Chunk sample: id=%q name=%q class=%q symbols=%v",
			cb.chunks[0].ID, cb.chunks[0].Name, cb.chunks[0].ClassName, cb.chunks[0].Symbols)
	}

	// Build cb.callGraph (used by call graph expansion during retrieval)
	cb.callGraph = make(map[string][]Relationship, len(cg.Nodes))
	for _, e := range cg.Edges {
		if e.EdgeType != "call" {
			continue
		}
		cb.callGraph[e.From] = append(cb.callGraph[e.From],
			Relationship{Type: "calls", Target: e.To, Distance: 1})
		cb.callGraph[e.To] = append(cb.callGraph[e.To],
			Relationship{Type: "called_by", Target: e.From, Distance: 1})
	}

	// Build cb.callSites (used by call-site expansion during retrieval)
	cb.callSites = buildCallSiteIndex(&cg, cb.chunks)
	cb.hasCallGraph = len(cb.callGraph) > 0
	cb.debugLog.Log("Call graph indexed: %d relationships, %d call sites",
		len(cb.callGraph), len(cb.callSites))
}
