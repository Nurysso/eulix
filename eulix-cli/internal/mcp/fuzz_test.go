// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"strings"
	"testing"
)

// Agents send arbitrary text, and the SDK does not recover handler panics:
// one panic in these parsers would take the whole MCP server down.

var fuzzQueries = []string{
	"How does SQLCompiler.get_from_clause() work?", "`a/b.go` x::y a->b c#d e.g. i.e.",
	"payment_total", "", "````", "((((", "\x00\xff", "日本語のクエリ snake_case_名前", strings.Repeat("a_b ", 500),
}

func FuzzExtractAnchors(f *testing.F) {
	for _, q := range fuzzQueries {
		f.Add(q)
	}
	f.Fuzz(func(t *testing.T, q string) {
		anchors := ExtractAnchors(q)
		if len(anchors) > maxAnchors {
			t.Fatalf("%d anchors", len(anchors))
		}
		for _, a := range anchors {
			if a.Text == "" {
				t.Fatal("empty anchor")
			}
			for _, k := range a.Keys {
				if k != strings.ToLower(k) {
					t.Fatalf("key %q is not lower-case", k)
				}
			}
		}
		_ = queryTerms(q)
		_ = anchorsFromHints(strings.Fields(q))
		_ = symbolsIn(q, 30)
	})
}

func FuzzAssess(f *testing.F) {
	for _, q := range fuzzQueries {
		f.Add(q, routerStyle(q, goodBody), "")
	}
	f.Fuzz(func(t *testing.T, q, result, echo string) {
		got := assess(q, ExtractAnchors(q), result, echo, q)
		if got.Score < 0 || got.Score > 1 {
			t.Fatalf("score %v out of range", got.Score)
		}
		m := newProjectMemory(50)
		m.Observe(result)
		_ = m.expand(ExtractAnchors(q), 8)
		_ = m.derive(q, result, 8)
	})
}

func FuzzURIPath(f *testing.F) {
	for _, u := range []string{"file:///C:/x", "file://", "file:///%zz", "file://host/share", "/plain", ""} {
		f.Add(u)
	}
	f.Fuzz(func(t *testing.T, uri string) {
		_ = uriPath(uri, true)
		_ = uriPath(uri, false)
		_ = splitList(uri)
	})
}

func FuzzLexExcluded(f *testing.F) {
	f.Add("docs/", "docs/a.md")
	f.Add("[", "x")
	f.Add("**/*.go", "a/b.go")
	f.Fuzz(func(t *testing.T, pattern, rel string) {
		_ = lexExcluded(rel, []string{pattern})
	})
}
