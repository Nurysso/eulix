package retrieval

import (
	"math"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Pseudo-relevance feedback.
//
// This is the paraphrase channel. Embeddings used to do this job: take a
// query, find code that means the same thing without saying it. Feedback does
// it symbolically — the top lexical hits vote on what else belongs to the
// answer, and the corpus promotes its own vocabulary into a second pass.
//
// It is strictly weaker than embedding similarity. It is also deterministic,
// free, and index-free, and for a codebase it recovers more than you'd think,
// because identifiers, doc comments and error strings are a large fraction of
// what a concept query actually names.
// ---------------------------------------------------------------------------

const (
	prfSeedHits    = 12  // top hits that vote
	prfMaxTerms    = 24  // terms promoted into the feedback query
	prfMaxDF       = 400 // never re-query a term this common
	prfGain        = 0.45
	prfMinTokenLen = 3
)

// prfTokenizer MUST match the tokenizer that built cb.invertedIdx. If your
// index stems, drops digits, or splits identifiers, set this accordingly —
// PRF quality is bounded by that consistency, not by the ranking math below.
// This is the one seam in the pipeline you will have to check.
var prfTokenizer = defaultPRFTokenize

func defaultPRFTokenize(s string) []string {
	if s == "" {
		return nil
	}
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	out := fields[:0]
	for _, f := range fields {
		if len(f) < prfMinTokenLen {
			continue
		}
		out = append(out, f)
	}
	return out
}

var prfStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "not": true, "but": true,
	"are": true, "was": true, "were": true, "you": true, "all": true,
	"can": true, "her": true, "has": true, "had": true, "our": true,
	"out": true, "get": true, "his": true, "how": true,
	"its": true, "let": true, "may": true, "new": true, "now": true,
	"old": true, "see": true, "two": true, "way": true, "who": true,
	"did": true, "any": true, "this": true, "that": true, "with": true,
	"from": true, "they": true, "have": true, "been": true, "into": true,
	"when": true, "what": true, "where": true, "which": true, "their": true,
	"use": true, "used": true, "using": true, "does": true,
	"impl": true, "implement": true, "implementation": true, "function": true,
	"func": true, "method": true, "class": true, "struct": true, "file": true,
	"code": true, "make": true, "made": true, "need": true,
	"want": true, "work": true, "works": true, "call": true, "called": true,
	"return": true, "error": true, "value": true, "values": true, "type": true,
	"types": true, "string": true, "strings": true, "int": true, "bool": true,
	"true": true, "false": true, "nil": true, "null": true, "self": true,
}

// prfExpand mines the top-ranked lexical hits for the terms that best
// characterise them and assembles a feedback query.
//
// Terms are scored Rocchio-style: concentrated in the seeds (rank-weighted
// term frequency) and rare in the corpus (inverse document frequency). That
// combination is why "handshake" can win and "error" never can — a term that
// is everywhere discriminates nothing.
func prfExpand(cb *ContextBuilder, hits []ScoredChunk, topN int) []string {
	idx := cb.invertedIdx
	if idx == nil || len(idx.Postings) == 0 || len(hits) == 0 {
		return nil
	}
	if topN > prfSeedHits {
		topN = prfSeedHits
	}

	// Rank-weighted term mass across the seed hits. Mass is scaled ×100 and
	// accumulated as an int so the hot loop stays allocation-light.
	mass := make(map[string]int, 512)
	scratch := make(map[string]bool, 64)
	for i := 0; i < len(hits) && i < topN; i++ {
		if hits[i].Score <= 0 {
			continue
		}
		w := int(100.0 / (1.0 + float64(i)))
		toks := prfTokenizer(hits[i].Content)
		toks = append(toks, prfTokenizer(hits[i].Name)...)
		toks = append(toks, prfTokenizer(hits[i].ClassName)...)
		for _, t := range toks {
			if prfStopwords[t] || len(t) < prfMinTokenLen || scratch[t] {
				continue
			}
			scratch[t] = true
			mass[t] += w
		}
		clear(scratch)
	}
	if len(mass) == 0 {
		return nil
	}

	type cand struct {
		term string
		w    float64
	}
	cands := make([]cand, 0, len(mass))
	docs := float64(idx.DocCount + 1)
	for term, m := range mass {
		postings, ok := idx.Postings[term]
		if !ok || len(postings) == 0 {
			continue // the tokenizer disagrees with the index — skip, don't guess
		}
		df := float64(len(postings))
		if df > prfMaxDF {
			continue // common connector term of the corpus
		}
		// Rare in the corpus, concentrated in the seeds.
		cands = append(cands, cand{term, float64(m) / 100.0 * math.Log(docs/df)})
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].w != cands[j].w {
			return cands[i].w > cands[j].w
		}
		return cands[i].term < cands[j].term
	})
	if len(cands) > prfMaxTerms {
		cands = cands[:prfMaxTerms]
	}
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.term
	}
	return out
}

// prfMerge folds a feedback pass into an existing result slice, keeping the
// better score per chunk rather than emitting duplicates. The run() helper
// already de-dupes across strategies by ID, but duplicates inside one
// strategy are wasted work and skew the avg score it reports.
func prfMerge(base, feedback []ScoredChunk) []ScoredChunk {
	if len(feedback) == 0 {
		return base
	}
	pos := make(map[string]int, len(base)+len(feedback))
	for i, r := range base {
		pos[r.ID] = i
	}
	for _, f := range feedback {
		f.Score *= prfGain
		f.MatchDetails += "; prf"
		if i, ok := pos[f.ID]; ok {
			if f.Score > base[i].Score {
				// Keep the original's structural flags — an exact hit that
				// also matched via feedback is still an exact hit.
				f.IsExact = base[i].IsExact
				base[i].Score = f.Score
				base[i].MatchDetails = f.MatchDetails
			}
			continue
		}
		pos[f.ID] = len(base)
		base = append(base, f)
	}
	return base
}
