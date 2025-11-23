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
	"eulix/internal/llm"
	"eulix/internal/types"
)

type ContextBuilder struct {
	eulixDir      string
	config        *config.Config
	llmClient     *llm.Client
	queryEmbedder *embeddings.QueryEmbedder
	embeddings    [][]float32
	chunks        []Chunk
	callGraph     map[string][]Relationship
	hasCallGraph  bool
	hasEmbeddings bool
	kbData        *EmbeddingsData
}

type Chunk struct {
	ID         string
	ChunkType  string
	File       string
	StartLine  int
	EndLine    int
	Content    string
	Tokens     int
	Symbols    []string
	Name       string
	Importance float64
}

type Relationship struct {
	Type     string
	Target   string
	Distance int
}

type ScoredChunk struct {
	Chunk
	Score        float64
	Distance     int
	FromID       string
	MatchType    string  // "exact", "symbol", "semantic", "keyword"
	MatchDetails string  // What matched
}

type EmbeddingsData struct {
	Model       string           `json:"model"`
	Dimension   int              `json:"dimension"`
	TotalChunks int              `json:"total_chunks"`
	Embeddings  []EmbeddingChunk `json:"embeddings"`
}

type EmbeddingChunk struct {
	ID        string   `json:"id"`
	ChunkType string   `json:"chunk_type"`
	Content   string   `json:"content"`
	Metadata  Metadata `json:"metadata"`
}

type Metadata struct {
	FilePath   string `json:"file_path"`
	Language   string `json:"language"`
	LineStart  int    `json:"line_start"`
	LineEnd    int    `json:"line_end"`
	Name       string `json:"name"`
	Complexity int    `json:"complexity"`
}

type CallGraphData struct {
	Functions map[string]struct {
		Calls    []string `json:"calls"`
		CalledBy []string `json:"called_by"`
	} `json:"functions"`
}

func ContextWindowCreator(eulixDir string, cfg *config.Config, llmClient *llm.Client) (*ContextBuilder, error) {
	cb := &ContextBuilder{
		eulixDir:  eulixDir,
		config:    cfg,
		llmClient: llmClient,
	}

	// Initialize query embedder
	eulixBinaryPath := filepath.Join(eulixDir, "..", "eulix_embed")
	cb.queryEmbedder = embeddings.VectorWeaver(
		eulixBinaryPath,
		cfg.Embeddings.Model,
	)

	// Load pre-computed KB embeddings
	if err := cb.loadEmbeddings(); err != nil {
		fmt.Printf("⚠️  Failed to load embeddings: %v\n", err)
		cb.hasEmbeddings = false
	} else {
		cb.hasEmbeddings = true
	}

	// Load chunks from embeddings.json
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

	if len(data) < 8 {
		return fmt.Errorf("invalid embeddings file: too short")
	}

	numEmbeddings := binary.LittleEndian.Uint32(data[0:4])
	dimension := binary.LittleEndian.Uint32(data[4:8])

	if int(dimension) != cb.config.Embeddings.Dimension {
		return fmt.Errorf("dimension mismatch: expected %d, got %d", cb.config.Embeddings.Dimension, dimension)
	}

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
	embJsonPath := filepath.Join(cb.eulixDir, "embeddings.json")
	data, err := os.ReadFile(embJsonPath)
	if err != nil {
		return fmt.Errorf("failed to read embeddings.json: %w", err)
	}

	var embData EmbeddingsData
	if err := json.Unmarshal(data, &embData); err != nil {
		return fmt.Errorf("failed to parse embeddings.json: %w", err)
	}

	cb.kbData = &embData
	cb.chunks = make([]Chunk, len(embData.Embeddings))

	for i, embChunk := range embData.Embeddings {
		symbols := extractSymbolsFromContent(embChunk.Content, embChunk.Metadata.Name)
		tokens := len(embChunk.Content) / 4

		cb.chunks[i] = Chunk{
			ID:         embChunk.ID,
			ChunkType:  embChunk.ChunkType,
			File:       embChunk.Metadata.FilePath,
			StartLine:  embChunk.Metadata.LineStart,
			EndLine:    embChunk.Metadata.LineEnd,
			Content:    embChunk.Content,
			Tokens:     tokens,
			Symbols:    symbols,
			Name:       embChunk.Metadata.Name,
			Importance: calculateImportance(embChunk.ChunkType, embChunk.Metadata.Complexity),
		}
	}

	return nil
}

func extractSymbolsFromContent(content, name string) []string {
	symbols := []string{}

	if name != "" {
		symbols = append(symbols, name)
	}

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			parts := strings.Split(line, " (")
			if len(parts) >= 1 {
				funcName := strings.TrimPrefix(parts[0], "- ")
				funcName = strings.TrimSpace(funcName)
				if funcName != "" && funcName != "..." {
					symbols = append(symbols, funcName)
				}
			}
		}
	}

	return symbols
}

