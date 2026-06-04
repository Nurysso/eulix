package query

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"eulix/internal/config"
	"eulix/internal/embeddings"
	"eulix/internal/llm"
	"eulix/internal/types"
)

func NewDebugLogger(eulixDir string) *DebugLogger {
	logPath := filepath.Join(eulixDir, "context_debug.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return &DebugLogger{} // silent fallback
	}
	return &DebugLogger{file: f}
}

func (d *DebugLogger) Log(format string, args ...interface{}) {
	if d.file == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	msg := fmt.Sprintf("[%s] ", time.Now().Format("15:04:05"))
	msg += fmt.Sprintf(format, args...)
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	d.file.WriteString(msg)
}

func (d *DebugLogger) Close() {
	if d.file != nil {
		d.file.Close()
	}
}

// Constants
const (
	BinaryVersion     = uint32(3)
	MagicBytes        = "EULX"
	VectorVersion     = uint32(3)
	VectorMagic       = "EULX"
	ivfBuildThreshold = 10_000
	invIdxThreshold   = 5_000
	lazyContentLimit  = 50_000
	ivfNClusters      = 256
	ivfNProbe         = 8
	ivfKMeansIter     = 20
)

// Query intent

const (
	IntentUnknown IntentType = iota
	IntentSymbolExact
	IntentSymbolFuzzy
	IntentConcept
	IntentFlow
	IntentDebug
	IntentCallers
	IntentCallees
)

const (
	mmrLambda            = 0.7
	headerOverhead       = 20
	distantLineThreshold = 150
	simPenaltyFactor     = 1.20
)

//  Constructor

func ContextWindowCreator(eulixDir string, cfg *config.Config, llmClient *llm.Client, sourceRoot string) (*ContextBuilder, error) {
	cb := &ContextBuilder{
		eulixDir:   eulixDir,
		config:     cfg,
		llmClient:  llmClient,
		vectorMap:  make(map[string]int),
		sourceRoot: sourceRoot,
		debugLog:   NewDebugLogger(eulixDir),
	}
	cb.debugLog.Log("Initializing ContextBuilder with source root: %s", sourceRoot)

	eulixBinaryPath := filepath.Join(eulixDir, "..", "eulix_embed")
	cb.queryEmbedder = embeddings.VectorWeaver(eulixBinaryPath, cfg.Embeddings.Model)
	// Load chunks from kb.json + kb_index.json (replaces embeddings.json)
	if err := cb.loadChunksFromKB(); err != nil {
		return nil, fmt.Errorf("failed to load chunks from KB: %w", err)
	}
	// Load raw float32 embeddings from embeddings.bin
	if err := cb.loadEmbeddings(); err != nil {
		cb.debugLog.Log("Embeddings not loaded: %v", err)
		cb.hasEmbeddings = false
	} else {
		cb.hasEmbeddings = true
		cb.debugLog.Log("Loaded %d embeddings", len(cb.embeddings))
	}
	// Load id→embedding-index map from vectors.bin
	if err := cb.loadVectorMap(); err != nil {
		cb.debugLog.Log("Vector map not loaded: %v", err)
		cb.vectorMap = make(map[string]int)
	} else {
		cb.debugLog.Log("Loaded %d vector mappings", len(cb.vectorMap))
	}

	cb.loadCallGraph()

	if err := cb.loadKnowledgeBase(); err != nil {
		cb.hasKB = false
	} else {
		cb.hasKB = true
	}

	cb.debugLog.Log("ContextBuilder initialized successfully")
	return cb, nil
}

//  Loaders

func (cb *ContextBuilder) loadKnowledgeBase() error {
	data, err := os.ReadFile(filepath.Join(cb.eulixDir, "kb.json"))
	if err != nil {
		return fmt.Errorf("kb.json not found: %w", err)
	}
	var kb KnowledgeBase
	if err := json.Unmarshal(data, &kb); err != nil {
		return fmt.Errorf("failed to parse kb.json: %w", err)
	}
	cb.kbData = &kb
	return nil
}

// loadChunksFromKB builds cb.chunks, cb.symbolIndex, and cb.invertedIdx from
// kb.json + kb_index.json — without touching embeddings.json at all.
//
// Memory strategy:
//   - For corpora with > lazyContentLimit chunks, Content is left empty and
//     hydrated on demand from the KB structures (no separate content file needed).
//   - For smaller corpora the content from the KB function/class records is
//     inlined immediately so keyword search has something to scan.
func (cb *ContextBuilder) loadChunksFromKB() error {
	//  1. Load kb_index.json (lightweight: just names + locations)
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
	var kb KnowledgeBase
	if err := json.Unmarshal(kbData, &kb); err != nil {
		return fmt.Errorf("failed to parse kb.json: %w", err)
	}
	cb.kbData = &kb // set here so kbExactLookup works without a second load

	//  3. Build chunk slice from all functions + classes in every file
	// Pre-count so we can decide lazy vs eager

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
	// 4. Build symbol index
	cb.symbolIndex = make(map[string][]int, len(cb.chunks)*2)
	for i, c := range cb.chunks {
		for _, sym := range c.Symbols {
			cb.symbolIndex[sym] = append(cb.symbolIndex[sym], i)
		}
	}

	//  5. Optionally build inverted index (large corpora)
	// Pass nil for embData — buildInvertedIndexFromKB reads from chunks directly,
	// falling back to the KB structures for content when lazyContent is on.
	if len(cb.chunks) > invIdxThreshold {
		cb.invertedIdx = cb.buildInvertedIndexFromKB(&kb)
	}

	cb.kbIdx = &kbIdx

	// Note: for lazy-content corpora (> lazyContentLimit chunks) chunk.Content is
	// empty at this point, so the index will be sparse; those corpora rely on the
	// inverted index + KB structures for caller discovery instead.
	cb.callSites = buildCallSiteIndex(cb.chunks)
	cb.debugLog.Log("Built call-site index with %d entries", len(cb.callSites))

	return nil
}

// buildInvertedIndexFromKB tokenises every chunk.  When lazyContent is set it
// reads full text from the KnowledgeBase structures directly so we never need
// to touch a separate content file.
// buildInvertedIndexFromKB - avoid rebuilding chunks, read content directly from KB structures
func (cb *ContextBuilder) buildInvertedIndexFromKB(kb *KnowledgeBase) *InvertedIndex {
	idx := &InvertedIndex{
		Postings: make(map[string][]Posting, len(cb.chunks)*10),
		DocCount: len(cb.chunks),
	}

	// Build a key→chunkIdx map so we can look up chunk metadata without rebuilding
	keyToIdx := make(map[string]int, len(cb.chunks))
	for i, c := range cb.chunks {
		key := fmt.Sprintf("%s:%d-%d", c.File, c.StartLine, c.EndLine)
		keyToIdx[key] = i
	}
	// Index directly from KB structures - never call buildChunksFromKB* here
	indexChunk := func(chunkIdx int, content, name string, symbols []string) {
		termFreq := make(map[string]int, 32)
		for _, kw := range extractQueryKeywords(strings.ToLower(content)) {
			termFreq[kw]++
		}
		for _, sym := range symbols {
			termFreq[strings.ToLower(sym)] += 3
		}
		if name != "" {
			termFreq[strings.ToLower(name)] += 5
		}
		total := 0
		for _, cnt := range termFreq {
			total += cnt
		}
		if total == 0 {
			return
		}
		for term, cnt := range termFreq {
			idx.Postings[term] = append(idx.Postings[term], Posting{
				ChunkIdx: chunkIdx,
				TF:       float32(cnt) / float32(total),
			})
		}
	}

	for filePath, fs := range kb.Structure {
		for _, fn := range fs.Functions {
			key := fmt.Sprintf("%s:%d-%d", filePath, fn.LineStart, fn.LineEnd)
			if i, ok := keyToIdx[key]; ok {
				// Use signature+docstrings only not full body - from indexing
				indexChunk(i, fn.Signature+" "+fn.Docstring, fn.Name, nil)
			}
		}
		for _, cls := range fs.Classes {
			key := fmt.Sprintf("%s:%d-%d", filePath, cls.LineStart, cls.LineEnd)
			if i, ok := keyToIdx[key]; ok {
				indexChunk(i, cls.Name+" "+cls.Docstring, cls.Name, nil)
			}
			for _, m := range cls.Methods {
				mk := fmt.Sprintf("%s:%d-%d", filePath, m.LineStart, m.LineEnd)
				if i, ok := keyToIdx[mk]; ok {
					indexChunk(i, m.Signature+" "+m.Docstring, m.Name, nil)
				}
			}
		}
	}

	return idx
}

