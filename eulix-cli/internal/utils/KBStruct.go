//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package utils provides Shared type and func across project

/*
Mirrors eulix_parser out exactly
*/

package utils

// ---------- Ref / report wrappers ----------

type KnowledgeBaseSimplifiedRef struct {
	Metadata  *Metadata           `json:"metadata"`
	Structure map[string]FileData `json:"structure"`
}

type IndexDataRef struct {
	Indices *Indices `json:"indices"`
}

type CallGraphRef struct {
	Nodes []CallGraphNode `json:"nodes"`
	Edges []CallGraphEdge `json:"edges"`
}

type EntryPointsRef struct {
	EntryPoints []EntryPoint `json:"entry_points"`
}

type ExternalDependencyRef struct {
	ExternalDependencies []ExternalDependency `json:"external_dependencies"`
}

type PatternsRef struct {
	Patterns *PatternInfo `json:"patterns"`
}

type MetricsReport struct {
	Metadata            *Metadata        `json:"metadata"`
	TopComplexFunctions []FunctionMetric `json:"top_complex_functions"`
}

type FunctionMetric struct {
	Name            string  `json:"name"`
	File            string  `json:"file"`
	Complexity      int     `json:"complexity"`
	ImportanceScore float32 `json:"importance_score"`
	LineStart       int     `json:"line_start"`
	LineEnd         int     `json:"line_end"`
}

// ---------- Main knowledge base ----------

type KnowledgeBase struct {
	Metadata             Metadata             `json:"metadata"`
	Structure            map[string]FileData  `json:"structure"`
	CallGraph            CallGraph            `json:"call_graph"`
	DependencyGraph      DependencyGraph      `json:"dependency_graph"`
	Indices              Indices              `json:"indices"`
	EntryPoints          []EntryPoint         `json:"entry_points"`
	ExternalDependencies []ExternalDependency `json:"external_dependencies"`
	Patterns             PatternInfo          `json:"patterns"`
}

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

type FileData struct {
	Language      string         `json:"language"`
	Loc           int            `json:"loc"`
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
	ImportType string   `json:"type"`
}

type KBFunction struct {
	ID              string               `json:"id"`
	Name            string               `json:"name"`
	Signature       string               `json:"signature"`
	Params          []Parameter          `json:"params"`
	ReturnType      string               `json:"return_type"`
	Docstring       string               `json:"docstring"`
	LineStart       int                  `json:"line_start"`
	LineEnd         int                  `json:"line_end"`
	Calls           []FunctionCall       `json:"calls"`
	CalledBy        []CallerInfo         `json:"called_by"`
	Variables       []Variable           `json:"variables"`
	ControlFlow     ControlFlow          `json:"control_flow"`
	Exceptions      ExceptionInfo        `json:"exceptions"`
	Complexity      int                  `json:"complexity"`
	IsAsync         bool                 `json:"is_async"`
	Decorators      []string             `json:"decorators"`
	Tags            []string             `json:"tags"`
	ImportanceScore float32              `json:"importance_score"`
	LangInfo        LanguageSpecificInfo `json:"lang_info"`
}

type Parameter struct {
	Name           string  `json:"name"`
	TypeAnnotation string  `json:"type_annotation"`
	DefaultValue   *string `json:"default_value"`
}

type FunctionCall struct {
	Callee        string   `json:"callee"`
	DefinedIn     *string  `json:"defined_in"`
	Line          int      `json:"line"`
	Args          []string `json:"args"`
	IsConditional bool     `json:"is_conditional"`
	Context       string   `json:"context"`
}

type CallerInfo struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

type Variable struct {
	Name            string              `json:"name"`
	VarType         *string             `json:"var_type"`
	Scope           string              `json:"scope"`
	DefinedAt       *int                `json:"defined_at"`
	Transformations []VarTransformation `json:"transformations"`
	UsedIn          []string            `json:"used_in"`
	Returned        bool                `json:"returned"`
}

type VarTransformation struct {
	Line    int    `json:"line"`
	Via     string `json:"via"`
	Becomes string `json:"becomes"`
}

