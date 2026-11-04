# CLI, MCP And TUI Issues

Known problems in the `eulix` command line, the terminal screens, the MCP server and the model client.

> **Scope:** `eulix-cli` (`internal/cli`, `internal/tui`, `internal/mcp`, `internal/llm`, `internal/cache`, `internal/utils`)  
> **Last verified against:** `ad744e9` (2026-11-03)

These entries were found by reading the current code; I did not run the screens or the server. Where an entry says *probably*, I am inferring the behaviour from the code. Read [Overview](Overview.md) for the legend.

---

## CLI-1: Each chat answer is probably saved to history twice

**Severity:** Medium · **Status:** Open · **Verification:** Read in code

**Symptom.** In `eulix history`, each chat question likely appears two times.

**Cause.** `eulix chat` gives the same history manager to the query router and to the chat screen. The router's `QueryEngine` saves the reply when it has a manager, and the chat screen saves it again when the reply arrives.

**Workaround.** Delete duplicates by ID with `eulix history delete <id>`.

**Fix direction.** Let only one component save. The screen is the better choice, because it already holds the split reasoning and answer.

---

## CLI-2: The history viewer deletes the wrong entry, does not refresh and has no way back

**Severity:** Medium · **Status:** Open · **Verification:** Read in code

Three problems in the history screen (`internal/tui/history-tui.go`):

1. **`d` in the list can delete the wrong entry.** The deletion uses `m.selected`, which is set only when you press Enter to open an entry. In the list it is still 0 (the newest entry) or the last entry you opened, not the highlighted one.
2. **After a deletion the list does not refresh.** The delete command works on a copy of the model and returns nothing. The database entry is removed, but the screen still shows it.
3. **There is no way back to the chat.** `/history` replaces the chat model entirely, and `q` in the list quits the whole program.

**Workaround.** Open an entry with Enter and delete it from the detail view, where `m.selected` is correct. Quit and reopen to see the result. Use `eulix history delete <id>` for precision.

Smaller issues in the same screen: answers are wrapped as plain text rather than rendered as Markdown, and long titles are cut by bytes, which can split a multi-byte character. See [History Viewer](../eulix-cli/tui/History-Viewer.md). The `tui` package has no tests at all.

---

## CLI-3: `[checksum]` settings are ignored

**Severity:** Low · **Status:** Open · **Verification:** Read in code

`checksum.change_threshold` and `checksum.force_reanalyze_threshold` are read, validated and clamped, but no command uses them. `eulix chat` uses fixed limits of 10% (ask first) and 30% (refuse).

---

## CLI-4: `eulix query` prints a prompt and never calls the model

**Severity:** Low · **Status:** By design · **Verification:** Read in code

For factual questions (locations, usage, call graphs, entry points, dependencies, metrics, todos) `eulix query` prints the answer. For everything else it prints the retrieved context and reasoning instructions, ready to paste into a model. It never contacts a model, though an older README said it did. Use `eulix chat` for model answers.

---

## CLI-5: Model requests cannot be cancelled, are not streamed, and have three smaller defects

**Severity:** Medium · **Status:** Open · **Verification:** Read in code

All in `internal/llm/client.go`:

- **No cancellation.** Requests are built with `http.NewRequest`, without a context, and the only limit is a five-minute HTTP timeout. A slow local model generating `max_tokens = 8192` can hit that limit, and the chat screen cannot abort a request.
- **No streaming.** The client waits for the whole reply, so the chat shows nothing until the model has finished.
- **Only the first Anthropic content block is returned** (`resp.Content[0].Text`). That is fine for plain text. If extended thinking or tool use is ever enabled, the first block may not be text and the answer would come back empty.
- **A debug-log failure blocks the answer.** With `DebugConfig` on, if `.eulix/debug/llmdebug.log` cannot be written (read-only checkout, full disk), `LlmResponse` returns that error instead of asking the model.
- **Typos in key names are silent.** A misspelled key in `eulix.toml` (for example `modle` under `[llm]`) is ignored without a warning. Reporting unknown keys would need the TOML decoder's metadata and a policy for old, removed keys.

