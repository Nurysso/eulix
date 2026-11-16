package retrieval

import (
	"slices"
	"testing"
)

func TestIndexKeysMatchQueryWordsToCode(t *testing.T) {
	for _, c := range []struct{ query, code string }{
		{"loaded", "func Load()"},
		{"validated", "validateAndClampConfig"},
		{"seeds", "mcSeedIndices"},
		{"chunked", "chunk_one_file"},
		{"embeddings", "embed"},
		{"expand", "prfExpand"},
		{"parsing", "parse"},
	} {
		q, code := indexKeys([]string{c.query}), indexKeys([]string{c.code})
		if !slices.ContainsFunc(q, func(k string) bool { return slices.Contains(code, k) }) {
			t.Errorf("%q %v shares no key with %q %v", c.query, q, c.code, code)
		}
	}
}

func TestIndexKeysSplitsCamelCase(t *testing.T) {
	got := indexKeys([]string{"mcSeedIndices"})
	slices.Sort(got)
	if want := []string{"indic", "mc", "mcseedindic", "seed"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The index builder, BM25 and PRF share one key path: every term PRF mines
// from a chunk must exist in the index, or prfExpand silently skips it.
func TestPRFTokensExistInIndex(t *testing.T) {
	content := "Function: mcSeedIndices\nDocumentation: picks the seeds the walk starts from, loaded_once"
	cb := testEhnasBuilder(t, []Chunk{{ID: "walk_diag.go:mcSeedIndices", File: "walk_diag.go", Content: content}})
	for _, k := range prfTokenizer(content) {
		if len(cb.invertedIdx.Postings[k]) == 0 {
			t.Errorf("PRF term %q not in the inverted index", k)
		}
	}
	for _, k := range indexKeys([]string{"How are the seeds selected"}) {
		if k == "seed" && len(cb.invertedIdx.Postings[k]) == 0 {
			t.Errorf("BM25 query key %q not in the inverted index", k)
		}
	}
}

func TestStemTermIsIdempotent(t *testing.T) {
	for _, w := range []string{"embeddings", "succeeds", "classes", "processes", "parsed", "validates", "seed", "spill", "status"} {
		if s := stemTerm(w); stemTerm(s) != s {
			t.Errorf("stemTerm(%q) = %q, but stemTerm(%q) = %q", w, s, s, stemTerm(s))
		}
	}
}
