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
	if kbIndex.FunctionsByName != nil {
		for funcName := range kbIndex.FunctionsByName {
			c.validSymbols[funcName] = true
		}
	}
	if kbIndex.TypesByName != nil {
		for typeName := range kbIndex.TypesByName {
			c.validSymbols[typeName] = true
			c.validTypes[typeName] = true
		}
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

// isCommonWord contains common words in english+programming language that may occur in query,
// this is all based upon speculation no concrete proof exists
func isCommonWord(word string) bool {
	commonWords := map[string]bool{
		// English question words & auxiliaries
		"the": true, "this": true, "that": true, "these": true, "those": true,
		"what": true, "where": true, "when": true, "why": true, "how": true,
		"can": true, "will": true, "should": true, "would": true, "could": true,
		"does": true, "has": true, "have": true, "been": true, "are": true,
		"is": true, "was": true, "were": true, "being": true,
		"do": true, "did": true, "done": true, "doing": true,
		"might": true, "may": true, "must": true, "shall": true,
		"who": true, "whom": true, "whose": true, "which": true,

		// Common English words
		"and": true, "but": true, "or": true, "nor": true, "for": true, "so": true, "yet": true,
		"with": true, "without": true, "within": true, "about": true, "above": true,
		"after": true, "before": true, "between": true, "into": true, "through": true,
		"during": true, "until": true, "against": true, "among": true,
		"from": true, "more": true, "most": true, "other": true, "some": true,
		"such": true, "only": true, "own": true, "same": true, "than": true,
		"too": true, "very": true, "just": true, "now": true, "then": true,
		"here": true, "there": true, "each": true, "every": true, "both": true,
		"few": true, "many": true, "much": true, "another": true, "any": true,
		"all": true, "none": true, "not": true, "also": true,

		// Common programming terms
		"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
		"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
		"float32": true, "float64": true, "complex64": true, "complex128": true,
		"bool": true, "byte": true, "rune": true, "string": true, "error": true,
		"uintptr": true, "nil": true, "true": true, "false": true, "iota": true,

		// General programming language keywords
		"break": true, "case": true, "catch": true, "class": true, "const": true,
		"continue": true, "debugger": true, "default": true, "delete": true,
		"else": true, "enum": true, "export": true, "extends": true,
		"finally": true, "function": true, "goto": true,
		"implements": true, "import": true, "in": true, "instanceof": true,
		"interface": true, "let": true, "namespace": true, "new": true,
		"of": true, "package": true, "private": true, "protected": true,
		"public": true, "return": true, "static": true, "super": true,
		"switch": true, "synchronized": true, "throw": true, "throws": true,
		"transient": true, "try": true, "typeof": true, "var": true,
		"void": true, "volatile": true, "while": true, "yield": true,
		"async": true, "await": true, "as": true, "except": true,
		"raise": true, "pass": true, "lambda": true, "def": true,
		"global": true, "nonlocal": true, "elif": true, "assert": true,
		"trait": true, "match": true, "impl": true, "mod": true, "pub": true,
		"ref": true, "mut": true, "unsafe": true, "extern": true, "crate": true,
		"abstract": true, "sealed": true, "internal": true, "virtual": true,
		"override": true, "final": true, "strictfp": true, "native": true,

		"chan": true, "defer": true, "fallthrough": true, "go": true,
		"map": true, "range": true, "select": true, "struct": true,
		"type": true,

		"boolean": true, "integer": true,
		"float": true, "double": true, "array": true,
		"list": true, "set": true, "dict": true, "dictionary": true,
		"tuple": true, "optional": true, "union": true,
		"never": true, "undefined": true, "null": true,

		"object": true, "method": true, "implementation": true,
		"variable": true, "parameter": true, "argument": true, "value": true,
		"instance": true, "module": true, "library": true, "framework": true,
		"api": true, "data": true, "code": true, "file": true, "line": true,
		"main": true, "make": true, "append": true,
		"copy": true, "close": true, "len": true, "cap": true,
		"print": true, "println": true, "printf": true, "sprintf": true,
		"init": true, "result": true, "output": true, "input": true,
		"test": true, "tests": true, "testing": true, "example": true,
		"examples": true, "sample": true, "demo": true, "usage": true,
		"generic": true, "generics": true, "template": true,
		"pointer": true, "reference": true, "slice": true,
		"channel": true, "goroutine": true, "mutex": true, "lock": true,
		"waitgroup": true, "context": true, "buffer": true, "reader": true,
		"writer": true, "closer": true, "seeker": true, "scanner": true,
		"handler": true, "middleware": true, "router": true, "server": true,
		"client": true, "request": true, "response": true, "header": true,
		"body": true, "status": true, "query": true, "database": true,
		"config": true, "configuration": true, "setting": true, "settings": true,
		"option": true, "options": true, "flag": true, "flags": true,
		"env": true, "environment": true, "logger": true, "logging": true,
		"user": true, "users": true, "admin": true, "role": true, "roles": true,
		"token": true, "session": true, "auth": true, "model": true, "models": true,
		"view": true, "views": true, "controller": true, "controllers": true,
		"service": true, "services": true, "repository": true, "repositories": true,
		"entity": true, "entities": true, "dto": true, "validator": true,
		"exception": true, "exceptions": true, "runtime": true, "build": true,
		"version": true, "feature": true, "features": true, "fix": true, "fixes": true,
		"patch": true, "merge": true, "commit": true, "branch": true, "tag": true,
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
