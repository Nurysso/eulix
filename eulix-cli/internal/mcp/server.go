// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

/*
Package mcp implements a Model Context Protocol (MCP) server for Eulix, built on the
official Go SDK (github.com/modelcontextprotocol/go-sdk). It runs over stdio for AI code
editors such as Windsurf, VS Code (Cline), Claude Code and more.

It exposes the same retrieval as 'eulix chat/query', keeping the knowledge base and context
builder warm in memory between queries and releasing them after a period of inactivity.

Features:
  - Dynamic repository selection via the 'set_repo_path' tool, the 'repo_path' argument
    on queries, or automatically from the client's MCP "roots".
  - Helpful guidance if a project has not been initialized with 'eulix analyze'.
  - Query with or without Chain-of-Thought (CoT) headers, as tools and as prompts.

Protocol concerns (JSON-RPC framing, initialize handshake, capability negotiation,
schema generation and validation, ping, cancellation) are all handled by the SDK.
*/
package mcp

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"eulix/internal/config"
	"eulix/internal/query"
	"eulix/internal/utils"
)

const (
	// DefaultIdleTimeout is the inactivity period after which ContextBuilder memory is freed.
	DefaultIdleTimeout = 5 * time.Minute

	// ServerName is the identity of this MCP server.
	ServerName = "eulix"

	instructions = "Eulix MCP Server: query any codebase on your machine for fast, accurate context. " +
		"Use 'set_repo_path' or pass 'repo_path' in queries to choose the repository. " +
		"The repository must have been initialized with 'eulix analyze'."

	rootsTimeout = 5 * time.Second
)

type Server struct {
	// Configuration (immutable after NewServer).
	idleTimeout time.Duration
	defaultCoT  bool
	initialRepo string
	in          io.Reader
	out         io.Writer
	log         *slog.Logger

	// mu guards all mutable state below and serializes queries.
	mu           sync.Mutex
	activeRepo   string
	eulixDir     string
	cfg          *config.Config
	router       *query.Router
	idleTimer    *time.Timer
	lastActivity time.Time

	// Adaptive retrieval (see strategy.go).
	strategy  StrategyConfig
	retriever Retriever                 // optional override (tests); defaults to the Router adapter
	memories  map[string]*projectMemory // per-repo project memory, guarded by mu

	mcp *mcpsdk.Server
}

// Option configures a Server.
type Option func(*Server)

// WithIdleTimeout sets the inactivity timeout before the context builder is released.
func WithIdleTimeout(d time.Duration) Option {
	return func(s *Server) {
		if d > 0 {
			s.idleTimeout = d
		}
	}
}

// WithDefaultCoT sets the default CoT behavior for the generic 'query' tool.
func WithDefaultCoT(enable bool) Option {
	return func(s *Server) { s.defaultCoT = enable }
}

// WithInitialRepo configures a repository to bind at startup.
func WithInitialRepo(path string) Option {
	return func(s *Server) { s.initialRepo = path }
}

// WithIO sets custom streams (useful for tests). Defaults to stdin/stdout.
func WithIO(in io.Reader, out io.Writer) Option {
	return func(s *Server) {
		s.in = in
		s.out = out
	}
}

// WithLogger sets a custom structured logger. Logs MUST NOT go to stdout, which carries
// the protocol; the default writes to stderr.
func WithLogger(l *slog.Logger) Option {
	return func(s *Server) {
		if l != nil {
			s.log = l
		}
	}
}

// WithStrategy tunes the adaptive retrieval cascade (zero fields use defaults).
func WithStrategy(cfg StrategyConfig) Option {
	return func(s *Server) { s.strategy = cfg }
}

// WithRetriever replaces the default Router-backed retriever (useful for tests, or to
// plug in dedicated anchored / graph-expansion calls).
func WithRetriever(r Retriever) Option {
	return func(s *Server) { s.retriever = r }
}

// NewServer initializes a new MCP server.
func NewServer(router *query.Router, cfg *config.Config, eulixDir string, opts ...Option) *Server {
	s := &Server{
		router:      router,
		cfg:         cfg,
		eulixDir:    eulixDir,
		idleTimeout: DefaultIdleTimeout,
		strategy:    DefaultStrategyConfig(),
		memories:    make(map[string]*projectMemory),
		log:         slog.New(slog.NewTextHandler(os.Stderr, nil)).With("component", "eulix-mcp"),
	}

	if router != nil {
		switch {
		case cfg != nil && cfg.Project.Path != "":
			s.activeRepo = cfg.Project.Path
		case eulixDir != "":
			// eulixDir is "<repo>/.eulix"; the repo is its parent.
			s.activeRepo = filepath.Dir(eulixDir)
		}
	}

	for _, opt := range opts {
		opt(s)
	}
	s.strategy = s.strategy.normalized()

	if s.router == nil && s.initialRepo != "" {
		if msg, err := s.SetRepoPath(s.initialRepo); err != nil {
			s.log.Warn("initial repository not bound", "err", err)
		} else {
			s.log.Info(msg)
		}
	}

	s.mcp = s.buildMCPServer()
	return s
}

// ========== MCP wiring ==========

// Tool/prompt argument types. The SDK derives each tool's JSON Schema from these
// structs (the `jsonschema` tag is the property description) and validates input
// before our handlers run, so we only need to check semantic constraints.

type setRepoArgs struct {
	RepoPath string `json:"repo_path" jsonschema:"Absolute path to the repository on your machine (e.g. /home/user/foo)"`
}

