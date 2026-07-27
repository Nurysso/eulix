// Copyright (C) 2026 Dawood Khan
// SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package query provides the context window builder and query routing for Eulix's
RAG (Retrieval-Augmented Generation) system.

This file orchestrates knowledge base artifact loading and indexing:
  - kb.json: full codebase structure (functions, classes, methods with signatures,
    line ranges, complexity, call graphs)
  - kb_index.json: lightweight symbol index (function/type names and locations)
  - kb_call_graph.json: inter-procedural call relationships
  - embeddings.bin: vector embeddings for all chunks (binary v3/v4)
  - vectors.bin: embedding→chunk ID map

Memory strategy (2-4 GB kb.json based on 16 GB Linux / Windows 11):

	The previous design materialised kb.json as a full
	types.KnowledgeBaseRef struct in loadKnowledgeBase, then loaded it
	AGAIN in loadChunksFromKB to build chunks. Peak memory was 2-3× the
	file size: parsed struct + duplicate parsed struct + chunk slice.
	On a 4 GB file this OOM'd on 16 GB.

	The new design streams kb.json via mmap + json.NewDecoder. The
	full struct is never materialised, chunks are built inline as
	each FileData is decoded and the FileData goes out of scope at
	the end of each iteration. Peak memory is just the chunk slice
	+ derived indices, which fits in 16 GB even with a 4 GB source. (hopefully)

	Binary files (embeddings.bin, vectors.bin) still use os.ReadFile
	because their parsers require direct []byte index arithmetic.
	TODO: Switch to mmap if your embedding files grow past ~2 GB.

	json.NewDecoder is used (not sonic) for the outer token walk
	because sonic's streaming decoder doesn't expose Token()/More().
	The inner FileData is decoded with sonicCopy.Unmarshal a hybrid
	that combines the streaming flexibility of encoding/json with
	the JIT field-access speed of sonic.

Boilerplate filtering is applied in three places:
 1. Symbol index construction (cb.buildDerivedIndices): skip
    boilerplate symbols when populating cb.symbolIndex.
 2. Inverted index construction (cb.buildInvertedIndex): tokens
    are checked via cb.isBoilerplateSymbol before insertion.
 3. Caller code: simBetween and any custom scorers should call
    cb.isBoilerplateSymbol.

File format notes:
  - embeddings.bin / vectors.bin: [4B magic][4B version][4B+str model] [4B count][4B dim]
    then per-entry: [4B+str id][dim*4B float32 vec]
  - kb_call_graph.json: flat map of function ID → {Calls, CalledBy}

Standalone routing helpers (loadKBIndex, loadCallGraph) are
provided for the query-classification path, which operates outside
the ContextBuilder lifecycle. They share the same mmap-backed
decode logic as the streaming loader.
*/
package query

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"corvux/internal/types"
)

const (
	ivfBuildThreshold = 50_000
	invIdxThreshold   = 5_000
	lazyContentLimit  = 50_000
	ivfNClusters      = 256
	ivfKMeansIter     = 20

	// dfThresholdDefault: fraction of chunks a symbol must appear in
	// to be suppressed as boilerplate. 0.30 is the recommended
	// starting point, raising it if legitimate short identifiers are
	// being filtered, lower if too many common helpers leak through.
	dfThresholdDefault = 0.30

	// bpMinChunks: minimum corpus size before statistics are trusted.
	bpMinChunks = 50
	PreAllocate = 320_000
)

// logFileLoad wraps a file load with timing + RSS delta logging.
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

// Package-level routing loaders.
//
// Used by the query-classification path, which does not have access
// to a ContextBuilder. Both are thin wrappers over decodeJSONFile, so
// they benefit from the same mmap-backed streaming-decode behaviour
// as the ContextBuilder loaders below.

func loadKBIndex(eulixDir string) (*types.KBIndices, error) {
	var idx types.KBIndices
	if err := decodeJSONFile(filepath.Join(eulixDir, "kb_index.json"), &idx); err != nil {
		return nil, fmt.Errorf("kb_index.json: %w", err)
	}
	return &idx, nil
}

// loadCallGraph returns the routing-layer *CallGraph. Note that a
// ContextBuilder method of the same name (cb).loadAndIndexCallGraph
// also exists; it reads the same file but produces the richer
// internal representation (map[string][]Relationship). In Go a
// package-level function and a method may share a name without
// conflict; both are intentional.
func loadCallGraph(eulixDir string) (*CallGraph, error) {
	var g CallGraph
	if err := decodeJSONFile(filepath.Join(eulixDir, "kb_call_graph.json"), &g); err != nil {
		return nil, fmt.Errorf("kb_call_graph.json: %w", err)
	}
	return &g, nil
}