type ControlFlow struct {
	Complexity int        `json:"complexity"`
	Branches   []Branch   `json:"branches"`
	Loops      []Loop     `json:"loops"`
	TryBlocks  []TryBlock `json:"try_blocks"`
}

type Branch struct {
	BranchType string         `json:"branch_type"`
	Condition  string         `json:"condition"`
	Line       int            `json:"line"`
	TruePath   ExecutionPath  `json:"true_path"`
	FalsePath  *ExecutionPath `json:"false_path"`
}

type ExecutionPath struct {
	Calls   []string `json:"calls"`
	Returns *string  `json:"returns"`
	Raises  *string  `json:"raises"`
}

type Loop struct {
	LoopType  string   `json:"loop_type"`
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
	Raises     []string `json:"raises"`
	Propagates []string `json:"propagates"`
	Handles    []string `json:"handles"`
}

type KBClass struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Bases      []string             `json:"bases"`
	Docstring  string               `json:"docstring"`
	LineStart  int                  `json:"line_start"`
	LineEnd    int                  `json:"line_end"`
	Methods    []KBFunction         `json:"methods"`
	Attributes []Attribute          `json:"attributes"`
	Decorators []string             `json:"decorators"`
	LangInfo   LanguageSpecificInfo `json:"lang_info"`
}

type Attribute struct {
	Name           string  `json:"name"`
	TypeAnnotation string  `json:"type_annotation"`
	Value          *string `json:"value"`
}

type GlobalVar struct {
	Name           string  `json:"name"`
	TypeAnnotation string  `json:"type_annotation"`
	Value          *string `json:"value"`
	Line           int     `json:"line"`
}

type Todo struct {
	Line     int    `json:"line"`
	Text     string `json:"text"`
	Priority string `json:"priority"`
}

type SecurityNote struct {
	NoteType    string `json:"note_type"`
	Line        int    `json:"line"`
	Description string `json:"description"`
}

// ---------- Graphs ----------

type CallGraph struct {
	Nodes []CallGraphNode `json:"nodes"`
	Edges []CallGraphEdge `json:"edges"`
}

type CallGraphNode struct {
	ID                string `json:"id"`
	NodeType          string `json:"node_type"`
	File              string `json:"file"`
	IsEntryPoint      bool   `json:"is_entry_point"`
	CallCountEstimate int    `json:"call_count_estimate"`
}

type CallGraphEdge struct {
	From         string `json:"from"`
	To           string `json:"to"`
	EdgeType     string `json:"edge_type"`
	Conditional  bool   `json:"conditional"`
	CallSiteLine int    `json:"call_site_line"`
}

type DependencyGraph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

type GraphNode struct {
	ID       string `json:"id"`
	NodeType string `json:"node_type"`
	Name     string `json:"name"`
}

type GraphEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	EdgeType string `json:"edge_type"`
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

// ---------- Language-specific info ----------

type LanguageSpecificInfo struct {
	Python     *PythonInfo     `json:"python,omitempty"`
	Rust       *RustInfo       `json:"rust,omitempty"`
	Go         *GoInfo         `json:"go,omitempty"`
	TypeScript *TypeScriptInfo `json:"typescript,omitempty"`
	JavaScript *JavaScriptInfo `json:"javascript,omitempty"`
	C          *CInfo          `json:"c,omitempty"`
	Cpp        *CppInfo        `json:"cpp,omitempty"`
}

type RustInfo struct {
	IsUnsafe             bool     `json:"is_unsafe"`
	IsPub                bool     `json:"is_pub"`
	IsPubCrate           bool     `json:"is_pub_crate"`
	IsConstFn            bool     `json:"is_const_fn"`
	IsAsync              bool     `json:"is_async"`
	IsExtern             bool     `json:"is_extern"`
	Abi                  *string  `json:"abi"`
	Lifetimes            []string `json:"lifetimes"`
	Generics             []string `json:"generics"`
	WhereClause          *string  `json:"where_clause"`
	IsGeneric            bool     `json:"is_generic"`
	Derives              []string `json:"derives"`
	IsTest               bool     `json:"is_test"`
	IsBench              bool     `json:"is_bench"`
	CfgAttrs             []string `json:"cfg_attrs"`
	UnknownAttrs         []string `json:"unknown_attrs"`
	TraitName            *string  `json:"trait_name"`
	IsTraitImplMethod    bool     `json:"is_trait_impl_method"`
	IsTraitDefaultMethod bool     `json:"is_trait_default_method"`
	IsOperatorOverload   bool     `json:"is_operator_overload"`
	OverloadedOperator   *string  `json:"overloaded_operator"`
	ItemKind             *string  `json:"item_kind"`
	Supertraits          []string `json:"supertraits"`
	IsMarkerTrait        bool     `json:"is_marker_trait"`
	UsesTryOperator      bool     `json:"uses_try_operator"`
	MacroCalls           []string `json:"macro_calls"`
}

