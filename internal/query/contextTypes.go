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
	kb              *KnowledgeBase
	currentChecksum string
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
	kbData       *KnowledgeBase
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
type KnowledgeBase struct {
	Metadata             KBMetadata           `json:"metadata"`
	Structure            map[string]FileData  `json:"structure"`
	CallGraph            KBCallGraph          `json:"call_graph"`
	Indices              KBIndices            `json:"indices"`
	EntryPoints          []EntryPoint         `json:"entry_points"`
	DependencyGraph      DependencyGraph      `json:"dependency_graph"`
	ExternalDependencies []ExternalDependency `json:"external_dependencies"`
	Patterns             PatternInfo          `json:"patterns"`
}

type KBMetadata struct {
	ProjectName    string   `json:"project_name"`
	Version        string   `json:"version"`
	TotalFunctions int      `json:"total_functions"`
	TotalClasses   int      `json:"total_classes"`
	TotalLOC       int      `json:"total_loc"`
	TotalFiles     int      `json:"total_files"`
	Languages      []string `json:"languages"`
	ParsedAt       string   `json:"parsed_at"`
}

type FileData struct {
	Language      string         `json:"language"`
	LOC           int            `json:"loc"`
	Imports       []Import       `json:"imports"`
	Functions     []KBFunction   `json:"functions"`
	Classes       []KBClass      `json:"classes"`
	GlobalVars    []GlobalVar    `json:"global_vars"`
	Todos         []Todo         `json:"todos"`
	SecurityNotes []SecurityNote `json:"security_notes"`
}

type Import struct {
	Module     string   `json:"module"`
	Items      []string `json:"items"`
	ImportType string   `json:"type"` // "external" | "internal"
}

type Attribute struct {
	Name           string  `json:"name"`
	TypeAnnotation string  `json:"type_annotation"`
	Value          *string `json:"value,omitempty"`
}

type GlobalVar struct {
	Name           string  `json:"name"`
	TypeAnnotation string  `json:"type_annotation"`
	Value          *string `json:"value,omitempty"`
	Line           int     `json:"line"`
}

type Todo struct {
	Line     int    `json:"line"`
	Text     string `json:"text"`
	Priority string `json:"priority"` // "high", "medium", "low"
}

type SecurityNote struct {
	NoteType    string `json:"note_type"`
	Line        int    `json:"line"`
	Description string `json:"description"`
}

type KBFunction struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Signature  string      `json:"signature"`
	Docstring  string      `json:"docstring"`
	LineStart  int         `json:"line_start"`
	LineEnd    int         `json:"line_end"`
	Params     []Parameter `json:"params"`
	ReturnType string      `json:"return_type"`

	// Call information
	Calls    []FunctionCall `json:"calls"`
	CalledBy []CallerInfo   `json:"called_by"`

	// Variable tracking
	Variables []Variable `json:"variables"`

	// Control flow
	ControlFlow ControlFlow `json:"control_flow"`

	// Exception handling
	Exceptions ExceptionInfo `json:"exceptions"`

	// Metadata
	Complexity      int                  `json:"complexity"`
	IsAsync         bool                 `json:"is_async"`
	Decorators      []string             `json:"decorators"`
	Tags            []string             `json:"tags"`
	ImportanceScore float32              `json:"importance_score"`
	LangInfo        LanguageSpecificInfo `json:"lang_info,omitempty"`
}

type Parameter struct {
	Name           string  `json:"name"`
	TypeAnnotation string  `json:"type_annotation"`
	DefaultValue   *string `json:"default_value,omitempty"`
}
type FunctionCall struct {
	Callee        string   `json:"callee"`
	DefinedIn     *string  `json:"defined_in,omitempty"` // File path where callee is defined
	Line          int      `json:"line"`
	Args          []string `json:"args"`
	IsConditional bool     `json:"is_conditional"` // Inside if/loop/try block?
	Context       string   `json:"context"`        // "if", "else", "loop", "try", "unconditional"
}

type CallerInfo struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}
type Variable struct {
	Name            string              `json:"name"`
	VarType         *string             `json:"var_type,omitempty"`
	Scope           string              `json:"scope"` // "param", "local", "global"
	DefinedAt       *int                `json:"defined_at,omitempty"`
	Transformations []VarTransformation `json:"transformations"`
	UsedIn          []string            `json:"used_in"` // Function calls that use this variable
	Returned        bool                `json:"returned"`
}

