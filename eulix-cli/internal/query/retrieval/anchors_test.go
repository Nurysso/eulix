// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package retrieval

import (
	"testing"

	"eulix/internal/utils"
)

func TestLookupCallGraphNodeFindsNestedFunctions(t *testing.T) {
	cb := &ContextBuilder{cgBuild: &CallGraphIdx{Nodes: map[string]*utils.CallGraphNode{
		"func_outer.inner::a.rs":                  {NodeType: "function"},
		"method_PrinterPath_as_path.imp@42::u.rs": {NodeType: "function"},
		"func_Foo::bar::src/a.cpp":                {NodeType: "function"},
	}}}
	for _, entity := range []string{"inner", "imp", "outer.inner", "bar", "Foo::bar"} {
		if _, ok := cb.lookupCallGraphNode(entity); !ok {
			t.Errorf("lookupCallGraphNode(%q) found nothing", entity)
		}
	}
	if _, ok := cb.lookupCallGraphNode("src/a.cpp"); ok {
		t.Error("matched a file path as a symbol")
	}
}
