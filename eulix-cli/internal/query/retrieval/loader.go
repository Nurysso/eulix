// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

/*
Package retrieval provides context window Creation for Eulix's RAG system.

Key Responsibilities:
  - Memory-optimized streaming loading of knowledge base artifacts (kb.json, call graphs)
  - Binary decoding of vector embeddings and mapping tables
  - In-memory index generation (symbol maps, inverted indexes, boilerplate filters)
*/
package retrieval

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"eulix/internal/checksum"
	"eulix/internal/kbfile"
	"eulix/internal/query/retrieval/mmap"
	"eulix/internal/utils"
)

const (
	ivfBuildThreshold  = 50_000
	invIdxThreshold    = 5_000
	lazyContentLimit   = 50_000
	ivfNClusters       = 256
	ivfKMeansIter      = 5
	dfThresholdDefault = 0.30

	// maxChunkPrealloc caps the chunk slice reserved up front (~46 MB at
	// 320k chunks), so a corrupt metadata block can't reserve gigabytes.
	// Larger corpora still load: append grows the slice past it.
	maxChunkPrealloc = 320_000
	// minChunkPrealloc is used when kb.fb has no usable metadata totals.
	minChunkPrealloc = 1_024
)

// chunkCapacity sizes cb.chunks from kb.fb's own totals. Each function,
// class and class method becomes one chunk (addChunksFromFile), so their sum
// is an upper bound; files the servable filter drops only make it looser.
// The old fixed 320k reservation cost ~46 MB per builder however small the
// repository was.
func chunkCapacity(meta *utils.Metadata) int {
	if meta == nil {
		return minChunkPrealloc
	}
	n := meta.TotalFunctions + meta.TotalClasses + meta.TotalMethods
	switch {
	case n <= 0:
		return minChunkPrealloc
	case n > maxChunkPrealloc:
		return maxChunkPrealloc
	}
	return n
}

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
			cb.debugLog.Log(
				"[LOAD] %-30s done     elapsed=%-10v heap_delta=+%d MB total_heap=%d MB",
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

// loadChunks is the primary loader: reads kb_index.fb, streams
// kb.fb, builds the chunk slice, and populates all derived
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
	done := cb.logFileLoad(kbfile.IndexFile)
	idx, err := kbfile.ReadIndex(filepath.Join(cb.eulixDir, kbfile.IndexFile))
	done(err)
	if err != nil {
		return fmt.Errorf("%s: %w", kbfile.IndexFile, err)
	}

	cb.kbIdx = idx
	cb.debugLog.Log("kbIdx FunctionsByName len=%d", len(cb.kbIdx.FunctionsByName))

	// lazyContent must be set BEFORE streamKBChunks so addChunksFromFile
	// sees the correct value and skips closure allocation on this path.
	cb.lazyContent = false

	if err := cb.streamKBChunks(); err != nil {
		return fmt.Errorf("%s: %w", kbfile.KBFile, err)
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

// streamKBChunks walks kb.fb front to back (kbfile.WalkKB), building
// chunks as each file's record is decoded. The whole knowledge base is never
// materialised: one record is decoded, passed to addChunksFromFile, and goes
// out of scope. The file is read through a buffered reader, not mapped:
// every mapped page the walk passed would stay resident until unmap (on a
// 178 MB kb.json the mapped walk peaked at 212 MB and the buffered one at 31 MB;
// see mmap.OpenForSequentialRead).
func (cb *ContextBuilder) streamKBChunks() error {
	path := filepath.Join(cb.eulixDir, kbfile.KBFile)
	servable := checksum.ServableMatcher(cb.sourceRoot)
	return kbfile.WalkKB(path,
		func(meta *utils.Metadata) {
			// The header's totals size the chunk slice.
			cb.chunks = make([]Chunk, 0, chunkCapacity(meta))
			cb.debugLog.Log("[LOAD] chunk slice preallocated for %d chunks", cap(cb.chunks))
		},
		func(filePath string, fd *utils.FileData) error {
			if !servable(filePath) {
				cb.debugLog.Log("[LOAD] skipping %s: hidden, .euignore'd or outside the source root", filePath)
				return nil
			}
			cb.addChunksFromFile(filePath, fd)
			return nil
		})
}

// buildDerivedIndices populates the boilerplate detector, symbol
// index, chunk file set, and inverted index from cb.chunks in a single pass.
func (cb *ContextBuilder) buildDerivedIndices() {
	// EHNAS uses slice indices for graph neighbours and seed tie-breaks.
	// Canonicalize before building them so artifact order is not a signal.
	if cb.Engine() == EngineEHNAS {
		sort.SliceStable(cb.chunks, func(i, j int) bool { return compareEhnasChunks(cb.chunks[i], cb.chunks[j]) < 0 })
	}
	// Boilerplate detector (over the full corpus)
	cb.buildBoilerplate()
	cb.debugLog.Log(
		"Boilerplate detector: %d symbols filtered (threshold=%.2f, corpus=%d) top: %v",
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
	cb.chunkFiles = make(map[string]struct{})
	for i, c := range cb.chunks {
		cb.chunkFiles[strings.ToLower(c.File)] = struct{}{}
		for _, sym := range c.Symbols {
			if cb.isBoilerplateSymbol(sym) {
				continue
			}
			cb.symbolIndex[sym] = append(cb.symbolIndex[sym], i)
		}
	}
	// Inverted index (large corpora only; EHNAS reads its threshold from config)
	minChunks := invIdxThreshold
	if cb.Engine() == EngineEHNAS {
		minChunks = cb.config.RetrievalConfig.Ehnas.InvertedIndexMinChunks
	}
	if len(cb.chunks) > minChunks {
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

	var terms []string
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
				terms = indexTerms(terms[:0], tok, norm)
				for _, t := range terms {
					counts[t] += 2
				}
			}
		}

		for _, tok := range strings.FieldsFunc(c.Content, isIdentBoundary) {
			norm := normalizeSymbol(tok)
			if norm == "" || cb.isBoilerplateSymbol(norm) {
				continue
			}
			terms = indexTerms(terms[:0], tok, norm)
			for _, t := range terms {
				counts[t]++
			}
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

// FilterServableCallGraph removes nodes in hidden or ignored files and the
// edges touching them. Edges are matched by removed node ID, so calls to
// external functions, which have no node, stay in the graph.
func FilterServableCallGraph(ref *utils.CallGraphRef, projectPath string) {
	servable := checksum.ServableMatcher(projectPath)
	removed := make(map[string]bool)
	nodes := ref.Nodes[:0]
	for _, n := range ref.Nodes {
		if servable(n.File) {
			nodes = append(nodes, n)
		} else {
			removed[n.ID] = true
		}
	}
	ref.Nodes = nodes
	edges := ref.Edges[:0]
	for _, e := range ref.Edges {
		if !removed[e.From] && !removed[e.To] {
			edges = append(edges, e)
		}
	}
	ref.Edges = edges
}

// loadAndIndexCallGraph loads kb_call_graph.fb and builds the
// inter-procedural call graph used by call graph expansion during
// retrieval. Gracefully skips if the file is absent or malformed.
func (cb *ContextBuilder) loadAndIndexCallGraph() {
	done := cb.logFileLoad(kbfile.CallGraphFile)
	ref, err := kbfile.ReadCallGraph(filepath.Join(cb.eulixDir, kbfile.CallGraphFile))
	done(err)
	if err != nil {
		cb.hasCallGraph = false
		cb.debugLog.Log("Call graph not loaded: %v", err)
		return
	}

	cg := *ref
	FilterServableCallGraph(&cg, cb.sourceRoot)
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

// loadEmbeddings reads embeddings.bin (version 5) into cb.embeddings and starts
// an async IVF index build if the embedding count exceeds ivfBuildThreshold.
func (cb *ContextBuilder) loadEmbeddings() error {
	done := cb.logFileLoad("embeddings.bin")
	path := filepath.Join(cb.eulixDir, "embeddings.bin")

	f, err := os.Open(path)
	done(err)
	if err != nil {
		return fmt.Errorf("embeddings.bin not found: %w", err)
	}
	defer func() { _ = f.Close() }()

	br := bufio.NewReaderSize(f, 4<<20)

	magic := make([]byte, 4)
	if _, err := io.ReadFull(br, magic); err != nil {
		return fmt.Errorf("reading magic: %w", err)
	}
	if string(magic) != utils.MagicBytes {
		return fmt.Errorf("wrong magic bytes: %q", magic)
	}

	var hdr [4]byte
	if _, err := io.ReadFull(br, hdr[:4]); err != nil {
		return fmt.Errorf("reading version: %w", err)
	}
	if version := binary.LittleEndian.Uint32(hdr[:4]); version != utils.BinaryVersion {
		return fmt.Errorf("unsupported embeddings.bin version %d (expected %d)", version, utils.BinaryVersion)
	}

	// model name: uint32 len + UTF-8
	if _, err := io.ReadFull(br, hdr[:4]); err != nil {
		return fmt.Errorf("reading model name length: %w", err)
	}
	modelLen := int(binary.LittleEndian.Uint32(hdr[:4]))
	if modelLen > 512 {
		return fmt.Errorf("implausible model name length %d", modelLen)
	}
	if _, err := io.ReadFull(br, make([]byte, modelLen)); err != nil {
		return fmt.Errorf("reading model name: %w", err)
	}

	// count + dimension
	var meta [8]byte
	if _, err := io.ReadFull(br, meta[:]); err != nil {
		return fmt.Errorf("reading count/dim: %w", err)
	}
	numEmb := int(binary.LittleEndian.Uint32(meta[:4]))
	dim := int(binary.LittleEndian.Uint32(meta[4:8]))
	if dim == 0 {
		return fmt.Errorf("embeddings.bin dim=0: regenerate with eulix-embed")
	}

	// quantized flag
	var flag [1]byte
	if _, err := io.ReadFull(br, flag[:]); err != nil {
		return fmt.Errorf("reading quantized flag: %w", err)
	}
	quantized := flag[0] == 1

	if numEmb == 0 {
		cb.embeddings = nil
		return nil
	}

	// allocate matrix (hugepage-aligned on Linux, flat elsewhere)
	cb.embeddings, cb.embeddingRelease = mmap.AllocEmbeddingMatrixOwned(numEmb, dim)
	loaded := false
	defer func() {
		if !loaded {
			_ = cb.embeddingRelease()
			cb.embeddingRelease = nil
			cb.embeddings = nil
		}
	}()

	if !quantized {
		cb.debugLog.Log("Non Quantize embeddings")
		// Fixed-width float32 records: read entire payload into the
		// contiguous backing buffer in one shot.
		// AllocEmbeddingMatrix lays out all rows in a single flat allocation,
		// so embeddings[0][:numEmb*dim] covers the whole thing.
		flat := cb.embeddings[0][:numEmb*dim]
		if err := binary.Read(br, binary.LittleEndian, flat); err != nil {
			return fmt.Errorf("reading float32 vectors: %w", err)
		}
		for i := 0; i < numEmb; i++ {
			Normalize(cb.embeddings[i])
		}
	} else {
		// SQ8: 4-byte scale + dim int8 bytes per embedding
		cb.debugLog.Log("Quantize embeddings")
		scaleBuf := make([]byte, 4)
		int8Buf := make([]byte, dim)
		for i := 0; i < numEmb; i++ {
			if _, err := io.ReadFull(br, scaleBuf); err != nil {
				return fmt.Errorf("reading scale at %d: %w", i, err)
			}
			scale := math.Float32frombits(binary.LittleEndian.Uint32(scaleBuf))
			if _, err := io.ReadFull(br, int8Buf); err != nil {
				return fmt.Errorf("reading quantized vector at %d: %w", i, err)
			}
			row := cb.embeddings[i]
			for j := 0; j < dim; j++ {
				row[j] = float32(int8(int8Buf[j])) * scale
			}
			Normalize(row)
		}
	}
	cb.debugLog.Log("numEmb: %d ivfBuildThreshold: %d", numEmb, ivfBuildThreshold)
	if numEmb > ivfBuildThreshold {
		cb.debugLog.Log("IVF condition met: numEmb (%d) > threshold (%d)", numEmb, ivfBuildThreshold) // <-- Log added
		cb.ivfBuild.Add(1)
		go func() {
			defer cb.ivfBuild.Done()
			idx := cb.buildIVFIndex(cb.embeddings, ivfNClusters, ivfKMeansIter)
			cb.mu.Lock()
			cb.ivfIndex = idx
			cb.mu.Unlock()
			cb.debugLog.Log("IVF index built: %d clusters", ivfNClusters)
		}()
		cb.debugLog.Log("IVF build started in background (%d embeddings)", numEmb)
	}
	loaded = true
	return nil
}

// loadVectorMap reads vectors.bin (version 5) and populates cb.vectorMap with
// chunk ID → embedding index mappings for fast lookup during scoring.
func (cb *ContextBuilder) loadVectorMap() error {
	done := cb.logFileLoad("vectors.bin")
	data, err := os.ReadFile(filepath.Join(cb.eulixDir, "vectors.bin"))
	done(err)
	if err != nil {
		return fmt.Errorf("vectors.bin not found: %w", err)
	}

	const minHeader = 4 + 4 + 4 + 1 + 4 // magic + version + model_len_prefix + 1char + count
	if len(data) < minHeader {
		return fmt.Errorf("vectors.bin too short: %d bytes", len(data))
	}

	off := 0

	// magic
	if string(data[off:off+4]) != utils.MagicBytes {
		return fmt.Errorf("wrong magic in vectors.bin: %q", data[off:off+4])
	}
	off += 4

	// version
	version := binary.LittleEndian.Uint32(data[off : off+4])
	off += 4
	if version != 5 {
		return fmt.Errorf("unsupported vectors.bin version %d (expected %d)", version, utils.BinaryVersion)
	}

	// model name
	if off+4 > len(data) {
		return fmt.Errorf("vectors.bin: truncated before model name length")
	}
	modelNameLen := int(binary.LittleEndian.Uint32(data[off : off+4]))
	cb.debugLog.Log("vectors.bin: model name length=%d", modelNameLen)
	off += 4
	if modelNameLen > 512 {
		return fmt.Errorf("vectors.bin: implausible model name length %d", modelNameLen)
	}
	if off+modelNameLen > len(data) {
		return fmt.Errorf("vectors.bin: truncated reading model name")
	}
	off += modelNameLen

	// count
	if off+4 > len(data) {
		return fmt.Errorf("vectors.bin: truncated before count")
	}
	count := int(binary.LittleEndian.Uint32(data[off : off+4]))
	off += 4

	if count > 10_000_000 {
		return fmt.Errorf("vectors.bin: count=%d exceeds sanity limit", count)
	}
	if count > (len(data)-off)/5 {
		return fmt.Errorf("vectors.bin: count=%d impossible given %d bytes remaining", count, len(data)-off)
	}

	// id entries
	cb.vectorMap = make(map[string]int, count)
	for i := 0; i < count; i++ {
		if off+4 > len(data) {
			return fmt.Errorf("vectors.bin: truncated reading id length at index %d", i)
		}
		idLen := int(binary.LittleEndian.Uint32(data[off : off+4]))
		off += 4
		if idLen == 0 || idLen > 1024 {
			return fmt.Errorf("vectors.bin: implausible id length %d at index %d", idLen, i)
		}
		if off+idLen > len(data) {
			return fmt.Errorf("vectors.bin: truncated reading id at index %d", i)
		}
		cb.vectorMap[string(data[off:off+idLen])] = i
		off += idLen
	}

	return nil
}