// readStr reads a length-prefixed string: 4-byte LE uint32 length, then UTF-8 bytes.
func readStr(data []byte, off int) (string, int, error) {
	if off+4 > len(data) {
		return "", off, fmt.Errorf("unexpected EOF reading string length at offset %d", off)
	}
	l := int(binary.LittleEndian.Uint32(data[off : off+4]))
	off += 4
	if off+l > len(data) {
		return "", off, fmt.Errorf("unexpected EOF reading string body at offset %d (len=%d)", off, l)
	}
	return string(data[off : off+l]), off + l, nil
}

// loadEmbeddings reads embeddings.bin (EULX magic, version 3).
// Format: [4B magic][4B version][4B+str model_name][4B count][4B dim]
//
//	then for each entry: [4B+str id][dim*4B float32 vector]
func (cb *ContextBuilder) loadEmbeddings() error {
	data, err := os.ReadFile(filepath.Join(cb.eulixDir, "embeddings.bin"))
	if err != nil {
		return fmt.Errorf("embeddings.bin not found: %w", err)
	}
	if len(data) < 8 {
		return fmt.Errorf("invalid embeddings file: too short (%d bytes)", len(data))
	}

	off := 0
	// Magic Byte
	if string(data[off:off+4]) != MagicBytes {
		return fmt.Errorf("wrong magic bytes in embeddings.bin: %q", data[off:off+4])
	}
	off += 4
	// Version 2 or 3 supported
	version := binary.LittleEndian.Uint32(data[off : off+4])
	off += 4
	if version != 2 && version != 3 {
		return fmt.Errorf("unsupported embeddings.bin version: %d", version)
	}
	if version != BinaryVersion {
		return fmt.Errorf("version mismatch: expected %d, got %d", BinaryVersion, version)
	}
	// Model name
	_, off, err = readStr(data, off)
	if err != nil {
		return fmt.Errorf("reading model name: %w", err)
	}
	// Count + dimensions
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
	ids := make([]string, numEmb) // todo: improve logic of preserve id order if needed

	for i := 0; i < numEmb; i++ {
		// Per-entry ID (version >= 3; version 2 used positional naming)
		if version >= 3 {
			ids[i], off, err = readStr(data, off)
			if err != nil {
				return fmt.Errorf("reading id at index %d: %w", i, err)
			}
		} else {
			ids[i] = fmt.Sprintf("embedding_%d", i)
		}
		// Vector payload
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
// Format: [4B magic][4B version][4B+str model_name][4B count][4B dim]

// then for each entry: [4B+str id][dim*4B float32 vector (skipped)]
func (cb *ContextBuilder) loadVectorMap() error {
	data, err := os.ReadFile(filepath.Join(cb.eulixDir, "vectors.bin"))
	if err != nil {
		return fmt.Errorf("vectors.bin not found: %w", err)
	}
	if len(data) < 8 {
		return fmt.Errorf("invalid vectors file: too short (%d bytes)", len(data))
	}

	off := 0
	// Magic
	if string(data[off:off+4]) != VectorMagic {
		return fmt.Errorf("wrong magic bytes in vectors.bin: %q", data[off:off+4])
	}
	off += 4
	// Version
	version := binary.LittleEndian.Uint32(data[off : off+4])
	off += 4
	if version != VectorVersion {
		return fmt.Errorf("vectors.bin version mismatch: expected %d, got %d", VectorVersion, version)
	}
	// Model name
	_, off, err = readStr(data, off)
	if err != nil {
		return fmt.Errorf("reading model name: %w", err)
	}
	// Count + dimension (both uint32, matching Python struct.pack("<II", ...))
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
		off += dim * 4 // skip vector payload — already in embeddings.bin
	}

	return nil
}

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

func (cb *ContextBuilder) BuildContext(query string) (*types.ContextWindow, error) {
	maxLines := cb.config.Project.MaxLines
	ctx, _, err := cb.buildContextInternal(query, maxLines)
	if err != nil {
		return nil, err
	}

	if cb.config.Project.DebugConfig {
		if err := cb.writeContextToFile(ctx); err != nil {
			// don’t fail main flow for debug write issues
			fmt.Printf("failed to write debug context: %v\n", err)
		}
	}

	return ctx, nil
}

func (cb *ContextBuilder) BuildContextWithDebug(query string) (*types.ContextWindow, *DebugTrace, error) {
	maxLines := cb.config.Project.MaxLines
	ctx, trace, err := cb.buildContextInternal(query, maxLines)
	if err != nil {
		return nil, nil, err
	}

	if cb.config.Project.DebugConfig {
		if err := cb.writeContextToFile(ctx); err != nil {
			fmt.Printf("failed to write debug context: %v\n", err)
		}
	}

	return ctx, trace, nil
}

func (cb *ContextBuilder) writeContextToFile(ctx *types.ContextWindow) error {
	fileName := fmt.Sprintf("debug_embedding_pipeline_%s.txt", time.Now().Format("20060102_150405"))
	f, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(fmt.Sprintf("%+v\n", ctx))
	return err
}

func (cb *ContextBuilder) GetLastTrace() *DebugTrace {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.lastTrace
}

//  Core pipeline

// hydrateSourceCode adds actual source code to chunks, prioritizing high-confidence chunks
func (cb *ContextBuilder) hydrateSourceCode(chunks []Chunk, sourceBudget int, maxLinesDefault int) []Chunk {
	if cb.sourceRoot == "" {
		cb.debugLog.Log("Source hydration skipped: no source root configured")
		return chunks // no source root configured
	}

	cb.debugLog.Log("=== SOURCE HYDRATION START ===")
	cb.debugLog.Log("Source root: %s", cb.sourceRoot)
	cb.debugLog.Log("Budget: %d tokens | Max lines: %d | Chunks: %d", sourceBudget, maxLinesDefault, len(chunks))

	result := make([]Chunk, len(chunks))
	usedTokens := 0
	successCount := 0

	// Chunks are already in priority order from mmrSelect
	for i, chunk := range chunks {
		remaining := sourceBudget - usedTokens
		if remaining <= 0 {
			result[i] = chunk
			continue
		}

		maxLines := maxLinesDefault
		budgetFraction := float64(remaining) / float64(sourceBudget)
		// Progressive reduction: higher priority chunks get more lines
		if budgetFraction < 0.5 {
			maxLines = maxLines / 2
		}
		if budgetFraction < 0.25 {
			maxLines = maxLines / 4
		}
		if maxLines < 10 {
			maxLines = 10 // minimum viable snippet
		}

		// Read and clean source
		sourceCode, tokens := cb.readSourceLines(chunk.File, chunk.StartLine, chunk.EndLine, maxLines)

		// Prepend source to KB metadata (keep metadata for call graph info)
		if sourceCode == "" || tokens > remaining {
			if sourceCode == "" {
				fmt.Printf("[DEBUG] No source found for %s:%d-%d\n", chunk.File, chunk.StartLine, chunk.EndLine)
			} else {

				cb.debugLog.Log("Chunk %d: source available but exceeds remaining budget (%d > %d)", i, tokens, remaining)
			}
			result[i] = chunk
			continue
		}

		chunk.Content = fmt.Sprintf("```%s\n%s\n```",
			detectLanguage(chunk.File),
			sourceCode)
		chunk.Tokens = tokens
		usedTokens += tokens
		successCount++

		cb.debugLog.Log("Chunk %d (%s:%d-%d): ✓ Added %d lines, %d tokens (budget: %d/%d)",
			i, chunk.File, chunk.StartLine, chunk.EndLine,
			strings.Count(sourceCode, "\n")+1, tokens, usedTokens, sourceBudget)

		result[i] = chunk
	}

	cb.debugLog.Log("=== SOURCE HYDRATION COMPLETE ===")
	cb.debugLog.Log("Success: %d/%d chunks | Used: %d/%d tokens",
		successCount, len(chunks), usedTokens, sourceBudget)

	return result
}

// readSourceLines reads lines from source file, strips comments/docstrings
func (cb *ContextBuilder) readSourceLines(filePath string, startLine, endLine, maxLines int) (string, int) {
	// Calculate actual end line respecting maxLines
	actualEnd := endLine
	if endLine-startLine+1 > maxLines {
		actualEnd = startLine + maxLines - 1
	}
	// Read source file
	sourceFile := filepath.Join(cb.sourceRoot, filePath)
	// Check if file exists
	if _, err := os.Stat(sourceFile); os.IsNotExist(err) {
		cb.debugLog.Log("Source file not found: %s", sourceFile)
		return "", 0
	}
	data, err := os.ReadFile(sourceFile)
	if err != nil {
		cb.debugLog.Log("Error reading %s: %v", sourceFile, err)
		return "", 0
	}

	lines := strings.Split(string(data), "\n")
	if startLine > len(lines) || startLine < 1 {
		cb.debugLog.Log("Invalid line range for %s: file has %d lines, requested %d-%d",
			filePath, len(lines), startLine, endLine)
		return "", 0
	}
	// Extract range (1-indexed to 0-indexed)
	start := startLine - 1
	end := actualEnd
	if end > len(lines) {
		end = len(lines)
	}

	relevantLines := lines[start:end]
	lang := detectLanguage(filePath)
	cleaned := stripCommentsAndDocs(relevantLines, lang)
	// Strip comments and docstrings
	code := strings.Join(cleaned, "\n")
	tokens := len(code) / 4 // rough estimate: 4 chars per token

	return code, tokens
}

// stripCommentsAndDocs removes comments and docstrings, keeping code structure
func stripCommentsAndDocs(lines []string, lang string) []string {
	result := make([]string, 0, len(lines))
	inBlockComment := false
	inDocstring := false
	docstringMarker := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		originalLine := line

		switch lang {
		case "python":
			// Handle docstrings (""" or ''')
			if !inDocstring {
				if strings.HasPrefix(trimmed, `"""`) {
					inDocstring = true
					docstringMarker = `"""`
					if strings.Count(trimmed, `"""`) >= 2 {
						inDocstring = false // single-line docstring
					}
					continue
				} else if strings.HasPrefix(trimmed, "'''") {
					inDocstring = true
					docstringMarker = "'''"
					if strings.Count(trimmed, "'''") >= 2 {
						inDocstring = false
					}
					continue
				}
			} else {
				if strings.Contains(trimmed, docstringMarker) {
					inDocstring = false
				}
				continue
			}
			// Strip inline comments
			if idx := strings.Index(line, "#"); idx >= 0 {
				line = line[:idx]
			}

		case "go", "javascript", "typescript", "rust", "c", "cpp", "java":
			// Handle block comments /* */
			if !inBlockComment && strings.Contains(line, "/*") {
				inBlockComment = true
				if idx := strings.Index(line, "/*"); idx >= 0 {
					line = line[:idx]
				}
			}
			if inBlockComment {
				if strings.Contains(line, "*/") {
					inBlockComment = false
					if idx := strings.Index(line, "*/"); idx >= 0 && idx+2 < len(line) {
						line = line[idx+2:]
					} else {
						continue
					}
				} else {
					continue
				}
			}
			// Strip inline comments //
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = line[:idx]
			}
		}

		// Keep line if it has content after comment removal
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		} else if originalLine != "" && strings.TrimSpace(originalLine) == "" {
			// Preserve empty lines that were originally empty
			result = append(result, "")
		}
	}

	return result
}

