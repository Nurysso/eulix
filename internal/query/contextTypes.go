package query
import (
	"eulix/internal/config"
	"eulix/internal/embeddings"
	"eulix/internal/llm"
	"eulix/internal/cache"

)

// Classifier.go
type QueryType int

// Router.go

type Router struct {
	eulixDir       string
	config         *config.Config
	classifier     *Classifier
	llmClient      *llm.Client
	cache          *cache.Manager
	contextBuilder *ContextBuilder
	kbIndex        *KBIndex
	callGraph      *CallGraph
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
	eulixDir       string
	config         *config.Config
	llmClient      *llm.Client
	queryEmbedder  *embeddings.QueryEmbedder
	embeddings     [][]float32
	chunks         []Chunk
	vectorMap      map[string]int // ID -> Index in embeddings slice
	callGraph      map[string][]Relationship
	hasCallGraph   bool
	hasEmbeddings  bool
	embData        *EmbeddingsData
	kbData         *KnowledgeBase
	hasKB          bool
}

type Chunk struct {
	ID        string
	ChunkType string
	File      string
	StartLine int
	EndLine   int
	Content   string
	Tokens    int
	Symbols   []string
	Name      string
	Importance float64
}

type Relationship struct {
	Type     string
	Target   string
	Distance int
}

type ScoredChunk struct {
	Chunk
	Score       float64
	Distance    int
	FromID      string
	MatchType   string // "exact", "symbol", "semantic", "keyword", "partial"
	MatchDetails string
}

type EmbeddingsData struct {
	Model       string             `json:"model"`
	Dimension   int                `json:"dimension"`
	TotalChunks int                `json:"total_chunks"`
	Embeddings  []EmbeddingChunk   `json:"embeddings"`
}

type EmbeddingChunk struct {
	ID       string      `json:"id"`
	ChunkType string     `json:"chunk_type"`
	Content  string      `json:"content"`
	Metadata Metadata    `json:"metadata"`
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
	Metadata   KBMetadata              `json:"metadata"`
	Structure  map[string]FileStructure `json:"structure"`
	CallGraph  KBCallGraph             `json:"call_graph"`
	Indices    KBIndices               `json:"indices"`
	EntryPoints []EntryPoint           `json:"entry_points"`
}

type KBMetadata struct {
	ProjectName     string   `json:"project_name"`
	Version         string   `json:"version"`
	TotalFunctions  int      `json:"total_functions"`
	TotalClasses    int      `json:"total_classes"`
}

type FileStructure struct {
	Language   string        `json:"language"`
	Functions  []KBFunction  `json:"functions"`
	Classes    []KBClass     `json:"classes"`
}

type KBFunction struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Signature   string       `json:"signature"`
	Docstring   string       `json:"docstring"`
	LineStart   int          `json:"line_start"`
	LineEnd     int          `json:"line_end"`
	Calls       []FunctionCall `json:"calls"`
}

type KBClass struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Docstring string        `json:"docstring"`
	LineStart int           `json:"line_start"`
	LineEnd   int           `json:"line_end"`
	Methods   []KBFunction  `json:"methods"`
}

type FunctionCall struct {
	Callee    string `json:"callee"`
	DefinedIn string `json:"defined_in"`
	Line      int    `json:"line"`
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

type KBIndices struct {
	FunctionsByName map[string][]string `json:"functions_by_name"`
	FunctionsCalling map[string][]string `json:"functions_calling"`
}

type EntryPoint struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}
