// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package utils provides Shared type and func across project

/*
Mirrors what eulix_parser writes, not libeulix's full kb_struct.rs: the
parser emits slim views (eulix-parser/src/views.rs) and drops everything
retrieval does not need, so a field missing here is absent from the JSON.
*/

package utils

// ---------- Ref / report wrappers ----------

// KnowledgeBaseSimplifiedRef is kb.json (output.rs write_kb_from_spill).
type KnowledgeBaseSimplifiedRef struct {
	Metadata  *Metadata           `json:"metadata"`
	Structure map[string]FileData `json:"structure"`
}

// IndexDataRef is kb_index.json.
type IndexDataRef struct {
	Indices *Indices `json:"indices"`
}

// CallGraphRef is kb_call_graph.json. The parser also writes per-node
// "tags" and "file_categories" maps, which nothing here reads.
type CallGraphRef struct {
	Nodes []CallGraphNode `json:"nodes"`
	Edges []CallGraphEdge `json:"edges"`
}

// EntryPointsRef is kb_entry_points.json.
type EntryPointsRef struct {
	EntryPoints []EntryPoint `json:"entry_points"`
}

// ExternalDependencyRef is kb_external_deps.json.
type ExternalDependencyRef struct {
	ExternalDependencies []ExternalDependency `json:"external_dependencies"`
}

// PatternsRef is kb_patterns.json.
type PatternsRef struct {
	Patterns *PatternInfo `json:"patterns"`
}

// MetricsReport is kb_metrics.json.
type MetricsReport struct {
	Metadata            *Metadata        `json:"metadata"`
	TopComplexFunctions []FunctionMetric `json:"top_complex_functions"`
}

type FunctionMetric struct {
	Name            string  `json:"name"`
	File            string  `json:"file"`
	Complexity      int     `json:"complexity"`
	LineStart       int     `json:"line_start"`
	LineEnd         int     `json:"line_end"`
	ImportanceScore float32 `json:"importance_score"`
}

// ---------- kb.json structure ----------

type Metadata struct {
	Version        string   `json:"version"`
	GitHash        string   `json:"git_hash"`
	ProjectName    string   `json:"project_name"`
	ProjectHash    string   `json:"project_hash"`
	ParsedAt       string   `json:"parsed_at"`
	Languages      []string `json:"languages"`
	TotalFiles     int      `json:"total_files"`
	TotalLoc       int      `json:"total_loc"`
	TotalFunctions int      `json:"total_functions"`
	TotalClasses   int      `json:"total_classes"`
	TotalMethods   int      `json:"total_methods"`
}

// FileData is FileDataSimpleView: no imports, globals or security notes.
type FileData struct {
	Language  string       `json:"language"`
	Loc       int          `json:"loc"`
	Functions []KBFunction `json:"functions"`
	Classes   []KBClass    `json:"classes"`
	Todos     []Todo       `json:"todos"`
}

// KBFunction is FunctionSimpleView: no calls, control flow, tags or lang_info.
type KBFunction struct {
	Params     []Parameter `json:"params"`
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Signature  string      `json:"signature"`
	ReturnType string      `json:"return_type"`
	Docstring  string      `json:"docstring"`
	LineStart  int         `json:"line_start"`
	LineEnd    int         `json:"line_end"`
}

type Parameter struct {
	Name           string  `json:"name"`
	TypeAnnotation string  `json:"type_annotation"`
	DefaultValue   *string `json:"default_value"`
}

// KBClass is ClassSimpleView: no lang_info.
type KBClass struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Bases      []string     `json:"bases"`
	Docstring  string       `json:"docstring"`
	LineStart  int          `json:"line_start"`
	LineEnd    int          `json:"line_end"`
	Methods    []KBFunction `json:"methods"`
	Attributes []Attribute  `json:"attributes"`
	Decorators []string     `json:"decorators"`
}

type Attribute struct {
	Name           string  `json:"name"`
	TypeAnnotation string  `json:"type_annotation"`
	Value          *string `json:"value"`
}

type Todo struct {
	Line     int    `json:"line"`
	Text     string `json:"text"`
	Priority string `json:"priority"`
}

// ---------- Call graph ----------

type CallGraphNode struct {
	ID                string `json:"id"`
	NodeType          string `json:"node_type"`
	File              string `json:"file"`
	CallCountEstimate int    `json:"call_count_estimate"`
	IsEntryPoint      bool   `json:"is_entry_point"`
}

type CallGraphEdge struct {
	From         string `json:"from"`
	To           string `json:"to"`
	EdgeType     string `json:"edge_type"`
	CallSiteLine int    `json:"call_site_line"`
	Conditional  bool   `json:"conditional"`
}

// ---------- Indices / entry points / deps / patterns ----------

type Indices struct {
	FunctionsByName  map[string][]string `json:"functions_by_name"`
	FunctionsCalling map[string][]string `json:"functions_calling"`
	FunctionsByTag   map[string][]string `json:"functions_by_tag"`
	TypesByName      map[string][]string `json:"types_by_name"`
	FilesByCategory  map[string][]string `json:"files_by_category"`
}

type EntryPoint struct {
	EntryType string   `json:"entry_type"`
	Path      *string  `json:"path"`
	Function  string   `json:"function"`
	Handler   string   `json:"handler"`
	File      string   `json:"file"`
	Line      int      `json:"line"`
	Methods   []string `json:"methods"`
}

type ExternalDependency struct {
	Name        string   `json:"name"`
	Version     *string  `json:"version"`
	Source      string   `json:"source"`
	UsedBy      []string `json:"used_by"`
	ImportCount int      `json:"import_count"`
}

type PatternInfo struct {
	NamingConvention  string  `json:"naming_convention"`
	StructureType     string  `json:"structure_type"`
	ArchitectureStyle *string `json:"architecture_style"`
}
