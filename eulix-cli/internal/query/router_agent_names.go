// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package query

import (
	"bytes"
	"sort"
	"strconv"
	"strings"

	"eulix/internal/checksum"
)

// ========== Name scan ==========
//
// The static call graph has no edge for a call whose receiver it could not
// type (a struct field, a variable, an interface value, a generic receiver),
// and none for a function passed as a value (an HTTP handler, a callback, a
// goroutine target). Told "no callers found", an agent greps for the name and
// then looks up the function around every hit: two turns, measured on every
// such reply in the agent benchmark. The name scan does both here, over the
// indexed source files, and labels what it finds as matched by name.

const (
	// maxNameScanFileBytes skips files larger than this (generated code).
	maxNameScanFileBytes = 2 << 20
	// maxNameUses caps the name uses listed in one report; the rest are counted.
	maxNameUses = 30
	// minSpecificName is the shortest name the scan trusts (see nameIsSpecific).
	minSpecificName = 6
	// maxNameScanUses stops a scan early; a name used this often is not
	// specific, and a report lists far fewer.
	maxNameScanUses = 1000
)

// NameUse is one whole-word use of a name outside its own definition.
type NameUse struct {
	File      string
	Line      int
	Text      string // the source line, trimmed and clipped
	Enclosing string // innermost function or method around the line; "" at top level
	Call      bool   // called here; otherwise used as a value (handler, callback, goroutine target)
	Test      bool
}

// nameIsSpecific reports whether a textual match of name almost certainly
// refers to the symbol and not to a field, a local or a library method of the
// same name: at least six characters with an inner capital or an underscore
// (runLLMPreHooks, retry_charge), and not a dunder (__init__). Common names
// (Close, run, get) are never scanned.
func nameIsSpecific(name string) bool {
	if len(name) < minSpecificName || strings.HasPrefix(name, "__") {
		return false
	}
	if strings.Contains(strings.Trim(name, "_"), "_") {
		return true
	}
	for i := 1; i < len(name); i++ {
		if name[i] >= 'A' && name[i] <= 'Z' {
			return true
		}
	}
	return false
}

// lastSegment returns the part after the last dot: the method name of
// "Bifrost.handleRequest".
func lastSegment(s string) string { return s[strings.LastIndex(s, ".")+1:] }

// definitionsNamed returns the function and method definitions spelled
// exactly name (the bare name, case-sensitive).
func (t *symTable) definitionsNamed(name string) []*symDef {
	var out []*symDef
	for _, d := range t.byName[strings.ToLower(name)] {
		if d.Name == name && d.Kind != "class" {
			out = append(out, d)
		}
	}
	return out
}

// scanName lists the uses of name in the knowledge base's source files,
// skipping its definitions, comments, imports and string literals: calls in
// non-test code first, then values, then tests. With method set, only uses
// behind a receiver count (x.name, x->name, T::name): a bare name is then a
// field, a local or a declaration. The scan stops after maxNameScanUses.
func (r *Router) scanName(lr *lineReader, name string, method bool) []NameUse {
	t, err := r.ensureSymTable()
	if err != nil || name == "" {
		return nil
	}
	defLines := map[string]bool{}
	for _, d := range t.definitionsNamed(name) {
		defLines[d.Location()] = true
	}

	r.mu.RLock()
	if r.kb == nil { // released while idle; the next lookup reloads it
		r.mu.RUnlock()
		return nil
	}
	files := make([]string, 0, len(r.kb.Structure))
	langs := make(map[string]string, len(r.kb.Structure))
	for p, fd := range r.kb.Structure {
		files = append(files, p)
		langs[p] = strings.ToLower(fd.Language)
	}
	r.mu.RUnlock()
	sort.Strings(files)

	needle := []byte(name)
	var uses []NameUse
	for _, file := range files {
		if len(uses) >= maxNameScanUses {
			break
		}
		ls := lr.linesContaining(file, needle)
		for i, line := range ls {
			if !strings.Contains(line, name) || defLines[file+":"+strconv.Itoa(i+1)] {
				continue
			}
			found, call := classifyUse(line, name, method, langs[file])
			if !found {
				continue
			}
			uses = append(uses, NameUse{
				File: file, Line: i + 1, Text: clipText(strings.TrimSpace(line), maxCallSiteText),
				Call: call, Test: isTestPath(file),
			})
		}
	}
	// Calls in non-test code first, then values, then tests; each by file and line.
	sort.SliceStable(uses, func(i, j int) bool {
		a, b := uses[i], uses[j]
		if a.Test != b.Test {
			return !a.Test
		}
		if a.Call != b.Call {
			return a.Call
		}
		return false
	})
	for i := range uses {
		uses[i].Enclosing = r.EnclosingSymbol(uses[i].File, uses[i].Line)
	}
	return uses
}