// detectLanguage determines programming language from file extension
func detectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".py":
		return "python"
	case ".go":
		return "go"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	case ".java":
		return "java"
	default:
		return "unknown"
	}
}

func (cb *ContextBuilder) buildContextInternal(query string, maxLinesDefault int) (*types.ContextWindow, *DebugTrace, error) {
	start := time.Now()
	trace := &DebugTrace{Query: query}

	cb.debugLog.Log("\n=== NEW QUERY ===")
	cb.debugLog.Log("Query: %s", query)

	intent := cb.classifyQueryIntent(query)
	trace.Intent = intent
	cb.debugLog.Log("Intent: %d (specificity: %.2f, confidence: %.2f)",
		intent.Type, intent.Specificity, intent.Confidence)

	budget := cb.allocateBudget(query, intent)
	trace.Budget = budget
	cb.debugLog.Log("Budget: %d tokens for context (total: %d)",
		budget.ContextBudget, budget.MaxTokens)
	// Always anchor on the queried symbol(s) first
	anchors := cb.exactSymbolSearch(query)
	if len(anchors) > 2 {
		anchors = anchors[:2]
	}
	anchorFiles := make(map[string]bool)
	for _, a := range anchors {
		anchorFiles[a.File] = true
	}
	cb.debugLog.Log("Found %d exact anchors", len(anchors))
	// For caller/callee intents do direct call-site scan
	var callSiteResults []ScoredChunk
	if intent.Type == IntentCallers || intent.Type == IntentCallees {
		callSiteResults = cb.findCallSites(query, intent)
		cb.debugLog.Log("Found %d call sites", len(callSiteResults))
	}

	candidateLimit := cb.candidateLimitForIntent(intent)
	candidates := cb.multiStrategySearch(query, candidateLimit, intent, trace)
	trace.TotalCandidates = len(candidates)
	cb.debugLog.Log("Multi-strategy search: %d candidates", len(candidates))
	// Merge anchors + callsite hits
	candidates = mergeWithPriority(anchors, callSiteResults, candidates)
	cb.debugLog.Log("After merge: %d candidates", len(candidates))

	var expanded []ScoredChunk
	if cb.hasCallGraph {
		expanded = cb.buildContextWithGraph(candidates, budget.ContextBudget, intent)
		cb.debugLog.Log("Graph expansion: %d chunks", len(expanded))
	} else {
		expanded = cb.buildContextWithoutGraph(candidates, budget.ContextBudget)
	}

	var selected []Chunk
	if cb.hasEmbeddings {
		trace.SelectionMethod = "mmr"
		var qEmb []float32
		if emb, err := cb.queryEmbedder.EmbedQueryBinary(query); err == nil {
			qEmb = emb
		} else {
			trace.Warnings = append(trace.Warnings, "query embedding failed: "+err.Error())
		}
		selected = cb.mmrSelect(expanded, budget.ContextBudget, qEmb, anchorFiles, trace)
	} else {
		trace.SelectionMethod = "greedy"
		selected = cb.selectChunks(expanded, budget.ContextBudget)
		for i, c := range selected {
			trace.ChunkTraces = append(trace.ChunkTraces, ChunkTrace{
				ID: c.ID, File: c.File,
				Lines:  [2]int{c.StartLine, c.EndLine},
				Tokens: c.Tokens, Rank: i + 1, Included: true,
			})
		}
	}
	cb.debugLog.Log("Selected %d chunks via %s", len(selected), trace.SelectionMethod)

	if cb.lazyContent {
		cb.hydrateContent(selected)
		cb.debugLog.Log("Hydrated KB content for %d chunks", len(selected))
	}
	// Add actual source code (30% of budget)
	sourceBudget := budget.ContextBudget * 65 / 100
	selected = cb.hydrateSourceCode(selected, sourceBudget, maxLinesDefault)

	ctx := cb.assembleContext(selected)
	trace.TotalTokens = ctx.TotalTokens
	trace.Duration = time.Since(start)

	cb.debugLog.Log("=== QUERY COMPLETE ===")
	cb.debugLog.Log("Final context: %d chunks, %d tokens, %d sources",
		len(ctx.Chunks), ctx.TotalTokens, len(ctx.Sources))
	cb.debugLog.Log("Duration: %v\n", trace.Duration)

	cb.mu.Lock()
	cb.lastTrace = trace
	cb.mu.Unlock()

	return ctx, trace, nil
}

// candidateLimitForIntent avoids pulling 120 candidates for narrow queries
func (cb *ContextBuilder) candidateLimitForIntent(intent QueryIntent) int {
	switch {
	case intent.Type == IntentCallers || intent.Type == IntentCallees:
		if intent.Specificity > 0.9 {
			return 5
		}
		return 30
	case intent.Type == IntentConcept || intent.Type == IntentFlow:
		if intent.Specificity > 0.8 {
			return 15
		}
		return 50
	case intent.Specificity > 0.8:
		return 20
	case intent.Specificity > 0.5:
		return 50
	default:
		return 80
	}
}

//  Intent classification

