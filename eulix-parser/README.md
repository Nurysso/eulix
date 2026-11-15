# eulix-parser

> The batch front-end to `libeulix`: parses a repository once and writes the JSON artifacts every other Eulix tool reads.

| Language | Version | Status | Docs |
| :-- | :-- | :-- | :-- |
| Rust | 0.9.0 | Stable | [docs/eulix-parser](../docs/eulix-parser/Overview.md) |

`eulix_parser` walks a source tree, hands each file to [`libeulix`](../libeulix/), runs the analysis passes and writes a set of synchronized JSON artifacts: symbols, call graphs, indices, metrics, entry points and dependencies. The Eulix query engine, the language server and retrieval pipelines consume them without ever reparsing the repository. `eulix analyze` runs it for you; you only need it directly to script the parser yourself.

## Install

From crates.io:

```bash
cargo install eulix-parser
```

From source:

```bash
git clone https://github.com/Nurysso/eulix
cd eulix/eulix-parser
cargo build --release
```

The binary is written to `target/release/eulix_parser`. Requires Rust stable and a C/C++ toolchain, because the Tree-sitter grammar crates compile C at build time.

## Usage

```text
eulix_parser [OPTIONS] --root <ROOT> --prism <PRISM>
```

| Option | Short | Description | Default |
| :-- | :-- | :-- | :-- |
| `--root <ROOT>` | `-r` | Repository root | Required |
| `--output <OUTPUT>` | `-o` | Knowledge-base output path | `knowledge_base.json` |
| `--threads <N>` | `-t` | Number of parser workers (`0` = physical cores) | `4` |
| `--languages <LANGS>` | `-l` | Comma-separated languages or `all` | `all` |
| `--prism <1\|2>` | `-p` | PRISM relationship engine version | Required |
| `--no-analyze` | `-n` | Parse files without the relationship and analysis phase | `false` |
| `--euignore <PATH>` | `-e` | Custom `.euignore` path | `<root>/.euignore` |
| `--parse-report <FILE>` | `-P` | Write a TSV listing every partial and failed file | off |
| `--verbose` | `-v` | Detailed phase output | `false` |
| `--version` | `-V` | Print version | |

### Examples

Full analysis with PRISM v2:

```bash
eulix_parser \
  --root /path/to/project \
  --prism 2 \
  --output .eulix/kb.json \
  --threads 12 \
  --verbose
```

Faster relationship analysis with PRISM v1:

```bash
eulix_parser \
  --root /path/to/project \
  --prism 1 \
  --output .eulix/kb.json \
  --threads 12 \
  --verbose
```

Selected languages only:

```bash
eulix_parser \
  --root /path/to/project \
  --languages rust,go \
  --prism 2 \
  --output out/kb.json
```

Parse without analysis, when you only need the knowledge base and want to skip the call graph and related passes:

```bash
eulix_parser \
  --root /path/to/huge-repo \
  --languages all \
  --prism 1 \
  --no-analyze \
  --output out/kb.json
```

## Output files

All files are written into the directory that contains the `--output` path, and the seven secondary files are named after its file stem. With the default output they are `knowledge_base_*.json`; with `-o .eulix/kb.json` (what `eulix analyze` uses) they are `kb_*.json`.

