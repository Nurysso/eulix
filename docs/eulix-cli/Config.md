# config

The `config` package reads `eulix.toml`, fills in defaults, keeps every value inside a safe range, and finds API keys.

> **Crate / path:** `eulix-cli/internal/config/config.go`  
> **Language:** Go  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

Every part of `eulix` gets its settings from one `Config` value. The file is `eulix.toml` in the project root, written by `eulix init`. Anything you leave out takes a built-in default, so a short file works. Values outside their allowed range are clamped silently, rather than rejected, so a typo such as a negative thread count cannot crash the program.

## Why It Exists

Retrieval quality depends on a dozen tuning knobs, and the right values differ between a small script collection and a ten-million-line monorepo. Putting them in one documented file lets you tune them without recompiling, and keeping the loading logic in one package means every command and the MCP server agree about what a setting means.

## How It Works

`Load()` starts from `DefaultConfig()`, overlays `eulix.toml` from the current directory, clamps the values, makes the project path absolute, and, if no API key is set, looks one up. `LoadFrom(dir)` does the same for another project directory; the MCP server uses it to serve several repositories. A missing file simply means defaults. A file that exists but cannot be parsed is an error, on purpose: silently falling back to defaults would hide a mistake.

**API keys.** If `api_key` is empty, the loader looks for a provider-specific environment variable (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `GROQ_API_KEY`, `TOGETHER_API_KEY`, `MISTRAL_API_KEY`, `DEEPSEEK_API_KEY`, `OPENROUTER_API_KEY`, `FIREWORKS_API_KEY`), then for a generic `LLM_API_KEY`. The process environment is checked first, then the project's own `.env` file. The `.env` file is read per project and never copied into the process environment, so one project's keys cannot leak into another's.

### Main Components

| Item | Responsibility |
| --- | --- |
| `Config` | The full settings tree, one sub-struct per `eulix.toml` section. |
| `DefaultConfig()` | The built-in defaults. |
| `Load()`, `LoadFrom(dir)` | Read, validate and return a `Config`. |
| `validateAndClampConfig` | Forces every number into its valid range. |
| `RetrievalConfig.ForEngine(name)` | Applies per-engine overrides. |

### Data In, Data Out

In: `eulix.toml`, `.env`, environment variables. Out: a `Config` that is safe to use without further checks.

## Key Algorithms

### Per-engine overrides

**The problem.** The two retrieval engines ([Engines](query/retrieval/Engines.md)) share most settings, but each wants a few different values, such as how much redundancy to tolerate.

**The approach.** The `[retrievalConfig]` section holds the shared values. Sub-sections `[retrievalConfig.etiya]` and `[retrievalConfig.ehnas]` may override a few of them. In the override structs the fields are pointers, so "not written in the file" (nil) is different from "set to zero". `ForEngine` copies the shared values and replaces only the ones that were set.

**Why this way.** Distinguishing "absent" from "zero" lets a user set a value to 0 deliberately, and means an engine section never has to repeat every key.

## Configuration

Defaults below are the ones in `DefaultConfig()`.

### `[project]`

| Key | Default | Meaning |
| --- | --- | --- |
| `path` | current directory | Absolute path of the repository. Moving the project means updating it. |
| `engine` | empty (means `etiya`) | Retrieval engine: `etiya` or `ehnas`. Anything else is an error. |
| `Max_Lines` | 100 | The longest span of real source shown for one chunk before it is shortened to a skeleton. |
| `DebugConfig` | false | Writes traces and logs to `.eulix/debug/`. |

### `[parser]`

| Key | Default | Meaning |
| --- | --- | --- |
| `threads` | 4 | Parser worker threads. |
| `prismVersion` | 2 | PRISM call-graph engine, `1` or `2`. See [PRISM v2](../libeulix/PRISM-V2.md). |
| `verbose` | true | Pass `--verbose` to the parser. |

### `[embeddings]`

