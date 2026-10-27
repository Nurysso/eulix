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
	}}}
	for _, entity := range []string{"inner", "imp", "outer.inner"} {
		if _, ok := cb.lookupCallGraphNode(entity); !ok {
			t.Errorf("lookupCallGraphNode(%q) found nothing", entity)
		}
	}
}
