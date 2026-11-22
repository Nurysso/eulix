// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"fmt"
	"regexp"
	"strings"

	"eulix/internal/query"
	"eulix/internal/utils"
)

// ========== Reply budgets ==========
//
// An MCP reply stays in the agent's context and is re-read on every later
// turn, so a 6k-token reply that replaces one grep costs far more than the
// grep. Replies are sized by what the question needs, not by the LLM's
// context window.

// Default context budgets (tokens) per answer type. Explicit max_tokens and
// [mcp] reply_tokens override them (reply_tokens is the ceiling for "search").
const (
	budgetDefinition = 600
	budgetExplain    = 1500
	budgetTrace      = 2000
	budgetFloor      = 200
	budgetCeiling    = 16_000

	defaultReplyTokens   = 1800
	defaultMaxChunkLines = 60

	// callSiteLimit caps the sites in one callers/callees reply.
	callSiteLimit = 40
	// definitionBodyLines caps the body lines of one definition.
	definitionBodyLines = 40
)

// replyTokens is the configured default budget for ranked context.
func (s *Server) replyTokens() int {
	if s.cfg != nil && s.cfg.MCP.ReplyTokens > 0 {
		return s.cfg.MCP.ReplyTokens
	}
	return defaultReplyTokens
}

// maxChunkLines is the configured per-chunk line cap.
func (s *Server) maxChunkLines() int {
	if s.cfg != nil && s.cfg.MCP.MaxChunkLines > 0 {
		return s.cfg.MCP.MaxChunkLines
	}
	return defaultMaxChunkLines
}

// budgetFor picks the context budget for an answer type: the caller's
// max_tokens when given, else a per-type default capped by reply_tokens.
func (s *Server) budgetFor(intent query.AgentIntent, maxTokens int) int {
	if maxTokens > 0 {
		return min(max(maxTokens, budgetFloor), budgetCeiling)
	}
	ceiling := s.replyTokens()
	switch intent {
	case query.IntentDefinitionQ:
		return min(budgetDefinition, ceiling)
	case query.IntentExplainQ:
		return min(budgetExplain, ceiling)
	case query.IntentTraceQ:
		// A trace is the one answer that needs room; it may exceed the
		// search default but not a user-raised reply_tokens.
		return max(budgetTrace, ceiling)
	default:
		return ceiling
	}
}

// ========== Reply header ==========

// header is the first line of every reply: what the reply is, how it was
// produced, and whether anything was left out. Its job is to remove the
// "let me verify with grep" turn when the answer is already complete.
func header(answer, source string, complete bool, why string) string {
	c := "yes"
	if !complete {
		c = "no"
		if why != "" {
			c += " — " + why
		}
	}
	return fmt.Sprintf("answer: %s · source: %s · complete: %s\n", answer, source, c)
}

// ========== Ranked context rendering ==========

// chunkRef matches a rendered chunk heading, "[3] path/file.go:10-42 ...".
var chunkRef = regexp.MustCompile(`(?m)^\[\d+\] (\S+:\d+-\d+)`)

