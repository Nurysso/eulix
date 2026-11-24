// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
	Symbol   string `json:"symbol" jsonschema:"Function or method name, bare or qualified (handleRequest, Bifrost.handleRequest). Several names, comma-separated, are answered in one reply"`
	Depth    int    `json:"depth,omitempty" jsonschema:"Hops to walk, 1-4. Default 1"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Max call sites listed. Default 40"`
	RepoPath string `json:"repo_path,omitempty" jsonschema:"Repository path; default: the active one"`
}

type definitionArgs struct {
	Symbol    string `json:"symbol" jsonschema:"Function, method, class or type name, bare or qualified. Several names, comma-separated, are answered in one reply"`
	BodyLines *int   `json:"body_lines,omitempty" jsonschema:"Body lines to include (0 = signature only). Default 40"`
	RepoPath  string `json:"repo_path,omitempty" jsonschema:"Repository path; default: the active one"`
}

type traceArgs struct {
	From      string `json:"from,omitempty" jsonschema:"Start function. Omit to walk back from 'to' to an entry point"`
	To        string `json:"to,omitempty" jsonschema:"Target function"`
	Question  string `json:"question,omitempty" jsonschema:"The flow question in words; names the endpoints when 'from'/'to' are omitted and ranks the code shown"`
	MaxTokens int    `json:"max_tokens,omitempty" jsonschema:"Token budget for the code after the path. Default 2000"`
	RepoPath  string `json:"repo_path,omitempty" jsonschema:"Repository path; default: the active one"`
}

type searchArgs struct {
	Question     string   `json:"question" jsonschema:"What to find, in words; include exact symbol names or file paths when known"`
	KnownSymbols []string `json:"known_symbols,omitempty" jsonschema:"Symbols or file paths you already know from this session; they focus retrieval"`
	MaxTokens    int      `json:"max_tokens,omitempty" jsonschema:"Reply token budget. Default 600-2000 by question type"`
	Format       string   `json:"format,omitempty" jsonschema:"'cot' appends Chain-of-Thought instructions"`
	Fresh        bool     `json:"fresh,omitempty" jsonschema:"Resend chunks already sent this session"`
	Bodies       bool     `json:"bodies,omitempty" jsonschema:"Send the code of every match, not only the best"`
	RepoPath     string   `json:"repo_path,omitempty" jsonschema:"Repository path; default: the active one"`
}

