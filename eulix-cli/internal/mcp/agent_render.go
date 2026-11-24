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
// produced, whether it answers the question, and what was left out. Its job
// is to remove the "let me verify with grep" turn when the answer is final.
//
// complete says whether the reply answers the question, and is "yes" only
// when that was checked: a call path resolved end to end, every use of a name
// listed. "no" carries why. more names what was left out and how to get it
// (a cut body, code for a hop); it does not make an answer incomplete, and it
// never suggests a step the agent cannot take.
func header(answer, source string, complete bool, why, more string) string {
	c := "yes"
	if !complete {
		c = "no"
		if why != "" {
			c += " — " + why
		}
	}
	return headerLine(answer, source, c, more)
}

// rankedHeader heads ranked code. Retrieval cannot check that the code
// answers the question, so complete is "n/a" and the agent judges the code;
// a "no" here sent agents on to verify replies that were already enough.
func rankedHeader(answer, source, more string) string {
	return headerLine(answer, source, "n/a (ranked)", more)
}

func headerLine(answer, source, complete, more string) string {
	h := fmt.Sprintf("answer: %s · source: %s · complete: %s", answer, source, complete)
	if more != "" {
		h += " · more: " + more
	}
	return h + "\n"
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
	// focus are identifiers the question is about (query words, trace hops).
	// A long lower-ranked chunk is sent as a skeleton: its signature,
	// docstring and the lines naming a focus term, instead of its first lines.
	focus []string
}