type GoInfo struct {
	IsExported        bool        `json:"is_exported"`
	ReceiverType      *string     `json:"receiver_type"`
	ReceiverName      *string     `json:"receiver_name"`
	IsInterfaceMethod bool        `json:"is_interface_method"`
	SpawnsGoroutines  bool        `json:"spawns_goroutines"`
	UsesChannels      bool        `json:"uses_channels"`
	UsesSelect        bool        `json:"uses_select"`
	UsesMutex         bool        `json:"uses_mutex"`
	UsesWaitgroup     bool        `json:"uses_waitgroup"`
	UsesAtomic        bool        `json:"uses_atomic"`
	ReturnsError      bool        `json:"returns_error"`
	UsesPanic         bool        `json:"uses_panic"`
	UsesRecover       bool        `json:"uses_recover"`
	DeferCount        int         `json:"defer_count"`
	TypeParams        []string    `json:"type_params"`
	TypeConstraints   []string    `json:"type_constraints"`
	BuildTags         []string    `json:"build_tags"`
	GoDirectives      []string    `json:"go_directives"`
	UsesCgo           bool        `json:"uses_cgo"`
	EmbedPatterns     []string    `json:"embed_patterns"`
	IsVariadic        bool        `json:"is_variadic"`
	TypeKind          *GoTypeKind `json:"type_kind"`
	IsPointerReceiver bool        `json:"is_pointer_receiver"`
	HasEmbeddedTypes  bool        `json:"has_embedded_types"`
}

type GoTypeKind string

const (
	GoTypeKindStruct    GoTypeKind = "struct"
	GoTypeKindInterface GoTypeKind = "interface"
	GoTypeKindFunction  GoTypeKind = "function"
	GoTypeKindMethod    GoTypeKind = "method"
)

type TypeScriptInfo struct {
	IsAsync         bool     `json:"is_async"`
	IsExported      bool     `json:"is_exported"`
	IsDefaultExport bool     `json:"is_default_export"`
	IsAbstract      bool     `json:"is_abstract"`
	AccessModifier  *string  `json:"access_modifier"`
	IsReadonly      bool     `json:"is_readonly"`
	IsOptional      bool     `json:"is_optional"`
	Decorators      []string `json:"decorators"`
	GenericParams   []string `json:"generic_params"`
	IsArrowFn       bool     `json:"is_arrow_fn"`
	IsOverload      bool     `json:"is_overload"`
}

type JavaScriptInfo struct {
	IsAsync            bool    `json:"is_async"`
	IsExported         bool    `json:"is_exported"`
	IsDefaultExport    bool    `json:"is_default_export"`
	IsArrowFn          bool    `json:"is_arrow_fn"`
	IsGenerator        bool    `json:"is_generator"`
	IsIife             bool    `json:"is_iife"`
	IsStrictMode       bool    `json:"is_strict_mode"`
	ModuleSystem       *string `json:"module_system"`
	IsCommonjsExport   bool    `json:"is_commonjs_export"`
	BindsThisLexically bool    `json:"binds_this_lexically"`
	IsCallback         bool    `json:"is_callback"`
	IsHigherOrder      bool    `json:"is_higher_order"`
	UsesHoistedVar     bool    `json:"uses_hoisted_var"`
}

