//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package embeddings provides the command-line interface implementation for EULIX.

/*
This file is responsible for the shared types of query package.
*/

package retrieval

import (
	"sync"
	"time"

	"eulix/internal/config"
	"eulix/internal/llm"
	"eulix/internal/utils"
)

type IntentType int
type callSiteIndex map[string][]int // symbol → []chunkIdx

type CallGraphIdx struct {
	Nodes    map[string]*utils.CallGraphNode
	CalledBy map[string][]string
	Calls    map[string][]string
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
type ContextBuilder struct {
	boilerplate *BoilerplateDetector
	callGraph   map[string][]Relationship
	callSites   callSiteIndex
	cgBuild     *CallGraphIdx
	cgRef       *utils.CallGraphRef
	chunks      []Chunk
	config      *config.Config
	debugLog    *utils.DebugLogger
	// depIdx        *depIndex
	embeddings [][]float32
	eulixDir   string
	// externalDeps  []utils.ExternalDependency
	hasCallGraph  bool
	hasEmbeddings bool
	hasKB         bool
	hydrateIdx    map[string]map[[2]int]func() string // file -> (start,end) -> content builder
	invertedIdx   *InvertedIndex                      // non-nil when len(chunks) > invIdxThreshold
	ivfIndex      *IVFIndex                           // non-nil when len(embeddings) > ivfBuildThreshold
	kbData        *utils.KnowledgeBaseSimplifiedRef
	kbIdx         *utils.Indices
	lastTrace     *DebugTrace
	lazyContent   bool
	llmClient     *llm.Client
	mu            sync.Mutex
	noisePaths    []string
	queryEmbedder QueryEmbedder
	sourceRoot    string
	subsystemTree []*SubsystemNode
	symbolIndex   map[string][]int
	vectorMap     map[string]int
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
	ClassName  string
	Name       string
	Importance float64
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
	mu             sync.RWMutex
	Postings       map[string][]Posting
	DocCount       int
	AvgChunkTokens float64
}

// QueryEmbedder abstracts the binary embedding client for testability.
type QueryEmbedder interface {
	EmbedQueryBinary(query string) ([]float32, error)
}

type BoilerplateDetector struct {
	boilerplate map[string]bool
	df          map[string]int
	totalChunks int
}

type Relationship struct {
	Type     string
	Target   string
	Distance int
}

type ScoredChunk struct {
	Chunk
	Distance     int
	FromID       string
	IsExact      bool
	MatchType    string // "exact", "symbol", "semantic", "keyword", "partial"
	MatchDetails string
	Pinned       bool
	Score        float64
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

type fnParts struct {
	name      string
	signature string
	docstring string
	lineStart int
	lineEnd   int
	calls     []callPart // only Callee + Line used
}

type callPart struct {
	callee string
	line   int
}

type classParts struct {
	name      string
	docstring string
	lineStart int
	lineEnd   int
	methods   []methodPart // only Name + LineStart + LineEnd used
}

type methodPart struct {
	name      string
	lineStart int
	lineEnd   int
}