func calculateImportance(chunkType string, complexity int) float64 {
	baseScore := 0.5

	switch chunkType {
	case "function":
		baseScore = 0.7
	case "class":
		baseScore = 0.8
	case "method":
		baseScore = 0.6
	case "file":
		baseScore = 0.4
	}

	if complexity > 5 {
		baseScore += 0.1
	}
	if complexity > 10 {
		baseScore += 0.1
	}

	if baseScore > 1.0 {
		baseScore = 1.0
	}

	return baseScore
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
	systemPromptTokens := 150
	queryTokens := len(query) / 4
	safetyBuffer := 200
	responseReserve := 2000

	available := cb.config.LLM.MaxTokens - queryTokens - systemPromptTokens - safetyBuffer - responseReserve
	tokenBudget := int(float64(available) * 0.85)

	candidates := cb.multiStrategySearch(query, 100)

	var scored []ScoredChunk

	if cb.hasCallGraph {
		scored = cb.buildContextWithGraph(candidates, tokenBudget)
	} else {
		scored = cb.buildContextWithoutGraph(candidates, tokenBudget)
	}

	selected := cb.selectChunks(scored, tokenBudget)

	return cb.assembleContext(selected), nil
}

// Multi-strategy search that combines exact match, keyword, and semantic search
func (cb *ContextBuilder) multiStrategySearch(query string, topK int) []ScoredChunk {
	allCandidates := make(map[string]ScoredChunk)

	// Strategy 1: Exact symbol match (HIGHEST PRIORITY)
	exactMatches := cb.exactSymbolSearch(query)
	for _, match := range exactMatches {
		match.MatchType = "exact"
		allCandidates[match.ID] = match
	}

	// Strategy 2: Keyword search (HIGH PRIORITY)
	keywordMatches := cb.keywordSearch(query, topK)
	for _, match := range keywordMatches {
		if existing, exists := allCandidates[match.ID]; exists {
			// Boost score if found by multiple strategies
			match.Score = math.Max(existing.Score, match.Score) + 2.0
			match.MatchType = "exact+keyword"
		} else {
			match.MatchType = "keyword"
		}
		allCandidates[match.ID] = match
	}

	// Strategy 3: Semantic search (if embeddings available)
	if cb.hasEmbeddings {
		queryEmbedding, err := cb.queryEmbedder.EmbedQueryBinary(query)
		if err == nil {
			semanticMatches := cb.vectorSearch(queryEmbedding, topK, 0.5)
			for _, match := range semanticMatches {
				if existing, exists := allCandidates[match.ID]; exists {
					// Combine scores
					match.Score = existing.Score + match.Score*0.5
					if existing.MatchType == "exact" {
						match.MatchType = "exact+semantic"
					} else {
						match.MatchType = "keyword+semantic"
					}
				} else {
					match.MatchType = "semantic"
				}
				allCandidates[match.ID] = match
			}
		}
	}

	// Convert map to slice
	result := make([]ScoredChunk, 0, len(allCandidates))
	for _, chunk := range allCandidates {
		result = append(result, chunk)
	}

	// Sort by score (prioritize exact matches)
	sort.Slice(result, func(i, j int) bool {
		// Exact matches always come first
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

// Exact symbol search for precise function/class lookups
func (cb *ContextBuilder) exactSymbolSearch(query string) []ScoredChunk {
	// queryLower := strings.ToLower(query)
	potentialSymbols := extractPotentialSymbols(query)

	scored := make([]ScoredChunk, 0)

	for _, chunk := range cb.chunks {
		nameLower := strings.ToLower(chunk.Name)

		// Check for exact name match
		for _, querySymbol := range potentialSymbols {
			querySymbolLower := strings.ToLower(querySymbol)

			if nameLower == querySymbolLower {
				scored = append(scored, ScoredChunk{
					Chunk:        chunk,
					Score:        100.0, // Very high score for exact match
					Distance:     0,
					MatchDetails: fmt.Sprintf("Exact match: %s", chunk.Name),
				})
				break
			}
		}

		// Check symbols
		for _, symbol := range chunk.Symbols {
			symbolLower := strings.ToLower(symbol)
			for _, querySymbol := range potentialSymbols {
				querySymbolLower := strings.ToLower(querySymbol)
				if symbolLower == querySymbolLower {
					scored = append(scored, ScoredChunk{
						Chunk:        chunk,
						Score:        90.0,
						Distance:     0,
						MatchDetails: fmt.Sprintf("Symbol match: %s", symbol),
					})
					break
				}
			}
		}
	}

	return scored
}

func (cb *ContextBuilder) keywordSearch(query string, topK int) []ScoredChunk {
	queryLower := strings.ToLower(query)
	keywords := extractQueryKeywords(queryLower)
	potentialSymbols := extractPotentialSymbols(query)

	scored := make([]ScoredChunk, 0)

	for _, chunk := range cb.chunks {
		score := 0.0
		contentLower := strings.ToLower(chunk.Content)
		nameLower := strings.ToLower(chunk.Name)
		matchDetails := []string{}

		// PRIORITY 1: Name matches
		for _, querySymbol := range potentialSymbols {
			querySymbolLower := strings.ToLower(querySymbol)
			if nameLower == querySymbolLower {
				score += 20.0
				matchDetails = append(matchDetails, fmt.Sprintf("name=%s", chunk.Name))
				break
			}
			if strings.Contains(nameLower, querySymbolLower) {
				score += 10.0
				matchDetails = append(matchDetails, fmt.Sprintf("name~%s", querySymbol))
			}
		}

		// PRIORITY 2: Symbol matches
		for _, symbol := range chunk.Symbols {
			symbolLower := strings.ToLower(symbol)

			for _, querySymbol := range potentialSymbols {
				querySymbolLower := strings.ToLower(querySymbol)

				if symbolLower == querySymbolLower {
					score += 15.0
					matchDetails = append(matchDetails, fmt.Sprintf("symbol=%s", symbol))
					break
				}
				if strings.Contains(symbolLower, querySymbolLower) {
					score += 7.0
				}
			}

			for _, keyword := range keywords {
				if symbolLower == keyword {
					score += 10.0
				} else if strings.Contains(symbolLower, keyword) {
					score += 5.0
				}
			}
		}

		// PRIORITY 3: Content keyword matches
		for _, keyword := range keywords {
			if strings.Contains(contentLower, keyword) {
				score += 2.0
				matchDetails = append(matchDetails, fmt.Sprintf("keyword=%s", keyword))
			}
		}

		// PRIORITY 4: File name relevance
		fileLower := strings.ToLower(chunk.File)
		for _, keyword := range keywords {
			if strings.Contains(fileLower, keyword) {
				score += 1.0
			}
		}

		// PRIORITY 5: Chunk type bonus
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
				Distance:     0,
				MatchDetails: strings.Join(matchDetails, ", "),
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

func extractPotentialSymbols(query string) []string {
	symbols := make([]string, 0)
	words := strings.Fields(query)

	for _, word := range words {
		word = strings.Trim(word, ".,!?;:'\"()[]{}")

		if len(word) > 2 {
			// Add snake_case and CamelCase identifiers
			if strings.Contains(word, "_") || hasUpperCase(word) {
				symbols = append(symbols, word)
			}

			lowerWord := strings.ToLower(word)
			// Common function prefixes
			if strings.HasSuffix(lowerWord, "ed") ||
				strings.HasPrefix(lowerWord, "get") ||
				strings.HasPrefix(lowerWord, "set") ||
				strings.HasPrefix(lowerWord, "create") ||
				strings.HasPrefix(lowerWord, "delete") ||
				strings.HasPrefix(lowerWord, "remove") ||
				strings.HasPrefix(lowerWord, "update") ||
				strings.HasPrefix(lowerWord, "handle") ||
				strings.HasPrefix(lowerWord, "init") ||
				strings.HasPrefix(lowerWord, "download") ||
				strings.HasPrefix(lowerWord, "upload") ||
				strings.HasPrefix(lowerWord, "process") ||
				strings.HasPrefix(lowerWord, "add") ||
				strings.HasPrefix(lowerWord, "build") ||
				strings.HasPrefix(lowerWord, "setup") {
				symbols = append(symbols, word)
			}
		}
	}

	return symbols
}

func hasUpperCase(s string) bool {
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
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

func (cb *ContextBuilder) buildContextWithGraph(candidates []ScoredChunk, budget int) []ScoredChunk {
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

	result := make([]ScoredChunk, 0, len(expanded))
	for _, sc := range expanded {
		result = append(result, sc)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})

	return result
}

func (cb *ContextBuilder) buildContextWithoutGraph(candidates []ScoredChunk, budget int) []ScoredChunk {
	if len(candidates) < 20 {
		return candidates
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
	return nil
}

// Helper functions
func extractQueryKeywords(queryLower string) []string {
	stopWords := map[string]bool{
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

	keywords := make([]string, 0)

	for _, word := range words {
		word = strings.Trim(word, "\"'")

		if len(word) > 2 && !stopWords[word] {
			keywords = append(keywords, word)

			if strings.Contains(word, "_") {
				parts := strings.Split(word, "_")
				for _, part := range parts {
					if len(part) > 2 && !stopWords[part] {
						keywords = append(keywords, part)
					}
				}
			}
		}
	}

	return keywords
}

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
		ChunkType:  a.ChunkType,
		File:       a.File,
		StartLine:  startLine,
		EndLine:    endLine,
		Content:    content,
		Tokens:     a.Tokens + b.Tokens,
		Symbols:    symbols,
		Name:       a.Name,
		Importance: math.Max(a.Importance, b.Importance),
	}
}