type VarTransformation struct {
	Line    int    `json:"line"`
	Via     string `json:"via"`     // Function that transforms it
	Becomes string `json:"becomes"` // New variable name
}

type KBClass struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Docstring  string               `json:"docstring"`
	LineStart  int                  `json:"line_start"`
	LineEnd    int                  `json:"line_end"`
	Methods    []KBFunction         `json:"methods"`
	Bases      []string             `json:"bases"`
	Attributes []Attribute          `json:"attributes"`
	Decorators []string             `json:"decorators"`
	LangInfo   LanguageSpecificInfo `json:"lang_info,omitempty"`
}
type ControlFlow struct {
	Complexity int        `json:"complexity"` // Cyclomatic complexity
	Branches   []Branch   `json:"branches"`
	Loops      []Loop     `json:"loops"`
	TryBlocks  []TryBlock `json:"try_blocks"`
}

type Branch struct {
	BranchType string         `json:"branch_type"` // "if", "elif", "else", "match"
	Condition  string         `json:"condition"`
	Line       int            `json:"line"`
	TruePath   ExecutionPath  `json:"true_path"`
	FalsePath  *ExecutionPath `json:"false_path,omitempty"`
}

type ExecutionPath struct {
	Calls   []string `json:"calls"`
	Returns *string  `json:"returns,omitempty"`
	Raises  *string  `json:"raises,omitempty"`
}

type Loop struct {
	LoopType  string   `json:"loop_type"` // "for", "while"
	Condition string   `json:"condition"`
	Line      int      `json:"line"`
	Calls     []string `json:"calls"`
}

type TryBlock struct {
	Line          int            `json:"line"`
	TryCalls      []string       `json:"try_calls"`
	ExceptClauses []ExceptClause `json:"except_clauses"`
	FinallyCalls  []string       `json:"finally_calls"`
}

type ExceptClause struct {
	ExceptionType string   `json:"exception_type"`
	Line          int      `json:"line"`
	Calls         []string `json:"calls"`
}

type ExceptionInfo struct {
	Raises     []string `json:"raises"`     // Explicitly raised exceptions
	Propagates []string `json:"propagates"` // Exceptions from called functions
	Handles    []string `json:"handles"`    // Exception types caught in try-except
}

type KBCallGraph struct {
	Nodes []CallGraphNode `json:"nodes"`
	Edges []CallGraphEdge `json:"edges"`
}

type CallGraphNode struct {
	ID       string `json:"id"`
	NodeType string `json:"node_type"`
	File     string `json:"file"`
}

type CallGraphEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	EdgeType string `json:"edge_type"`
}

type DependencyGraph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

type GraphNode struct {
	ID       string `json:"id"`
	NodeType string `json:"node_type"` // "file", "module", "package"
	Name     string `json:"name"`
}

type GraphEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	EdgeType string `json:"edge_type"` // "imports", "depends_on"
}

type KBIndices struct {
	FunctionsByName  map[string][]string `json:"functions_by_name"`
	FunctionsCalling map[string][]string `json:"functions_calling"`
}

type EntryPoint struct {
	EntryType string   `json:"entry_type"`     // "api_endpoint", "cli_command", "main"
	Path      *string  `json:"path,omitempty"` // API path or CLI command
	Function  string   `json:"function"`
	Handler   string   `json:"handler"`
	File      string   `json:"file"`
	Line      int      `json:"line"`
	Methods   []string `json:"methods,omitempty"` // HTTP methods for API endpoints
}

type ExternalDependency struct {
	Name        string   `json:"name"`
	Version     *string  `json:"version,omitempty"`
	Source      string   `json:"source"`
	UsedBy      []string `json:"used_by"` // Files that import this
	ImportCount int      `json:"import_count"`
}

type PatternInfo struct {
	NamingConvention  string  `json:"naming_convention"`
	StructureType     string  `json:"structure_type"`
	ArchitectureStyle *string `json:"architecture_style,omitempty"` // "layered", "microservices", "mvc"
}

// LanguageSpecificInfo carries parser-populated metadata for a single language.
// Only the relevant variant is non-nil.
type LanguageSpecificInfo struct {
	Python     *PythonInfo     `json:"python,omitempty"`
	Rust       *RustInfo       `json:"rust,omitempty"`
	Go         *GoInfo         `json:"go,omitempty"`
	TypeScript *TypeScriptInfo `json:"typescript,omitempty"`
	C          *CInfo          `json:"c,omitempty"`
	Cpp        *CppInfo        `json:"cpp,omitempty"`
}