// classifyUse reports whether line uses name as a whole word, outside a
// comment, an import, a string, a declaration or an assignment target, and
// whether one such use is a call.
func classifyUse(line, name string, method bool, lang string) (found, call bool) {
	trimmed := strings.TrimSpace(line)
	if isCommentOrImport(trimmed) {
		return false, false
	}
	cut := commentStart(line, lang)
	for off := 0; off < len(line); {
		j := strings.Index(line[off:], name)
		if j < 0 {
			break
		}
		j += off
		off = j + 1
		end := j + len(name)
		if j >= cut {
			break
		}
		if (j > 0 && isIdentByte(line[j-1])) || (end < len(line) && isIdentByte(line[end])) {
			continue
		}
		if inString(line, j, lang) {
			continue
		}
		recv := (j > 0 && line[j-1] == '.') || (j > 1 && (line[j-2:j] == "->" || line[j-2:j] == "::"))
		if method && !recv {
			continue
		}
		if isDeclaration(line[:j]) {
			continue
		}
		rest := strings.TrimLeft(line[end:], " \t")
		switch {
		case strings.HasPrefix(rest, "("):
			return true, true
		case strings.HasPrefix(rest, ":") && !strings.HasPrefix(rest, "::"):
			// A struct-literal or dict key, a parameter annotation, a Go ':='.
			continue
		case strings.HasPrefix(rest, "=") && !strings.HasPrefix(rest, "=="):
			// An assignment target or a keyword argument's name.
			continue
		}
		found = true
	}
	return found, false
}

func isIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// isCommentOrImport reports whether a trimmed line is a whole-line comment or
// an import, which name a symbol without using it.
func isCommentOrImport(t string) bool {
	if t == "*" {
		return true
	}
	for _, p := range []string{"//", "#", "/*", "* ", "*/", "--", "import ", "from ", "use ", "using "} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// commentStart returns the index where a trailing comment starts, or
// len(line).
func commentStart(line, lang string) int {
	marks := []string{"//"}
	if usesHashComments(lang) {
		marks = append(marks, "#")
	}
	best := len(line)
	for _, m := range marks {
		for off := 0; off < best; {
			i := strings.Index(line[off:], m)
			if i < 0 {
				break
			}
			i += off
			if !inString(line, i, lang) {
				best = min(best, i)
				break
			}
			off = i + 1
		}
	}
	return best
}

func usesHashComments(lang string) bool {
	switch lang {
	case "python", "ruby", "shell", "bash", "perl", "r", "yaml", "toml":
		return true
	}
	return false
}

// inString reports whether index i of line sits inside a string literal on
// that line: an odd number of unescaped quotes before it. Single quotes only
// count in languages where they delimit strings (in Go they are runes, in Rust
// also lifetimes).
func inString(line string, i int, lang string) bool {
	quotes := []byte{'"', '`'}
	switch lang {
	case "python", "javascript", "typescript", "ruby", "php", "shell", "bash":
		quotes = append(quotes, '\'')
	}
	for _, q := range quotes {
		n := 0
		for k := 0; k < i; k++ {
			if line[k] == q && (k == 0 || line[k-1] != '\\') {
				n++
			}
		}
		if n%2 == 1 {
			return true
		}
	}
	return false
}

// isDeclaration reports whether the text before a name declares it: "func",
// "def", "fn", "function", or a Go method receiver "func (r *T)".
func isDeclaration(prefix string) bool {
	p := strings.TrimSpace(prefix)
	for _, kw := range []string{"func", "def", "fn", "function", "fun", "class", "struct", "type", "interface"} {
		if p == kw || strings.HasSuffix(p, " "+kw) || strings.HasSuffix(p, "\t"+kw) {
			return true
		}
	}
	return strings.HasPrefix(p, "func (") && strings.HasSuffix(p, ")")
}

// linesContaining returns the lines of file when it contains needle, nil
// otherwise. Files without the needle are not split or cached, so a scan of
// the whole repository costs one read per file.
func (lr *lineReader) linesContaining(file string, needle []byte) []string {
	if lr == nil {
		return nil
	}
	if ls, ok := lr.files[file]; ok {
		return ls
	}
	if !lr.servable(file) {
		lr.files[file] = nil
		return nil
	}
	data, err := checksum.ReadRegularNoSymlinks(lr.root, file)
	if err != nil || len(data) > maxNameScanFileBytes || !bytes.Contains(data, needle) {
		return nil
	}
	ls := strings.Split(string(data), "\n")
	lr.files[file] = ls
	return ls
}
