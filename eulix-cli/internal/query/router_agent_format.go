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

// edgeLabels mark the edges the parser inferred rather than resolved, so an
// agent can tell a guess from a fact.
var edgeLabels = map[string]string{
	"async":    asyncLabel,
	"dispatch": "[interface dispatch: an implementation of the method called, inferred]",
	"inferred": "[receiver type unknown; the only method of that name, inferred]",
}

// asyncLabel marks an edge inferred across a Go channel: the caller sends on
// a field channel and the callee is a goroutine receiving from that field.
const asyncLabel = "[async: channel send → receiver, inferred]"

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
	if code+tests == 0 && rep.Scanned {
		if len(rep.ByName)+len(rep.AsValue) == 0 {
			fmt.Fprintf(&b, "%s %s%s: none. Its name is not called or used anywhere else in the indexed code: "+
				"it is an entry point, or called from outside the repository (a framework, the runtime, reflection).\n",
				dir, rep.Symbol, loc)
		} else {
			found := "found " + nameCounts(rep)
			if found == "found " {
				found = "only tests use the name"
			}
			fmt.Fprintf(&b, "%s %s%s: none in the static call graph; %s:\n", dir, rep.Symbol, loc, found)
			writeNameUses(&b, rep, 0)
		}
		writeOthers(&b, rep)
		return b.String()
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
	if c := nameCounts(rep); c != "" {
		b.WriteString(" + " + c)
	}
	if rep.Depth > 1 {
		fmt.Fprintf(&b, " (depth 1), walked to depth %d", rep.Depth)
	}
	switch {
	case rep.Complete():
		b.WriteString(", complete\n")
	case rep.Omitted+rep.NameOmitted > 0:
		fmt.Fprintf(&b, ", %d more not shown\n", rep.Omitted+rep.NameOmitted)
	default:
		b.WriteString("\n")
	}

	// Column width for the enclosing-function name.
	w := 0
	for _, s := range rep.Sites {
		if !s.Test && len(s.Name) > w {
			w = len(s.Name)
		}
	}
	for _, u := range append(append([]NameUse(nil), rep.ByName...), rep.AsValue...) {
		if !u.Test && len(u.Enclosing) > w {
			w = len(u.Enclosing)
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
		if l := edgeLabels[s.Edge]; l != "" {
			b.WriteString("  " + l)
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
	writeNameUses(&b, rep, w)
	writeOthers(&b, rep)
	return b.String()
}

// nameCounts summarizes a report's name uses outside tests, "3 calls by
// name + 1 use as a value", or "" when there are none.
func nameCounts(rep *CallReport) string {
	var parts []string
	if n := countNonTest(rep.ByName); n > 0 {
		parts = append(parts, plural(n, "call", "calls")+" by name")
	}
	if n := countNonTest(rep.AsValue); n > 0 {
		parts = append(parts, plural(n, "use", "uses")+" as a value")
	}
	return strings.Join(parts, " + ")
}

func countNonTest(uses []NameUse) int {
	n := 0
	for _, u := range uses {
		if !u.Test {
			n++
		}
	}
	return n
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// writeNameUses lists the name scan's finds under the static sites: calls the
// graph could not resolve, then uses as a value, tests counted apart.
func writeNameUses(b *strings.Builder, rep *CallReport, w int) {
	if len(rep.ByName)+len(rep.AsValue) == 0 {
		if rep.NameOmitted > 0 {
			fmt.Fprintf(b, "  +%d more uses of the name not shown\n", rep.NameOmitted)
		}
		return
	}
	if w == 0 {
		for _, u := range append(append([]NameUse(nil), rep.ByName...), rep.AsValue...) {
			w = max(w, min(len(u.Enclosing), 40))
		}
	}
	var tests []string
	section := func(title string, uses []NameUse) {
		first := true
		for _, u := range uses {
			at := fmt.Sprintf("%s:%d", u.File, u.Line)
			if u.Test {
				tests = append(tests, at)
				continue
			}
			if first {
				b.WriteString(title)
				first = false
			}
			enc := u.Enclosing
			if enc == "" {
				enc = "(top level)"
			}
			fmt.Fprintf(b, "  %s  %-*s  %s\n", at, w, enc, u.Text)
		}
	}
	section("  by name (the call graph could not resolve the receiver):\n", rep.ByName)
	section("  used as a value (handler, callback or goroutine target):\n", rep.AsValue)
	if len(tests) > 0 {
		more := ""
		if len(tests) > maxTestSitesListed {
			more = fmt.Sprintf(" +%d more", len(tests)-maxTestSitesListed)
			tests = tests[:maxTestSitesListed]
		}
		fmt.Fprintf(b, "  (tests, by name: %s%s)\n", strings.Join(tests, ", "), more)
	}
	if rep.NameOmitted > 0 {
		fmt.Fprintf(b, "  +%d more uses of the name not shown\n", rep.NameOmitted)
	}
	if len(rep.Twins) > 0 {
		fmt.Fprintf(b, "  note: %s is also defined as %s; a use by name may be one of those.\n",
			lastSegment(rep.Symbol), strings.Join(rep.Twins, ", "))
	}
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
		if len(t.Upstream) > 0 {
			fmt.Fprintf(&b, "%s is reached by this chain instead, %d hops (static call graph):\n", t.To, len(t.Upstream))
			writeHops(&b, t.Upstream)
		}
		writeStartUses(&b, t)
		return b.String()
	}
	start := t.From
	if t.EntryPoint {
		start += " [entry point]"
	}
	fmt.Fprintf(&b, "call path %s → %s, %d hops (static call graph):\n", start, t.To, len(t.Hops))
	writeHops(&b, t.Hops)
	if loc := t.Locations[t.To]; loc != "" {
		fmt.Fprintf(&b, "  target %s defined at %s\n", t.To, loc)
	}
	if t.Reason != "" {
		fmt.Fprintf(&b, "  note: %s.\n", t.Reason)
	}
	writeStartUses(&b, t)
	return b.String()
}

func writeHops(b *strings.Builder, hops []Hop) {
	for i, h := range hops {
		at := h.File
		if h.Line > 0 {
			at = fmt.Sprintf("%s:%d", h.File, h.Line)
		}
		fmt.Fprintf(b, "  %d. %s → %s  at %s", i+1, h.Caller, h.Callee, at)
		if h.Text != "" {
			fmt.Fprintf(b, "  %s", h.Text)
		}
		if l := edgeLabels[h.Edge]; l != "" {
			b.WriteString("  " + l)
		}
		b.WriteString("\n")
	}
}

// writeStartUses says where the chain's first function is used by name when
// nothing calls it statically: that is where the chain starts.
func writeStartUses(b *strings.Builder, t *TraceResult) {
	if t.Start == "" || !t.StartScanned {
		return
	}
	if len(t.StartUses) == 0 {
		fmt.Fprintf(b, "  %s is not called or used by name anywhere else in the indexed code outside tests: "+
			"it is called from outside the repository (a framework, the runtime, reflection).\n", t.Start)
		return
	}
	fmt.Fprintf(b, "  %s is used by name here (where the chain starts):\n", t.Start)
	for _, u := range t.StartUses {
		enc := u.Enclosing
		if enc == "" {
			enc = "(top level)"
		}
		how := "calls it"
		if !u.Call {
			how = "passes it as a value"
		}
		fmt.Fprintf(b, "    %s:%d  %s %s  %s\n", u.File, u.Line, enc, how, u.Text)
	}
	if len(t.StartTwins) > 0 {
		fmt.Fprintf(b, "    note: %s is also defined as %s; a use by name may be one of those.\n",
			lastSegment(t.Start), strings.Join(t.StartTwins, ", "))
	}
}
