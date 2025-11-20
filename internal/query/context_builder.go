package query

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"eulix/internal/config"
	"eulix/internal/embeddings"
	"eulix/internal/types"
)

type ContextBuilder struct {
	eulixDir     string
	config       *config.Config
	embedder     *embeddings.Embedder
	embeddings   [][]float32
	chunks       []Chunk
	callGraph    map[string][]Relationship
	hasCallGraph bool
	kbData       *KBData
}

type Chunk struct {
	ID         string
	File       string
	StartLine  int
	EndLine    int
	Content    string
	Tokens     int
	Symbols    []string
	Importance float64
}

type Relationship struct {
	Type     string
	Target   string
	Distance int
}

type ScoredChunk struct {
	Chunk
	Score    float64
	Distance int
	FromID   string
}

type KBData struct {
	Chunks []struct {
		ID        string   `json:"id"`
		File      string   `json:"file"`
		StartLine int      `json:"start_line"`
		EndLine   int      `json:"end_line"`
		Content   string   `json:"content"`
		Tokens    int      `json:"tokens"`
		Symbols   []string `json:"symbols"`
	} `json:"chunks"`
}

type CallGraphData struct {
	Functions map[string]struct {
		Calls    []string `json:"calls"`
		CalledBy []string `json:"called_by"`
	} `json:"functions"`
}

func NewContextBuilder(eulixDir string, cfg *config.Config) (*ContextBuilder, error) {
	// Initialize embedder for query embedding
	embedder, err := embeddings.NewEmbedder(
		cfg.Embeddings.Model,
		cfg.Embeddings.Backend,
		cfg.Embeddings.Dimension,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedder: %w", err)
	}

	cb := &ContextBuilder{
		eulixDir: eulixDir,
		config:   cfg,
		embedder: embedder,
	}

	// Load pre-computed embeddings from Rust binary
	if err := cb.loadEmbeddings(); err != nil {
		return nil, fmt.Errorf("failed to load embeddings: %w", err)
	}

	// Load chunks
	if err := cb.loadChunks(); err != nil {
		return nil, fmt.Errorf("failed to load chunks: %w", err)
	}

	// Try to load call graph
	cb.loadCallGraph()

	return cb, nil
}

func (cb *ContextBuilder) loadEmbeddings() error {
	embPath := filepath.Join(cb.eulixDir, "embeddings.bin")

	data, err := os.ReadFile(embPath)
	if err != nil {
		return err
	}

	// Binary format from Rust: [num_embeddings:u32][dimension:u32][float32...]
	if len(data) < 8 {
		return fmt.Errorf("invalid embeddings file: too short")
	}

	// Read header (little-endian)
	numEmbeddings := binary.LittleEndian.Uint32(data[0:4])
	dimension := binary.LittleEndian.Uint32(data[4:8])

	if int(dimension) != cb.config.Embeddings.Dimension {
		return fmt.Errorf("dimension mismatch: expected %d, got %d", cb.config.Embeddings.Dimension, dimension)
	}

	// Read embeddings
	cb.embeddings = make([][]float32, numEmbeddings)
	offset := 8

	for i := 0; i < int(numEmbeddings); i++ {
		embedding := make([]float32, dimension)
		for j := 0; j < int(dimension); j++ {
			bits := binary.LittleEndian.Uint32(data[offset : offset+4])
			embedding[j] = math.Float32frombits(bits)
			offset += 4
		}
		cb.embeddings[i] = embedding
	}

	return nil
}

func (cb *ContextBuilder) loadChunks() error {
	kbPath := filepath.Join(cb.eulixDir, "kb.json")
	data, err := os.ReadFile(kbPath)
	if err != nil {
		return err
	}

	var kbData KBData
	if err := json.Unmarshal(data, &kbData); err != nil {
		return err
	}

	cb.kbData = &kbData
	cb.chunks = make([]Chunk, len(kbData.Chunks))

	for i, kbChunk := range kbData.Chunks {
		cb.chunks[i] = Chunk{
			ID:         kbChunk.ID,
			File:       kbChunk.File,
			StartLine:  kbChunk.StartLine,
			EndLine:    kbChunk.EndLine,
			Content:    kbChunk.Content,
			Tokens:     kbChunk.Tokens,
			Symbols:    kbChunk.Symbols,
			Importance: 0.5,
		}
	}

	return nil
}

func (cb *ContextBuilder) loadCallGraph() {
	graphPath := filepath.Join(cb.eulixDir, "kb_call_graph.json")
	data, err := os.ReadFile(graphPath)
	if err != nil {
		cb.hasCallGraph = false
		return
	}

	var graphData CallGraphData
	if err := json.Unmarshal(data, &graphData); err != nil {
		cb.hasCallGraph = false
		return
	}

	cb.callGraph = make(map[string][]Relationship)

	for funcName, funcData := range graphData.Functions {
		relationships := make([]Relationship, 0)

		for _, callee := range funcData.Calls {
			relationships = append(relationships, Relationship{
				Type:     "calls",
				Target:   callee,
				Distance: 1,
			})
		}

		for _, caller := range funcData.CalledBy {
			relationships = append(relationships, Relationship{
				Type:     "called_by",
				Target:   caller,
				Distance: 1,
			})
		}

		cb.callGraph[funcName] = relationships
	}

	cb.hasCallGraph = len(cb.callGraph) > 0
}

