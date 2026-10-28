// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package query

import (
	"path/filepath"
	"strings"
	"testing"

	"eulix/internal/config"
	"eulix/internal/utils"
)

func diskRouter(t *testing.T) *Router {
	t.Helper()
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "kb_index.json"), &utils.IndexDataRef{Indices: &utils.Indices{}})
	r, err := QueryTrafficController(dir, config.DefaultConfig(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestProductionRouterLoadsKBOnDemand(t *testing.T) {
	for _, query := range []string{"what is in file a.go", "todo list"} {
		t.Run(query, func(t *testing.T) {
			r := diskRouter(t)
			if r.kb != nil {
				t.Fatal("KB loaded eagerly")
			}
			kb := &utils.KnowledgeBaseSimplifiedRef{}
			fill(t, kb, M{"Metadata": M{"ProjectName": "disk"}, "Structure": M{"pkg/a.go": fileSpec()}})
			writeJSON(t, filepath.Join(r.eulixDir, "kb.json"), kb)
			for _, run := range []func(string) (string, error){r.QueryEngine, r.PromptOrAnswer} {
				got, err := run(query)
				if err != nil || !strings.Contains(got, "pkg/a.go") || strings.Contains(got, "not loaded") {
					t.Errorf("query=%q got=(%q,%v)", query, got, err)
				}
			}
			if r.kb == nil {
				t.Error("KB was not cached")
			}
		})
	}
}

func TestProductionRouterLoadsOwnDependencies(t *testing.T) {
	setDeps(t, []utils.ExternalDependency{{Name: "stale-global"}})
	for _, name := range []string{"first-repo", "second-repo"} {
		r := diskRouter(t)
		writeJSON(t, filepath.Join(r.eulixDir, "kb_external_deps.json"), &utils.ExternalDependencyRef{ExternalDependencies: []utils.ExternalDependency{{Name: name}}})
		got, err := r.QueryEngine("list dependencies")
		if err != nil || !strings.Contains(got, name) || strings.Contains(got, "stale-global") {
			t.Errorf("got=(%q,%v), want %s", got, err, name)
		}
	}
}
