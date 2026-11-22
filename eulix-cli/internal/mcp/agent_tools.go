// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"eulix/internal/query"
)

// ========== Typed tools ==========
//
// One free-text box made every request go through a keyword classifier,
// which is how "What calls X?" ended up answered with X's definition. Typed
// tools let the agent say what it wants; the classifier only runs for
// 'search' and the legacy 'query'.

type callsArgs struct {
	Symbol   string `json:"symbol" jsonschema:"Function or method name, bare or qualified (handleRequest, Bifrost.handleRequest, SearchWorker::search)"`
	Depth    int    `json:"depth,omitempty" jsonschema:"Hops to walk (1 = direct edges only, max 4). Default 1"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum call sites to list. Default 40"`
	RepoPath string `json:"repo_path,omitempty" jsonschema:"Optional absolute path to the repository; defaults to the active one"`
}

type definitionArgs struct {
	Symbol    string `json:"symbol" jsonschema:"Function, method, class or type name, bare or qualified"`
	BodyLines *int   `json:"body_lines,omitempty" jsonschema:"Lines of body to include (0 = signature only). Default 40"`
	RepoPath  string `json:"repo_path,omitempty" jsonschema:"Optional absolute path to the repository; defaults to the active one"`
}

type traceArgs struct {
	From      string `json:"from,omitempty" jsonschema:"Start symbol (an entry point, handler, or any caller). Omit to walk back from 'to' to an entry point"`
	To        string `json:"to,omitempty" jsonschema:"Target symbol the path should reach"`
	Question  string `json:"question,omitempty" jsonschema:"The flow question in words; used for 'from'/'to' when they are omitted and to rank the code shown after the path"`
	MaxTokens int    `json:"max_tokens,omitempty" jsonschema:"Token budget for the code after the path. Default 2000"`
	RepoPath  string `json:"repo_path,omitempty" jsonschema:"Optional absolute path to the repository; defaults to the active one"`
}

type searchArgs struct {
	Question     string   `json:"question" jsonschema:"What to find, in words. Include exact symbol names or file paths when you know them"`
	KnownSymbols []string `json:"known_symbols,omitempty" jsonschema:"Symbols or file paths relevant to the question that you already know from this session; they enable precise multi-hop retrieval"`
	MaxTokens    int      `json:"max_tokens,omitempty" jsonschema:"Token budget for the reply. Default sized by question type (600 to 2000)"`
	Format       string   `json:"format,omitempty" jsonschema:"'plain' (default) or 'cot' to append Chain-of-Thought instructions"`
	Fresh        bool     `json:"fresh,omitempty" jsonschema:"Resend chunks already returned in this session instead of referencing them"`
	RepoPath     string   `json:"repo_path,omitempty" jsonschema:"Optional absolute path to the repository; defaults to the active one"`
}

const (
	toolCallersDesc = "Exact call sites of a function or method from the call graph: one line per site with the " +
		"enclosing function and the calling line, tests counted separately, and a 'complete' flag. Use for " +
		"'who calls X', 'where is X used', impact of changing X. Much cheaper and more complete than grep."
	toolCalleesDesc = "Calls made by a function or method, from the call graph, one line per call with the calling " +
		"line. Use for 'what does X call' and to walk down a flow one hop at a time."
	toolDefinitionDesc = "Where a symbol is defined: file:line range, signature, first docstring paragraph and a short " +
		"body (a class comes back as its header and method list). Use instead of grepping for 'func X' / 'def X'."
	toolTraceDesc = "How one function reaches another: the ordered call path from the call graph (each hop with " +
		"file:line and the calling line), then the relevant code within a budget. Give 'from' and 'to', or only " +
		"'to' to walk back to an entry point. Says where a chain breaks (goroutine, channel, callback)."
	toolSearchDesc = "Ranked code for a concept or open question (how does X work, where is Y handled). Replies are " +
		"budgeted (max_tokens), start with a completeness line, and refer to chunks already sent this session " +
		"instead of resending them. For literal strings, config keys or log messages, grep is better."
)

func (s *Server) addTypedTools(srv *mcpsdk.Server) {
	readOnly := &mcpsdk.ToolAnnotations{ReadOnlyHint: true}

	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "callers", Description: toolCallersDesc, Annotations: readOnly},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, a callsArgs) (*mcpsdk.CallToolResult, any, error) {
			return s.toolCalls(ctx, a, false)
		})
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "callees", Description: toolCalleesDesc, Annotations: readOnly},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, a callsArgs) (*mcpsdk.CallToolResult, any, error) {
			return s.toolCalls(ctx, a, true)
		})
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "definition", Description: toolDefinitionDesc, Annotations: readOnly},
		s.toolDefinition)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "trace", Description: toolTraceDesc, Annotations: readOnly},
		s.toolTrace)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "search", Description: toolSearchDesc, Annotations: readOnly},
		s.toolSearch)
}

