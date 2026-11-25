---
name: eulix
description: Use when working in a repository indexed by Eulix (a `.eulix/` folder or an `eulix` MCP server) and you need where a symbol is defined, who calls a function, what it calls, how one function reaches another, how a feature works, or the impact of a change; also when a repo needs indexing or the Eulix MCP server needs setting up.
---

# Eulix

Eulix answers code-structure questions from a prebuilt call graph and symbol index. It only
retrieves; no model writes the answer. Every turn re-reads the whole conversation, so **the
saving comes from fewer turns**: batch questions, trust finished answers, read only the lines a
reply points to.

## Pick the tool

| Question | Use |
| --- | --- |
| Text the code contains: default values, patterns, constants, messages, config keys, file names | grep a word that sits next to it (the setting's name, a nearby identifier), then Read those lines. `search` returns noise for these |
| Where X is defined, its signature | `definition` (`body_lines: 0` for the signature only) |
| Who calls X, impact of changing X | `callers` |
| What X calls | `callees` |
| How A reaches B, how X gets reached | `trace` with `from` and `to` (or only `to`) |
| How a feature works, debugging, no single symbol | `search` |
| Exact lines to quote or edit | Read the `file:a-b` range Eulix gave (offset/limit), not the whole file |

No MCP tools, only a shell: read [cli.md](cli.md). No `.eulix/kb.fb`, a "not analyzed" error, or
the MCP server needs registering: read [setup.md](setup.md).

## Spend fewer turns

- **Many symbols, one call.** `callers`, `callees` and `definition` take up to 8 comma-separated names: `"symbol": "Server.handleRequest, Server.authorize"`. Independent calls to different tools go in the same message.
- **`complete: yes` is final.** It was checked, including uses found by name. Don't confirm it with grep or a file read.
- **`more:` is optional.** Follow it only if you need what it names, and read just the range it gives. `no code shown for X, Y` means one `definition` call with both names.
- **Don't widen by default.** `bodies: true`, a bigger `max_tokens` or `depth` > 1 resend more than most questions need. Reach for them only when the code shown was not enough.
- **`trace` for the path alone:** pass `max_tokens: 200`. The budget covers only the code after the path, and when no static path exists that code is loosely ranked filler.
- **Repeats come back as references.** `(already sent this session) file:a-b` points at an earlier result in your context. On `search`, pass `fresh: true` only when that result is gone (after compaction).

A typical task: `definition` or `callers` on every name the task gives, in one call; `trace` if
the flow crosses files; `search` with what you learned in `known_symbols`; Read only the lines
you will edit. Stop as soon as the question is answered.

## When `complete:` is not `yes`

| Header says | Do |
| --- | --- |
| `n/a (ranked)` (`search`) | Judge the code shown; nothing checked that it answers the question |
| `symbol not found` | Retry with a name from the closest matches it lists |
| `may belong to another definition` | Pass the qualified name (`Type.method`) |
| `none found statically` | A common name the scan skips; grep is the right check |
| `no static path`, `chain starts at …` | A graph gap (below): `callers` on the function that registers or starts it, or `search` the channel or handler name |
| `list cut by the limit` | Raise `limit` or qualify the name |
| `source: lexical fallback` | Text matches, not ranked code: treat as leads, or re-ask with exact names |

## Where the call graph stops

It records direct calls and `go f()`. For Go it also infers channel, interface and
unknown-receiver edges, labelled `[async: …]`, `[interface dispatch: …]` and
`[receiver type unknown; …]`: strong leads, so check the line when it matters. It does not follow
callbacks stored and called later, event handlers, or, outside Go, channels and interface calls.
For distinctive names, `callers` still lists uses as a value (a handler, a callback) found by
name.

A call site shown as `(top level)` is package-level code, typically a function literal in a `var`
(a cobra `RunE`, a handler table). The graph has no node for it, so `trace` can't pass through:
Read the lines above the call site to see what owns it, and continue from that.

## Asking `search`

`search` ranks code by the concrete names in the question. Plain English words never count.

- Use the real identifier in backticks (`` `applyBudget` ``, not "the budget function"). Qualify methods: `Server.handleRequest`; a bare method name can return the whole class.
- Put symbols and paths you have already seen this session in `known_symbols` (up to 20). They widen what is searched and never lower the score, so extra ones are safe.
- One behaviour per question. Split "the parser, the embedder and the cache" into three calls, carrying `known_symbols` forward.
- Start "how is X run" questions with "How"; "Where are X run" is read as "where is X defined".
- Add the domain words the code would contain (token, retry, cache). Filler words are dropped.
- Eulix declines to write code. Ask how the existing code works, then write it yourself.

## Reading replies

- Shown source has comments and docstrings stripped; line ranges still refer to the file. Read the file before quoting or editing it.
- A signature-only chunk proves a function exists, not what it does. Don't describe a body you were not shown.
- Files that no longer exist, or code that looks old: run `eulix analyze` (incremental, cheap after small edits). If answers stay stale, restart the MCP server; a running server may keep the index it loaded.
- `search` pulling in unrelated code after many questions is session memory drift: restart the MCP server.