func (cb *ContextBuilder) classifyQueryIntent(query string) QueryIntent {
	lower := strings.ToLower(query)
	words := strings.Fields(lower)
	wordSet := make(map[string]bool, len(words))
	for _, w := range words {
		wordSet[w] = true
	}

	// Call-graph direction detection — checked first, highest priority
	callersKW := []string{"call sites", "who calls", "called by", "callers of", "invokes", "usage of", "uses of"}
	calleesKW := []string{"what does", "calls internally", "invokes inside", "dependencies of", "callees"}

	containsPhrase := func(kws []string) bool {
		for _, kw := range kws {
			if strings.Contains(lower, kw) {
				return true
			}
		}
		return false
	}

	debugKW := []string{"error", "panic", "nil", "crash", "fail", "bug", "exception", "segfault", "undefined", "invalid"}
	flowKW := []string{"trace", "flow", "lifecycle", "sequence", "step", "chain", "order", "when", "path"}
	conceptKW := []string{"how", "why", "interact", "implement", "how does", "what", "explain", "describe", "understand", "difference", "between", "purpose"}

	score := func(list []string) float64 {
		s := 0.0
		for _, kw := range list {
			if strings.Contains(lower, kw) {
				s++
			}
		}
		return s
	}

	dbg, flow, concept := score(debugKW), score(flowKW), score(conceptKW)
	symbols := extractPotentialSymbols(query)
	kwds := extractQueryKeywords(lower)

	codeSymbols := 0
	for _, s := range symbols {
		if strings.ContainsAny(s, "_") || (len(s) > 1 && s[0] >= 'A' && s[0] <= 'Z') {
			codeSymbols++
		}
	}
	specificity := math.Min(1.0, float64(codeSymbols)*0.35)

	switch {
	case containsPhrase(callersKW):
		return QueryIntent{
			Type:        IntentCallers,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: 0.9,
			Confidence:  0.9,
		}
	case containsPhrase(calleesKW):
		return QueryIntent{
			Type:        IntentCallees,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: 0.9,
			Confidence:  0.9,
		}
	case dbg > 0:
		return QueryIntent{
			Type:        IntentDebug,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: specificity,
			Confidence:  math.Min(1, dbg/2),
		}
	case flow > 0:
		return QueryIntent{
			Type:        IntentFlow,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: specificity,
			Confidence:  math.Min(1, flow/2),
		}
	case concept > 1 && codeSymbols >= 1:
		return QueryIntent{
			Type:        IntentConcept,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: math.Min(0.9, specificity),
			Confidence:  math.Min(1, concept/3),
		}
	case flow > 1 && codeSymbols >= 1:
		return QueryIntent{
			Type:        IntentFlow,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: math.Min(0.9, specificity),
			Confidence:  math.Min(1, flow/2),
		}
	case codeSymbols >= 2 && len(words) <= 5:
		// Short, symbol-heavy query → likely a "what does X call?"
		return QueryIntent{
			Type:        IntentCallees,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: 0.9,
			Confidence:  0.85,
		}
	case codeSymbols >= 1:
		// IntentSymbolExact look up the symbol, don't chase its dependencies.
		return QueryIntent{
			Type:        IntentSymbolExact,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: specificity,
			Confidence:  0.7,
		}
	case concept > 0:
		return QueryIntent{
			Type:        IntentConcept,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: 0.2,
			Confidence:  math.Min(1, concept/3),
		}
	default:
		return QueryIntent{
			Type:        IntentConcept,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: 0.3,
			Confidence:  0.5,
		}
	}
}

//  Call-site direct search

func buildCallSiteIndex(chunks []Chunk) callSiteIndex {
	idx := make(callSiteIndex, 512)

	for i, chunk := range chunks {
		content := chunk.Content
		if content == "" {
			continue
		}

		for j := 0; j < len(content); {
			paren := strings.IndexByte(content[j:], '(')
			if paren < 0 {
				break
			}
			paren += j

			start := paren - 1
			for start >= 0 && isIdentRune(content[start]) {
				start--
			}
			start++

			if start < paren {
				sym := strings.ToLower(content[start:paren])
				idx[sym] = append(idx[sym], i)
			}
			j = paren + 1
		}
	}
	return idx
}

