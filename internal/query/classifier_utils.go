//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package embeddings provides the command-line interface implementation for EULIX.

/*
Package query provides query classification functionality.
This file is provids utils/helpers for query classification
*/

package query

import (
	"encoding/json"
	"os"
	"strings"
	"unicode"
)

type Entity struct {
	Name string
	Type string
}

type SymbolIndex struct {
	Symbols []string `json:"symbols"`
}

func (c *Classifier) extractSymbols(query string) []string {
	matches := c.symbolPattern.FindAllString(query, -1)
	symbolMap := make(map[string]bool)
	symbols := []string{}

	for _, match := range matches {
		if !isCommonWord(strings.ToLower(match)) && !symbolMap[match] {
			symbolMap[match] = true
			symbols = append(symbols, match)
		}
	}

	return symbols
}

func (c *Classifier) validateSymbols(symbols []string) []string {
	if len(c.validSymbols) == 0 {
		return symbols
	}

	validated := []string{}
	for _, symbol := range symbols {
		if c.validSymbols[symbol] {
			validated = append(validated, symbol)
		}
	}

	return validated
}

func (c *Classifier) extractEntities(symbols []string) []Entity {
	entities := []Entity{}

	for _, symbol := range symbols {
		entityType := "unknown"

		if c.validTypes[symbol] {
			entityType = "type"
		} else if c.validSymbols[symbol] {
			entityType = "function"
		}

		entities = append(entities, Entity{
			Name: symbol,
			Type: entityType,
		})
	}

	return entities
}

func extractKeywords(queryLower string) []string {
	stopWords := map[string]bool{
		"how": true, "does": true, "the": true, "a": true, "an": true,
		"is": true, "are": true, "what": true, "where": true, "when": true,
		"can": true, "will": true, "should": true, "would": true, "could": true,
		"this": true, "that": true, "these": true, "those": true, "of": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "with": true,
	}

	words := strings.FieldsFunc(queryLower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_'
	})

	keywords := []string{}
	for _, word := range words {
		if !stopWords[word] && len(word) > 2 {
			keywords = append(keywords, word)
		}
	}

	return keywords
}

func (c *Classifier) loadSymbols(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var kbIndex struct {
		FunctionsByName map[string][]string `json:"functions_by_name"`
		TypesByName     map[string][]string `json:"types_by_name"`
	}

	if err := json.Unmarshal(data, &kbIndex); err != nil {
		return err
	}

	for funcName := range kbIndex.FunctionsByName {
		c.validSymbols[funcName] = true
	}

	for typeName := range kbIndex.TypesByName {
		c.validSymbols[typeName] = true
		c.validTypes[typeName] = true
	}

	return nil
}

func isCommonWord(word string) bool {
	commonWords := map[string]bool{
		"the": true, "this": true, "that": true, "these": true, "those": true,
		"what": true, "where": true, "when": true, "why": true, "how": true,
		"can": true, "will": true, "should": true, "would": true, "could": true,
		"does": true, "has": true, "have": true, "been": true, "are": true,
	}
	return commonWords[word]
}

func containsAny(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}