// loadExternalDeps reads and parses kb_external_deps.json
func (cb *ContextBuilder) loadExternalDeps() error {
	done := cb.logFileLoad("kb_external_deps.json")

	var FileData types.ExternalDependencyRef
	err := decodeJSONFile(filepath.Join(cb.eulixDir, "kb_external_deps.json"), &FileData)

	done(err)
	if err != nil {
		return fmt.Errorf("kb_external_deps: %w", err)
	}
	cb.externalDeps = FileData.ExternalDependency
	cb.depIdx = buildDepIndex(cb.externalDeps)
	return nil
}

// loadChunks is the primary loader: streams kb_index.json and
// kb.json, builds the chunk slice, and populates all derived
// indices (boilerplate detector, symbol index, inverted index).
//
// Replaces the previous loadKnowledgeBase + loadChunksFromKB pair,
// which double-loaded kb.json and materialised a 4-6 GB
// KnowledgeBaseRef struct on top of the chunk slice. Peak memory
// in the old design was 8-12 GB for a 4 GB source OOM on 16 GB.
// The new design peaks at ~6-8 GB (chunks + derived indices only).
//
// Phase breakdown:
//
//  1. kb_index.json (small, 10-100 MB): mmap + Unmarshal. No
//     streaming needed.
//  2. kb.json (2-4 GB): mmap + json.NewDecoder streaming decode.
//     Each FileData is decoded inline with sonic and discarded
//     after chunks are built. The full KnowledgeBaseRef struct
//     is never materialised.
//  3. Derived indices: built from cb.chunks in a single pass.
//  4. State + free intermediate memory: cb.kbIdx is kept (used by
//     the routing path), kb.json's bytes are released by the
//     mmap cleanup, and a runtime.GC() reclaims any decoder
//     buffer that the streaming pass left behind.
//
// Note: cb.lazyContent is forced to false in this path. The old
// design populated a hydrateIdx map with closures that rebuilt
// Content on demand from the source struct; the streaming path
// doesn't keep the source, so lazy content isn't possible. Chunks
// always carry their Content inline, which is the right tradeoff
// for search use cases.
func (cb *ContextBuilder) loadChunks() error {
	done := cb.logFileLoad("kb_index.json")
	var ref types.IndexRef
	err := decodeJSONFile(filepath.Join(cb.eulixDir, "kb_index.json"), &ref)
	done(err)
	if err != nil {
		return fmt.Errorf("kb_index.json: %w", err)
	}
	cb.kbIdx = &ref.Indices
	cb.debugLog.Log("kbIdx FunctionsByName len=%d", len(cb.kbIdx.FunctionsByName))

	// lazyContent must be set BEFORE streamKBChunks so addChunksFromFile
	// sees the correct value and skips closure allocation on this path.
	cb.lazyContent = false

	if err := cb.streamKBChunks(); err != nil {
		return fmt.Errorf("kb.json: %w", err)
	}

	cb.buildDerivedIndices()

	cb.kbData = nil

	// streaming path never populates hydrateIdx, release the map
	// so the GC can collect it rather than leaving an empty shell alive.
	cb.hydrateIdx = nil

	cb.hasKB = true
	runtime.GC()
	cb.debugLog.Log("Chunks loaded: %d, heap: %d MB",
		len(cb.chunks), getHeapAlloc()/1024/1024)
	return nil
}

func (cb *ContextBuilder) GetDepIndex() *depIndex {
	return cb.depIdx
}