func isIdentRune(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// findCallSites does a literal string scan for ".symbol(" patterns across all
// chunks. This is O(n × |symbols|) but is the single most reliable signal for
// "who calls X" queries — this should run first, before any fuzzy matching.
func (cb *ContextBuilder) findCallSites(query string, intent QueryIntent) []ScoredChunk {
	symbols := extractPotentialSymbols(query)
	results := make([]ScoredChunk, 0, 32)
	seen := make(map[int]bool)

	for _, sym := range symbols {
		symLower := strings.ToLower(sym)

		indices, ok := cb.callSites[symLower]
		if !ok {
			continue
		}

		for _, idx := range indices {
			if seen[idx] {
				continue
			}
			seen[idx] = true

			chunk := cb.chunks[idx]

			score := 95.0
			matchDetail := "calls " + sym

			if intent.Type == IntentCallees && strings.EqualFold(chunk.Name, sym) {
				score = 70.0
				matchDetail = "definition of " + sym
			}
			if strings.HasPrefix(strings.ToLower(chunk.Name), "_"+symLower) {
				score -= 20.0
			}

			results = append(results, ScoredChunk{
				Chunk:        chunk,
				Score:        score,
				MatchType:    "callsite",
				MatchDetails: matchDetail,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return results
}

// hydrateOne returns the full text for a single chunk from kbData without
// touching the Content field (used during call-site scanning in lazy mode).
func (cb *ContextBuilder) hydrateOne(c Chunk) string {
	if cb.kbData == nil {
		return ""
	}
	if fs, ok := cb.kbData.Structure[c.File]; ok {
		for _, fn := range fs.Functions {
			if fn.LineStart == c.StartLine && fn.LineEnd == c.EndLine {
				return cb.buildChunkFromKBFunction(fn, c.File).Content
			}
		}
		for _, cls := range fs.Classes {
			if cls.LineStart == c.StartLine && cls.LineEnd == c.EndLine {
				return cb.buildChunkFromKBClass(cls, c.File).Content
			}
			for _, m := range cls.Methods {
				if m.LineStart == c.StartLine && m.LineEnd == c.EndLine {
					return cb.buildChunkFromKBFunction(m, c.File).Content
				}
			}
		}
	}
	return ""
}

// mergeWithPriority blends pre-computed high-priority slices (anchors, callsites)
// into the main candidate list, ensuring they appear at the top.
func mergeWithPriority(anchors, callSites, candidates []ScoredChunk) []ScoredChunk {
	seen := make(map[string]bool, len(anchors)+len(callSites)+len(candidates))
	out := make([]ScoredChunk, 0, len(anchors)+len(callSites)+len(candidates))

	add := func(sc ScoredChunk) {
		if !seen[sc.ID] {
			seen[sc.ID] = true
			out = append(out, sc)
		}
	}
	for _, sc := range anchors {
		add(sc)
	}
	for _, sc := range callSites {
		add(sc)
	}
	for _, sc := range candidates {
		add(sc)
	}
	return out
}

//  Dynamic budget allocation

func (cb *ContextBuilder) allocateBudget(query string, intent QueryIntent) BudgetAllocation {
	sysReserve := 150
	qTokens := len(query) / 4
	safetyBuf := 200
	respReserve := 2000
	total := cb.config.LLM.MaxTokens
	ctxBudget := int(float64(total-qTokens-sysReserve-safetyBuf-respReserve) * 0.85)

	w := map[string]float64{
		"kb_exact": 0.30, "keyword": 0.25, "semantic": 0.25, "graph": 0.20,
	}

	switch intent.Type {
	case IntentCallers, IntentCallees:
		// callsite scan is the primary signal — reduce semantic noise
		w["kb_exact"], w["keyword"], w["semantic"], w["graph"] = 0.45, 0.30, 0.10, 0.15
	case IntentSymbolExact:
		w["kb_exact"], w["keyword"], w["semantic"], w["graph"] = 0.55, 0.20, 0.10, 0.15
	case IntentSymbolFuzzy:
		w["kb_exact"], w["keyword"], w["semantic"], w["graph"] = 0.40, 0.25, 0.20, 0.15
	case IntentConcept:
		w["kb_exact"], w["keyword"], w["semantic"], w["graph"] = 0.15, 0.20, 0.50, 0.15
	case IntentFlow:
		w["kb_exact"], w["keyword"], w["semantic"], w["graph"] = 0.20, 0.15, 0.25, 0.40
	case IntentDebug:
		w["kb_exact"], w["keyword"], w["semantic"], w["graph"] = 0.35, 0.35, 0.15, 0.15
	}

	return BudgetAllocation{
		MaxTokens:       total,
		SystemReserve:   sysReserve,
		QueryCost:       qTokens,
		ResponseReserve: respReserve,
		ContextBudget:   ctxBudget,
		StrategyWeights: w,
	}
}

//  Multi-strategy search

func (cb *ContextBuilder) multiStrategySearch(
	query string, topK int, intent QueryIntent, trace *DebugTrace,
) []ScoredChunk {
	all := make(map[string]ScoredChunk, topK*2)

	run := func(name string, fn func() []ScoredChunk, multiBoost float64) {
		t0 := time.Now()
		results := fn()
		st := StrategyTrace{Name: name, Found: len(results), Duration: time.Since(t0)}
		sum := 0.0
		for _, m := range results {
			m.MatchType = name
			if ex, ok := all[m.ID]; ok {
				m.Score = math.Max(ex.Score, m.Score) + multiBoost
				m.MatchType = ex.MatchType + "+" + name
			}
			all[m.ID] = m
			if m.Score > st.TopScore {
				st.TopScore = m.Score
			}
			sum += m.Score
		}
		if len(results) > 0 {
			st.AvgScore = sum / float64(len(results))
		}
		if trace != nil {
			trace.Strategies = append(trace.Strategies, st)
		}
	}

	if cb.hasKB {
		run("kb_exact", func() []ScoredChunk { return cb.kbExactLookup(query, intent) }, 2.5)
	}

	run("exact", func() []ScoredChunk { return cb.exactSymbolSearch(query) }, 2.0)
	run("partial", func() []ScoredChunk { return cb.partialIdentifierMatch(query) }, 1.5)

	kwTopK := int(float64(topK) * (0.4 + 0.3*intent.Budget()["keyword"]))
	syms := extractPotentialSymbols(query)
	run("keyword", func() []ScoredChunk {
		var res []ScoredChunk
		if cb.invertedIdx != nil {
			res = cb.invertedKeywordSearch(query, kwTopK)
		} else {
			res = cb.keywordSearch(query, kwTopK)
		}
		for i := range res {
			content := res[i].Content
			if content == "" {
				content = cb.hydrateOne(res[i].Chunk)
			}
			for _, sym := range syms {
				if strings.EqualFold(res[i].Name, sym) {
					continue
				}
				if strings.Contains(content, "."+sym+"(") || strings.Contains(content, sym+"(") {
					res[i].Score += 25.0
					break
				}
			}
			// Penalise _sym* matches
			for _, sym := range syms {
				if strings.HasPrefix(strings.ToLower(res[i].Name), "_"+strings.ToLower(sym)) {
					res[i].Score -= 10.0
				}
			}
		}
		return res
	}, 2.0)

	skipSemantic := intent.Type == IntentCallers ||
		intent.Type == IntentCallees ||
		intent.Specificity > 0.85

	if cb.hasEmbeddings && !skipSemantic {
		semTopK := int(float64(topK) * (0.3 + 0.3*intent.Budget()["semantic"]))
		run("semantic", func() []ScoredChunk {
			qEmb, err := cb.queryEmbedder.EmbedQueryBinary(query)
			if err != nil {
				return nil
			}
			var raw []ScoredChunk
			if cb.ivfIndex != nil {
				raw = cb.vectorSearchIVF(qEmb, semTopK, 0.5)
			} else {
				raw = cb.vectorSearch(qEmb, semTopK, 0.5)
			}
			for i := range raw {
				raw[i].Score *= 20.0
			}
			return raw
		}, 1.5)
	}

	result := make([]ScoredChunk, 0, len(all))
	for _, sc := range all {
		result = append(result, sc)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].MatchType == "exact" && result[j].MatchType != "exact" {
			return true
		}
		if result[i].MatchType != "exact" && result[j].MatchType == "exact" {
			return false
		}
		return result[i].Score > result[j].Score
	})
	if len(result) > topK {
		result = result[:topK]
	}
	return result
}

func (qi QueryIntent) Budget() map[string]float64 {
	return map[string]float64{"keyword": qi.Specificity, "semantic": 1 - qi.Specificity}
}

//  Inverted index keyword search

func (cb *ContextBuilder) invertedKeywordSearch(query string, topK int) []ScoredChunk {
	if cb.invertedIdx == nil {
		return cb.keywordSearch(query, topK)
	}

	keywords := extractQueryKeywords(strings.ToLower(query))
	symbols := extractPotentialSymbols(query)
	terms := uniqueStrings(append(keywords, symbols...))

	cb.invertedIdx.mu.RLock()
	defer cb.invertedIdx.mu.RUnlock()

	scores := make(map[int]float64, topK*4)
	matched := make(map[int][]string, topK*4)
	n := float64(cb.invertedIdx.DocCount)

	for _, term := range terms {
		posts := cb.invertedIdx.Postings[strings.ToLower(term)]
		if len(posts) == 0 {
			continue
		}
		idf := math.Log(n/float64(len(posts))) + 1
		for _, p := range posts {
			scores[p.ChunkIdx] += float64(p.TF) * idf
			matched[p.ChunkIdx] = append(matched[p.ChunkIdx], term)
		}
	}

	for _, sym := range symbols {
		symLow := strings.ToLower(sym)
		for idx := range scores {
			if strings.ToLower(cb.chunks[idx].Name) == symLow {
				scores[idx] += 20.0
			}
		}
	}

	result := make([]ScoredChunk, 0, len(scores))
	for idx, score := range scores {
		result = append(result, ScoredChunk{
			Chunk:        cb.chunks[idx],
			Score:        score,
			MatchDetails: strings.Join(uniqueStrings(matched[idx]), ", "),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Score > result[j].Score })
	if len(result) > topK {
		result = result[:topK]
	}
	return result
}

//  IVF vector search

func (cb *ContextBuilder) vectorSearchIVF(qEmb []float32, topK int, threshold float64) []ScoredChunk {
	if cb.ivfIndex == nil {
		return cb.vectorSearch(qEmb, topK, threshold)
	}
	idx := cb.ivfIndex

	type centDist struct {
		d float64
		i int
	}
	cd := make([]centDist, idx.NClusters)
	for i, c := range idx.Centroids {
		cd[i] = centDist{-cosineSimilarity(qEmb, c), i}
	}
	sort.Slice(cd, func(i, j int) bool { return cd[i].d < cd[j].d })

	nProbe := ivfNProbe
	if nProbe > idx.NClusters {
		nProbe = idx.NClusters
	}

	scored := make([]ScoredChunk, 0, topK*2)
	seen := make(map[int32]bool, topK*2)
	for p := 0; p < nProbe; p++ {
		for _, embIdx := range idx.Lists[cd[p].i] {
			if seen[embIdx] {
				continue
			}
			seen[embIdx] = true
			if int(embIdx) >= len(cb.chunks) || int(embIdx) >= len(cb.embeddings) {
				continue
			}
			if sim := cosineSimilarity(qEmb, cb.embeddings[embIdx]); sim >= threshold {
				scored = append(scored, ScoredChunk{Chunk: cb.chunks[embIdx], Score: sim})
			}
		}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > topK {
		scored = scored[:topK]
	}
	return scored
}

func buildIVFIndex(embs [][]float32, k, maxIter int) *IVFIndex {
	n := len(embs)
	if n == 0 || k <= 0 {
		return nil
	}
	if k > n {
		k = n
	}
	dim := len(embs[0])
	rng := rand.New(rand.NewSource(42))

	perm := rng.Perm(n)
	centroids := make([][]float32, k)
	for i := range centroids {
		c := make([]float32, dim)
		copy(c, embs[perm[i]])
		centroids[i] = c
	}

	assignments := make([]int32, n)
	sums := make([][]float64, k)
	for i := range sums {
		sums[i] = make([]float64, dim)
	}
	counts := make([]int, k)

	for iter := 0; iter < maxIter; iter++ {
		changed := 0
		for ei, emb := range embs {
			best, bestSim := int32(0), -math.MaxFloat64
			for ci, c := range centroids {
				if s := cosineSimilarity(emb, c); s > bestSim {
					bestSim, best = s, int32(ci)
				}
			}
			if assignments[ei] != best {
				changed++
			}
			assignments[ei] = best
		}
		if changed == 0 {
			break
		}

		for i := range sums {
			counts[i] = 0
			for j := range sums[i] {
				sums[i][j] = 0
			}
		}
		for ei, emb := range embs {
			ci := assignments[ei]
			counts[ci]++
			for j, v := range emb {
				sums[ci][j] += float64(v)
			}
		}
		for ci := range centroids {
			if counts[ci] == 0 {
				continue
			}
			for j := range centroids[ci] {
				centroids[ci][j] = float32(sums[ci][j] / float64(counts[ci]))
			}
		}
	}

	lists := make([][]int32, k)
	for i := range lists {
		lists[i] = make([]int32, 0)
	}
	for ei, ci := range assignments {
		lists[ci] = append(lists[ci], int32(ei))
	}
	return &IVFIndex{Centroids: centroids, Lists: lists, NClusters: k, Dim: dim}
}

//  MMR chunk selection

func (cb *ContextBuilder) mmrSelect(
	candidates []ScoredChunk, budget int, qEmb []float32, anchorFiles map[string]bool, trace *DebugTrace,
) []Chunk {
	if len(candidates) == 0 {
		return nil
	}

	maxSc := 0.0
	for _, c := range candidates {
		if c.Score > maxSc {
			maxSc = c.Score
		}
	}
	if maxSc == 0 {
		maxSc = 1
	}

	embOf := func(id string) []float32 {
		if idx, ok := cb.vectorMap[id]; ok && idx < len(cb.embeddings) {
			return cb.embeddings[idx]
		}
		return nil
	}

	simToQuery := func(c ScoredChunk) float64 {
		base := 0.0
		if qEmb != nil {
			if e := embOf(c.ID); e != nil {
				base = cosineSimilarity(qEmb, e)
			}
		}
		if base == 0 {
			base = c.Score / maxSc
		}
		if anchorFiles[c.File] {
			base = math.Min(1.0, base*1.25)
		}
		return base
	}

	simBetween := func(a, b ScoredChunk) float64 {
		if ea, eb := embOf(a.ID), embOf(b.ID); ea != nil && eb != nil {
			sim := cosineSimilarity(ea, eb)

			if a.File == b.File && sim > 0.4 {
				dist := a.StartLine - b.StartLine
				if dist < 0 {
					dist = -dist
				}
				if dist > distantLineThreshold {
					sim = math.Min(1.0, sim*simPenaltyFactor)
				}
			}
			return sim
		}

		filtered := func(syms []string) []string {
			out := make([]string, 0, len(syms))
			for _, s := range syms {
				if !cb.isBoilerplateSymbol(s) {
					out = append(out, s)
				}
			}
			return out
		}

		aSyms := filtered(a.Symbols)
		bSyms := filtered(b.Symbols)

		if len(aSyms) == 0 && len(bSyms) == 0 {
			return 1.0
		}
		if len(aSyms) == 0 || len(bSyms) == 0 {
			return 0.0
		}

		setA := make(map[string]bool, len(aSyms))
		for _, s := range aSyms {
			setA[s] = true
		}
		inter := 0
		for _, s := range bSyms {
			if setA[s] {
				inter++
			}
		}
		union := len(aSyms) + len(bSyms) - inter
		if union == 0 {
			return 0
		}
		return float64(inter) / float64(union)
	}

	remaining := make([]ScoredChunk, len(candidates))
	copy(remaining, candidates)

	selected := make([]Chunk, 0, 24)
	selSC := make([]ScoredChunk, 0, 24)
	tokenSum := 0
	chunkTraces := make([]ChunkTrace, 0, len(candidates))

	for len(remaining) > 0 {
		bestIdx, bestMMR := -1, -math.MaxFloat64

		for i, c := range remaining {
			rel := simToQuery(c)

			maxRedund := 0.0
			for _, sel := range selSC {
				if r := simBetween(c, sel); r > maxRedund {
					maxRedund = r
				}
			}

			if mmr := mmrLambda*rel - (1-mmrLambda)*maxRedund; mmr > bestMMR {
				bestMMR, bestIdx = mmr, i
			}
		}

		if bestIdx < 0 {
			break
		}

		pick := remaining[bestIdx]
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)

		cost := pick.Tokens + headerOverhead
		ct := ChunkTrace{
			ID:           pick.ID,
			File:         pick.File,
			Lines:        [2]int{pick.StartLine, pick.EndLine},
			Tokens:       pick.Tokens,
			Score:        pick.Score,
			MatchType:    pick.MatchType,
			MatchDetails: pick.MatchDetails,
			Rank:         len(selected) + 1,
		}

		if tokenSum+cost > budget {
			ct.Included = false
			ct.ExcludeReason = "exceeds token budget"
			chunkTraces = append(chunkTraces, ct)
			continue
		}

		ct.Included = true
		chunkTraces = append(chunkTraces, ct)
		selected = append(selected, pick.Chunk)
		selSC = append(selSC, pick)
		tokenSum += cost

		if n := len(selected); n > 1 && canMerge(selected[n-2], selected[n-1]) {
			selected[n-2] = mergeChunks(selected[n-2], selected[n-1])
			selected = selected[:n-1]
			selSC = selSC[:n-1]
			tokenSum -= headerOverhead
		}
	}

	if trace != nil {
		trace.ChunkTraces = chunkTraces
	}
	return selected
}

// hydrateContent fills Chunk.Content for selected chunks when in lazy mode,
// reading from kbData directly (no separate content file needed).
func (cb *ContextBuilder) hydrateContent(chunks []Chunk) {
	if cb.kbData == nil {
		return
	}
	for i := range chunks {
		if chunks[i].Content != "" {
			continue
		}
		chunks[i].Content = cb.hydrateOne(chunks[i])
	}
}

//  Call-graph expansion (direction-aware)

func (cb *ContextBuilder) buildContextWithGraph(
	candidates []ScoredChunk, budget int, intent QueryIntent,
) []ScoredChunk {
	expanded := make(map[string]ScoredChunk, len(candidates))
	for _, c := range candidates {
		expanded[c.ID] = c
	}
	topN := 20
	if len(candidates) < topN {
		topN = len(candidates)
	}
	const maxGraphExpansions = 15
	relCount := 0

outerLoop:
	for i := 0; i < topN; i++ {
		cand := candidates[i]
		for _, sym := range cand.Symbols {
			if rels, ok := cb.callGraph[sym]; ok {
				for _, rel := range rels {
					if relCount >= maxGraphExpansions {
						break outerLoop
					}
					score := cand.Score

					switch {
					case intent.Type == IntentCallers && rel.Type == "called_by":
						score *= 1.2
					case intent.Type == IntentCallees && rel.Type == "calls":
						score *= 1.2
					case rel.Type == "calls" || rel.Type == "called_by":
						score *= 0.9
					case rel.Distance <= 2:
						score *= 0.6
					default:
						continue
					}

					for _, idx := range cb.symbolIndex[rel.Target] {
						chunk := cb.chunks[idx]
						if ex, ok := expanded[chunk.ID]; !ok || score > ex.Score {
							expanded[chunk.ID] = ScoredChunk{
								Chunk: chunk, Score: score,
								Distance: rel.Distance, FromID: cand.ID,
							}
						}
					}
					relCount++ // count per relationship processed, not per symbol
				}
			}
		}
	}

	result := make([]ScoredChunk, 0, len(expanded))
	for _, sc := range expanded {
		result = append(result, sc)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Score > result[j].Score })
	// Enforce token budget walk higest scored chunk first, stop when full
	used := 0
	for i, sc := range result {
		used += sc.Chunk.Tokens
		if used > budget {
			return result[:i]
		}
	}
	return result
}

func (cb *ContextBuilder) buildContextWithoutGraph(candidates []ScoredChunk, budget int) []ScoredChunk {
	if len(candidates) < 20 {
		return cb.applyBudget(candidates, budget)
	}

	fileGroups := make(map[string][]ScoredChunk)
	for _, c := range candidates {
		fileGroups[c.File] = append(fileGroups[c.File], c)
	}
	type hotFile struct {
		file     string
		avgScore float64
	}
	hot := make([]hotFile, 0)
	for file, chunks := range fileGroups {
		if len(chunks) >= 3 {
			sum := 0.0
			for _, c := range chunks {
				sum += c.Score
			}
			hot = append(hot, hotFile{file, sum / float64(len(chunks))})
		}
	}
	sort.Slice(hot, func(i, j int) bool { return hot[i].avgScore > hot[j].avgScore })
	hotMap := make(map[string]float64, len(hot))
	for _, h := range hot {
		hotMap[h.file] = h.avgScore
	}
	candidatesCopy := make([]ScoredChunk, len(candidates))
	copy(candidatesCopy, candidates)
	for i := range candidatesCopy {
		if _, ok := hotMap[candidatesCopy[i].File]; ok {
			candidatesCopy[i].Score += 0.2
		}
	}
	sort.Slice(candidatesCopy, func(i, j int) bool { return candidatesCopy[i].Score > candidatesCopy[j].Score })
	return candidatesCopy
}

func (cb *ContextBuilder) assembleContext(chunks []Chunk) *types.ContextWindow {
	totalTokens := 0
	sources := make(map[string]bool, len(chunks))
	ctxChunks := make([]types.ContextChunk, len(chunks))
	for i, c := range chunks {
		totalTokens += c.Tokens + 20
		sources[c.File] = true
		ctxChunks[i] = types.ContextChunk{
			File:       c.File,
			StartLine:  c.StartLine,
			EndLine:    c.EndLine,
			Content:    c.Content,
			Importance: c.Importance,
		}
	}
	srcList := make([]string, 0, len(sources))
	for s := range sources {
		srcList = append(srcList, s)
	}
	return &types.ContextWindow{Chunks: ctxChunks, TotalTokens: totalTokens, Sources: srcList}
}

//  KB helpers

func (cb *ContextBuilder) kbExactLookup(query string, intent QueryIntent) []ScoredChunk {
	if !cb.hasKB {
		return nil
	}
	potentialSymbols := extractPotentialSymbols(query)
	scored := make([]ScoredChunk, 0)
	matchedIDs := make(map[string]bool)

	for _, symbol := range potentialSymbols {
		symLow := strings.ToLower(symbol)

		if cb.kbIdx != nil {
			if locs, ok := cb.kbIdx.FunctionsByName[symbol]; ok {
				for _, loc := range locs {
					if !matchedIDs[loc] {
						matchedIDs[loc] = true
						if chunk := cb.findChunkForLocation(loc); chunk != nil {
							scored = append(scored, ScoredChunk{
								Chunk: *chunk, Score: 120.0,
								MatchType:    "kb_exact",
								MatchDetails: "KB index: " + symbol,
							})
						}
					}
				}
			}
		}

		for filePath, fs := range cb.kbData.Structure {
			for _, fn := range fs.Functions {
				if strings.ToLower(fn.Name) == symLow {
					id := fmt.Sprintf("%s:%d-%d", filePath, fn.LineStart, fn.LineEnd)
					if !matchedIDs[id] {
						matchedIDs[id] = true
						chunk := cb.buildChunkFromKBFunction(fn, filePath)
						scored = append(scored, ScoredChunk{
							Chunk: chunk, Score: 115.0,
							MatchType:    "kb_function",
							MatchDetails: "Function: " + fn.Name,
						})
						if intent.Type == IntentCallees {
							scored = append(scored, cb.expandFromKBFunction(fn, filePath, 110.0)...)
						}
					}
				}
			}
			for _, class := range fs.Classes {
				if strings.ToLower(class.Name) == symLow {
					id := fmt.Sprintf("%s:%d-%d", filePath, class.LineStart, class.LineEnd)
					if !matchedIDs[id] {
						matchedIDs[id] = true
						scored = append(scored, ScoredChunk{
							Chunk: cb.buildChunkFromKBClass(class, filePath), Score: 118.0,
							MatchType: "kb_class", MatchDetails: "Class: " + class.Name,
						})
					}
				}
				for _, method := range class.Methods {
					if strings.ToLower(method.Name) == symLow {
						id := fmt.Sprintf("%s:%d-%d", filePath, method.LineStart, method.LineEnd)
						if !matchedIDs[id] {
							matchedIDs[id] = true
							scored = append(scored, ScoredChunk{
								Chunk: cb.buildChunkFromKBFunction(method, filePath), Score: 113.0,
								MatchType: "kb_method", MatchDetails: "Method: " + class.Name + "." + method.Name,
							})
							scored = append(scored, ScoredChunk{
								Chunk: cb.buildChunkFromKBClass(class, filePath), Score: 95.0,
								Distance: 1, MatchType: "kb_parent_class",
								MatchDetails: "Parent class of " + method.Name,
							})
						}
					}
				}
			}
		}
	}
	return scored
}

func (cb *ContextBuilder) findChunkForLocation(loc string) *Chunk {
	parts := strings.Split(loc, ":")
	if len(parts) < 2 {
		return nil
	}
	file := parts[0]
	if strings.Contains(parts[1], "-") {
		rp := strings.Split(parts[1], "-")
		if len(rp) == 2 {
			var s, e int
			fmt.Sscanf(rp[0], "%d", &s)
			fmt.Sscanf(rp[1], "%d", &e)
			for i := range cb.chunks {
				if cb.chunks[i].File == file && cb.chunks[i].StartLine <= e && cb.chunks[i].EndLine >= s {
					return &cb.chunks[i]
				}
			}
		}
	} else {
		var line int
		fmt.Sscanf(parts[1], "%d", &line)
		for i := range cb.chunks {
			if cb.chunks[i].File == file && cb.chunks[i].StartLine <= line && cb.chunks[i].EndLine >= line {
				return &cb.chunks[i]
			}
		}
	}
	return nil
}

func (cb *ContextBuilder) buildChunkFromKBFunction(fn KBFunction, filePath string) Chunk {
	content := fmt.Sprintf("Function: %s\nSignature: %s\nLines: %d-%d\n",
		fn.Name, fn.Signature, fn.LineStart, fn.LineEnd)
	if fn.Docstring != "" {
		content += "Documentation: " + fn.Docstring + "\n"
	}
	if len(fn.Calls) > 0 {
		content += "Calls:\n"
		for _, c := range fn.Calls {
			content += fmt.Sprintf("  - %s (line %d)\n", c.Callee, c.Line)
		}
	}
	return Chunk{
		ID:        fmt.Sprintf("%s:%d-%d", filePath, fn.LineStart, fn.LineEnd),
		ChunkType: "function", File: filePath,
		StartLine: fn.LineStart, EndLine: fn.LineEnd,
		Content: content, Tokens: len(content) / 4,
		Symbols: []string{fn.Name}, Name: fn.Name, Importance: 0.9,
	}
}

func (cb *ContextBuilder) buildChunkFromKBClass(class KBClass, filePath string) Chunk {
	content := fmt.Sprintf("Class: %s\nLines: %d-%d\n", class.Name, class.LineStart, class.LineEnd)
	if class.Docstring != "" {
		content += "Documentation: " + class.Docstring + "\n"
	}
	if len(class.Methods) > 0 {
		content += "Methods:\n"
		for _, m := range class.Methods {
			content += fmt.Sprintf("  - %s (lines %d-%d)\n", m.Name, m.LineStart, m.LineEnd)
		}
	}
	syms := []string{class.Name}
	for _, m := range class.Methods {
		syms = append(syms, m.Name)
	}
	return Chunk{
		ID:        fmt.Sprintf("%s:%d-%d", filePath, class.LineStart, class.LineEnd),
		ChunkType: "class", File: filePath,
		StartLine: class.LineStart, EndLine: class.LineEnd,
		Content: content, Tokens: len(content) / 4,
		Symbols: syms, Name: class.Name, Importance: 0.95,
	}
}

func (cb *ContextBuilder) expandFromKBFunction(fn KBFunction, filePath string, baseScore float64) []ScoredChunk {
	exp := make([]ScoredChunk, 0)
	const maxCallees = 5
	for _, call := range fn.Calls {
		if len(exp) >= maxCallees {
			break
		}
		if call.DefinedIn == "" {
			continue
		}
		if fs, ok := cb.kbData.Structure[call.DefinedIn]; ok {
			for _, calledFn := range fs.Functions {
				if calledFn.Name == call.Callee {
					score := baseScore * 0.75
					// Reward calles that live in same file as the caller
					// they are cheaper to include and more likely to be relevant.
					if call.DefinedIn == filePath {
						score *= 1.03
					}
					exp = append(exp, ScoredChunk{
						Chunk:        cb.buildChunkFromKBFunction(calledFn, call.DefinedIn),
						Score:        score,
						Distance:     1,
						MatchType:    "kb_called",
						MatchDetails: "Called by " + fn.Name,
					})
					break
				}
			}
		}
	}
	return exp
}

// applyBudget truncates a pre-sorted slice to fit within the token budget.
func (cb *ContextBuilder) applyBudget(chunks []ScoredChunk, budget int) []ScoredChunk {
	used := 0
	for i, sc := range chunks {
		used += sc.Chunk.Tokens
		if used > budget {
			return chunks[:i]
		}
	}
	return chunks
}

//  Exact / partial symbol search

func (cb *ContextBuilder) exactSymbolSearch(query string) []ScoredChunk {
	potSyms := extractPotentialSymbols(query)
	scored := make([]ScoredChunk, 0)
	for _, chunk := range cb.chunks {
		nameLow := strings.ToLower(chunk.Name)
		for _, qs := range potSyms {
			if nameLow == strings.ToLower(qs) {
				scored = append(scored, ScoredChunk{
					Chunk: chunk, Score: 100.0,
					MatchDetails: "Exact name: " + chunk.Name,
				})
				break
			}
		}
		for _, sym := range chunk.Symbols {
			symLow := strings.ToLower(sym)
			for _, qs := range potSyms {
				if symLow == strings.ToLower(qs) {
					scored = append(scored, ScoredChunk{
						Chunk: chunk, Score: 90.0,
						MatchDetails: "Symbol: " + sym,
					})
					break
				}
			}
		}
	}
	return scored
}

func (cb *ContextBuilder) partialIdentifierMatch(query string) []ScoredChunk {
	qTokens := extractPotentialSymbols(query)
	scored := make([]ScoredChunk, 0)
	seen := make(map[string]bool)

	for _, chunk := range cb.chunks {
		cTokens := splitIdentifierToTokens(chunk.Name)
		cNameLow := strings.ToLower(chunk.Name)
		matchCount, totalScore := 0, 0.0
		matchedToks := []string{}

		for _, qt := range qTokens {
			qtLow := strings.ToLower(qt)
			for _, ct := range cTokens {
				if ct == qtLow {
					matchCount++
					totalScore += 15.0
					matchedToks = append(matchedToks, ct)
					break
				}
			}
			if strings.Contains(cNameLow, qtLow) && matchCount == 0 {
				totalScore += 8.0
				matchedToks = append(matchedToks, qtLow)
			}
		}
		for _, sym := range chunk.Symbols {
			symToks := splitIdentifierToTokens(sym)
			symLow := strings.ToLower(sym)
			for _, qt := range qTokens {
				qtLow := strings.ToLower(qt)
				for _, st := range symToks {
					if st == qtLow {
						matchCount++
						totalScore += 12.0
						matchedToks = append(matchedToks, st)
						break
					}
				}
				if strings.Contains(symLow, qtLow) && matchCount == 0 {
					totalScore += 6.0
					matchedToks = append(matchedToks, qtLow)
				}
			}
		}
		if (matchCount >= 2 || (matchCount == 1 && totalScore >= 15)) && !seen[chunk.ID] {
			seen[chunk.ID] = true
			scored = append(scored, ScoredChunk{
				Chunk: chunk, Score: totalScore,
				MatchDetails: "Partial: " + strings.Join(uniqueStrings(matchedToks), ", "),
			})
		}
	}
	return scored
}

func (cb *ContextBuilder) keywordSearch(query string, topK int) []ScoredChunk {
	qLow := strings.ToLower(query)
	keywords := extractQueryKeywords(qLow)
	potSyms := extractPotentialSymbols(query)
	scored := make([]ScoredChunk, 0)

	for _, chunk := range cb.chunks {
		score := 0.0
		content := chunk.Content
		if content == "" {
			content = cb.hydrateOne(chunk)
		}
		contentLow := strings.ToLower(content)
		nameLow := strings.ToLower(chunk.Name)
		details := []string{}

		for _, qs := range potSyms {
			qsLow := strings.ToLower(qs)
			if nameLow == qsLow {
				score += 20.0
				details = append(details, "name="+chunk.Name)
			} else if strings.Contains(nameLow, qsLow) {
				score += 10.0
				details = append(details, "name~"+qs)
			}
			// Penalise _sym* matches
			if strings.HasPrefix(nameLow, "_"+qsLow) {
				score -= 10.0
			}
		}
		for _, sym := range chunk.Symbols {
			symLow := strings.ToLower(sym)
			for _, qs := range potSyms {
				qsLow := strings.ToLower(qs)
				if symLow == qsLow {
					score += 15.0
					details = append(details, "symbol="+sym)
				} else if strings.Contains(symLow, qsLow) {
					score += 7.0
				}
			}
			for _, kw := range keywords {
				if symLow == kw {
					score += 10.0
				} else if strings.Contains(symLow, kw) {
					score += 5.0
				}
			}
		}
		for _, kw := range keywords {
			if strings.Contains(contentLow, kw) {
				score += 2.0
			}
		}
		fileLow := strings.ToLower(chunk.File)
		for _, kw := range keywords {
			if strings.Contains(fileLow, kw) {
				score += 1.0
			}
		}
		switch chunk.ChunkType {
		case "function":
			score += 1.0
		case "class":
			score += 0.8
		case "method":
			score += 0.6
		}
		if score > 0 {
			scored = append(scored, ScoredChunk{
				Chunk:        chunk,
				Score:        score,
				MatchDetails: strings.Join(details, ", "),
			})
		}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > topK {
		scored = scored[:topK]
	}
	return scored
}

func (cb *ContextBuilder) vectorSearch(qEmb []float32, topK int, threshold float64) []ScoredChunk {
	scored := make([]ScoredChunk, 0)
	for i, chunkEmb := range cb.embeddings {
		if i >= len(cb.chunks) {
			break
		}
		if sim := cosineSimilarity(qEmb, chunkEmb); sim >= threshold {
			scored = append(scored, ScoredChunk{Chunk: cb.chunks[i], Score: sim})
		}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > topK {
		scored = scored[:topK]
	}
	return scored
}

func (cb *ContextBuilder) selectChunks(scored []ScoredChunk, budget int) []Chunk {
	selected := make([]Chunk, 0)
	tokenSum := 0
	hdr := 20

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].File != scored[j].File {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].StartLine < scored[j].StartLine
	})
	for _, sc := range scored {
		if tokenSum+sc.Tokens+hdr > budget {
			break
		}
		if n := len(selected); n > 0 && canMerge(selected[n-1], sc.Chunk) {
			selected[n-1] = mergeChunks(selected[n-1], sc.Chunk)
			tokenSum += sc.Tokens
		} else {
			selected = append(selected, sc.Chunk)
			tokenSum += sc.Tokens + hdr
		}
	}
	return selected
}

func (cb *ContextBuilder) Close() error { return nil }

//  Pure helpers

func extractSymbolsFromContent(content, name string) []string {
	syms := []string{}
	if name != "" {
		syms = append(syms, name)
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			parts := strings.Split(line, " (")
			if len(parts) >= 1 {
				fn := strings.TrimSpace(strings.TrimPrefix(parts[0], "- "))
				if fn != "" && fn != "..." {
					syms = append(syms, fn)
				}
			}
		}
	}
	return syms
}

func calculateImportance(chunkType string, complexity int) float64 {
	base := 0.5
	switch chunkType {
	case "function":
		base = 0.7
	case "class":
		base = 0.8
	case "method":
		base = 0.6
	case "file":
		base = 0.4
	}
	if complexity > 5 {
		base += 0.1
	}
	if complexity > 10 {
		base += 0.1
	}
	if base > 1.0 {
		base = 1.0
	}
	return base
}

func extractQueryKeywords(queryLower string) []string {
	stop := map[string]bool{
		"how": true, "does": true, "the": true, "a": true, "an": true,
		"is": true, "are": true, "what": true, "where": true, "when": true,
		"can": true, "will": true, "should": true, "would": true, "could": true,
		"this": true, "that": true, "these": true, "those": true, "of": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "with": true,
	}
	words := strings.FieldsFunc(queryLower, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '!' || r == '?' ||
			r == ';' || r == ':' || r == '(' || r == ')' || r == '[' || r == ']'
	})
	kws := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.Trim(w, "\"'")
		if len(w) > 2 && !stop[w] {
			kws = append(kws, w)
			if strings.Contains(w, "_") {
				for _, p := range strings.Split(w, "_") {
					if len(p) > 2 && !stop[p] {
						kws = append(kws, p)
					}
				}
			}
		}
	}
	return kws
}