// Tool descriptions are sent to the model on every turn of every session, so
// each word costs; agent_test.go guards their total size.
const (
	toolCallersDesc = "Who calls a function or method: one line per call site (file:line, enclosing function, " +
		"calling line), tests apart. Calls the call graph could not resolve (through a field or interface) and " +
		"uses as a value (handler, callback) are found by name. Use instead of grep for usages and impact."
	toolCalleesDesc = "Calls made by a function or method, one line each with the calling line. " +
		"Use to walk down a flow."
	toolDefinitionDesc = "Where a symbol is defined: file:line range, signature, docstring and a short body " +
		"(a class: its method list). Use instead of grepping for 'func X' or 'def X'."
	toolTraceDesc = "Call path from one function to another, each hop with file:line and the calling line, " +
		"then code for the path. Give 'from' and 'to', or only 'to' to walk back to an entry point. Where the " +
		"static chain breaks (goroutine, channel, callback), says where it starts instead."
	toolSearchDesc = "Ranked code for a concept or open question (how does X work, where is Y handled): the " +
		"best match in full, the others one line each (file:lines, signature). Use grep for literal strings, " +
		"config keys and log messages."
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

// maxSymbolsPerCall caps the symbols one callers/callees/definition call
// answers, so a long list cannot turn into one huge reply.
const maxSymbolsPerCall = 8

// splitSymbols splits a comma-separated symbol argument, dropping blanks and
// repeats. Every call is a turn the agent pays for in full, so several
// symbols asked together cost one turn instead of several.
func splitSymbols(arg string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(arg, ",") {
		if p = strings.TrimSpace(p); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	switch {
	case len(out) == 0:
		return nil, errors.New("argument 'symbol' must be a non-empty string")
	case len(out) > maxSymbolsPerCall:
		return nil, fmt.Errorf("at most %d symbols per call; got %d", maxSymbolsPerCall, len(out))
	}
	return out, nil
}

// answerEach answers each symbol with one, then joins the replies. One
// symbol gets its reply unchanged; several get a summary line first, then
// each reply with its own header.
func answerEach(answer string, symbols []string, one func(string) (string, bool, error)) (string, error) {
	if len(symbols) == 1 {
		text, _, err := one(symbols[0])
		return text, err
	}
	var parts, open []string
	for _, sym := range symbols {
		text, complete, err := one(sym)
		if err != nil {
			return "", err
		}
		if !complete {
			open = append(open, sym)
		}
		parts = append(parts, text)
	}
	c := "yes for all"
	if len(open) > 0 {
		c = "no for " + strings.Join(open, ", ") + " (see each header)"
	}
	return fmt.Sprintf("answer: %s for %d symbols · complete: %s\n\n%s", answer, len(symbols), c,
		strings.Join(parts, "\n")), nil
}

func (s *Server) toolCalls(ctx context.Context, a callsArgs, callees bool) (*mcpsdk.CallToolResult, any, error) {
	symbols, err := splitSymbols(a.Symbol)
	if err != nil {
		return nil, nil, err
	}
	answer := "callers"
	if callees {
		answer = "callees"
	}
	var out string
	err = s.withRepo(ctx, a.RepoPath, func() error {
		limit := a.Limit
		if limit <= 0 {
			limit = callSiteLimit
		}
		var err error
		out, err = answerEach(answer, symbols, func(sym string) (string, bool, error) {
			rep, err := s.router.Calls(sym, callees, a.Depth, min(limit, 500))
			if err != nil {
				return "", false, err
			}
			return renderCalls(rep), rep.Complete(), nil
		})
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return textResult(out), nil, nil
}

func (s *Server) toolDefinition(ctx context.Context, _ *mcpsdk.CallToolRequest, a definitionArgs) (*mcpsdk.CallToolResult, any, error) {
	symbols, err := splitSymbols(a.Symbol)
	if err != nil {
		return nil, nil, err
	}
	body := definitionBodyLines
	if a.BodyLines != nil {
		body = min(max(*a.BodyLines, 0), 400)
	}
	var out string
	err = s.withRepo(ctx, a.RepoPath, func() error {
		var err error
		out, err = answerEach("definition", symbols, func(sym string) (string, bool, error) {
			defs, suggested, err := s.router.Definitions(sym, body, 3)
			if err != nil {
				return "", false, err
			}
			return renderDefinitions(sym, defs, suggested), len(defs) > 0, nil
		})
		return err
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
		queryOpts{cot: cot, maxTokens: a.MaxTokens, fresh: a.Fresh, answer: "search", lean: !a.Bodies && !cot})
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
	// Without a path, the code worth showing is the target's own chain and
	// the functions that use the chain's start by name: ranked retrieval on
	// the question alone returned unrelated code here.
	for _, h := range tr.Upstream {
		hopSyms = append(hopSyms, h.Caller)
	}
	for i, u := range tr.StartUses {
		if i < 3 && u.Enclosing != "" {
			hopSyms = append(hopSyms, u.Enclosing)
		}
	}
	q := strings.TrimSpace(question)
	if q == "" {
		q = strings.TrimSpace(fmt.Sprintf("how does %s reach %s", from, to))
	}
	code, noRoom := "", remaining < 300
	var missing []string
	var st renderStats
	if !noRoom {
		ac, err := s.router.AgentContextFor(composeAnchored(q, dedupeStrings(hopSyms)), remaining, s.maxChunkLines())
		if err == nil {
			code, st = renderChunks(ac, renderOpts{answer: "trace", maxLines: s.maxChunkLines(), sent: s.sentLocked(),
				focus: focusTerms(q, hopSyms...)})
			s.markSentLocked(code)
			if tr.Found {
				missing = withoutCode(ac, dedupeStrings(hopSyms), tr.Locations)
			}
		} else if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
	}

	complete, why := traceVerdict(from, to, tr)
	var more []string
	switch {
	case !tr.Found:
	case noRoom:
		more = append(more, "no room for code after the path; call definition on a hop for its body")
	case len(missing) > 0:
		more = append(more, fmt.Sprintf("no code shown for %s (definition takes several names, comma-separated)",
			strings.Join(capList(missing, 4), ", ")))
	}
	if st.cut > 0 {
		more = append(more, fmt.Sprintf("%d long body(ies) cut, each names the lines to read", st.cut))
	}
	source := "call graph (static) + ranked code"
	if tr.StartScanned {
		source = "call graph (static) + name scan + ranked code"
	}
	var b strings.Builder
	b.WriteString(header("trace", source, complete, why, strings.Join(more, "; ")))
	if path != "" {
		b.WriteString(path)
	}
	if code != "" {
		b.WriteString("\ncode for the path:\n\n")
		b.WriteString(strings.TrimRight(code, "\n") + "\n")
	}
	return b.String(), nil
}

// traceVerdict decides whether a trace answers its question. A path resolved
// end to end does, whatever code fitted after it. So does a chain whose first
// function nothing calls statically, once the name scan shows where it is
// started or registered and no other definition shares its name.
func traceVerdict(from, to string, tr *query.TraceResult) (bool, string) {
	startFound := tr.StartScanned && len(tr.StartUses) > 0 && len(tr.StartTwins) == 0
	switch {
	case to == "":
		return false, "no symbol to trace to; pass 'to' (and 'from')"
	case len(tr.Missing) > 0:
		return false, "not in the call graph: " + strings.Join(tr.Missing, ", ") + "; check the name with definition"
	case !tr.Found && from != "":
		if len(tr.Upstream) > 0 || len(tr.StartUses) > 0 {
			return false, "no static path; the target's own chain and where it starts are listed"
		}
		return false, "no static path, see the note"
	case !tr.Found:
		if startFound {
			return true, ""
		}
		return false, "nothing calls it statically, see the note"
	case from == "" && !tr.EntryPoint && !startFound:
		return false, fmt.Sprintf("chain starts at %s, which nothing calls statically; look for where it is registered", tr.From)
	}
	return true, ""
}

// withoutCode returns the path functions whose definition no chunk of the
// window covers, in path order.
func withoutCode(ac *query.AgentContext, syms []string, locs map[string]string) []string {
	if ac == nil || ac.Window == nil {
		return syms
	}
	var out []string
	for _, sym := range syms {
		file, line, ok := splitLoc(locs[sym])
		if !ok {
			continue
		}
		covered := false
		for _, c := range ac.Window.Chunks {
			if c.File == file && c.StartLine <= line && line <= c.EndLine {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, sym)
		}
	}
	return out
}

// splitLoc parses "file:line".
func splitLoc(loc string) (string, int, bool) {
	i := strings.LastIndex(loc, ":")
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(loc[i+1:])
	if err != nil {
		return "", 0, false
	}
	return loc[:i], n, true
}

// capList keeps the first n items and counts the rest.
func capList(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(append([]string(nil), xs[:n]...), fmt.Sprintf("+%d more", len(xs)-n))
}