// streamKBChunks opens kb.json with mmap + sequential-read hints
// and walks the JSON token-by-token, building chunks as each
// FileData is decoded. The full types.KnowledgeBaseRef struct is
// never materialised FileData is decoded, passed to
// addChunksFromFile, and goes out of scope at the end of each
// iteration. The mmap cleanup (defer cleanup) releases the
// underlying pages as soon as this function returns.
//
// Why a hybrid (encoding/json + sonic)?
//
//   - json.NewDecoder is used for the outer token walk because
//     sonic's streaming decoder doesn't expose Token()/More().
//   - The inner FileData is decoded with sonicCopy.Unmarshal for
//     JIT-speed field access (3-5× faster than encoding/json on
//     large structs).
//
// Why mmap + json.NewDecoder (and not os.ReadFile + Unmarshal)?
//
//   - For a 4 GB file, os.ReadFile allocates 4 GB of heap for the
//     raw bytes. mmap keeps the raw bytes in the page cache, so
//     the only heap cost is the parsed chunks.
//   - json.NewDecoder reads from the mmap'd io.Reader one FileData
//     at a time, so the FileData structs are GC'd as we go.
func (cb *ContextBuilder) streamKBChunks() error {
	path := filepath.Join(cb.eulixDir, "kb.json")

	r, cleanup, err := openForSequentialRead(path)
	if err != nil {
		return err
	}
	defer cleanup()

	// encoding/json required here: sonic's decoder doesn't
	// implement Token()/More() streaming. using stdlib for the
	// token walk and sonic only for the inner FileData decode.
	dec := json.NewDecoder(r)

	if err := jsonSkipToKey(dec, "structure"); err != nil {
		return fmt.Errorf("finding 'structure' key: %w", err)
	}
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("reading structure opening brace: %w", err)
	}

	// Pre-allocate. a reasonable upper bound for RAG
	// based on corpora. If corpora are larger, raise this value
	// to avoid slice growth copies. addChunksFromFile may further
	// grow the slice if the source has more chunks than expected.
	cb.chunks = make([]Chunk, 0, PreAllocate)

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

		// Decode the FileData with sonic for JIT-speed field
		// access. The raw byte slice is a sub-slice of the
		// decoder's internal buffer; sonicCopy copies all
		// decoded strings out before we move to the next
		// iteration, so the source can be safely reused.
		var fs types.FileData
		if err := sonicCopy.Unmarshal(raw, &fs); err != nil {
			return fmt.Errorf("decoding FileData for %s: %w ", filePath, err)
		}
		cb.addChunksFromFile(filePath, &fs)
		fs = types.FileData{}
	}

	return nil
}

// buildDerivedIndices populates the boilerplate detector, symbol
// index, and inverted index from cb.chunks in a single pass.
//
// Called once after streamKBChunks has finished. Boilerplate runs
// first so the symbol index can skip the high-frequency symbols
// during its pass; the inverted index reuses the same predicate
// during tokenisation.
func (cb *ContextBuilder) buildDerivedIndices() {
	// Boilerplate detector (over the full corpus)
	cb.buildBoilerplate()
	cb.debugLog.Log("Boilerplate detector: %d symbols filtered (threshold=%.2f, corpus=%d) top: %v",
		len(cb.boilerplate.boilerplate),
		dfThresholdDefault,
		len(cb.chunks),
		cb.boilerplate.TopBoilerplate(5),
	)

	// Build the subsystem tree first so detectNoisePatterns has nodes to
	// inspect. Both are read-only after this point.
	cb.buildSubsystemTree()
	cb.detectNoisePatterns()

	cb.symbolIndex = make(map[string][]int, len(cb.chunks)*2)
	for i, c := range cb.chunks {
		for _, sym := range c.Symbols {
			if cb.isBoilerplateSymbol(sym) {
				continue
			}
			cb.symbolIndex[sym] = append(cb.symbolIndex[sym], i)
		}
	}
	// Inverted index (large corpora only)
	if len(cb.chunks) > invIdxThreshold {
		cb.invertedIdx = cb.buildInvertedIndex()
	}
}