// sentChunks returns the chunk ranges a reply carries in full, so the
// session can refer to them later instead of resending them.
func sentChunks(text string) []string {
	var out []string
	for _, m := range chunkRef.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

// renderOpts controls renderContext.
type renderOpts struct {
	answer   string          // answer type for the header: search, explain, trace
	maxLines int             // per-chunk line cap (top chunk); others get half
	sent     map[string]bool // chunk ranges already sent this session (read-only)
	cot      string          // CoT block to append, "" for none
}

// renderContext renders ranked context for an agent: a header, then each
// chunk with its file:line range, long chunks cut with a pointer to the rest,
// and chunks already sent this session replaced by a one-line reference.
func renderContext(ac *query.AgentContext, o renderOpts) string {
	win := ac.Window
	var src, meta []utils.ContextChunk
	if win != nil {
		for _, c := range win.Chunks {
			switch {
			case strings.TrimSpace(c.Content) == "":
			case strings.HasPrefix(c.Content, "[METADATA]"):
				meta = append(meta, c)
			default:
				src = append(src, c)
			}
		}
	}
	if len(src)+len(meta) == 0 {
		return ""
	}

	// A reply made only of repeats tells the agent nothing new: it asked again
	// because it no longer has them (compaction, a new subtask). Send those
	// in full; reference repeats only next to new chunks.
	sent := o.sent
	allRepeats := len(src) > 0
	for _, c := range src {
		if !sent[fmt.Sprintf("%s:%d-%d", c.File, c.StartLine, c.EndLine)] {
			allRepeats = false
			break
		}
	}
	if allRepeats {
		sent = nil
	}

	var body strings.Builder
	cut, repeats := 0, 0
	for i, c := range src {
		key := fmt.Sprintf("%s:%d-%d", c.File, c.StartLine, c.EndLine)
		if sent[key] {
			repeats++
			fmt.Fprintf(&body, "(already sent this session) %s  %s\n\n", key, firstCodeLine(c.Content))
			continue
		}
		limit := o.maxLines
		if i > 0 {
			limit = max(o.maxLines/2, 10)
		}
		content, wasCut := capChunk(c, limit)
		if wasCut {
			cut++
		}
		fmt.Fprintf(&body, "[%d] %s (score %.2f)\n%s\n\n", i+1, key, c.Importance, content)
	}
	for i, c := range meta {
		fmt.Fprintf(&body, "[M%d] %s:%d-%d\n%s\n\n", i+1, c.File, c.StartLine, c.EndLine, c.Content)
	}

	var notes []string
	if ac.BudgetHit() {
		notes = append(notes, fmt.Sprintf("budget hit (%d tokens), lower-ranked chunks dropped; "+
			"raise max_tokens or narrow with known_symbols for more", ac.Budget))
	}
	if cut > 0 {
		notes = append(notes, fmt.Sprintf("%d long chunk(s) cut, each names the lines to read", cut))
	}
	source := fmt.Sprintf("ranked code, %d chunks, ~%d/%d tokens", len(src)+len(meta), win.TotalTokens, ac.Budget)
	if repeats > 0 {
		source += fmt.Sprintf(", %d already sent", repeats)
	}

	var b strings.Builder
	b.WriteString(header(o.answer, source, len(notes) == 0, strings.Join(notes, "; ")))
	b.WriteString("\n")
	b.WriteString(body.String())
	if o.cot != "" {
		b.WriteString(o.cot)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// capChunk keeps at most limit code lines of a chunk, closing its fence and
// naming the rest by file:line so the agent can read it if it needs to.
func capChunk(c utils.ContextChunk, limit int) (string, bool) {
	lines := squeezeBlank(strings.Split(strings.TrimRight(c.Content, "\n"), "\n"))
	content := strings.Join(lines, "\n")
	fenced := len(lines) >= 2 && strings.HasPrefix(lines[0], "```") && strings.HasPrefix(lines[len(lines)-1], "```")
	code := lines
	if fenced {
		code = lines[1 : len(lines)-1]
	}
	if limit <= 0 || len(code) <= limit {
		return content, false
	}
	var b strings.Builder
	if fenced {
		b.WriteString(lines[0])
		b.WriteByte('\n')
	}
	for _, l := range code[:limit] {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "… %d more lines cut (read %s:%d-%d for the full chunk)\n",
		len(code)-limit, c.File, c.StartLine, c.EndLine)
	if fenced {
		b.WriteString("```")
	}
	return b.String(), true
}

// squeezeBlank collapses runs of blank lines to one. Class skeletons leave a
// blank line where each method body was; they cost tokens and say nothing.
func squeezeBlank(lines []string) []string {
	out := lines[:0:0]
	blank := false
	for _, l := range lines {
		isBlank := strings.TrimSpace(l) == ""
		if isBlank && blank {
			continue
		}
		blank = isBlank
		out = append(out, l)
	}
	return out
}

// firstCodeLine returns the first non-empty line inside a chunk's fence,
// so a repeated chunk's reference still names what it is.
func firstCodeLine(content string) string {
	for _, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "```") {
			continue
		}
		return truncate(t, 120)
	}
	return ""
}

// ========== Structured answers ==========

// renderCalls renders a callers/callees report with its header.
func renderCalls(rep *query.CallReport) string {
	answer := "callers"
	if rep.Callees {
		answer = "callees"
	}
	why := ""
	switch {
	case !rep.Found:
		why = "symbol not found"
	case len(rep.Sites) == 0:
		why = "none found statically; unresolved calls are possible, confirm with grep"
	case rep.Omitted > 0:
		why = fmt.Sprintf("%d more sites; pass a larger limit or a qualified name", rep.Omitted)
	}
	src := "call graph (exact, static)"
	if rep.Depth > 1 {
		src = fmt.Sprintf("call graph (exact, static, depth %d)", rep.Depth)
	}
	return header(answer, src, rep.Complete(), why) + query.FormatCallReport(rep)
}

// renderDefinitions renders definitions with their header.
func renderDefinitions(symbol string, defs []query.Definition, suggested []string) string {
	why := ""
	complete := len(defs) > 0
	if !complete {
		why = "symbol not found"
	}
	for _, d := range defs {
		if d.Truncated() {
			complete, why = false, "body cut, read the named lines for the rest"
			break
		}
	}
	return header("definition", "index (exact)", complete, why) + query.FormatDefinitions(symbol, defs, suggested)
}