type JavaInfo struct {
	IsStatic                bool     `json:"is_static"`
	IsFinal                 bool     `json:"is_final"`
	IsAbstract              bool     `json:"is_abstract"`
	IsSynchronized          bool     `json:"is_synchronized"`
	IsNative                bool     `json:"is_native"`
	IsDefaultMethod         bool     `json:"is_default_method"`
	IsConstructor           bool     `json:"is_constructor"`
	AccessModifier          *string  `json:"access_modifier"`
	Annotations             []string `json:"annotations"`
	Throws                  []string `json:"throws"`
	GenericParams           []string `json:"generic_params"`
	Extends                 *string  `json:"extends"`
	Implements              []string `json:"implements"`
	TypeKind                *string  `json:"type_kind"`
	IsFunctionalInterface   bool     `json:"is_functional_interface"`
	IsLambda                bool     `json:"is_lambda"`
	IsAnonymousClass        bool     `json:"is_anonymous_class"`
	IsInnerClass            bool     `json:"is_inner_class"`
	IsStaticNestedClass     bool     `json:"is_static_nested_class"`
	IsRecord                bool     `json:"is_record"`
	IsSealed                bool     `json:"is_sealed"`
	PermittedSubclasses     []string `json:"permitted_subclasses"`
	OverridesEqualsHashcode bool     `json:"overrides_equals_hashcode"`
	UsesStreams             bool     `json:"uses_streams"`
	UsesTryWithResources    bool     `json:"uses_try_with_resources"`
	IsVarargs               bool     `json:"is_varargs"`
}

type CInfo struct {
	IsStatic          bool    `json:"is_static"`
	IsInline          bool    `json:"is_inline"`
	IsExtern          bool    `json:"is_extern"`
	IsVariadic        bool    `json:"is_variadic"`
	CallingConvention *string `json:"calling_convention"`
}

type CppInfo struct {
	IsStatic                 bool        `json:"is_static"`
	IsInline                 bool        `json:"is_inline"`
	IsVirtual                bool        `json:"is_virtual"`
	IsPureVirtual            bool        `json:"is_pure_virtual"`
	IsOverride               bool        `json:"is_override"`
	IsFinal                  bool        `json:"is_final"`
	IsConstMethod            bool        `json:"is_const_method"`
	IsNoexcept               bool        `json:"is_noexcept"`
	IsExplicit               bool        `json:"is_explicit"`
	IsConstexpr              bool        `json:"is_constexpr"`
	IsConstructor            bool        `json:"is_constructor"`
	IsDestructor             bool        `json:"is_destructor"`
	AccessSpecifier          *string     `json:"access_specifier"`
	TemplateParams           []string    `json:"template_params"`
	TypeKind                 CppTypeKind `json:"type_kind"`
	IsPod                    bool        `json:"is_pod"`
	IsPacked                 bool        `json:"is_packed"`
	HasVtable                bool        `json:"has_vtable"`
	IsScopedEnum             bool        `json:"is_scoped_enum"`
	IsFlagsEnum              bool        `json:"is_flags_enum"`
	UnderlyingType           *string     `json:"underlying_type"`
	IsTemplate               bool        `json:"is_template"`
	IsPartialSpecialization  bool        `json:"is_partial_specialization"`
	IsExplicitSpecialization bool        `json:"is_explicit_specialization"`
	ConceptConstraints       []string    `json:"concept_constraints"`
	IsExternC                bool        `json:"is_extern_c"`
	IsThreadLocal            bool        `json:"is_thread_local"`
	IsConsteval              bool        `json:"is_consteval"`
	IsConstinit              bool        `json:"is_constinit"`
	IsOperatorOverload       bool        `json:"is_operator_overload"`
	OverloadedOperator       *string     `json:"overloaded_operator"`
	IsAbstract               bool        `json:"is_abstract"`
	InheritanceType          *string     `json:"inheritance_type"`
}

type CppTypeKind string

const (
	CppTypeKindFunction CppTypeKind = "function"
	CppTypeKindStruct   CppTypeKind = "struct"
	CppTypeKindUnion    CppTypeKind = "union"
	CppTypeKindEnum     CppTypeKind = "enum"
	CppTypeKindClass    CppTypeKind = "class"
)

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
	IsOverride        bool     `json:"is_override"`
	IsFinal           bool     `json:"is_final"`
	FlaskRoute        *string  `json:"flask_route"`
	UnknownDecorators []string `json:"unknown_decorators"`
}