// buildInvertedIndex tokenises each chunk's Content and builds a
// map from normalised token to the list of chunk indices that
// contain it. Boilerplate tokens are filtered out via the
// detector built in buildBoilerplate.
//
// Operates on cb.chunks directly so it works with the streaming
// loader (which doesn't keep the full KnowledgeBaseRef).
//
// Tokenisation: strings.FieldsFunc splits on any Unicode
// whitespace or punctuation boundary. For natural-language chunks
// this is a reasonable default; for code chunks you may want a
// language-aware tokenizer (CamelCase split, snake_case split,
// etc.), drop in a custom splitter via the chunkIndexer hook
// if your retrieval quality depends on it.
//
// Performance: O(total tokens) with one allocation per chunk
// for the token slice. The inverted map is pre-sized to
// len(chunks)*8 (heuristic; tune if your token/cardinality
// ratio differs).
func (cb *ContextBuilder) buildInvertedIndex() *InvertedIndex {
	postings := make(map[string][]Posting, len(cb.chunks)*8)
	totalTokens, nonEmpty := 0, 0

	for i, c := range cb.chunks {
		if c.Content == "" {
			continue
		}
		counts := make(map[string]int)
		for _, tok := range strings.FieldsFunc(c.Content, isIdentBoundary) {
			norm := normalizeSymbol(tok)
			if norm == "" || cb.isBoilerplateSymbol(norm) {
				continue
			}
			counts[norm]++
		}
		if len(counts) == 0 {
			continue
		}
		nonEmpty++
		totalTokens += len(counts)
		for tok, cnt := range counts {
			tf := float32(cnt) / float32(len(counts))
			postings[tok] = append(postings[tok], Posting{ChunkIdx: i, TF: tf})
		}
		// counts goes out of scope here and is immediately GC-eligible
	}

	avgTokens := 0.0
	if nonEmpty > 0 {
		avgTokens = float64(totalTokens) / float64(nonEmpty)
	}
	cb.debugLog.Log("Inverted index: %d unique tokens, %d chunks, avg %.1f tokens/chunk",
		len(postings), len(cb.chunks), avgTokens)
	return &InvertedIndex{
		Postings:       postings,
		DocCount:       len(cb.chunks),
		AvgChunkTokens: avgTokens,
	}
}

