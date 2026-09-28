package retrieval

import (
	"sort"
	"strings"
)

type AnchorPin struct {
	Chunk          Chunk
	Confidence     float64
	RetrievalScore float64
	Reason         string
	Entity         string
}

const pinnedAnchorTopN = 3

var trivialEntityWords = map[string]struct{}{
	"get": {}, "set": {}, "add": {}, "has": {}, "use": {},
	"new": {}, "old": {}, "run": {}, "init": {}, "call": {},
	"load": {}, "save": {}, "make": {}, "find": {},
	"list": {}, "dict": {}, "map": {}, "key": {}, "row": {},
	"col": {}, "self": {}, "base": {}, "item": {}, "code": {},
	"from": {}, "into": {}, "with": {}, "when": {}, "where": {},
	"that": {}, "this": {}, "which": {}, "what": {}, "name": {},
	"type": {}, "value": {}, "data": {}, "test": {}, "main": {},
	"expression": {}, "clause": {}, "object": {}, "attribute": {},
	"track": {}, "table": {}, "alias": {}, "block": {}, "query": {},
	// todo add common code noise:
	"err": {}, "error": {}, "ctx": {}, "req": {}, "res": {},
}

// isPinWorthyEntity checks if pin is worthy or not, there is no good
// determenistic way to distinguish wether a var/function is worthy or not
// This is just a honest and hopefull attempt.
func isPinWorthyEntity(s string) bool {
	// Lower limit of 2 to allow terms like "db", "ui", "os"
	// but still block 1-character like "i", "j", "x"
	if len(s) < 2 {
		return false
	}
	low := s
	if hasUpper(s) {
		low = strings.ToLower(s)
	}

	_, isTrivial := trivialEntityWords[low]
	return !isTrivial
}

