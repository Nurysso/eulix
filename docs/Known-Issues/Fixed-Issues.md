# Fixed Issues

Problems that used to exist and no longer do, with the test that now guards each one. This is the record behind the open lists, so that a regression can be recognized.

> **Scope:** all subsystems  
> **Source:** the maintainers' pre-launch and EHNAS fix notes, plus changes visible in the commit history  
> **Last verified against:** `ad744e9` (2026-11-03)

I did not re-test these fixes. Test names are taken from the maintainers' notes. Where a fix is a recent commit, the commit is named. If one of these returns, move it back to the matching page and mark it *Open*.

## MCP server and security

| Problem | What users saw | Fix | Test |
| --- | --- | --- | --- |
| **Critical:** a repository could run code through its embedding model. | A repository whose `eulix.toml` named `engine = "torch"` and a model containing "jina" had that Hugging Face repository's Python executed on the first semantic question, automatically when an editor sent workspace roots. `trust_remote_code` was on for every "jina" name, and a local directory of that name was preferred. | `trusts_remote_code()` grants remote code only to Jina's own models (`jinaai/…`) and never to a name that resolves to a local path. A fine-tuned Jina model under another organization now needs the ONNX engine. Latest change: commit `ad744e9`. | `eulix-embed/tests/test_trust_remote_code.py` (confirmed by reading the code path, not by running an exploit). |
| Usage queries could read any file on the machine. | "Usage of X" printed a signature for each `file:line` the index listed, so a shipped `kb_index.json` could list `/home/user/.ssh/id_rsa:1` and have it printed. Usage queries also failed under MCP because relative paths opened against the server's working directory. | Locations are read through a rooted file handle that blocks `../`, absolute paths and escaping symlinks, and must pass the servable-file filter. | `TestE2EUsageQueryReadsOnlyInsideTheRepo`, `TestHandleUsage`. |
| `.euignore`'d files leaked through the text-search fallback. | Of 14 files the parser excluded in a test corpus, all 14 came back through MCP. | The Go matcher now follows gitignore semantics: `**` at the start, middle and end; parent-directory exclusion; nested `.euignore` files; the file read whole, including lines over 64 KiB and CRLF. Negation (`!pattern`) is not supported, which fails in the safe direction. | `TestLexicalSearchHonoursEuignoreLikeTheParser`, `TestLexicalSearchNeverShowsWhatTheParserExcludes` (runs the real parser when built), `TestIgnoreMatcherGitignoreSemantics`, `TestIgnoreFilesNestedLongLinesAndCRLF`. |
| Hidden and ignored files were served from a stale or shipped knowledge base. | A knowledge base listing `.env` (where API keys are stored) had that file read and returned by every tool. | `ServableMatcher` allows a path only if it stays in the root, has no hidden component and is not excluded by the current rules. Applied when chunks load and when the full knowledge base loads. | `TestE2ENeverReturnsHiddenOrIgnoredFiles`. |
| The request path could corrupt the protocol stream. | The MCP client got a parse error and dropped the session, because two error cases printed to standard output. | Both go to standard error or the debug log. | `TestE2ENothingIsWrittenToStdout`. |
| Single-symbol queries got text-search output instead of the answer. | An exact symbol such as `payment_total` returned lexical matches under a "low-confidence" note. The scorer removed the query from the result before scoring, which deleted the symbol from the retrieved code. | Only the part before the router's `QUERY` echo is scored, and only multi-word echoes are stripped. The same query now scores 0.93 at the multi-hop stage. | `TestCascadeSingleSymbolQueryKeepsItsSemanticAnswer`. |
| A running MCP server locked chat history. | While an editor ran `eulix mcp` in a project, `eulix chat` and `eulix history` failed after a 2-second timeout. | MCP builds its routers without a history cache. | `TestE2EBindingDoesNotLockChatHistory`. |
| Symlinked repository paths got no text-search fallback. | The directory walker does not enter a symlinked root. | Binding resolves the root to the real folder, and search stays on it. | `TestSetRepoPathPinsAndRebuildsSymlinkedRoot`, `TestPRGap8LexicalSearchDoesNotFollowRootReplacedAfterBind`. |
| Symlinks could expose hidden, ignored or outside files. | With `app/billing.py` linked to `.env`, a query returned the `.env` contents. A file swapped for a symlink during a text search could return a file from outside the repository. | Text search and the source shown in answers read through `checksum.ReadRegularNoSymlinks`, which refuses links at every path component. Other routes remain open: see [MCP-5](CLI-MCP-And-TUI.md#mcp-5-symlinks-can-still-read-or-write-outside-a-repository). | `TestPRGap1LexicalWalkDoesNotFollowSwappedSymlink`, `TestPRGap4QueryDoesNotFollowSymlinkToExcludedFile`. |
| Workspace roots never bound on Windows. | `file:///C:/repo` became `\C:\repo`. | The slash before a drive letter is dropped. | `TestURIPath`. |
| Roots binding could overwrite an explicit `set_repo_path`. | The check and the bind took the lock separately. | They share one lock. | `TestRootsNeverOverrideAnExplicitChoice`. |

## Setup, configuration and the model client

| Problem | What users saw | Fix | Test |
| --- | --- | --- | --- |
| Saving `.env` deleted comments and blank lines. | The wizard rewrote `.env`, losing every non-pair line. | `EnvFile` keeps the original lines and rewrites only keys that changed. | `TestEnvFile`. |
| Adding ignore patterns could corrupt `.euignore`. | Without a trailing newline, `*.log` plus `dist/` became `*.logdist/`. | A newline is written first when needed. | `TestEuignoreSteps`. |
| A broken `eulix.toml` was silently ignored. | A syntax error made Eulix use defaults with no warning. | A parse error is returned with the file and line; a missing file still means defaults. | `TestLoadRejectsBrokenConfig`. |
| A query after a timeout raced the timed-out one. | A data race, and possibly mismatched replies. | A timeout marks the embedder dead and later requests fail at once. | `TestEmbedderTimeoutKillsServer` (under `-race`). |
| Shutdown and start-up errors could lose the embedder's last log lines. | The crash reason was missing from the diagnostics; failed starts leaked file descriptors. | A `reap` step waits for the log reader before waiting on the process, capped at 5 s. | `TestEmbedderRoundTrip`, `TestEmbedderStartupFailures`. |
| On Windows, the Python subprocess lost the system `PATH`. | Anything found through `PATH` (DLLs, CUDA tools) was missing. | `prependPath` matches the name case-insensitively. | `TestPrependPath`. |
| The Gemini API key leaked into error messages. | Network errors printed the URL with `?key=…`, and it reached the debug log. | The key is sent in an `x-goog-api-key` header. | `TestErrorsDoNotLeakAPIKey`. |
| Ollama ignored `temperature = 0`. | `omitempty` dropped the zero. | Removed `omitempty`. | `TestProviderWireFormats`. |
| Local mode with a blank provider called the wrong endpoint. | Every answer failed with "ollama: empty response". | The provider is resolved once for both the base URL and the path. | `TestProviderWireFormats`. |
| The unknown-provider hint named a key that does not exist. | It said `base_url`; the key is `baseURL`. | Message corrected. | `TestValidateConfig`. |
| Empty context chunks were sent as metadata. | Empty `[M2] file:0-0` entries inflated the prompt header. | Empty chunks are skipped. | `TestBuildFullPrompt`. |
| Go structs did not match what the parser writes. | Answers showed `complexity 0, importance 0.00`, and a "Security notes" section that could never fill. | `KBStruct.go` now mirrors the parser's slim views; metrics are read from `kb_metrics.json`; the security-note sections are gone. | Verified against real parser output for a mixed-language sample, with unknown fields rejected. |

## Retrieval (EHNAS and shared)

| Problem | What users saw | Fix | Test |
| --- | --- | --- | --- |
| **Fix 1:** the path filter removed every result for non-code file names. | "How is eulix.toml loaded" returned nothing from EHNAS, because `eulix.toml` was treated as a required path no chunk contains. | A file anchor is required only if some chunk path contains it; otherwise it is a soft preference. Covers all three callers. | `gate_test.go`. |
| **Fix 2:** non-determinism in BM25. | The same query on two fresh servers could return different chunks (6 of 20 questions). | Ties break on chunk index or chunk ID in the BM25 sorts and in `byScoreDesc`. | `determinism_test.go`. |
| **Fix 3:** query words did not match code words. | "seeds" did not find `mcSeedIndices`; "loaded" did not find `Load`. | One shared tokenizer splits camelCase and stems words, used by the index, BM25 and feedback. Expected file ranked first rose from 12 to 14 of 20, mean reciprocal rank 0.657 to 0.748. | `stem_test.go`. |
| **Fix 4:** long functions won conceptual questions. | A 523-line orchestrator was returned and hydrated for unrelated questions. | A length penalty `sqrt(120 / lines)` with an exemption when the chunk's own name, class or path shares a key with the question, plus exemptions for exact matches. A plain penalty regressed two benchmark questions and was replaced. | `long_chunk_test.go`. |
| Smaller EHNAS changes. | Random seed choice for tied walk seeds; a 5000+ anchor pin set the score floor too high. | Tie-breaks in seed picking and final ordering; the pre-selection floor ignores pinned chunks; per-engine config sections. | Retrieval package tests. |
| `eulix-embed` README and help text disagreed with the code (EMB-8). | The README listed a `--no-comments` flag that does not exist and a commented-out `--save-json`, and a binary format without the `quantized` byte and scales. `embed --help` said the engine default was `torch` (it is `onnx`), the server help named the wrong default model, and `-d` omitted `rocm`. | README and help strings corrected to match the code. | `embed --help` output checked by hand. |
| The query embedder ignored the configured engine. | With `engine = "torch"`, queries still used ONNX. | `VectorWeaver` passes `-e <engine>` (commit `6e1050a`). | None recorded. See [EMB-1](Embeddings.md#emb-1-onnx-on-rocm-produces-wrong-embeddings) for what remains. |

## Parser and PRISM

| Problem | What users saw | Fix |
| --- | --- | --- |
| PRISM v2 never linked calls to methods. | `self.validate()`, `this.step()` and Rust `self.x()` got no edge; every method's `called_by` was empty (950 method nodes with zero incoming edges in the Eulix repository's own knowledge base). | Commit `95f1288` stores the call receiver separately and indexes methods by declared name for **Python, TypeScript and Rust**, resolving `self`, `this`, `cls` and `Self` through the class and its parents. I confirmed on 2026-11-03 that these three now resolve. Go, Java, JavaScript, C and C++ remain open as [PRISM-2](Parser-And-PRISM.md#prism-2-v2-never-links-calls-to-methods-in-go-java-javascript-c-and-c). |
| Calls through unknown receivers could invent edges. | A uniquely named method was linked by guessing. | Commit `02f3674` disables that guess. This is the behaviour described in [PRISM-3](Parser-And-PRISM.md#prism-3-calls-on-a-receiver-of-unknown-type-get-no-edge-in-v2). |
| Rust module paths and JavaScript lookups mis-resolved. | Wrong targets for `crate::`/`self::` paths and cross-file JavaScript names. | Commit `c6acd89`. |
| Method resolution broke compatibility with older callers. | Regressions after the receiver work. | Commit `dcc1633` (restore method resolution compatibility). |
| `self.x()` in Python was not recognized as an implicit receiver. | The shortcut looked for a `self` parameter that the Python parser removes. | Python methods are now treated as having a receiver unless they are static methods (verified: an inherited method in another directory resolves). |

## Related Docs

- [Overview](Overview.md): the open list.
- [Parser And PRISM](Parser-And-PRISM.md), [Retrieval And Query](Retrieval-And-Query.md), [Embeddings](Embeddings.md), [CLI, MCP And TUI](CLI-MCP-And-TUI.md).
