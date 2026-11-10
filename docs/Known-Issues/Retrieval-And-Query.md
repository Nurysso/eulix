# Retrieval And Query Issues

Known problems in how Eulix chooses the context it shows the model.

> **Scope:** `eulix-cli` (`internal/query`, `internal/query/retrieval`)  
> **Last verified against:** `ad744e9` (2026-11-03)

Read [Overview](Overview.md) for the legend. Most entries here are *Read in code* or *Carried over*, because checking retrieval quality means running questions against a real knowledge base. The *Carried over* EHNAS items come from the maintainers' benchmark of 20 questions about this monorepo (`scripts/eval_mcp.py`); I did not rerun it.

---

## RET-1: The `kb_exact` search strategy never returns anything

**Severity:** High · **Status:** Open · **Verification:** Read in code

**Symptom.** Retrieval is documented as running a "knowledge-base exact lookup" first, with the highest boost (+2.5). In practice that strategy contributes nothing to any question, with either engine.

**Cause.** `kbExactLookup` starts with `if !cb.hasKB || cb.kbData == nil { return nil }`. The streaming loader deliberately never keeps the decoded knowledge base, and `cb.kbData` is only ever assigned `nil` (`loader.go`, `utils.go`). So the check always returns early. The strategy is dead code or a broken path.

**Impact.** The other exact-style strategies (`grep`, `exact`, `partial`) still run, and the anchor machinery does not depend on this function, so retrieval still works. But the strategy that was meant to carry the highest weight is absent, and its share of the budget weights (`kb_exact` in the intent table) buys nothing.

**Workaround.** None needed to keep working; expect exact-name questions to rely on `grep` and `exact`.

**Fix direction.** Rewrite the lookup to use the chunk list and symbol index (as `exact` does), or remove the strategy and its weight.

---

## RET-2: Cross-root demotion only works for two folder names

**Severity:** Medium · **Status:** Open · **Verification:** Read in code

**Symptom.** `apply_cross_root_isolation` and `cross_root_penalty` are documented as demoting code outside the main module. They have no effect on most repositories.

**Cause.** In the scope-demotion step, a file is penalized only if its top-level folder starts with `charm-` or equals `requirements`. These names look tuned to one test repository.

**Workaround.** None in configuration.

**Fix direction.** Penalize any file outside the detected primary subsystem's top-level folder, subject to the existing exemptions, or take the list of folders from `eulix.toml`.

---

## RET-3: Deep multi-hop questions may be answered inaccurately

**Severity:** Medium · **Status:** Open · **Verification:** Carried over

**Symptom.** For a question that needs a chain of related functions across several files (for example, "which `Set` attribute on the inner `Query` does `resolve_expression()` mutate, and how does `get_from_clause()` use it?"), retrieval returns good-enough chunks, but the final answer may still be inaccurate.

**Cause.** The limit is mostly in source hydration ([Selection And Hydration](../eulix-cli/query/retrieval/Selection-And-Hydration.md)). A deep chain can exceed the source-token budget, or leave important intermediate code with too little context.

**Workaround.** Raise `llm.max_tokens` in `eulix.toml`. It relaxes the budget but does not guarantee a better answer. With the EHNAS engine, a depth-2 graph expansion or the Monte Carlo walk may help surface the chain.

**Status note.** The maintainers list this as under active work.

---

## RET-4: EHNAS: word-feedback drift hurts some conceptual questions

**Severity:** Medium · **Status:** Open · **Verification:** Carried over

**Symptom.** For "how are the seeds for the Monte Carlo walk selected", the right function (`mcSeedIndices`) ranks 10th, even after the long-function fix.

**Cause.** The keyword strategy's pseudo-relevance feedback adds terms mined from the top hits. Some are generic type names (`scoredchunk`, `contextbuilder`) and line numbers (`503`, `759`) that come from the `Lines: 25-547` text written into every chunk. They match most of the package about equally, producing a flat cluster in which the right answer is not first. The function's doc comment also never says "Monte Carlo".

**Fix direction.** Drop numeric tokens from feedback terms, and cap feedback terms that are type names in the seeds' signatures.