// renderContext renders ranked context for an agent: a header, then each
// chunk with its file:line range, long chunks cut with a pointer to the rest,
// and chunks already sent this session replaced by a one-line reference.
func renderContext(ac *query.AgentContext, o renderOpts) string {
	body, st := renderChunks(ac, o)
	if body == "" {
		return ""
	}
	var more []string
	if ac.BudgetHit() {
		more = append(more, fmt.Sprintf("lower-ranked chunks dropped at the %d-token budget "+
			"(raise max_tokens, or narrow with known_symbols)", ac.Budget))
	}
	if st.cut > 0 {
		more = append(more, fmt.Sprintf("%d long chunk(s) cut, each names the lines to read", st.cut))
	}
	source := fmt.Sprintf("ranked code, %d chunks, ~%d/%d tokens", st.chunks, ac.Window.TotalTokens, ac.Budget)
	if st.repeats > 0 {
		source += fmt.Sprintf(", %d already sent", st.repeats)
	}

	var b strings.Builder
	b.WriteString(rankedHeader(o.answer, source, strings.Join(more, "; ")))
	b.WriteString("\n")
	b.WriteString(body)
	if o.cot != "" {
		b.WriteString(o.cot)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// renderStats counts what renderChunks did.
type renderStats struct {
	chunks  int // source and metadata chunks rendered
	cut     int // long chunks cut or sent as a skeleton
	repeats int // chunks replaced by an "already sent" reference
}

// renderChunks renders the chunks of ranked context without a header; ""
// when there are none.
func renderChunks(ac *query.AgentContext, o renderOpts) (string, renderStats) {
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
		return "", renderStats{}
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
		content, wasCut := "", false
		if i > 0 {
			content, wasCut = skeletonChunk(c, limit, o.focus)
		} else {
			content, wasCut = capChunk(c, limit)
		}
		if wasCut {
			cut++
		}
		fmt.Fprintf(&body, "[%d] %s (score %.2f)\n%s\n\n", i+1, key, c.Importance, content)
	}
	for i, c := range meta {
		fmt.Fprintf(&body, "[M%d] %s:%d-%d\n%s\n\n", i+1, c.File, c.StartLine, c.EndLine, c.Content)
	}
	return body.String(), renderStats{chunks: len(src) + len(meta), cut: cut, repeats: repeats}
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

// skeletonHeadMax caps the signature + docstring lines kept by a skeleton.
const skeletonHeadMax = 8

// skeletonChunk renders a long lower-ranked chunk as its signature and
// docstring, then only the lines that name one of the focus identifiers, with
// "…" for each gap and a file:line pointer to the full chunk. With no focus
// line in the chunk, or a chunk within limit, it falls back to capChunk.
func skeletonChunk(c utils.ContextChunk, limit int, focus []string) (string, bool) {
	lines := squeezeBlank(strings.Split(strings.TrimRight(c.Content, "\n"), "\n"))
	fenced := len(lines) >= 2 && strings.HasPrefix(lines[0], "```") && strings.HasPrefix(lines[len(lines)-1], "```")
	code := lines
	if fenced {
		code = lines[1 : len(lines)-1]
	}
	if limit <= 0 || len(code) <= limit || len(focus) == 0 {
		return capChunk(c, limit)
	}

	head := skeletonHeadLen(code)
	keep := make([]bool, len(code))
	for i := 0; i < head; i++ {
		keep[i] = true
	}
	kept, hits := head, 0
	for i := head; i < len(code) && kept < limit; i++ {
		if mentionsAny(code[i], focus) {
			keep[i] = true
			kept++
			hits++
		}
	}
	if hits == 0 {
		return capChunk(c, limit)
	}
	// The closing line (a lone brace) keeps the shape readable.
	if last := strings.TrimSpace(code[len(code)-1]); (last == "}" || last == "};") && !keep[len(code)-1] {
		keep[len(code)-1] = true
		kept++
	}

	var b strings.Builder
	if fenced {
		b.WriteString(lines[0])
		b.WriteByte('\n')
	}
	gap := false
	for i, l := range code {
		if !keep[i] {
			gap = true
			continue
		}
		if gap {
			b.WriteString(indentOf(l) + "…\n")
			gap = false
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if gap {
		b.WriteString("…\n")
	}
	fmt.Fprintf(&b, "… skeleton: %d of %d lines shown (read %s:%d-%d for the full chunk)\n",
		kept, len(code), c.File, c.StartLine, c.EndLine)
	if fenced {
		b.WriteString("```")
	}
	return b.String(), true
}

// skeletonHeadLen returns how many leading lines form the signature and
// docstring: up to the line that opens the body ("{" or a trailing ":"), plus
// a docstring or comment block right after it, capped at skeletonHeadMax.
func skeletonHeadLen(code []string) int {
	n := 0
	for n < len(code) && n < skeletonHeadMax {
		t := strings.TrimSpace(code[n])
		n++
		if strings.HasSuffix(t, "{") || strings.HasSuffix(t, ":") {
			break
		}
	}
	// Docstring / leading comments of the body.
	inDoc := false
	for n < len(code) && n < skeletonHeadMax {
		t := strings.TrimSpace(code[n])
		switch {
		case inDoc:
			n++
			if strings.Contains(t, `"""`) || strings.Contains(t, "'''") || strings.HasSuffix(t, "*/") {
				inDoc = false
			}
			continue
		case strings.HasPrefix(t, `"""`) || strings.HasPrefix(t, "'''"):
			n++
			q := t[:3]
			inDoc = strings.Count(t, q) < 2
			continue
		case strings.HasPrefix(t, "/*"):
			n++
			inDoc = !strings.Contains(t, "*/")
			continue
		case strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#"):
			n++
			continue
		}
		break
	}
	return n
}

// mentionsAny reports whether line contains one of the identifiers as a
// whole word (so "run" does not match "runtime").
func mentionsAny(line string, ids []string) bool {
	for _, id := range ids {
		for off := 0; ; {
			j := strings.Index(line[off:], id)
			if j < 0 {
				break
			}
			j += off
			end := j + len(id)
			if (j == 0 || !isIdentByte(line[j-1])) && (end == len(line) || !isIdentByte(line[end])) {
				return true
			}
			off = j + 1
		}
	}
	return false
}

func isIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func indentOf(l string) string {
	return l[:len(l)-len(strings.TrimLeft(l, " \t"))]
}

// focusIdentRe matches identifier-like words worth looking for in code:
// camelCase, snake_case or dotted names, not plain English.
var focusIdentRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*`)

// focusTerms pulls code identifiers out of a question and extra symbols.
// From the question it keeps words with an underscore or an inner capital,
// and the last part of a dotted name; plain English words match too much.
// From syms (resolved symbols such as "Bifrost.handleRequest") it keeps the
// last part: the type name would match every method of the type.
func focusTerms(q string, syms ...string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(w string) {
		if len(w) < 3 || seen[w] {
			return
		}
		seen[w] = true
		out = append(out, w)
	}
	for _, w := range focusIdentRe.FindAllString(q, -1) {
		parts := strings.Split(w, ".")
		for i, p := range parts {
			if looksLikeIdent(p) || (len(parts) > 1 && i == len(parts)-1) {
				add(p)
			}
		}
	}
	for _, w := range syms {
		add(w[strings.LastIndex(w, ".")+1:])
	}
	return out
}

func looksLikeIdent(w string) bool {
	if strings.Contains(w, "_") {
		return true
	}
	for i := 1; i < len(w); i++ {
		if w[i] >= 'A' && w[i] <= 'Z' {
			return true
		}
	}
	return false
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
	why, more := "", ""
	code, tests := rep.DirectCount()
	switch {
	case !rep.Found:
		why = "symbol not found"
	case rep.Scanned && len(rep.Twins) > 0 && len(rep.ByName)+len(rep.AsValue) > 0:
		why = "a use by name may belong to another definition of the name, see the note"
	case !rep.Scanned && code+tests == 0:
		why = "none found statically; unresolved calls are possible, confirm with grep"
	case rep.Scanned && rep.Depth > 1 && len(rep.ByName)+len(rep.AsValue) > 0:
		why = "uses by name are listed for the first level only"
	}
	if n := rep.Omitted + rep.NameOmitted; n > 0 {
		more = fmt.Sprintf("%d more sites not shown (pass a larger limit, or a qualified name)", n)
		if why == "" {
			why = "list cut by the limit"
		}
	}
	src := "call graph (exact, static)"
	if rep.Depth > 1 {
		src = fmt.Sprintf("call graph (exact, static, depth %d)", rep.Depth)
	}
	if rep.Scanned {
		src += " + name scan"
	}
	return header(answer, src, rep.Complete(), why, more) + query.FormatCallReport(rep)
}

// renderDefinitions renders definitions with their header. A definition
// answers "where is X" once it is found; a cut body is named under more, with
// the exact lines to read for the rest.
func renderDefinitions(symbol string, defs []query.Definition, suggested []string) string {
	why := ""
	if len(defs) == 0 {
		why = "symbol not found"
	}
	var cut []string
	for _, d := range defs {
		if d.Truncated() {
			cut = append(cut, fmt.Sprintf("%s lines %d-%d", d.File, d.BodyEnd+1, d.End))
		}
	}
	more := ""
	if len(cut) > 0 {
		more = "body cut; read " + strings.Join(cut, ", ") + " for the rest"
	}
	return header("definition", "index (exact)", len(defs) > 0, why, more) +
		query.FormatDefinitions(symbol, defs, suggested)
}

// ========== Lean search replies ==========
//
// A search reply is re-read on every later turn, and agents read the files
// it points to anyway: in the Opus 5.5 benchmark the median search reply was
// 2.4k tokens, and the agent followed it with reads of the same code. A lean
// reply sends the best match in full (a question one chunk answers still
// costs one call) and every other match as one line: file:lines, signature,
// and the question's identifiers it mentions. The agent opens what it needs.

// maxLeanListed caps the one-line matches of a lean reply.
const maxLeanListed = 12

// maxLeanShortLines: a chunk this short (fences included) is sent in full;
// one line naming it would cost about as much.
const maxLeanShortLines = 8

// chunkHead matches the first line of a rendered chunk.
var chunkHead = regexp.MustCompile(`^\[(M?\d+)\] (\S+:\d+-\d+)(?: \(score ([\d.]+)\))?$`)

// leanSearch rewrites a rendered ranked reply (renderContext's format) into a
// lean one: header, the first chunk in full, then one line per other chunk.
// Replies without chunks (the lexical fallback, an exact answer) come back
// unchanged.
func leanSearch(text string, focus []string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "answer: ") {
		return text
	}
	type block struct {
		head  string
		lines []string
	}
	var pre []string // between the header and the first chunk
	var blocks []block
	for _, l := range lines[1:] {
		switch {
		case chunkHead.MatchString(l):
			blocks = append(blocks, block{head: l})
		case strings.HasPrefix(l, "(already sent this session) "):
			blocks = append(blocks, block{head: l})
		case len(blocks) == 0:
			pre = append(pre, l)
		default:
			b := &blocks[len(blocks)-1]
			b.lines = append(b.lines, l)
		}
	}
	// The best chunk, and every short one (listing it would save nothing),
	// are sent in full; the others are listed.
	keep := make([]bool, len(blocks))
	first := true
	for i, b := range blocks {
		m := chunkHead.FindStringSubmatch(b.head)
		if m == nil { // an "(already sent this session)" line stays as it is
			keep[i] = true
			continue
		}
		if strings.HasPrefix(m[1], "M") {
			continue
		}
		if first || len(b.lines) <= maxLeanShortLines {
			keep[i] = true
			first = false
		}
	}
	var list []string
	others := 0
	for i, b := range blocks {
		if keep[i] {
			continue
		}
		others++
		if len(list) >= maxLeanListed {
			continue
		}
		m := chunkHead.FindStringSubmatch(b.head)
		body := strings.Join(b.lines, "\n")
		line := fmt.Sprintf("  #%s %s", m[1], m[2])
		if m[3] != "" {
			line += " (" + m[3] + ")"
		}
		if sum := chunkSummary(body); sum != "" {
			line += "  " + sum
		}
		if hits := focusHits(body, focus, 3); len(hits) > 0 {
			line += "  · mentions " + strings.Join(hits, ", ")
		}
		list = append(list, line)
	}
	if others == 0 || first {
		return text
	}

	answer, source, complete, more, ok := parseHeader(lines[0])
	if !ok {
		return text
	}
	source = fmt.Sprintf("%s; %d listed without code", source, others)
	lean := "listed matches carry no code (bodies: true sends it)"
	if more != "" {
		more = lean + "; " + more
	} else {
		more = lean
	}

	var b strings.Builder
	b.WriteString(headerLine(answer, source, complete, more))
	for _, l := range pre {
		b.WriteString(l + "\n")
	}
	for i, blk := range blocks {
		if !keep[i] {
			continue
		}
		b.WriteString(blk.head + "\n")
		for _, l := range blk.lines {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString("other matches, best first:\n")
	for _, l := range list {
		b.WriteString(l + "\n")
	}
	if others > len(list) {
		fmt.Fprintf(&b, "  … %d more\n", others-len(list))
	}
	return b.String()
}

// parseHeader splits a header line built by headerLine.
func parseHeader(h string) (answer, source, complete, more string, ok bool) {
	rest, found := strings.CutPrefix(h, "answer: ")
	if !found {
		return
	}
	answer, rest, found = strings.Cut(rest, " · source: ")
	if !found {
		return
	}
	source, rest, found = strings.Cut(rest, " · complete: ")
	if !found {
		return
	}
	complete, more, _ = strings.Cut(rest, " · more: ")
	return answer, source, complete, more, true
}

// chunkSummary names a chunk in one line: its signature when the chunk
// carries one ("Signature: ..."), else its first line of code.
func chunkSummary(body string) string {
	for _, l := range strings.Split(body, "\n") {
		if sig, ok := strings.CutPrefix(strings.TrimSpace(l), "Signature: "); ok {
			return truncate(sig, 120)
		}
	}
	return firstCodeLine(body)
}

// focusHits returns up to n of the focus identifiers the text mentions.
func focusHits(text string, focus []string, n int) []string {
	var out []string
	for _, f := range focus {
		if len(out) == n {
			break
		}
		if mentionsAny(text, []string{f}) {
			out = append(out, f)
		}
	}
	return out
}