---

## CLI-6: History entries with equal timestamps list in arbitrary order

**Severity:** Low · **Status:** Open · **Verification:** Read in code

`ListAll` sorts by `CreatedAt` alone with an unstable sort. On a coarse clock (Windows), two entries can share a timestamp and swap places between calls. Sorting by `ID`, which only grows, fixes it.

---

## CLI-7: Developer-facing loose ends

**Severity:** Low · **Status:** Open · **Verification:** Read in code

- **`DebugLogger` data races.** `Log` and `Flush` read the `closed` flag without the lock, and `Lines` and `Write` do not lock. Harmless today, but `go test -race` will flag any test that logs while the logger is closing.
- **Lint config is never loaded.** The file is `eulix-cli/golangci.yml`; golangci-lint looks for `.golangci.yml`, and the file uses the version 1 format that golangci-lint 2.x rejects. `make lint` runs only the default linters.
- **A misleading comment.** `chat.go` says the checksum "is already saved by `checksum.Run()`". `Run` only compares; the parser writes the baseline during `eulix analyze`. The code is right; the comment is not.
- **Dead repair tools.** `internal/fixers` (`GLaDOS`, `Aspirine`) is not imported by anything, the `glados` and `aspirine` commands are stubs, and the code assumes a binary layout that no longer matches the quantized files. See [Fixers](../eulix-cli/Fixers.md).
- **Leftovers in the chat screen.** `StateTyping` is never set, and the welcome text and title bar are fixed strings (the title says "local-first" even with a cloud provider).

---

## MCP-1: A repository's own `.eulix/` and `eulix.toml` are trusted

**Severity:** High · **Status:** Open · **Verification:** Carried over

**Symptom.** The MCP server binds any directory that contains `.eulix/kb.json`. A cloned repository can therefore ship its own knowledge base and `eulix.toml` and be queried without you ever running `eulix analyze`. With an editor that sends workspace roots, the binding is automatic.

**Impact.** The known abuses are closed (see [Fixed Issues](Fixed-Issues.md): code execution through a model name, reading arbitrary files through the index, and leaking ignored files). The trust itself remains: a hostile repository still chooses the configuration, including which model is downloaded ([EMB-2](Embeddings.md#emb-2-a-repository-still-chooses-which-embedding-model-is-downloaded)), the language-model provider address, and the contents of the knowledge base.

**Workaround.** Only open repositories you trust in an editor that uses Eulix; or pass `--repo` explicitly and decline automatic roots.

**Fix direction.** Bind only when the checksum file shows the knowledge base was built on this machine, or ask the user once per repository.

---

## MCP-2: A panic in a handler takes the MCP server down

**Severity:** Low · **Status:** Open · **Verification:** Read in code

The SDK does not recover panics, and the package has no `recover`. Fuzzing the query parsers found no panic, but one deeper in retrieval would kill the editor's server. A `recover` that turns the panic into a tool error would keep it alive.

---

## MCP-3: MCP text-search fallback gaps

**Severity:** Low · **Status:** Open · **Verification:** Read in code

- It does not remove identical content across files, so repositories with backup trees waste budget. Use `.euignore` or `lexical_exclude`.
- `retrievalConfig.etiya.lexical_exclude` is read only when the engine is `etiya`.
- The client "roots" feature the server uses to find a repository is deprecated in newer protocol versions, and clients on those versions never send the message that triggers it. For them, use `--repo`, `set_repo_path` or `repo_path`.
- Multi-hop retrieval depends on an implicit contract: anchors are handed to the router by appending a `Key symbols:` line to the question. Changing the router's prompt format could silently weaken it.

---

## Related Docs

- [Cli](../eulix-cli/Cli.md), [Chat Screen](../eulix-cli/tui/Chat-Screen.md), [MCP Overview](../eulix-cli/mcp/Overview.md), [Llm](../eulix-cli/Llm.md).
- [Fixed Issues](Fixed-Issues.md): what the pre-launch pass fixed.
