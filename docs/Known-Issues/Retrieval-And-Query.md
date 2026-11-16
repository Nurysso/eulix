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

## RET-10: Rewriting `kb.json` while Eulix reads it can crash the reader

**Severity:** Low · **Status:** Won't fix · **Verification:** Reproduced on Linux (2026-11-13, `60aec81`); the Windows part is read in code

**Symptom.** If `eulix analyze` rewrites the knowledge base while another Eulix process (the MCP server or `eulix chat`) is still loading it, the loading process can die with `fatal error: fault` and `signal SIGBUS` on Linux and macOS. On Windows nothing crashes; the parser's write fails instead, with a sharing violation or `ERROR_USER_MAPPED_FILE` (1224).

**Cause.** `DecodeJSONFile` memory-maps files of 4 MiB or more while it decodes them whole ([Mmap](../eulix-cli/query/retrieval/Mmap.md)): `kb_index.json`, `kb_call_graph.json`, and `kb.json` when a question needs the full knowledge base (`ensureKB` in `router_helpers.go`). The streaming chunk load of `kb.json` reads through a buffer and is not affected. The parser writes `kb.json`, `kb_index.json` and `kb_call_graph.json` in place with `File::create`, which empties the file first (`eulix-parser/src/output.rs`). On Linux and macOS, reading a mapped page that now lies past the end of the file raises `SIGBUS`, and Go cannot recover from it by default. The size re-check in `DecodeViaMmap` covers only the moment before the file is mapped, not the parse. On Windows the reader opens the file without write sharing, and a mapped view blocks truncation, so the writer is refused.

To reproduce it, map a 16 MB file, rewrite it in place, then read on through the mapping: the process crashes with `SIGBUS`.

**Why it is not fixed.** The parser and the CLI are not meant to run at the same time on the same repository. Both are fast, so if it does happen, running the failed command again recovers: `eulix analyze` rebuilds a half-written knowledge base, and the next query loads the new one. The possible fixes (the parser writing to a temporary file and renaming it, or catching the fault with `debug.SetPanicOnFault`) add code to a path that normal use never reaches.

**Workaround.** Don't run `eulix analyze` on a repository while a query on that same repository is loading. If either one fails, run it again.

---

## RET-11: Quitting chat during a vector search can crash on Linux

**Severity:** Low · **Status:** Won't fix · **Verification:** Read in code

**Symptom.** On Linux, pressing Ctrl+C in `eulix chat` while a question is in its vector search can end with a segmentation fault and a Go stack trace instead of a clean exit.

**Cause.** On Linux the embedding matrix lives in its own memory mapping (`AllocEmbeddingMatrixOwned` in `mmap_linux.go`), and closing the context builder unmaps it. The router hands a query its context builder and then releases its lock (`router_core.go`), so nothing stops `Close` from running while that query is still reading vectors. Chat runs each question in the background (`tui/app.go`), and on quit `chat.go` closes the router without waiting for the question. On macOS and Windows the matrix is ordinary Go memory, so the same timing frees it a little later and nothing crashes.

**Why it is not fixed.** The MCP server is the main way Eulix is used, and it cannot hit this: each query holds the server lock, and both the idle release and the repository switch wait for that lock. In chat the window is only the vector search, not the model call, and the process is exiting anyway. The fix, a read lock or reference count held for all of `BuildContext`, would touch every query path to prevent a crash that only happens on the way out.

**Workaround.** Let the current question finish before quitting chat.

---

## RET-12: A knowledge-base file that fails to decode is parsed twice

**Severity:** Low · **Status:** Won't fix · **Verification:** Read in code

**Symptom.** When `kb.json`, `kb_index.json` or `kb_call_graph.json` is corrupt and 4 MiB or larger, Eulix takes about twice as long to report the error.

**Cause.** `DecodeJSONFile` (`mmap_loaders.go`) falls back to the buffered reader after any error from the mapped decode, not only when the file can't be mapped. A JSON syntax or type error is therefore hit twice: once through the mapping, then again through the reader.

**Why it is not fixed.** This is intended. One unconditional fallback keeps the loader simple, and the second parse only costs time on a knowledge base that is already broken, which `eulix analyze` rebuilds quickly.

**Workaround.** Run `eulix analyze` to rebuild the knowledge base.

---

## RET-13: A question naming a Python method gets the class, not the method

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced (2026-11-16, `07907ad`, on Flask 3.1.3 with the default configuration)

**Symptom.** Asked about a method by its bare name, retrieval returns the summary of the class that defines it and misses the method and its callers. `eulix query "full_dispatch_request"` and the MCP `query` tool both rank `src/flask/app.py:81-1536`, the 1,450-line `Flask` class summary, first. The method's own chunk, `app.py:904-920`, is not in the top five, and its caller `wsgi_app` is absent. `finalize_request` behaves the same way. Written as `Flask.full_dispatch_request`, the method comes back at rank 2, but over MCP `wsgi_app` is still missing even when the question asks who calls it. The index is correct: `functions_by_name` maps `full_dispatch_request` to `src/flask/app.py:904`, and `functions_calling` lists `wsgi_app` as its caller.

**Cause.** Probably [RET-1](#ret-1-the-kb_exact-search-strategy-never-returns-anything). The only lookup that maps a bare name through `functions_by_name` to its chunk is in `kbExactLookup` (`internal/query/retrieval/kb.go`), which always returns before reaching it. Not confirmed by a fix.

**Workaround.** Qualify the name with its class (`Flask.full_dispatch_request`). For callers, `eulix query "who calls full_dispatch_request"` takes the call-graph route and prints `← Flask.wsgi_app ← Flask.__call__` correctly.

**Fix direction.** Fix RET-1 so the `functions_by_name` lookup runs (or move it into the `exact` strategy), then check that a method's chunk outranks its class summary and that MCP caller questions reach the call graph.

---

## Related Docs

- [Retrieval Overview](../eulix-cli/query/retrieval/Overview.md): the pipeline.
- [Search Pipeline](../eulix-cli/query/retrieval/Search-Pipeline.md): where RET-1 and RET-2 occur.
- [Mmap](../eulix-cli/query/retrieval/Mmap.md): the file-mapping helpers behind RET-10 to RET-12.
- [Fixed Issues](Fixed-Issues.md): the EHNAS fixes already shipped.
