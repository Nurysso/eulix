// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package query

import (
	"fmt"
	"strings"
)

// maxTestSitesListed caps how many test call sites are named on the summary
// line; the rest are only counted.
const maxTestSitesListed = 4

// FormatCallReport renders a call report compactly: one summary line, then
// one line per call site with the enclosing function and the calling line.
// It is written for an agent (every line carries file:line) but reads fine in
// a terminal.
func FormatCallReport(rep *CallReport) string {
	var b strings.Builder
	dir := "callers of"
	if rep.Callees {
		dir = "calls made by"
	}

	if !rep.Found {
		fmt.Fprintf(&b, "%s %s: not found in the call graph.", dir, rep.Symbol)
		if len(rep.Suggested) > 0 {
			fmt.Fprintf(&b, " Closest symbols: %s.", strings.Join(rep.Suggested, ", "))
		}
		b.WriteString("\n")
		return b.String()
	}

	code, tests := rep.DirectCount()
	loc := ""
	if rep.Location != "" {
		loc = " (" + rep.Location + ")"
	}
	if code+tests == 0 {
		name := rep.Symbol[strings.LastIndex(rep.Symbol, ".")+1:]
		if rep.Callees {
			fmt.Fprintf(&b, "%s %s%s: none found in the call graph (a leaf, or its calls go through "+
				"values the graph could not resolve).\n", dir, rep.Symbol, loc)
		} else {
			fmt.Fprintf(&b, "%s %s%s: none found in the static call graph. Either it is an entry point or "+
				"exported API, or its calls go through something the graph could not resolve (a variable "+
				"or generic receiver, interface dispatch, a callback, a goroutine or channel). "+
				"Confirm with grep for `%s(`.\n", dir, rep.Symbol, loc, name)
		}
		writeOthers(&b, rep)
		return b.String()
	}

	noun := "call site"
	if rep.Callees {
		noun = "call"
	}
	if code != 1 {
		noun += "s"
	}
	fmt.Fprintf(&b, "%s %s%s: %d %s", dir, rep.Symbol, loc, code, noun)
	if tests > 0 {
		fmt.Fprintf(&b, " + %d in tests", tests)
	}
	if rep.Depth > 1 {
		fmt.Fprintf(&b, " (depth 1), walked to depth %d", rep.Depth)
	}
	if rep.Complete() {
		b.WriteString(", complete\n")
	} else {
		fmt.Fprintf(&b, ", %d more not shown\n", rep.Omitted)
	}

	// Column width for the enclosing-function name.
	w := 0
	for _, s := range rep.Sites {
		if !s.Test && len(s.Name) > w {
			w = len(s.Name)
		}
	}
	if w > 40 {
		w = 40
	}

	var testSites []string
	for _, s := range rep.Sites {
		at := s.File
		if s.Line > 0 {
			at = fmt.Sprintf("%s:%d", s.File, s.Line)
		}
		if s.Test {
			if len(testSites) < maxTestSitesListed {
				testSites = append(testSites, at)
			}
			continue
		}
		indent := "  "
		if s.Depth > 1 {
			indent = strings.Repeat("  ", s.Depth) + "↳ "
		}
		fmt.Fprintf(&b, "%s%s  %-*s", indent, at, w, s.Name)
		if s.Via != "" {
			fmt.Fprintf(&b, "  (→ %s)", s.Via)
		}
		if s.Text != "" {
			fmt.Fprintf(&b, "  %s", s.Text)
		}
		b.WriteString("\n")
	}
	if len(testSites) > 0 {
		more := ""
		if allTests := countTests(rep); allTests > len(testSites) {
			more = fmt.Sprintf(" +%d more", allTests-len(testSites))
		}
		fmt.Fprintf(&b, "  (tests: %s%s)\n", strings.Join(testSites, ", "), more)
	}
	writeOthers(&b, rep)
	return b.String()
}

func countTests(rep *CallReport) int {
	n := 0
	for _, s := range rep.Sites {
		if s.Test {
			n++
		}
	}
	return n
}