| Key | Default | Meaning |
| --- | --- | --- |
| `model` | `BAAI/bge-base-en-v1.5` | Embedding model. |
| `dimension` | 768 | Vector size. |
| `engine` | `onnx` | `onnx` or `torch`. It must match between `analyze` and querying. |
| `debug` | false | Stream the embedder's log output. |

### `[llm]`

| Key | Default | Meaning |
| --- | --- | --- |
| `local` | true | If the provider is blank, `true` means Ollama. |
| `provider` | `ollama` | See [Llm](Llm.md). |
| `model` | `llama3.2:3b` | Model name. Required. |
| `api_key` | empty | Prefer environment variables or `.env`. |
| `baseURL`, `endpoint` | `http://localhost:11434`, empty | Override a provider's address or path. |
| `max_tokens` | 8192 | Total token budget for context, question and reply. |
| `temperature` | 0.2 | Clamped to 0 to 2. |

### `[retrievalConfig]`

| Key | Default | Meaning |
| --- | --- | --- |
| `code_to_ast_ratio` | 0.85 | Share of the context budget spent on real source rather than signatures. |
| `top_k_candidates` | 150 | Base number of candidates gathered; scaled up for big repositories. |
| `max_context_chunks` | 30 | Most chunks in the final context. |
| `mmr_diversity_factor` | 0.85 | 1.0 is pure relevance, 0.0 is maximum diversity. |
| `pre_mmr_score_floor_ratio` | 0.05 | Drop candidates scoring below this share of the best. |
| `semantic_min_similarity` | 0.15 | Smallest cosine similarity a vector hit may have. |
| `max_graph_expansion_depth` | 1 | Call-graph hops; `0` turns graph expansion off. |
| `max_graph_expansions` | 15 | Most call relationships followed. |
| `max_exact_anchors` | 2 | Most exact-symbol matches pinned. |
| `enable_subsystem_boosting` | true | Favour code in directories the question points at. |
| `apply_cross_root_isolation` | true | Demote code outside the main module. |
| `cross_root_penalty` | 0.32 | Score multiplier for that code. |
| `test_file_penalty` | 0.30 | Score multiplier for test files when the question is not about tests. |

`[retrievalConfig.ehnas]` adds `mmr_stop_floor`, `novelty_gain` (0.85), `mc_seed_min_score` (60), `mc_walkers` (256), `mc_seed_cap` (16), `common_symbol_df` (200) and `inverted_index_min_chunks` (5000). `[retrievalConfig.etiya]` accepts `lexical_exclude`, a list of paths the MCP text-search fallback skips. Both accept overrides for `pre_mmr_score_floor_ratio`, `mmr_diversity_factor` and `max_context_chunks`; ETIYA also accepts `semantic_min_similarity`. See [Ehnas Augmentation](query/retrieval/Ehnas-Augmentation.md).

### `[cache]` and `[checksum]`

| Key | Default | Meaning |
| --- | --- | --- |
| `cache.enable` | true | Keep a query history. |
| `cache.path` | `.eulix/history.db` | Where it lives. |
| `checksum.change_threshold` | 0.10 | Read and clamped, but not used by any command today. |
| `checksum.force_reanalyze_threshold` | 0.30 | Same; always kept at or above the first. |

## Usage Examples

A minimal file that switches the engine and the model:

```toml
[project]
engine = "ehnas"

[llm]
provider = "anthropic"
model = "claude-sonnet-5-5"
```

With `ANTHROPIC_API_KEY` set in your environment or `.env`, this is enough.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| `Project.Path` is absolute and written into the file. | The default is the directory where `init` ran. | Update it if you move the project. |
| `[checksum]` values have no effect. | `chat` uses fixed limits. | None. |
| Out-of-range values are clamped without a warning. | By design. | Check `eulix.toml` against this page. |
| A `# TODO` in the source calls the retrieval section unfinished. | More settings are expected. | Expect additions. |

## Related Docs

- [Overview](Overview.md): where configuration fits.
- [Engines](query/retrieval/Engines.md): what `engine` changes.
- [Llm](Llm.md): providers and keys.