// extractAnchorEntities
func extractAnchorEntities(query string) []string {
	set := make(map[string]bool, 16)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if isPinWorthyEntity(s) {
			set[s] = true
		}
	}
	for _, s := range extractPotentialSymbols(query) {
		add(s)
		if strings.Contains(s, ".") {
			for _, part := range strings.Split(s, ".") {
				add(part)
			}
		}
	}
	// TODO
	// I think we can skip this loop need to sit and find a way.
	for _, a := range extractExplicitAnchors(query) {
		if a.FuncName != "" {
			add(a.FuncName)
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	return out
}

// resolveAnchorPins is the pinning entry point. It:
//  1. extracts candidate entity names from the query,
//  2. confirms each against KB index and call graph,
//  3. finds chunks whose Name or Symbols match a confirmed entity,
//  4. ranks matches and returns the top pinnedAnchorTopN.
//
// Returns nil if the KB index and call graph are both absent, or no entity
// resolves.
func (cb *ContextBuilder) resolveAnchorPins(query string, pool map[string]ScoredChunk) []AnchorPin {
	if cb == nil {
		return nil
	}
	entities := extractAnchorEntities(query)
	if len(entities) == 0 {
		return nil
	}

	type entityInfo struct {
		inKB        bool
		inCallGraph bool
		nodeType    string
	}
	info := make(map[string]entityInfo, len(entities))
	for _, e := range entities {
		var ei entityInfo
		if cb.kbIdx != nil {
			if _, ok := cb.kbIdx.FunctionsByName[e]; ok {
				ei.inKB = true
			} else if _, ok := cb.kbIdx.TypesByName[e]; ok {
				ei.inKB = true
			}
		}
		if cb.cgBuild != nil {
			if nt, ok := cb.lookupCallGraphNode(e); ok {
				ei.inCallGraph = true
				ei.nodeType = nt
			}
		}
		if ei.inKB || ei.inCallGraph {
			info[e] = ei
		}
	}
	if len(info) == 0 {
		return nil
	}

	pins := make([]AnchorPin, 0, 8)
	seen := make(map[string]bool, 8)
	for i := range cb.chunks {
		c := &cb.chunks[i]
		bestConf := 0.0
		bestEntity := ""
		bestReason := ""
		for ent, ei := range info {
			conf := 0.0
			reason := ""
			switch {
			case strings.EqualFold(c.Name, ent):
				conf = 100.0
				reason = "name"
			case symbolEqualsFold(c.Symbols, ent):
				conf = 85.0
				reason = "symbol"
			default:
				continue
			}
			switch {
			case ei.inKB && ei.inCallGraph:
				conf += 30.0
				reason += "+kb+cg"
			case ei.inCallGraph:
				conf += 20.0
				reason += "+cg"
			case ei.inKB:
				conf += 10.0
				reason += "+kb"
			}
			if conf > bestConf {
				bestConf = conf
				bestEntity = ent
				bestReason = reason
			}
		}
		if bestConf > 0 && !seen[c.ID] {
			seen[c.ID] = true
			retrieval := 0.0
			if sc, ok := pool[c.ID]; ok {
				retrieval = sc.Score
			}
			pins = append(pins, AnchorPin{
				Chunk:          *c,
				Confidence:     bestConf,
				RetrievalScore: retrieval,
				Reason:         bestReason,
				Entity:         bestEntity,
			})
		}
	}

	entityBest := make(map[string]AnchorPin, len(pins))
	for _, p := range pins {
		if existing, ok := entityBest[p.Entity]; !ok || p.Confidence > existing.Confidence {
			entityBest[p.Entity] = p
		}
	}
	deduped := make([]AnchorPin, 0, len(entityBest))
	for _, p := range entityBest {
		deduped = append(deduped, p)
	}
	sort.Slice(deduped, func(i, j int) bool {
		if deduped[i].Confidence != deduped[j].Confidence {
			return deduped[i].Confidence > deduped[j].Confidence
		}
		return deduped[i].RetrievalScore > deduped[j].RetrievalScore
	})
	pins = deduped

	// Then the existing truncation to pinnedAnchorTopN
	if len(pins) > pinnedAnchorTopN {
		pins = pins[:pinnedAnchorTopN]
	}
	return pins
}

// symbolEqualsFold reports whether `target` appears in `syms`, case-insensitively,
// with either the exact spelling or with a class/method prefix stripped.
func symbolEqualsFold(syms []string, target string) bool {
	targetLow := strings.ToLower(target)
	for _, s := range syms {
		if strings.EqualFold(s, target) {
			return true
		}
		for i := 0; i < len(s); i++ {
			if s[i] == '_' || s[i] == '.' {
				if strings.ToLower(s[i+1:]) == targetLow {
					return true
				}
			}
		}
	}
	return false
}

// lookupCallGraphNode checks the call-graph node table for an entity, trying
// every '_'-seprated suffix for each node key. this is what makes
// `method_SQLCompiler_get_from_clause` match entity `get_from_clause`
//
// IMPORTANT: eithout this, no pin ever gets the +cg confidence bonus and
// every candidate ties at 110(name+kb), which reduces pin selection to
// an arbitary sort of ties
func (cb *ContextBuilder) lookupCallGraphNode(entity string) (string, bool) {
	if cb.cgBuild == nil {
		return "", false
	}
	entityLow := strings.ToLower(entity)
	for key, n := range cb.cgBuild.Nodes {
		if i := strings.Index(key, "::"); i != -1 {
			key = key[:i]
		}
		if strings.ToLower(key) == entityLow {
			return n.NodeType, true
		}
		for i := 0; i < len(key); i++ {
			if key[i] == '_' && strings.ToLower(key[i+1:]) == entityLow {
				return n.NodeType, true
			}
		}
	}
	return "", false
}

func (cb *ContextBuilder) logAnchorPins(pins []AnchorPin) {
	if cb.debugLog == nil || len(pins) == 0 {
		return
	}
	cb.debugLog.Log("Anchor pins resolved: %d", len(pins))
	for i, p := range pins {
		cb.debugLog.Log("  pin[%d] entity=%q reason=%s conf=%.1f retrieval=%.1f id=%s",
			i, p.Entity, p.Reason, p.Confidence, p.RetrievalScore, p.Chunk.ID)
	}
}