// isIdentBoundary returns true if r is a non-identifier rune
// (anything outside [A-Za-z0-9_]). Used as the splitter predicate
// in buildInvertedIndex.
func isIdentBoundary(r rune) bool {
	return !((r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_')
}

// loadAndIndexCallGraph loads kb_call_graph.json and builds the
// inter-procedural call graph used by call graph expansion during
// retrieval. Gracefully skips if the file is absent or malformed.
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

	cb.cgRef = &cg

	if len(cg.Edges) > 0 {
		cb.debugLog.Log("Call graphs: %d nodes, %d edges", len(cg.Nodes), len(cg.Edges))
		cb.debugLog.Log("Edge samples: from=%q to=%q type=%q",
			cg.Edges[0].From, cg.Edges[0].To, cg.Edges[0].EdgeType)
	}
	if len(cb.chunks) > 0 {
		cb.debugLog.Log("Chunk sample: id=%q name=%q class=%q symbols=%v",
			cb.chunks[0].ID, cb.chunks[0].Name, cb.chunks[0].ClassName, cb.chunks[0].Symbols)
	}

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

	cb.callSites = buildCallSiteIndex(&cg, cb.chunks)
	cb.hasCallGraph = len(cb.callGraph) > 0
	cb.debugLog.Log("Call graph indexed: %d relationships, %d call sites",
		len(cb.callGraph), len(cb.callSites))
}

// loadEmbeddings reads embeddings.bin (EULX magic, version 2/3/4).
// Supports legacy (v3, with IDs), and current quantized (v4, SQ8 int8) formats.
//
// Uses os.ReadFile rather than mmap because the binary parser
// requires direct []byte index arithmetic (off+4, off+dim*4
// slicing) that isn't compatible with an io.Reader-based approach.
// For typical RAG corpora (100K-1M embeddings, dim 384-1536) the
// file is 200 MB - 6 GB. If your embeddings grow past ~6 GB, switch
// to mmap by replacing the os.ReadFile with
// openForSequentialRead and indexing into a bytes.Reader.
func (cb *ContextBuilder) loadEmbeddings() error {
	done := cb.logFileLoad("embeddings.bin")
	path := filepath.Join(cb.eulixDir, "embeddings.bin")
	f, err := os.Open(path)
	done(err)
	if err != nil {
		return fmt.Errorf("embeddings.bin not found: %w", err)
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 4<<20) // 4MB read buffer

	// read magic
	magic := make([]byte, 4)
	if _, err := io.ReadFull(br, magic); err != nil {
		return fmt.Errorf("reading magic: %w", err)
	}
	if string(magic) != MagicBytes {
		return fmt.Errorf("wrong magic bytes: %q", magic)
	}

	var hdr [4]byte
	// version
	if _, err := io.ReadFull(br, hdr[:4]); err != nil {
		return err
	}
	version := binary.LittleEndian.Uint32(hdr[:4])
	if version != 3 && version != 4 {
		return fmt.Errorf("unsupported version: %d", version)
	}
	// model name (length-prefixed)
	if _, err := io.ReadFull(br, hdr[:4]); err != nil {
		return err
	}
	modelLen := int(binary.LittleEndian.Uint32(hdr[:4]))
	modelBuf := make([]byte, modelLen)
	if _, err := io.ReadFull(br, modelBuf); err != nil {
		return err
	}
	// numEmb, dim
	var meta [8]byte
	if _, err := io.ReadFull(br, meta[:]); err != nil {
		return err
	}
	numEmb := int(binary.LittleEndian.Uint32(meta[:4]))
	dim := int(binary.LittleEndian.Uint32(meta[4:8]))
	quantized := false
	if version == 4 {
		var flag [1]byte
		if _, err := io.ReadFull(br, flag[:]); err != nil {
			return err
		}
		quantized = flag[0] == 1
	}
	if dim == 0 {
		return fmt.Errorf("embeddings.bin dim=0: regenerate with eulix-embed")
	}

	cb.embeddings = make([][]float32, numEmb)
	idLenBuf := make([]byte, 4)
	scaleBuf := make([]byte, 4)
	int8Buf := make([]byte, dim)  // reused per vector
	f32Buf := make([]byte, dim*4) // reused per vector

	for i := 0; i < numEmb; i++ {
		// id length + id bytes (skip id, we use positional index)
		if _, err := io.ReadFull(br, idLenBuf); err != nil {
			return fmt.Errorf("reading id length at %d: %w", i, err)
		}
		idLen := int(binary.LittleEndian.Uint32(idLenBuf))
		if idLen > 1024 {
			return fmt.Errorf("implausible id length %d at %d", idLen, i)
		}
		if _, err := io.ReadFull(br, make([]byte, idLen)); err != nil { // discard id
			return fmt.Errorf("reading id at %d: %w", i, err)
		}

		emb := make([]float32, dim)
		if quantized {
			if _, err := io.ReadFull(br, scaleBuf); err != nil {
				return fmt.Errorf("reading scale at %d: %w", i, err)
			}
			scale := math.Float32frombits(binary.LittleEndian.Uint32(scaleBuf))
			if _, err := io.ReadFull(br, int8Buf); err != nil {
				return fmt.Errorf("reading quantized vector at %d: %w", i, err)
			}
			for j := 0; j < dim; j++ {
				emb[j] = float32(int8(int8Buf[j])) * scale
			}
		} else {
			if _, err := io.ReadFull(br, f32Buf); err != nil {
				return fmt.Errorf("reading vector at %d: %w", i, err)
			}
			for j := 0; j < dim; j++ {
				emb[j] = math.Float32frombits(binary.LittleEndian.Uint32(f32Buf[j*4:]))
			}
		}
		cb.embeddings[i] = emb
	}

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

// validateEmbeddingsHeader checks that the declared numEmb is
// plausible given the file size and embedding format. Catches
// corrupt or truncated files before we enter the parse loop.
func validateEmbeddingsHeader(numEmb, dim int, quantized bool, fileLen int) error {
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

// loadVectorMap reads vectors.bin to build the id→embedding-index
// map. Maps embedding IDs (from kb_index.json or manually assigned)
// to their positions in the embeddings slice for fast lookup during
// scoring.
//
// Like loadEmbeddings, this loader uses os.ReadFile because the
// binary format requires direct []byte index arithmetic; streaming
// is not applicable here. See loadEmbeddings for the os.ReadFile
// vs mmap tradeoff.
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

	if string(data[off:off+4]) != MagicBytes {
		return fmt.Errorf("wrong magic in vectors.bin: %q", data[off:off+4])
	}
	off += 4

	version := binary.LittleEndian.Uint32(data[off : off+4])
	off += 4
	cb.debugLog.Log("vectors.bin: version=%d (expected=%d)", version, BinaryVersion)
	if version != BinaryVersion {
		return fmt.Errorf("vectors.bin version mismatch: expected %d, got %d", BinaryVersion, version)
	}

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
		return fmt.Errorf("vectors.bin: truncated reading model name (need %d bytes at off=%d, have %d)", modelNameLen, off, len(data)-off)
	}
	modelName := string(data[off : off+modelNameLen])
	off += modelNameLen
	cb.debugLog.Log("vectors.bin: model=%q", modelName)

	if off+4 > len(data) {
		return fmt.Errorf("vectors.bin: truncated before count field")
	}
	count := int(binary.LittleEndian.Uint32(data[off : off+4]))
	off += 4
	cb.debugLog.Log("vectors.bin: count=%d, bytes remaining=%d", count, len(data)-off)

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
