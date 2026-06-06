//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides query classification functionality.
/*
This file is responsible for loading json files for routing.
*/
package query

import (
	"encoding/json"
	"os"
	"path/filepath"

	"eulix/internal/types"
)

func loadKBIndex(eulixDir string) (*KBIndex, error) {
	indexPath := filepath.Join(eulixDir, "kb_index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, err
	}
	var index KBIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	return &index, nil
}
func loadKBFull(eulixDir string) (*types.KnowledgeBase, error) {
	path := filepath.Join(eulixDir, "kb.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var kb types.KnowledgeBase
	if err := json.Unmarshal(data, &kb); err != nil {
		return nil, err
	}
	return &kb, nil
}

func loadCallGraph(eulixDir string) (*CallGraph, error) {
	graphPath := filepath.Join(eulixDir, "kb_call_graph.json")
	data, err := os.ReadFile(graphPath)
	if err != nil {
		return nil, err
	}
	var graph CallGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, err
	}
	return &graph, nil
}