func splitIdentifierToTokens(s string) []string {
	toks := []string{}
	for _, part := range strings.Split(s, "_") {
		start := 0
		for i := 1; i < len(part); i++ {
			if unicode.IsUpper(rune(part[i])) {
				if tok := strings.ToLower(part[start:i]); tok != "" {
					toks = append(toks, tok)
				}
				start = i
			}
		}
		if tok := strings.ToLower(part[start:]); tok != "" {
			toks = append(toks, tok)
		}
	}
	return toks
}

// isCodeIdentifier returns true when w looks like a source-code identifier
// rather than a plain English word. Matches snake_case, camelCase, and
// PascalCase with ≥2 uppercase letters (e.g. BuildContext, KBIndex, IVFIndex).
// Plain words like "how", "does", "work" return false.
func isCodeIdentifier(w string) bool {
	// snake_case: load_chunks, kb_index, QUERY_BATCH_SIZE
	if strings.Contains(w, "_") && len(w) > 3 {
		return true
	}
	// camelCase: buildContext, mmrSelect, loadChunksFromKB
	prevLower := false
	for _, r := range w {
		if unicode.IsUpper(r) && prevLower {
			return true
		}
		prevLower = unicode.IsLower(r)
	}
	// PascalCase with multiple capitals: BuildContext, IVFIndex, KBIndex
	upperCount := 0
	for _, r := range w {
		if unicode.IsUpper(r) {
			upperCount++
		}
	}
	return upperCount >= 2
}