type queryArgs struct {
	Query      string `json:"query" jsonschema:"Question, symbol, or search query about the codebase"`
	RepoPath   string `json:"repo_path,omitempty" jsonschema:"Optional absolute path to the repository. If omitted, the currently active repository is used"`
	IncludeCoT *bool  `json:"include_cot,omitempty" jsonschema:"Include Chain-of-Thought reasoning instructions and headers. Defaults to the server setting"`

	KnownSymbols []string `json:"known_symbols,omitempty" jsonschema:"Symbols, class or function names, or file paths relevant to the question that you already know from this session (code you have read, earlier results, the user's message). Pass them whenever you have them: they enable precise multi-hop retrieval"`
}

type fixedCoTArgs struct {
	KnownSymbols []string `json:"known_symbols,omitempty" jsonschema:"Symbols, class or function names, or file paths relevant to the question that you already know from this session (code you have read, earlier results, the user's message). Pass them whenever you have them: they enable precise multi-hop retrieval"`

	Query    string `json:"query" jsonschema:"Question, symbol, or search query about the codebase"`
	RepoPath string `json:"repo_path,omitempty" jsonschema:"Optional absolute path to the repository. If omitted, the currently active repository is used"`
}

func (s *Server) buildMCPServer() *mcpsdk.Server {
	srv := mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: ServerName, Version: utils.AppVersion},
		&mcpsdk.ServerOptions{
			Instructions:       instructions,
			Logger:             s.log,
			InitializedHandler: s.onInitialized,
		},
	)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "set_repo_path",
		Description: "Set or switch the active codebase repository for Eulix queries. " +
			"The repository must have been analyzed with 'eulix analyze'.",
		Annotations: &mcpsdk.ToolAnnotations{Title: "Set repository", IdempotentHint: true},
	}, s.handleSetRepo)

	readOnly := &mcpsdk.ToolAnnotations{ReadOnlyHint: true}

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "query",
		Description: "Query the codebase using Eulix retrieval (same as 'eulix query'). " +
			"Retrieves relevant source chunks, symbols, AST metadata, and call graphs. " +
			"Optional 'repo_path' targets/switches the repository; optional 'include_cot' " +
			"chooses between plain context and Chain-of-Thought headers. " +
			"Include exact symbol names, function names, or file paths whenever you know them " +
			"(e.g. 'SQLCompiler.get_from_clause'): they anchor exact lookup and multi-hop graph " +
			"expansion, while open-ended questions rely on semantic signals only. " +
			"Also pass 'known_symbols' with any relevant symbols or files you already know " +
			"from this session (code you have read, earlier results).",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a queryArgs) (*mcpsdk.CallToolResult, any, error) {
		cot := s.defaultCoT
		if a.IncludeCoT != nil {
			cot = *a.IncludeCoT
		}
		return s.toolQuery(ctx, a.RepoPath, a.Query, a.KnownSymbols, cot)
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "query_with_cot",
		Description: "Query the codebase and return retrieved context with Chain-of-Thought (CoT) " +
			"reasoning instructions, honesty contract, and citation legend. Accepts optional 'repo_path'.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a fixedCoTArgs) (*mcpsdk.CallToolResult, any, error) {
		return s.toolQuery(ctx, a.RepoPath, a.Query, a.KnownSymbols, true)
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "query_without_cot",
		Description: "Query the codebase and return clean retrieved context without Chain-of-Thought " +
			"instructions. Ideal for direct context injection or models with their own system " +
			"instructions. Accepts optional 'repo_path'.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a fixedCoTArgs) (*mcpsdk.CallToolResult, any, error) {
		return s.toolQuery(ctx, a.RepoPath, a.Query, a.KnownSymbols, false)
	})

	promptArgs := []*mcpsdk.PromptArgument{
		{Name: "query", Description: "Question or search query about the codebase", Required: true},
		{Name: "repo_path", Description: "Optional path to the project repository"},
		{Name: "known_symbols", Description: "Optional comma-separated symbols or in query symbols or file paths you already know from this session"},
	}
	srv.AddPrompt(&mcpsdk.Prompt{
		Name:        "query_with_cot",
		Description: "Build prompt with retrieved codebase context and Chain-of-Thought headers",
		Arguments:   promptArgs,
	}, s.promptHandler(true))
	srv.AddPrompt(&mcpsdk.Prompt{
		Name:        "query_without_cot",
		Description: "Build prompt with retrieved codebase context only (no Chain-of-Thought headers)",
		Arguments:   promptArgs,
	}, s.promptHandler(false))

	return srv
}

// onInitialized binds a repository from the client's MCP roots when none is active yet.
// This replaces the legacy rootUri/workspaceFolders sniffing of the initialize request.
func (s *Server) onInitialized(_ context.Context, req *mcpsdk.InitializedRequest) {
	if s.GetActiveRepo() != "" || req.Session == nil {
		return
	}
	// Run off the receive loop: ListRoots is a round trip to the client.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rootsTimeout)
		defer cancel()
		//nolint:staticcheck // roots feature is deprecated but still functional during the migration window
		res, err := req.Session.ListRoots(ctx, nil)
		if err != nil {
			s.log.Debug("client did not provide roots", "err", err)
			return
		}
		for _, root := range res.Roots {
			if s.GetActiveRepo() != "" {
				return // someone (e.g. set_repo_path) won the race
			}
			path := parseURIPath(root.URI)
			if _, err := s.SetRepoPath(path); err != nil {
				s.log.Info("client root not usable", "root", path, "err", err)
				continue
			}
			return
		}
	}()
}
