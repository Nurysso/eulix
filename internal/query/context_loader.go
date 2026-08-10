// Copyright (C) 2026 Dawood Khan
// SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package query provides context window building and query routing for Eulix's RAG system.

Key Responsibilities:
  - Memory-optimized streaming loading of knowledge base artifacts (kb.json, call graphs)
  - Binary decoding of vector embeddings and mapping tables
  - In-memory index generation (symbol maps, inverted indexes, boilerplate filters)
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

	"eulix/internal/types"
)

const (
	ivfBuildThreshold  = 50_000
	invIdxThreshold    = 5_000
	lazyContentLimit   = 50_000
	ivfNClusters       = 256
	ivfKMeansIter      = 5
	dfThresholdDefault = 0.30
	bpMinChunks        = 50
	PreAllocate        = 320_000
)

// logFileLoad wraps a file load with timing + RSS delta logging.
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
func (cb *ContextBuilder) buildInvertedIndex() *InvertedIndex {
	postings := make(map[string][]Posting, len(cb.chunks)*8)
	totalTokens, nonEmpty := 0, 0

	for i, c := range cb.chunks {
		if c.Content == "" {
			continue
		}
		counts := make(map[string]int)

		pathStr := c.ID
		for _, tok := range strings.FieldsFunc(pathStr, isIdentBoundary) {
			norm := normalizeSymbol(tok)
			if norm != "" && !cb.isBoilerplateSymbol(norm) {
				// Give path tokens a slight artificial frequency boost
				counts[norm] += 2
			}
		}

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

// isIdentBoundary returns true if r is a boundary character.
// By returning true for '_', we automatically split snake_case!
func isIdentBoundary(r rune) bool {
	// If it's a lowercase letter, uppercase letter, or digit, it's NOT a boundary.
	if (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') {
		return false
	}
	// Everything else (spaces, punctuation, and underscores) is a boundary.
	return true
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

	cb.callSites = buildCallSiteIndex(&cg, cb.chunks, cb.debugLog)
	cb.hasCallGraph = len(cb.callGraph) > 0
	cb.debugLog.Log("Call graph indexed: %d relationships, %d call sites",
		len(cb.callGraph), len(cb.callSites))
}

// loadEmbeddings reads embeddings.bin (EULX magic, version 2/3/4).
// Supports legacy (v3, with IDs), and current quantized (v4, SQ8 int8) formats.
func (cb *ContextBuilder) loadEmbeddings() error {
	done := cb.logFileLoad("embeddings.bin")
	path := filepath.Join(cb.eulixDir, "embeddings.bin")
	f, err := os.Open(path)
	done(err)
	if err != nil {
		return fmt.Errorf("embeddings.bin not found: %w", err)
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReaderSize(f, 4<<20) // 4MB read buffer

	magic := make([]byte, 4)
	if _, err := io.ReadFull(br, magic); err != nil {
		return fmt.Errorf("reading magic: %w", err)
	}
	if string(magic) != MagicBytes {
		return fmt.Errorf("wrong magic bytes: %q", magic)
	}

	var hdr [4]byte
	if _, err := io.ReadFull(br, hdr[:4]); err != nil {
		return err
	}
	version := binary.LittleEndian.Uint32(hdr[:4])
	if version != 3 && version != 4 {
		return fmt.Errorf("unsupported version: %d", version)
	}
	if _, err := io.ReadFull(br, hdr[:4]); err != nil {
		return err
	}
	modelLen := int(binary.LittleEndian.Uint32(hdr[:4]))
	modelBuf := make([]byte, modelLen)
	if _, err := io.ReadFull(br, modelBuf); err != nil {
		return err
	}
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
	int8Buf := make([]byte, dim)
	f32Buf := make([]byte, dim*4)

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
			idx := cb.buildIVFIndex(cb.embeddings, ivfNClusters, ivfKMeansIter)
			cb.mu.Lock()
			cb.ivfIndex = idx
			cb.mu.Unlock()
			cb.debugLog.Log("IVF index BUILT completed: %d clusters", ivfNClusters)
		}()
		cb.debugLog.Log("IVF build started in background (%d embeddings)", numEmb)
	}
	return nil
}

// loadVectorMap reads vectors.bin to build the id→embedding-index
// map. Maps embedding IDs (from kb_index.json or manually assigned)
// to their positions in the embeddings slice for fast lookup during
// scoring.
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
