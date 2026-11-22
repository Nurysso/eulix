# Directory listings (prepared, not submitted)

Every entry below is ready to paste. Nothing has been submitted. Submission rules were checked on 2026-11-20; the ones marked **verify** could not be read that day.

**Shared facts.** Repo `https://github.com/Nurysso/eulix` · license Apache-2.0 · v0.9.0 · local stdio server · Linux x86-64, macOS Apple Silicon, Windows x86-64 · Rust (parser), Go (CLI and MCP server), Python (optional embeddings).

**Figures you may quote.** Each has a measured source in the README:
- 98.6% of PRISM v2's call edges are right (8 projects, checked against each language's compiler tooling).
- Warm query under 20 ms at p50, server memory 81–89 MB (EHNAS, 5 repos).
- Linux kernel (37.3M lines) parsed with its call graph in 53.9 s.

Don't quote a token-savings figure until it stands up. See [`bench/results/agent/`](../../bench/results/agent/) and the note at the end of this file.

**Blockers before most of these:**
1. **Publish the npm and PyPI launchers** (`packaging/`). The official registry needs a package on npm or PyPI, and most other directories copy from it.
2. **A 400×400 PNG logo** (Cline needs it; Glama and mcp.so display one).
3. **An `llms-install.md`** in the repo root is recommended by Cline. A draft is at the end of this file.

---

## 1. Official MCP Registry

- **Where:** https://registry.modelcontextprotocol.io (publishing is by CLI only; a PR with a server.json does **not** list you).
- **File:** [`server.json`](../../server.json) in the repo root, valid against the registry's schema (2025-12-11). Name `io.github.nurysso/eulix`. It lists the npm package (`npx eulix mcp`) and the PyPI package (`uvx eulix mcp`).
- **Ownership markers:** already in place: `"mcpName": "io.github.nurysso/eulix"` in `packaging/npm/package.json`, and `<!-- mcp-name: io.github.nurysso/eulix -->` in `packaging/pypi/README.md`.

**Steps**
1. Publish `eulix` to npm and PyPI (see `packaging/README.md`). The registry checks both packages for the markers above.
2. Install the publisher: `brew install mcp-publisher`, or download `mcp-publisher_<os>_<arch>.tar.gz` from the registry's GitHub releases.
3. From the repo root, run `mcp-publisher init` once and check that the name it proposes matches `io.github.nurysso/eulix` (registry names are case-sensitive; if it proposes `io.github.Nurysso/eulix`, use that everywhere instead).
4. Run `mcp-publisher login github`, then `mcp-publisher validate`, then `mcp-publisher publish`.
5. For each new release, run `python3 packaging/sync_release.py vX.Y.Z`, bump `version` in `server.json` (both places), and publish again.

---

## 2. Smithery — verify

Smithery's old docs (`smithery.ai/docs`) return 404, and the site now says *"Smithery is now part of Arcade."* We could not confirm the current way to list a local stdio server. Check in a browser whether it still has an "Add server" flow for GitHub repos, or whether it now picks servers up from the official registry.

**Listing text if a form asks for it**
- **Name:** Eulix
- **Short description:** Local code retrieval for AI coding agents: a call-graph index of your repo, served as ranked, token-budgeted context over MCP.
- **Install command:** `npx -y eulix mcp --repo /path/to/repo` (once the npm package is published)
- **Tags:** code-search, call-graph, developer-tools, local, rust, go

---

## 3. Glama — verify mechanics

- **Where:** https://glama.ai/mcp/servers → "Add Server" button in the header. Glama also indexes public GitHub MCP repos on its own.
- **Why it matters:** every entry in awesome-mcp-servers carries a Glama score badge, so get listed here first.
- **Optional `glama.json`** in the repo root to claim the listing. Glama didn't document this format on the day we checked, so confirm it before committing:
  ```json
  {
    "$schema": "https://glama.ai/mcp/schemas/server.json",
    "maintainers": ["Nurysso"]
  }
  ```