type RustInfo struct {
	IsUnsafe     bool     `json:"is_unsafe"`
	IsPub        bool     `json:"is_pub"`
	IsPubCrate   bool     `json:"is_pub_crate"`
	IsConstFn    bool     `json:"is_const_fn"`
	IsAsync      bool     `json:"is_async"`
	IsExtern     bool     `json:"is_extern"`
	Lifetimes    []string `json:"lifetimes"`     // e.g. ["'a", "'b"]
	Derives      []string `json:"derives"`       // #[derive(Debug, Clone, ...)]
	IsTest       bool     `json:"is_test"`       // #[test]
	IsBench      bool     `json:"is_bench"`      // #[bench]
	CfgAttrs     []string `json:"cfg_attrs"`     // #[cfg(target_os = "linux")] etc.
	UnknownAttrs []string `json:"unknown_attrs"` // catch-all for unrecognized #[...]
}

type GoInfo struct {
	IsExported        bool     `json:"is_exported"`             // name starts with uppercase
	ReceiverType      *string  `json:"receiver_type,omitempty"` // e.g. "(*MyStruct)"
	IsInterfaceMethod bool     `json:"is_interface_method"`
	BuildTags         []string `json:"build_tags"`    // //go:build linux && amd64
	GoDirectives      []string `json:"go_directives"` // //go:generate, //go:noescape etc.
}

type TypeScriptInfo struct {
	IsAsync         bool     `json:"is_async"`
	IsExported      bool     `json:"is_exported"`
	IsDefaultExport bool     `json:"is_default_export"`
	IsAbstract      bool     `json:"is_abstract"`
	AccessModifier  *string  `json:"access_modifier,omitempty"` // "public" | "private" | "protected"
	IsReadonly      bool     `json:"is_readonly"`
	IsOptional      bool     `json:"is_optional"`
	Decorators      []string `json:"decorators"`     // @Component, @Injectable etc.
	GenericParams   []string `json:"generic_params"` // e.g. ["T", "K extends string"]
	IsArrowFn       bool     `json:"is_arrow_fn"`    // const foo = () => ...
	IsOverload      bool     `json:"is_overload"`    // TS function overloads
}

type CInfo struct {
	IsStatic          bool    `json:"is_static"` // file-scoped linkage
	IsInline          bool    `json:"is_inline"`
	IsExtern          bool    `json:"is_extern"`
	IsVariadic        bool    `json:"is_variadic"`                  // printf-style ...
	CallingConvention *string `json:"calling_convention,omitempty"` // __cdecl, __stdcall etc.
}

type CppInfo struct {
	IsStatic        bool     `json:"is_static"`
	IsInline        bool     `json:"is_inline"`
	IsVirtual       bool     `json:"is_virtual"`
	IsPureVirtual   bool     `json:"is_pure_virtual"` // = 0
	IsOverride      bool     `json:"is_override"`     // override keyword
	IsFinal         bool     `json:"is_final"`        // final keyword
	IsConstMethod   bool     `json:"is_const_method"` // void foo() const
	IsNoexcept      bool     `json:"is_noexcept"`
	IsExplicit      bool     `json:"is_explicit"` // explicit constructors
	IsConstexpr     bool     `json:"is_constexpr"`
	IsConstructor   bool     `json:"is_constructor"`
	IsDestructor    bool     `json:"is_destructor"`
	AccessSpecifier *string  `json:"access_specifier,omitempty"` // "public" | "private" | "protected"
	TemplateParams  []string `json:"template_params"`            // e.g. ["typename T", "int N"]
}

type PythonInfo struct {
	IsDataclass       bool     `json:"is_dataclass"`
	IsStaticmethod    bool     `json:"is_staticmethod"`
	IsClassmethod     bool     `json:"is_classmethod"`
	IsProperty        bool     `json:"is_property"`
	IsPropertySetter  bool     `json:"is_property_setter"`
	IsPropertyDeleter bool     `json:"is_property_deleter"`
	IsAbstractmethod  bool     `json:"is_abstractmethod"`
	IsCachedProperty  bool     `json:"is_cached_property"`
	IsOverload        bool     `json:"is_overload"`
	IsOverride        bool     `json:"is_override"`           // typing.override (Python 3.12+)
	IsFinal           bool     `json:"is_final"`              // typing.final
	FlaskRoute        *string  `json:"flask_route,omitempty"` // e.g. "/users/<id>"
	UnknownDecorators []string `json:"unknown_decorators"`    // catch-all for unrecognized
}
