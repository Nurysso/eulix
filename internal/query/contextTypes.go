//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package embeddings provides the command-line interface implementation for EULIX.

/*
This file is responsible for the shared types of query package.
*/

package query

import (
	"os"
	"sync"
	"time"

	"eulix/internal/cache"
	"eulix/internal/config"
	"eulix/internal/llm"
	"eulix/internal/types"
)

type QueryType int
type IntentType int
type callSiteIndex map[string][]int // symbol → []chunkIdx

type Router struct {
	eulixDir        string
	config          *config.Config
	classifier      *Classifier
	llmClient       *llm.Client
	cache           *cache.Manager
	contextBuilder  *ContextBuilder
	kbIndex         *KBIndex
	callGraph       *CallGraph
	kb              *types.KnowledgeBase
	currentChecksum string
}

type match struct {
	name  string
	score int
	typ   string
}

type todoItem struct {
	file     string
	line     int
	text     string
	priority string
}

type fnEntry struct {
	file string
	fn   *types.KBFunction
}
type KBIndex struct {
	FunctionsByName  map[string][]string `json:"functions_by_name"`
	FunctionsCalling map[string][]string `json:"functions_calling"`
	FunctionsByTag   map[string][]string `json:"functions_by_tag"`
	TypesByName      map[string][]string `json:"types_by_name"`
}

type CallGraph struct {
	Functions map[string]FunctionNode `json:"functions"`
	Types     map[string]TypeNode     `json:"types"`
}

type FunctionNode struct {
	Name     string   `json:"name"`
	Location string   `json:"location"`
	Calls    []string `json:"calls"`
	CalledBy []string `json:"called_by"`
}

type TypeNode struct {
	Name     string   `json:"name"`
	Location string   `json:"location"`
	Methods  []string `json:"methods"`
}

type centralFunction struct {
	name  string
	count int
}

// context_window.go
type ContextBuilder struct {
	eulixDir      string
	config        *config.Config
	llmClient     *llm.Client
	queryEmbedder QueryEmbedder

	// Embeddings (may be nil for very large corpora; use ivfIndex instead)
	hasEmbeddings bool
	embeddings    [][]float32
	embData       *EmbeddingsData

	// Chunks (Content field is empty when lazyContent is true)
	chunks      []Chunk
	callSites   callSiteIndex
	symbolIndex map[string][]int
	vectorMap   map[string]int
	lazyContent bool // true: Content loaded on demand during assembleContext

	// GB-scale indices — built automatically based on corpus size
	ivfIndex    *IVFIndex      // non-nil when len(embeddings) > ivfBuildThreshold
	invertedIdx *InvertedIndex // non-nil when len(chunks) > invIdxThreshold

	// Knowledge base and call graph
	hasKB        bool
	kbData       *types.KnowledgeBase
	hasCallGraph bool
	callGraph    map[string][]Relationship
	kbIdx        *KBIndex
	sourceRoot   string
	debugLog     *DebugLogger

	// Thread-safe trace storage
	mu          sync.Mutex
	lastTrace   *DebugTrace
	boilerplate *BoilerplateDetector
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
type DebugLogger struct {
	file *os.File
	mu   sync.Mutex
}

// QueryIntent is derived from the raw query before any search begins.
// It drives per-strategy budget weights so that, e.g., a pinpoint symbol
// lookup gives almost all budget to KB/exact search rather than semantic.
type QueryIntent struct {
	Type           IntentType
	Symbols        []string
	Keywords       []string
	Specificity    float64 // 0 = broad question, 1 = pinpoint identifier
	Confidence     float64
	RunnerUpType   string
	RunnerUpWeight float64
}

//  Debug / tracing types

// BudgetAllocation records how the token budget was split.
type BudgetAllocation struct {
	MaxTokens       int
	SystemReserve   int
	QueryCost       int
	ResponseReserve int
	ContextBudget   int
	StrategyWeights map[string]float64
}

// StrategyTrace records one search strategy's outcome.
type StrategyTrace struct {
	Name     string
	Duration time.Duration
	Found    int
	TopScore float64
	AvgScore float64
	Skipped  bool
	Reason   string
}

// ChunkTrace records the MMR selection decision for one candidate.
type ChunkTrace struct {
	ID            string
	File          string
	Lines         [2]int
	Tokens        int
	Score         float64
	MatchType     string
	MatchDetails  string
	Rank          int
	Included      bool
	ExcludeReason string
}

// DebugTrace is the full diagnostic for one BuildContext call.
// Access it with cb.GetLastTrace() — no callsite changes required.
type DebugTrace struct {
	Query           string
	Intent          QueryIntent
	Budget          BudgetAllocation
	Strategies      []StrategyTrace
	TotalCandidates int
	SelectionMethod string // "mmr" or "greedy"
	ChunkTraces     []ChunkTrace
	TotalTokens     int
	Duration        time.Duration
	Warnings        []string
}

//  Scalability index types

// IVFIndex is an Inverted File Index for sub-linear approximate nearest-neighbour
// search. It partitions embedding space into ivfNClusters cells via k-means; at
// query time only ivfNProbe cells are scanned instead of the full corpus.
type IVFIndex struct {
	Centroids [][]float32 // [nClusters][dim]
	Lists     [][]int32   // [nClusters] → embedding indices in that cell
	NClusters int
	Dim       int
}

// Posting is one entry in the inverted keyword index.
type Posting struct {
	ChunkIdx int
	TF       float32 // normalised term frequency
}

// InvertedIndex maps lowercase terms to sorted posting lists.
// Lookup cost is O(|unique query terms|) rather than O(n_chunks × |terms|).
type InvertedIndex struct {
	mu       sync.RWMutex
	Postings map[string][]Posting
	DocCount int
}

// QueryEmbedder abstracts the binary embedding client for testability.
type QueryEmbedder interface {
	EmbedQueryBinary(query string) ([]float32, error)
}

type BoilerplateDetector struct {
	// symbols whose document-frequency ratio exceeds the threshold
	boilerplate map[string]bool
	// raw DF counts, kept for diagnostics / threshold tuning
	df map[string]int
	// total number of chunks the detector was built from
	totalChunks int
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
	MatchType    string // "exact", "symbol", "semantic", "keyword", "partial"
	MatchDetails string
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

type VectorStoreHeader struct {
	Version   uint32
	Count     uint64
	Dimension uint32
}