- **Description:** Local-first retrieval engine for AI coding agents. Parses your repo with Tree-sitter (9 languages) into a symbol index and call graph (98.6% precise edges), then answers agent questions over MCP with ranked, token-budgeted code chunks, each with file:line. No embeddings, GPU or network needed in EHNAS mode.
- **Support contact:** support@glama.ai

---

## 4. PulseMCP

- **Status:** submissions are **paused** (page updated 2026-09-03). PulseMCP says to publish to the official MCP Registry and it will pick the server up automatically when it reopens.
- **Steps:** do #1. Then check https://www.pulsemcp.com/submit occasionally and use the Glama description above if a form comes back.

---

## 5. mcp.so

- **Where:** https://mcp.so/submit → type "MCP Server".
- **Form fields:** Repository URL `https://github.com/Nurysso/eulix` · Name `Eulix`.
- **Cost:** the submit page shows a **$39 one-time fee** (published immediately, verified badge, dofollow link) and no free path. The project's GitHub repo (`chatmcp/mcpso`) used to take free submissions through issues; check it before paying.
- **Description (if asked):** use the Glama description.

---

## 6. awesome-mcp-servers (punkpeye)

- **Where:** PR to https://github.com/punkpeye/awesome-mcp-servers, editing `README.md`. One server per line, alphabetical within the section.
- **Section:** `### 💻 Developer Tools`
- **Legend used:** 🏎️ Go codebase (the MCP server itself) · 🏠 local service · 🍎 macOS · 🪟 Windows · 🐧 Linux
- **Prerequisite:** the Glama listing (#3), because entries carry a Glama score badge.
- **Line:**
  ```markdown
  - [Nurysso/eulix](https://github.com/Nurysso/eulix) [![Nurysso/eulix MCP server](https://glama.ai/mcp/servers/Nurysso/eulix/badges/score.svg)](https://glama.ai/mcp/servers/Nurysso/eulix) 🏎️ 🏠 🍎 🪟 🐧 - Indexes a repo into a Tree-sitter symbol index and call graph (9 languages), then answers agent questions with ranked, token-budgeted code chunks with file:line references. Local, no embeddings or GPU needed.
  ```
- **PR title:** `Add Nurysso/eulix (local code retrieval with call graphs)`. The list fast-tracks PRs whose title ends in "🤖🤖🤖", but that is only for PRs opened by agents, so leave it off if you open it yourself.

---

## 7. awesome-claude-code (hesreallyhim)

- **Where:** issue form only: https://github.com/hesreallyhim/awesome-claude-code/issues/new?template=recommend-resource.yml. **No PRs, and no `gh` CLI.** A human has to submit it in the browser.
- **Eligibility:** at least 14 days old with active development, or at least 100 stars. One line, no emojis, no sales pitch.
- **Risk:** one checklist item reads "This resource is specific to Claude Code". Eulix is a generic MCP server, so the case for it rests on the Claude Code setup (`claude mcp add`, the agent skill, the benchmark run with Claude Code). If you have a Claude Code skill for Eulix (`skills/eulix/SKILL.md` exists in the EulixClaude checkout), recommend the skill plus server together, since that part is Claude Code specific.
- **Form fields:**
  - **Display Name:** Eulix
  - **Category:** Memory & Context Persistence
  - **Link:** https://github.com/Nurysso/eulix
  - **Author Name:** Dawood K. (Nurysso)
  - **Author Link:** https://github.com/Nurysso
  - **Description:** Local MCP server and agent skill that index a repo into a Tree-sitter symbol index and call graph, so Claude Code can ask where code lives or who calls what and get ranked, token-budgeted chunks with file:line references instead of grepping.
  - **Checklist:** read each item and tick it honestly. Leave the last box unchecked, as the form says.

---

## 8. Cursor MCP directory — verify

`cursor.directory/mcp` rate-limited our check (HTTP 429), and Cursor's MCP and deeplink docs pages returned 404. Open https://cursor.directory/mcp in a browser and use its submit button.

- **Name:** Eulix
- **Description:** use the Glama description.
- **Config for `.cursor/mcp.json`** (this is Cursor's long-standing format; check it against their current docs):
  ```json
  {
    "mcpServers": {
      "eulix": {
        "command": "eulix",
        "args": ["mcp", "--repo", "${workspaceFolder}"]
      }
    }
  }
  ```
  If `${workspaceFolder}` isn't expanded in your Cursor version, drop `--repo` and let the agent call `set_repo_path`.
- **"Add to Cursor" button:** Cursor's install deeplink is usually `cursor://anysphere.cursor-deeplink/mcp/install?name=eulix&config=<base64 of {"command":"eulix","args":["mcp"]}>`. We could not confirm the format against Cursor's docs, so test the link before using it anywhere.

---

## 9. Cline MCP Marketplace

- **Where:** issue at https://github.com/cline/mcp-marketplace/issues/new?template=mcp-server-submission.yml
- **Fields:**
  - **GitHub Repo URL:** https://github.com/Nurysso/eulix
  - **Logo Image:** a 400×400 PNG (to make)
  - **Reason for Addition:** Eulix gives Cline structural code lookup (where is X, what calls X, trace a flow) through one MCP call. The call returns ranked, token-budgeted code chunks with file:line references, from a call graph whose edges are 98.6% precise across 8 open-source projects. It runs locally with no API key, GPU or network, and its tools are read-only.
- **Before submitting:** Cline's reviewers check that Cline can install the server from `README.md` and/or `llms-install.md` alone. Add the `llms-install.md` below, then test it by giving Cline only that file in a clean environment.

### Draft `llms-install.md`

```markdown
# Installing the Eulix MCP server

1. Install the binary:
   - Linux / macOS: `curl -fsSL https://raw.githubusercontent.com/Nurysso/eulix/main/install.sh | bash`
   - Windows (PowerShell): `irm https://raw.githubusercontent.com/Nurysso/eulix/main/install.ps1 | iex`
   It installs `eulix` to `~/.local/bin` (Windows: `%LOCALAPPDATA%\Eulix\bin`). Make sure that is on PATH.
2. Index the user's project. `eulix init` is interactive, so create its three files directly instead:
   - an empty `.eulix/` folder
   - `.euignore` with the lines `*.test*`, `vendor/`, `dist/`, `test/`, `build/`
   - `eulix.toml` containing `[project]` and `engine = "ehnas"` (no embeddings, no GPU)
   Then run `eulix analyze` in the project root.
3. Add the server to the MCP settings:
   {"mcpServers": {"eulix": {"command": "eulix", "args": ["mcp", "--repo", "<absolute path to the project>"]}}}
4. Check it: call the `query` tool with "Where is the main entry point?". The reply starts with
   "Context: N source chunks ..." and lists file:line references.
```

**Verify before adding:** step 2 assumes a minimal `eulix.toml` with only `engine = "ehnas"` is enough. The full template in `bench/suite/eulix.toml.tmpl` is known to work; trim it down and test.

---

## A note on the token-savings claim

The first agent benchmark (`bench/agent/`, run `2026-11-20-sonnet`: 10 tasks on Flask, Django and ripgrep, Claude Sonnet 5.5, 5 reps per setup) found **no token saving**: with a "query Eulix first" hint the agent made 31% fewer tool calls but cost 18% more. That was the single `query` tool with ~5,700-token replies.

After the typed tools (`callers`, `callees`, `definition`, `trace`, `search`) and reply budgets, Claude Code on bifrost (a 1.6M-line Go repo newer than the model's training data) cost less with Eulix and the `eulix mcp install` hint:
- **Opus 4.8**, one session per task, 15 tasks: **−22% cost** ($8.67 to $6.78), 14/15 correct against 13/15 (`2026-11-20-opus48-bifrost`).
- **Sonnet 5.5**, medium effort, one chained session, 13 tasks: **−6% cost**, 13/13 against 13/13 (`2026-11-20-bifrost-chain-medium`).
- *Who calls X* questions cost 27–30% less; "where is X" questions are about even.

These are one session per task and arm, on one repo, with Claude Code only. A listing may quote them with that scope. Leave out "Stop paying your agent to grep" and any claim of savings on every question, on other agents, or on repos the model already knows. Details: [`eulix-use-cases.md`](eulix-use-cases.md).
