package fixers

// Types for fixer packages
type EmbeddingsFile struct {
	Model       string      `json:"model"`
	Dimension   int         `json:"dimension"`
	TotalChunks int         `json:"total_chunks"`
	Embeddings  []KBChunk   `json:"embeddings"`
}

type KBChunk struct {
	ID        string    `json:"id"`
	ChunkType string    `json:"chunk_type"`
	Content   string    `json:"content"`
	Embedding []float32 `json:"embedding,omitempty"`
	Metadata  Metadata  `json:"metadata"`
}

type Metadata struct {
	FilePath   string `json:"file_path"`
	Language   string `json:"language"`
	LineStart  int    `json:"line_start"`
	LineEnd    int    `json:"line_end"`
	Name       string `json:"name"`
	Complexity int    `json:"complexity"`
}

// Structures for kb.json (comprehensive codebase analysis)
type KBFile struct {
	Metadata         KBMetadata                  `json:"metadata"`
	Structure        map[string]FileStructure    `json:"structure"`
	CallGraph        CallGraph                   `json:"call_graph"`
	DependencyGraph  DependencyGraph             `json:"dependency_graph"`
	Indices          Indices                     `json:"indices"`
	EntryPoints      []EntryPoint                `json:"entry_points"`
	ExternalDeps     []ExternalDependency        `json:"external_dependencies"`
	Patterns         Patterns                    `json:"patterns"`
}

type KBMetadata struct {
	ProjectName    string   `json:"project_name"`
	Version        string   `json:"version"`
	ParsedAt       string   `json:"parsed_at"`
	Languages      []string `json:"languages"`
	TotalFiles     int      `json:"total_files"`
	TotalLOC       int      `json:"total_loc"`
	TotalFunctions int      `json:"total_functions"`
	TotalClasses   int      `json:"total_classes"`
	TotalMethods   int      `json:"total_methods"`
}

type FileStructure struct {
	Language   string        `json:"language"`
	LOC        int           `json:"loc"`
	Functions  []Function    `json:"functions"`
	Classes    []Class       `json:"classes"`
	GlobalVars []GlobalVar   `json:"global_vars"`
}

type Function struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Signature  string `json:"signature"`
	Docstring  string `json:"docstring"`
	LineStart  int    `json:"line_start"`
	LineEnd    int    `json:"line_end"`
	Complexity int    `json:"complexity"`
}

type Class struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Bases     []string   `json:"bases"`
	LineStart int        `json:"line_start"`
	LineEnd   int        `json:"line_end"`
	Methods   []Function `json:"methods"`
}

type GlobalVar struct {
	Name string `json:"name"`
	Line int    `json:"line"`
}

type CallGraph struct {
	Nodes []CallGraphNode `json:"nodes"`
	Edges []CallGraphEdge `json:"edges"`
}

type CallGraphNode struct {
	ID           string `json:"id"`
	NodeType     string `json:"node_type"`
	File         string `json:"file"`
	IsEntryPoint bool   `json:"is_entry_point"`
}

type CallGraphEdge struct {
	From         string `json:"from"`
	To           string `json:"to"`
	EdgeType     string `json:"edge_type"`
	Conditional  bool   `json:"conditional"`
	CallSiteLine int    `json:"call_site_line"`
}

type DependencyGraph struct {
	Nodes []DepGraphNode `json:"nodes"`
	Edges []DepGraphEdge `json:"edges"`
}

type DepGraphNode struct {
	ID       string `json:"id"`
	NodeType string `json:"node_type"`
	Name     string `json:"name"`
}

type DepGraphEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	EdgeType string `json:"edge_type"`
}

type Indices struct {
	FunctionsByName   map[string][]string `json:"functions_by_name"`
	FunctionsCalling  map[string][]string `json:"functions_calling"`
	FunctionsByTag    map[string][]string `json:"functions_by_tag"`
	TypesByName       map[string][]string `json:"types_by_name"`
	FilesByCategory   map[string][]string `json:"files_by_category"`
}

type EntryPoint struct {
	EntryType string   `json:"entry_type"`
	Path      string   `json:"path"`
	Function  string   `json:"function"`
	Handler   string   `json:"handler"`
	File      string   `json:"file"`
	Line      int      `json:"line"`
	Methods   []string `json:"methods,omitempty"`
}

type ExternalDependency struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Source      string   `json:"source"`
	UsedBy      []string `json:"used_by"`
	ImportCount int      `json:"import_count"`
}

type Patterns struct {
	NamingConvention  string `json:"naming_convention"`
	StructureType     string `json:"structure_type"`
	ArchitectureStyle string `json:"architecture_style"`
}