func (s *Server) toolCalls(ctx context.Context, a callsArgs, callees bool) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(a.Symbol) == "" {
		return nil, nil, errors.New("argument 'symbol' must be a non-empty string")
	}
	var out string
	err := s.withRepo(ctx, a.RepoPath, func() error {
		limit := a.Limit
		if limit <= 0 {
			limit = callSiteLimit
		}
		rep, err := s.router.Calls(a.Symbol, callees, a.Depth, min(limit, 500))
		if err != nil {
			return err
		}
		out = renderCalls(rep)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return textResult(out), nil, nil
}

func (s *Server) toolDefinition(ctx context.Context, _ *mcpsdk.CallToolRequest, a definitionArgs) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(a.Symbol) == "" {
		return nil, nil, errors.New("argument 'symbol' must be a non-empty string")
	}
	body := definitionBodyLines
	if a.BodyLines != nil {
		body = min(max(*a.BodyLines, 0), 400)
	}
	var out string
	err := s.withRepo(ctx, a.RepoPath, func() error {
		defs, suggested, err := s.router.Definitions(a.Symbol, body, 3)
		if err != nil {
			return err
		}
		out = renderDefinitions(a.Symbol, defs, suggested)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return textResult(out), nil, nil
}

func (s *Server) toolSearch(ctx context.Context, _ *mcpsdk.CallToolRequest, a searchArgs) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(a.Question) == "" {
		return nil, nil, errors.New("argument 'question' must be a non-empty string")
	}
	cot := strings.EqualFold(strings.TrimSpace(a.Format), "cot")
	out, err := s.executeQuery(ctx, a.RepoPath, a.Question, a.KnownSymbols,
		queryOpts{cot: cot, maxTokens: a.MaxTokens, fresh: a.Fresh, answer: "search"})
	if err != nil {
		return nil, nil, fmt.Errorf("query execution failed: %w", err)
	}
	return textResult(out), nil, nil
}

func (s *Server) toolTrace(ctx context.Context, _ *mcpsdk.CallToolRequest, a traceArgs) (*mcpsdk.CallToolResult, any, error) {
	from, to := strings.TrimSpace(a.From), strings.TrimSpace(a.To)
	if to == "" && strings.TrimSpace(a.Question) == "" {
		return nil, nil, errors.New("give 'to' (and optionally 'from'), or a 'question' naming the symbols")
	}
	var out string
	err := s.withRepo(ctx, a.RepoPath, func() error {
		var err error
		out, err = s.traceLocked(ctx, from, to, a.Question, a.MaxTokens)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return textResult(out), nil, nil
}

// traceLocked builds a trace reply: the call path first, then ranked code
// for the hops within the remaining budget. Caller must hold s.mu.
func (s *Server) traceLocked(ctx context.Context, from, to, question string, maxTokens int) (string, error) {
	if to == "" {
		// Take the endpoints from the symbols the question names, in order.
		var syms []string
		for _, a := range ExtractAnchors(question) {
			if !rePath.MatchString(a.Text) && s.router.HasSymbol(a.Text) {
				syms = append(syms, strings.TrimSuffix(strings.Trim(a.Text, "`"), "()"))
			}
		}
		switch {
		case len(syms) >= 2:
			from, to = syms[0], syms[len(syms)-1]
		case len(syms) == 1:
			to = syms[0]
		}
	}

	var path string
	tr := &query.TraceResult{}
	if to != "" {
		var err error
		if tr, err = s.router.Trace(from, to, 8); err != nil {
			return "", err
		}
		path = query.FormatTrace(tr)
	}

	budget := s.budgetFor(query.IntentTraceQ, maxTokens)
	remaining := budget - len(path)/4

	// Code for the hops (or, without a path, for the question itself).
	var hopSyms []string
	for _, h := range tr.Hops {
		hopSyms = append(hopSyms, h.Caller)
	}
	if tr.To != "" {
		hopSyms = append(hopSyms, tr.To)
	}
	q := strings.TrimSpace(question)
	if q == "" {
		q = strings.TrimSpace(fmt.Sprintf("how does %s reach %s", from, to))
	}
	code, budgetHit := "", false
	if remaining >= 300 {
		ac, err := s.router.AgentContextFor(composeAnchored(q, dedupeStrings(hopSyms)), remaining, s.maxChunkLines())
		if err == nil {
			code = renderContext(ac, renderOpts{answer: "trace", maxLines: s.maxChunkLines(), sent: s.sentLocked()})
			code = stripHeader(code)
			budgetHit = ac.BudgetHit()
			s.markSentLocked(code)
		} else if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
	}

	var why []string
	switch {
	case to == "":
		why = append(why, "no symbol to trace to; pass 'to' (and 'from')")
	case !tr.Found:
		why = append(why, "no static path, see the note")
	case tr.Reason != "":
		why = append(why, "chain starts at a function with no static callers")
	}
	if budgetHit {
		why = append(why, "code budget hit; raise max_tokens for more")
	}
	if code == "" && remaining < 300 {
		why = append(why, "no room left for code after the path; raise max_tokens or use definition on a hop")
	}
	source := "call graph (static) + ranked code"
	var b strings.Builder
	b.WriteString(header("trace", source, len(why) == 0, strings.Join(why, "; ")))
	if path != "" {
		b.WriteString(path)
	}
	if code != "" {
		b.WriteString("\ncode for the path:\n\n")
		b.WriteString(code)
	}
	return b.String(), nil
}

// stripHeader drops the "answer: ..." line renderContext puts first, for
// replies that carry their own header.
func stripHeader(text string) string {
	if strings.HasPrefix(text, "answer: ") {
		if i := strings.Index(text, "\n"); i >= 0 {
			return strings.TrimLeft(text[i+1:], "\n")
		}
	}
	return text
}