func (cb *ContextBuilder) BuildContext(query string) (*types.ContextWindow, error) {
	// Calculate token budget
	systemPromptTokens := 150
	queryTokens := len(query) / 4
	safetyBuffer := 200
	responseReserve := 2000

	available := cb.config.LLM.MaxTokens - queryTokens - systemPromptTokens - safetyBuffer - responseReserve
	tokenBudget := int(float64(available) * 0.85)

	// Embed query using ONNX
	queryEmbedding, err := cb.embedQuery(query)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	// Vector search against pre-computed embeddings
	candidates := cb.vectorSearch(queryEmbedding, 100, 0.6)

	var scored []ScoredChunk

	if cb.hasCallGraph {
		scored = cb.buildContextWithGraph(candidates, queryEmbedding, tokenBudget)
	} else {
		scored = cb.buildContextWithoutGraph(candidates, queryEmbedding, tokenBudget)
	}

	selected := cb.selectChunks(scored, tokenBudget)

	return cb.assembleContext(selected), nil
}

func (cb *ContextBuilder) embedQuery(query string) ([]float32, error) {
	return cb.embedder.Embed(query)
}

func (cb *ContextBuilder) vectorSearch(queryEmb []float32, topK int, threshold float64) []ScoredChunk {
	scored := make([]ScoredChunk, 0)

	for i, chunkEmb := range cb.embeddings {
		if i >= len(cb.chunks) {
			break
		}

		similarity := cosineSimilarity(queryEmb, chunkEmb)
		if similarity >= threshold {
			scored = append(scored, ScoredChunk{
				Chunk:    cb.chunks[i],
				Score:    similarity,
				Distance: 0,
			})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	if len(scored) > topK {
		scored = scored[:topK]
	}

	return scored
}

func (cb *ContextBuilder) buildContextWithGraph(candidates []ScoredChunk, queryEmb []float32, budget int) []ScoredChunk {
	expanded := make(map[string]ScoredChunk)

	for _, c := range candidates {
		expanded[c.ID] = c
	}

	topN := 20
	if len(candidates) < topN {
		topN = len(candidates)
	}

	for i := 0; i < topN; i++ {
		candidate := candidates[i]

		for _, symbol := range candidate.Symbols {
			if rels, exists := cb.callGraph[symbol]; exists {
				for _, rel := range rels {
					score := candidate.Score

					if rel.Type == "calls" || rel.Type == "called_by" {
						score *= 0.9
						rel.Distance = 1
					} else if rel.Distance <= 2 {
						score *= 0.6
					} else {
						continue
					}

					for _, chunk := range cb.chunks {
						if contains(chunk.Symbols, rel.Target) {
							if existing, exists := expanded[chunk.ID]; !exists || score > existing.Score {
								expanded[chunk.ID] = ScoredChunk{
									Chunk:    chunk,
									Score:    score,
									Distance: rel.Distance,
									FromID:   candidate.ID,
								}
							}
							break
						}
					}
				}
			}
		}
	}

	for id, sc := range expanded {
		fileBoost := 0.0
		for i := 0; i < 5 && i < len(candidates); i++ {
			if sc.File == candidates[i].File {
				fileBoost += 0.3
				break
			}
		}
		sc.Score += fileBoost
		expanded[id] = sc
	}

	result := make([]ScoredChunk, 0, len(expanded))
	for _, sc := range expanded {
		result = append(result, sc)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})

	return result
}

func (cb *ContextBuilder) buildContextWithoutGraph(candidates []ScoredChunk, queryEmb []float32, budget int) []ScoredChunk {
	if len(candidates) < 200 {
		moreCandidates := cb.vectorSearch(queryEmb, 200, 0.5)
		candidates = moreCandidates
	}

	fileGroups := make(map[string][]ScoredChunk)
	for _, c := range candidates {
		fileGroups[c.File] = append(fileGroups[c.File], c)
	}

	type hotFile struct {
		file     string
		avgScore float64
		count    int
	}

	hotFiles := make([]hotFile, 0)
	for file, chunks := range fileGroups {
		if len(chunks) >= 3 {
			sum := 0.0
			for _, c := range chunks {
				sum += c.Score
			}
			hotFiles = append(hotFiles, hotFile{
				file:     file,
				avgScore: sum / float64(len(chunks)),
				count:    len(chunks),
			})
		}
	}

	sort.Slice(hotFiles, func(i, j int) bool {
		return hotFiles[i].avgScore > hotFiles[j].avgScore
	})

	hotFileMap := make(map[string]float64)
	for _, hf := range hotFiles {
		hotFileMap[hf.file] = hf.avgScore
	}

	for i := range candidates {
		if _, exists := hotFileMap[candidates[i].File]; exists {
			candidates[i].Score += 0.2

			fileChunkCount := len(fileGroups[candidates[i].File])
			if fileChunkCount >= 5 {
				candidates[i].Score += 0.1
			}
		}

		for _, hf := range hotFiles {
			similarity := pathSimilarity(candidates[i].File, hf.file)
			if similarity > 0.7 {
				candidates[i].Score += 0.2
			} else if similarity > 0.4 {
				candidates[i].Score += 0.1
			}
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	return candidates
}

func (cb *ContextBuilder) selectChunks(scored []ScoredChunk, budget int) []Chunk {
	selected := make([]Chunk, 0)
	currentTokens := 0
	headerOverhead := 20
	coveredFiles := make(map[string]bool)

	for _, sc := range scored {
		chunkTokens := sc.Tokens + headerOverhead

		if currentTokens+chunkTokens > budget {
			break
		}

		if len(selected) > 0 {
			last := selected[len(selected)-1]
			if canMerge(last, sc.Chunk) {
				merged := mergeChunks(last, sc.Chunk)
				selected[len(selected)-1] = merged
				currentTokens += chunkTokens - headerOverhead
				continue
			}
		}

		selected = append(selected, sc.Chunk)
		currentTokens += chunkTokens
		coveredFiles[sc.File] = true
	}

	if len(coveredFiles) < 3 && len(scored) > len(selected) {
		for _, sc := range scored {
			if len(coveredFiles) >= 3 {
				break
			}
			if !coveredFiles[sc.File] {
				chunkTokens := sc.Tokens + headerOverhead
				if currentTokens+chunkTokens <= budget {
					selected = append(selected, sc.Chunk)
					currentTokens += chunkTokens
					coveredFiles[sc.File] = true
				}
			}
		}
	}

	return selected
}

func (cb *ContextBuilder) assembleContext(chunks []Chunk) *types.ContextWindow {
	totalTokens := 0
	sources := make(map[string]bool)

	contextChunks := make([]types.ContextChunk, len(chunks))
	for i, chunk := range chunks {
		totalTokens += chunk.Tokens + 20
		sources[chunk.File] = true

		contextChunks[i] = types.ContextChunk{
			File:       chunk.File,
			StartLine:  chunk.StartLine,
			EndLine:    chunk.EndLine,
			Content:    chunk.Content,
			Importance: chunk.Importance,
		}
	}

	sourceList := make([]string, 0, len(sources))
	for source := range sources {
		sourceList = append(sourceList, source)
	}

	return &types.ContextWindow{
		Chunks:      contextChunks,
		TotalTokens: totalTokens,
		Sources:     sourceList,
	}
}

func (cb *ContextBuilder) Close() error {
	if cb.embedder != nil {
		return cb.embedder.Close()
	}
	return nil
}

// Helper functions

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0.0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}

	if normA == 0 || normB == 0 {
		return 0.0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
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
	startLine := a.StartLine
	endLine := a.EndLine
	if b.StartLine < startLine {
		startLine = b.StartLine
	}
	if b.EndLine > endLine {
		endLine = b.EndLine
	}

	content := a.Content
	if b.StartLine > a.EndLine {
		content += "\n" + b.Content
	} else if a.StartLine > b.EndLine {
		content = b.Content + "\n" + content
	}

	symbols := make([]string, 0)
	symbolMap := make(map[string]bool)
	for _, s := range append(a.Symbols, b.Symbols...) {
		if !symbolMap[s] {
			symbols = append(symbols, s)
			symbolMap[s] = true
		}
	}

	return Chunk{
		ID:         a.ID,
		File:       a.File,
		StartLine:  startLine,
		EndLine:    endLine,
		Content:    content,
		Tokens:     a.Tokens + b.Tokens,
		Symbols:    symbols,
		Importance: math.Max(a.Importance, b.Importance),
	}
}

func pathSimilarity(path1, path2 string) float64 {
	parts1 := strings.Split(filepath.Clean(path1), string(filepath.Separator))
	parts2 := strings.Split(filepath.Clean(path2), string(filepath.Separator))

	if len(parts1) > 1 && len(parts2) > 1 {
		dir1 := strings.Join(parts1[:len(parts1)-1], "/")
		dir2 := strings.Join(parts2[:len(parts2)-1], "/")
		if dir1 == dir2 {
			return 1.0
		}
	}

	commonParts := 0
	minLen := len(parts1)
	if len(parts2) < minLen {
		minLen = len(parts2)
	}

	for i := 0; i < minLen; i++ {
		if parts1[i] == parts2[i] {
			commonParts++
		} else {
			break
		}
	}

	if commonParts == 0 {
		return 0.0
	}

	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	return float64(commonParts) / float64(maxLen)
}
