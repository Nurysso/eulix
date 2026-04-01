# Eulix — Architecture Documentation

This directory contains architectural diagrams and design documentation for the
Eulix code-intelligence system. Each document focuses on one layer or subsystem,
includes Mermaid diagrams (rendered natively on GitHub), and lists the
**purpose** and **limitations** of the view it describes.

---

## Document Index

| # | Document | What it covers |
|---|----------|---------------|
| [01](./01-system-overview.md) | **System Overview** | Three-binary architecture; language choices; component responsibilities |
| [02](./02-data-pipeline.md) | **Data Pipeline** | `eulix analyze` — from source files to `.eulix/` artifacts |
| [03](./03-query-pipeline.md) | **Query Pipeline** | `eulix chat` — from question to LLM answer; retrieval strategy scoring |
| [04](./04-module-structure.md) | **Go Module Structure** | Internal Go package dependency graph; proposed interfaces |
| [05](./05-cache-architecture.md) | **Cache System** | SQLite + Redis dual backend; key design; TTL; CLI operations |
| [06](./06-classifier.md) | **Query Classifier** | 3-level classification; 15 query types; known routing bugs; how to add types |
| [07](./07-parser-internals.md) | **Parser Internals** | `eulix_parser` component map; parallel parse pipeline; language support status |

---

## How to Read These Diagrams

All diagrams use [Mermaid](https://mermaid.js.org/), which GitHub renders
automatically in `.md` files. If you are reading locally, use VS Code with the
[Mermaid Preview](https://marketplace.visualstudio.com/items?itemName=bierner.markdown-mermaid)
extension, or paste diagram code into the [Mermaid Live Editor](https://mermaid.live).

### Diagram types used

| Mermaid type | Used for |
|-------------|---------|
| `graph TD/LR` | Component and dependency graphs |
| `flowchart TD/LR` | Data and control flow |
| `sequenceDiagram` | Request/response interactions (cache) |

---

## Architectural Principles

1. **Offline-first** — all persistent state lives in `.eulix/` local files; no
   daemon or server is required.

2. **Source code privacy** — the LLM receives only AST/semantic data extracted
   by the parser, never raw source code. This is both a design choice and a
   constraint — the LLM cannot reason about implementation logic.

3. **Three-binary separation** — Go for orchestration/UX, Rust for
   performance-critical analysis and embedding. Each binary is independently
   buildable and testable.

4. **Cache-invalidation via checksum** — query responses are keyed on both the
   query hash and the KB checksum so that any codebase change automatically
   invalidates stale answers.

5. **Multi-strategy retrieval** — no single retrieval signal is relied upon;
   exact symbol, partial identifier, keyword, and vector searches are combined
   and score-boosted when they agree.

---

## Known Cross-Cutting Issues

> These issues affect multiple subsystems and are tracked here for visibility.

| Issue | Affected diagrams | Status |
|-------|------------------|--------|
| KB exact-lookup results discarded by a `make(map)` reset | 03, 04 | 🐛 Bug |
| `eulix_embed` binary hardcoded at `../eulix_embed` | 02, 03 | 🐛 Bug |
| Token count approximated as `chars / 4` | 02, 03 | ⚠ Inaccurate |
| No Go interfaces — all deps are concrete structs | 04 | ⚠ Testability gap |
| JS / TS / Rust parsers return errors silently | 07 | ⚠ Missing feature |
| Cache `Set` errors silently swallowed | 05 | ⚠ Observability gap |