**Note.** This was measured before [#158](https://github.com/Nurysso/eulix/pull/158) (EHNAS call layer, determinism and twin fixes). Re-run the benchmark before relying on it.

---

## RET-5: EHNAS: several benchmark questions still fall back to text search

**Severity:** Medium · **Status:** Open · **Verification:** Carried over

From the maintainers' benchmark after the shipped fixes:

- **"LSP hover" regressed from 1st to 7th**, answered by the text-search fallback. After word-splitting improved matching, common words like "server" and "requests" match the MCP server's code heavily, raising its score; a relative score floor then keeps only a few candidates, none of them the answer. Likely fix: weight matches in a chunk's name above matches in its body.
- **"Source hydration"** has the right file first but is answered by the fallback because EHNAS's result scored below the MCP quality bar.
- **"Chunking for embedding"** is never found.
- **Two more questions** (the engine switch and `find_entry_points`) still need the fallback.

With 20 questions, a single one moves the "ranked first" figure by 5 points. Treat small differences as noise. The shipped fixes are in [Fixed Issues](Fixed-Issues.md).

**Caveat when benchmarking.** The text-search fallback skips hidden directories but searches the rest of the repository. Result files written to the repository root get found by later queries, which adds noise. Write results under `.eulix/` instead.

**Note.** This was measured before [#158](https://github.com/Nurysso/eulix/pull/158) (EHNAS call layer, determinism and twin fixes). Re-run the benchmark before relying on it.

---

## RET-6: The Monte Carlo lexical layer depends on map order

**Severity:** Low · **Status:** Open · **Verification:** Carried over

`buildLexLayer` sums 32-bit edge weights while iterating a map of postings. Different iteration orders round differently, which can change which neighbour a walker picks. The layer exists only when the inverted index is built (`inverted_index_min_chunks`). Fix: iterate the postings in sorted key order.

Two related details in `walk_graph.go`: the walk's random generator is seeded only from the number of seeds and chunks (`rawSeed`), and the XOR with a hash of the query is commented out, so two different queries with the same counts share one random sequence. This keeps runs repeatable, but it is not documented as intended. Also, `mcDiagEnabled` is `true`, so the diagnostic counters stay compiled into the hot path. The stemmer used for matching also has suffix-only rules, so irregular forms (`index`/`indices`, `entry`/`entries`) and `-er` words (`parser`/`parse`) do not meet.

---

## RET-7: Embedder is never restarted after a timeout

**Severity:** Medium · **Status:** Open · **Verification:** Carried over

After one query embedding times out (30 seconds), the embedder is marked dead on purpose and every later request fails at once. Retrieval treats that as a warning, so the session continues, but semantic search (ETIYA) stays off until you restart `eulix`. Restarting it would need a recreate path in the context builder. **Workaround:** restart the chat or the MCP server; or use `engine = "ehnas"`, which has no embedder.

---

## RET-8: Names from a stale knowledge base can still appear

**Severity:** Low · **Status:** Open · **Verification:** Carried over

Location and call-graph answers come from `kb_index.json` and `kb_call_graph.json`, which are not filtered by the current ignore rules. After a file is excluded or becomes hidden, its *name and path* can still appear in those answers (never its contents). Re-run `eulix analyze`.

---

## RET-9: Goroutine leak per repository switch; debug logging stops after the first idle release

**Severity:** Low · **Status:** Open · **Verification:** Carried over

Every router starts a debug-log flush goroutine that never stops, and closing the router closes the log only if a context builder exists. A long-lived MCP server that keeps switching repositories accumulates them. Separately, closing a context builder closes the router's shared debug logger, so `context_debug.log` stays silent for the rest of an MCP session after the first idle release.

---

## Related Docs

- [Retrieval Overview](../eulix-cli/query/retrieval/Overview.md): the pipeline.
- [Search Pipeline](../eulix-cli/query/retrieval/Search-Pipeline.md): where RET-1 and RET-2 occur.
- [Fixed Issues](Fixed-Issues.md): the EHNAS fixes already shipped.