// extractPotentialSymbols extracts tokens that look like code identifiers from
// the query. Plain English words are excluded so that queries like
// "how does BuildContext work" don't pollute symbol searches with "how", "does",
// and "work".
func extractPotentialSymbols(query string) []string {
	syms := make([]string, 0)
	for _, w := range strings.Fields(query) {
		w = strings.Trim(w, ".,!?;:'\"()[]{}")
		if len(w) <= 2 || !isCodeIdentifier(w) {
			continue
		}
		syms = append(syms, w)
		syms = append(syms, splitIdentifierToTokens(w)...)
	}
	return uniqueStrings(syms)
}

func uniqueStrings(input []string) []string {
	seen := make(map[string]bool, len(input))
	out := make([]string, 0, len(input))
	for _, s := range input {
		if !seen[s] && s != "" {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func canMerge(a, b Chunk) bool {
	if a.File != b.File {
		return false
	}
	gap := 0
	if a.EndLine < b.StartLine {
		gap = b.StartLine - a.EndLine
	} else if b.EndLine < a.StartLine {
		gap = a.StartLine - b.EndLine
	}
	return gap <= 5
}

func mergeChunks(a, b Chunk) Chunk {
	start, end := a.StartLine, a.EndLine
	if b.StartLine < start {
		start = b.StartLine
	}
	if b.EndLine > end {
		end = b.EndLine
	}
	content := a.Content
	if b.StartLine > a.EndLine {
		content += "\n" + b.Content
	} else if a.StartLine > b.EndLine {
		content = b.Content + "\n" + content
	}
	symMap := make(map[string]bool)
	syms := make([]string, 0)
	for _, s := range append(a.Symbols, b.Symbols...) {
		if !symMap[s] {
			symMap[s] = true
			syms = append(syms, s)
		}
	}
	return Chunk{
		ID: a.ID, ChunkType: a.ChunkType, File: a.File,
		StartLine: start, EndLine: end,
		Content:    content,
		Tokens:     a.Tokens + b.Tokens,
		Symbols:    syms,
		Name:       a.Name,
		Importance: math.Max(a.Importance, b.Importance),
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
