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
  - Typed tools for structural questions (callers, callees, definition, trace) answered
    from the call graph and index, plus budgeted ranked search ('search', and 'query' for
    older clients). Every reply starts with a line saying whether it is complete.

Protocol concerns (JSON-RPC framing, initialize handshake, capability negotiation,
schema generation and validation, ping, cancellation) are all handled by the SDK.
*/
package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

	// instructions tell the agent when Eulix beats grep. Without them agents
	// rarely call it (5 of 50 sessions in the agent benchmark).
	instructions = "Eulix answers structural questions about an indexed codebase from its call graph and symbol index. " +
		"Use 'callers' / 'callees' for who calls what (exact call sites, complete list), 'definition' for where a " +
		"symbol is defined, 'trace' for how one function reaches another across files, and 'search' for concepts " +
		"and open questions. Prefer grep for literal strings, config values and log messages. " +
		"Every reply starts with 'answer: ... · complete: yes|no'; when it says complete: yes, do not re-check with grep. " +
		"Choose the repository with 'set_repo_path' or 'repo_path'; it must have been indexed with 'eulix analyze'."

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
	activeInfo   os.FileInfo // the folder bound as activeRepo
	eulixDir     string
	cfg          *config.Config
	router       *query.Router
	idleTimer    *time.Timer
	lastActivity time.Time

	// Adaptive retrieval (see strategy.go).
	strategy  StrategyConfig
	retriever Retriever                  // optional override (tests); defaults to the Router adapter
	memories  map[string]*projectMemory  // per-repo project memory, guarded by mu
	sent      map[string]map[string]bool // per-repo chunk ranges already sent, guarded by mu

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
	MaxTokens    int      `json:"max_tokens,omitempty" jsonschema:"Token budget for the reply. Default sized by question type"`
}

type fixedCoTArgs struct {
	Query        string   `json:"query" jsonschema:"Question, symbol, or search query about the codebase"`
	RepoPath     string   `json:"repo_path,omitempty" jsonschema:"Optional absolute path to the repository"`
	KnownSymbols []string `json:"known_symbols,omitempty" jsonschema:"Symbols or file paths you already know from this session"`
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

	s.addTypedTools(srv)

	// 'query' is the free-text tool of earlier releases, kept as an alias. It
	// routes callers / definition questions to the index first, like the typed
	// tools, and replaces query_with_cot / query_without_cot via include_cot.
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "query",
		Description: "Free-text question about the codebase (kept for older clients; prefer callers, callees, " +
			"definition, trace or search). Callers and definition questions are answered from the index; others " +
			"return ranked code within a budget. Include exact symbol names or file paths when you know them, " +
			"and pass 'known_symbols' you already know from this session.",
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a queryArgs) (*mcpsdk.CallToolResult, any, error) {
		cot := s.defaultCoT
		if a.IncludeCoT != nil {
			cot = *a.IncludeCoT
		}
		if strings.TrimSpace(a.Query) == "" {
			return nil, nil, errors.New("argument 'query' must be a non-empty string")
		}
		out, err := s.executeQuery(ctx, a.RepoPath, a.Query, a.KnownSymbols,
			queryOpts{cot: cot, maxTokens: a.MaxTokens, route: true})
		if err != nil {
			return nil, nil, fmt.Errorf("query execution failed: %w", err)
		}
		return textResult(out), nil, nil
	})

	// Deprecated aliases with the exact pre-budget output. The retrieval
	// benchmark (bench/lib/mcp.py, scripts/eval_mcp.py) compares releases on
	// query_without_cot, so its output must not change under it.
	for _, legacy := range []struct {
		name string
		cot  bool
	}{{"query_without_cot", false}, {"query_with_cot", true}} {
		cot := legacy.cot
		mcpsdk.AddTool(srv, &mcpsdk.Tool{
			Name:        legacy.name,
			Description: "Deprecated: unbudgeted legacy output, kept for benchmarks. Use search.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true},
		}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a fixedCoTArgs) (*mcpsdk.CallToolResult, any, error) {
			if strings.TrimSpace(a.Query) == "" {
				return nil, nil, errors.New("argument 'query' must be a non-empty string")
			}
			out, err := s.executeQuery(ctx, a.RepoPath, a.Query, a.KnownSymbols, queryOpts{cot: cot, legacy: true})
			if err != nil {
				return nil, nil, fmt.Errorf("query execution failed: %w", err)
			}
			return textResult(out), nil, nil
		})
	}

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
		paths := make([]string, 0, len(res.Roots))
		for _, root := range res.Roots {
			paths = append(paths, parseURIPath(root.URI))
		}
		s.bindFromRoots(paths)
	}()
}

// bindFromRoots binds the first usable root, unless a repository is active.
// The check and the bind share one lock, so a set_repo_path that arrives in
// between is never overwritten.
func (s *Server) bindFromRoots(paths []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range paths {
		if s.activeRepo != "" {
			return // set_repo_path or a query's repo_path got there first
		}
		if _, err := s.setRepoPathLocked(path); err != nil {
			s.log.Info("client root not usable", "root", path, "err", err)
			continue
		}
		return
	}
}
