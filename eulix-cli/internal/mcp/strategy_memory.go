// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"math"
	"sort"
	"strings"
)

// ========== Project memory ==========
//
// projectMemory remembers which symbols appeared in trusted past results and
// which ones appeared together. It does NOT decide the strategy, that
// depends on what the calling agent knows. Its only job is to enrich
// multi-hop queries with remembered neighbours and to derive follow-up
// anchors from an open-ended result.
//
// The memory is small (MaxMemorySymbols) and is discarded when the active
// repository changes, alongside the ContextBuilder.

// maxRelated caps how many neighbours are kept per symbol, so a hub symbol
// does not connect to everything it ever appeared with.
const maxRelated = 8

// symbolInfo is one remembered symbol and its co-occurrence neighbours.
type symbolInfo struct {
	name    string
	count   int
	seq     int
	related map[string]struct{}
}

// projectMemory is the per-repository symbol memory. It is guarded by
// Server.mu; none of its methods are safe for concurrent use on their own.
type projectMemory struct {
	symbols map[string]*symbolInfo
	seq     int
	max     int
}

// newProjectMemory returns an empty memory capped at max symbols.
func newProjectMemory(max int) *projectMemory {
	return &projectMemory{symbols: make(map[string]*symbolInfo), max: max}
}

// size reports how many symbols are remembered. Used by tests.
//
// nolint:unused
func (m *projectMemory) size() int { return len(m.symbols) }

// Observe learns symbols and their co-occurrences from a result. It is only
// ever called on results that cleared the quality bar, so the memory does not
// accumulate noise.
//
// Blocks are split on blank lines; symbols within one block are treated as
// co-occurring.
func (m *projectMemory) Observe(text string) {
	for _, block := range strings.Split(text, "\n\n") {
		syms := symbolsIn(block, 30)
		if len(syms) == 0 {
			continue
		}
		m.seq++
		for _, s := range syms {
			info := m.touch(s)
			for _, o := range syms {
				if o == s || len(info.related) >= maxRelated {
					continue
				}
				info.related[o] = struct{}{}
			}
		}
	}
	m.evict()
}

// touch returns the symbolInfo for s, creating it on first sight, and
// refreshes its recency and count.
func (m *projectMemory) touch(s string) *symbolInfo {
	k := strings.ToLower(s)
	info, ok := m.symbols[k]
	if !ok {
		info = &symbolInfo{name: s, related: make(map[string]struct{})}
		m.symbols[k] = info
	}
	info.count++
	info.seq = m.seq
	return info
}

// evict trims the memory back to ~90% of its cap, dropping the least used and
// least recently seen symbols first.
func (m *projectMemory) evict() {
	if m.max <= 0 || len(m.symbols) <= m.max {
		return
	}
	type kv struct {
		k string
		i *symbolInfo
	}
	all := make([]kv, 0, len(m.symbols))
	for k, i := range m.symbols {
		all = append(all, kv{k, i})
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a].i.count != all[b].i.count {
			return all[a].i.count < all[b].i.count
		}
		return all[a].i.seq < all[b].i.seq
	})
	for _, e := range all[:len(m.symbols)-m.max*9/10] {
		delete(m.symbols, e.k)
	}
}

// expand returns remembered neighbours of the given anchors — the "hops"
// taken from memory — ranked by how often they co-occur with the anchors and
// how prominent they are themselves. Anchors' own keys are excluded so an
// anchor cannot expand to itself.
func (m *projectMemory) expand(anchors []Anchor, limit int) []string {
	exclude := map[string]bool{}
	for _, a := range anchors {
		for _, k := range a.Keys {
			exclude[k] = true
		}
	}
	score := map[string]int{}
	for _, a := range anchors {
		for _, k := range a.Keys {
			info := m.symbols[k]
			if info == nil {
				continue
			}
			for r := range info.related {
				rk := strings.ToLower(r)
				if exclude[rk] {
					continue
				}
				w := 1
				if ri := m.symbols[rk]; ri != nil {
					w += ri.count
				}
				score[r] += w
			}
		}
	}
	return topByScore(score, limit)
}

// derive picks symbols from an open-ended result whose name parts overlap the
// query. They become the anchors for the follow-up anchored retrieval.
//
// Scoring weights overlap with query terms first, then symbol frequency in
// the result, then the symbol's history in memory.
func (m *projectMemory) derive(q, result string, limit int) []string {
	if strings.TrimSpace(result) == "" {
		return nil
	}
	terms := map[string]bool{}
	for _, t := range queryTerms(q) {
		terms[t] = true
	}
	freq := map[string]int{}
	display := map[string]string{}
	scanSymbols(result, func(s string) {
		k := strings.ToLower(s)
		if len(s) < 3 {
			return
		}
		freq[k]++
		display[k] = s
	})

	scored := map[string]int{}
	for k, f := range freq {
		overlap := 0
		for _, w := range splitIdent(display[k]) {
			if terms[w] {
				overlap++
			}
		}
		if overlap == 0 {
			continue
		}
		bonus := 0.0
		if info := m.symbols[k]; info != nil {
			bonus = 0.5 * math.Log1p(float64(info.count))
		}
		// scaled to an int so topByScore can be shared with expand()
		scored[display[k]] = int(100 * (3*float64(overlap) + math.Log1p(float64(f)) + bonus))
	}
	return topByScore(scored, limit)
}

// topByScore returns the keys of score ranked by descending value, breaking
// ties lexicographically. At most limit keys are returned.
func topByScore(score map[string]int, limit int) []string {
	type kv struct {
		k string
		v int
	}
	all := make([]kv, 0, len(score))
	for k, v := range score {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a].v != all[b].v {
			return all[a].v > all[b].v
		}
		return all[a].k < all[b].k
	})
	if len(all) > limit {
		all = all[:limit]
	}
	out := make([]string, 0, len(all))
	for _, e := range all {
		out = append(out, e.k)
	}
	return out
}