| File | Purpose |
| :-- | :-- |
| `knowledge_base.json` | Main knowledge base: file, symbol, AST-derived and language metadata |
| `knowledge_base_call_graph.json` | Call-graph nodes and edges, with call sites and relationship metadata |
| `knowledge_base_index.json` | Lookup indexes: functions by name, callers, tags, types and file categories |
| `knowledge_base_summary.json` | Project summary and language / line-count information |
| `knowledge_base_metrics.json` | Complexity and code metrics |
| `knowledge_base_entry_points.json` | Application entry points with route and command metadata |
| `knowledge_base_external_deps.json` | External package and dependency information |
| `knowledge_base_patterns.json` | Detected structural and architectural patterns |
| `checksum.json.zst` | Checksum of the parsed files |
| `parse_cache/` | Private cache that makes repeat runs fast (see [Incremental runs](#incremental-runs)) |

With `--no-analyze`, only the main knowledge base is written. See [Output Files](../docs/eulix-parser/Output-Files.md) for what each file holds.

## Incremental runs

Every run keeps a cache in `<output directory>/parse_cache/`. On the next run, files whose size and modification time are unchanged are reused without being read, and files whose content hash is unchanged are reused without being parsed. Only changed files are parsed again. The call graph and the other analysis files are always rebuilt, and the result is identical to a fresh full parse. With `--verbose`, the parser prints `Reused N cached files`.

On the full Linux kernel, an incremental run takes under 10 s, and most of that time goes to rewriting the JSON output rather than parsing or analysis.

Rebuilding the parser invalidates the cache. To force a full parse, delete the `parse_cache` directory. Design: [Incremental Parsing](../docs/Architecture/Incremental-Parsing.md).

## `.euignore`

`eulix_parser` uses its own ignore file instead of relying only on `.gitignore`. Create `.euignore` in the repository root; it takes GitIgnore-style patterns:

```gitignore
fixtures/
generated/
vendor/
*.generated.go
```

## How it works

```text
repository
    │
    ▼
file walker               (ignore-aware)
    │
    ▼
libeulix::grammar_files   (Tree-sitter parsing)
    │
    ▼
FileData (one per file)
    │
    ▼
libeulix::analyze         (PRISM, indices, patterns, metrics, deps)
    │
    ▼
KnowledgeBase
    │
    ▼
JSON artifacts
```

The CLI handles file discovery, spill files, the incremental cache, output writing and progress reporting. Each file's detailed output goes to disk as soon as it is parsed, and `kb.json` is assembled from those pieces at the end ([Spill to Disk](../docs/Architecture/Spill-To-Disk.md)). All parsing and analysis lives in `libeulix`; to embed the engine in your own Rust project instead of shelling out, depend on `libeulix` directly.

Tree-sitter is the syntax foundation, not the whole analysis system. Eulix adds repository-level and language-specific analysis on top of the syntax tree and writes the results into specialized artifacts, so the query engine can answer questions without reparsing.

## Performance

The parser processes repository-scale codebases with parallel workers and does structural analysis in the same pass instead of rediscovering repository information repeatedly. Current numbers for the full pipeline are in the [root README](../README.md#-benchmarks).

### v0.9.0

AMD Ryzen 5 5600X (12 CPUs, 16 GB), 12 parser threads, PRISM v2, three cold runs per repository (fresh output directory, so no parse cache). Measured with the script in the repo:

```bash
scripts/parser_perf.sh -t 12 -p 2 /path/to/repo
```

| Repository | Files | Lines of code | Parser total | Peak RSS | Clean / partial / failed |
| :-- | --: | --: | --: | --: | --: |
| gin (Go) | 99 | 24,515 | 0.12 s | 85–93 MB | 99 / 0 / 0 |
| JDK (Java, C/C++, JS, Python) | 20,058 | 6,761,860 | 11.84–12.42 s | 1,349–1,396 MB | 16,963 / 3,095 / 0 |
| Linux kernel (C, Rust, Python) | 64,460 | 37,322,695 | 52.18–54.66 s | 3,688–3,749 MB | 32,338 / 32,122 / 0 |

Linux kernel, median run (53.85 s):

| Metric | Result |
| :-- | --: |
| Functions / classes / methods | 647,514 / 211,297 / 7,978 |
| Parse | 48.69 s (peak 2,365 MB) |
| Call graph + indices (PRISM v2) | 1.99 s (peak 3,065 MB) |
| Summary + metrics | 0.26 s |
| Parser-reported total | 53.85 s |
| `/usr/bin/time` elapsed | 54.44 s |
| Graph nodes / edges | 774,876 / 1,506,441 |
| Peak RSS (write phase) | 3,749 MB |

Zero failed files. The kernel's partial parses are almost all C, expected without a preprocessor pass. Numbers depend on hardware, storage, repository layout, thread count, parser version and `.euignore`; they are local measurements, not guarantees.

### Output size, Linux kernel, v0.9.0

| File | Size |
| :-- | --: |
| `kb.json` | 361,959 KB (~353 MB) |
| `kb_index.json` | 340,440 KB (~332 MB) |
| `kb_call_graph.json` | 444,043 KB (~434 MB) |
| `kb_external_deps.json` | 10,579 KB |
| `kb_summary.json` | 3,272 KB |
| `kb_entry_points.json` | 237 KB |
| `kb_metrics.json` | 4.4 KB |
| `kb_patterns.json` | 0.13 KB |

The main `kb.json` went from ~2.06 GB in v0.7.6 to ~673 MB in v0.7.7, a 67.3% reduction, while output generation dropped from ~6.89 s to ~4.08 s. The reduction targets the serialized form, not the parser's in-memory structures.

## Security and pattern signals

The parser records signals that help downstream analysis: TODOs, entry points, dependency information and architectural conventions. They are aids, not a replacement for dedicated security tooling, testing or manual review. Neither PRISM nor the call graph is a complete security data-flow or vulnerability-analysis system.

## Development

```bash
cargo test
cargo fmt --check
cargo clippy
cargo build --release
```

## Related components

| Component | Language | Role |
| :-- | :-- | :-- |
| [libeulix](../libeulix/) | Rust | Analysis engine: parsing, PRISM call graphs, indexes |
| [eulix-parser](../eulix-parser/) | Rust | CLI that runs libeulix over a repository and writes `.eulix/` |
| [eulix-embed](../eulix-embed/) | Python | Embedding pipeline for the ETIYA engine |
| [eulix-cli](../eulix-cli/) | Go | The `eulix` binary: retrieval engines, MCP server, chat |
| [eulix-lsp](../eulix-lsp/) | Rust | Language server built on libeulix (beta) |
| [vizEulize](../vizEulize/) | HTML/JS | Call-graph explorer in your browser |

## License

Apache 2.0, like every Eulix component. See [LICENSE](../LICENSE).