func writeOthers(b *strings.Builder, rep *CallReport) {
	if len(rep.Others) == 0 {
		return
	}
	others := rep.Others
	extra := ""
	if len(others) > 5 {
		extra = fmt.Sprintf(" +%d more", len(others)-5)
		others = others[:5]
	}
	fmt.Fprintf(b, "  note: the name also matches %s%s; pass a qualified name (Type.method) to pick one.\n",
		strings.Join(others, ", "), extra)
}

// FormatDefinitions renders definitions: location, signature, first
// docstring paragraph and a short body (or a class's method list).
func FormatDefinitions(symbol string, defs []Definition, suggested []string) string {
	var b strings.Builder
	if len(defs) == 0 {
		fmt.Fprintf(&b, "definition of %s: not found.", symbol)
		if len(suggested) > 0 {
			fmt.Fprintf(&b, " Closest symbols: %s.", strings.Join(suggested, ", "))
		}
		b.WriteString("\n")
		return b.String()
	}
	for i, d := range defs {
		if i > 0 {
			b.WriteString("\n")
		}
		span := d.Location
		if d.End > d.Start && d.Start > 0 {
			span = fmt.Sprintf("%s:%d-%d", d.File, d.Start, d.End)
		}
		fmt.Fprintf(&b, "%s %s  %s\n", d.Kind, d.Name, span)
		if d.Docstring != "" {
			fmt.Fprintf(&b, "  doc: %s\n", strings.ReplaceAll(d.Docstring, "\n", " "))
		}
		switch {
		case len(d.Methods) > 0:
			if d.Signature != "" {
				fmt.Fprintf(&b, "  %s\n", d.Signature)
			}
			fmt.Fprintf(&b, "  methods (%d): %s", len(d.Methods)+d.MoreMethods, strings.Join(d.Methods, ", "))
			if d.MoreMethods > 0 {
				fmt.Fprintf(&b, ", +%d more (ask for %s.<method>)", d.MoreMethods, d.Name)
			}
			b.WriteString("\n")
		case d.Body != "":
			fmt.Fprintf(&b, "```\n%s\n```\n", strings.TrimRight(d.Body, "\n"))
			if d.Truncated() {
				fmt.Fprintf(&b, "  … body continues to line %d (read %s:%d-%d for the rest)\n",
					d.End, d.File, d.BodyEnd+1, d.End)
			}
		case d.Signature != "":
			fmt.Fprintf(&b, "  %s\n", d.Signature)
		}
	}
	return b.String()
}

// FormatTrace renders a call path as ordered hops.
func FormatTrace(t *TraceResult) string {
	var b strings.Builder
	switch {
	case len(t.Missing) > 0:
		fmt.Fprintf(&b, "trace: %s.\n", t.Reason)
		return b.String()
	case !t.Found:
		fmt.Fprintf(&b, "trace to %s (%s): %s.\n", t.To, t.Locations[t.To], t.Reason)
		return b.String()
	}
	start := t.From
	if t.EntryPoint {
		start += " [entry point]"
	}
	fmt.Fprintf(&b, "call path %s → %s, %d hops (static call graph):\n", start, t.To, len(t.Hops))
	for i, h := range t.Hops {
		at := h.File
		if h.Line > 0 {
			at = fmt.Sprintf("%s:%d", h.File, h.Line)
		}
		fmt.Fprintf(&b, "  %d. %s → %s  at %s", i+1, h.Caller, h.Callee, at)
		if h.Text != "" {
			fmt.Fprintf(&b, "  %s", h.Text)
		}
		b.WriteString("\n")
	}
	if loc := t.Locations[t.To]; loc != "" {
		fmt.Fprintf(&b, "  target %s defined at %s\n", t.To, loc)
	}
	if t.Reason != "" {
		fmt.Fprintf(&b, "  note: %s.\n", t.Reason)
	}
	return b.String()
}
