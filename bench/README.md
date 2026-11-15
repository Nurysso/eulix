# bench

Compares two eulix versions, from indexing speed to retrieval accuracy, on frozen copies of real code.

```sh
make bench-compare A=v0.8.3 B=HEAD                 # build, measure what's missing, report
make bench-compare A=v0.8.3 B=HEAD ARGS='--corpus eulix --groups retrieval'
make bench-run REV=.                               # measure the working tree (never cached)
make bench-list                                    # what has been measured
make bench-test                                    # harness unit tests
```

To time just the parser binary you have built, with no comparison and no embedding, use `scripts/parser_perf.sh -t 12 -p 2 <repo> [<repo>...]` (three cold runs per repo by default). The parser numbers in the READMEs come from it.

Results are saved in `~/.Eulix/bench`, keyed by commit, so a version is measured once. Reports go to `~/.Eulix/bench/reports/`.

| Path | What it holds |
| --- | --- |
| `eulix_bench.py` | Command-line entry |
| `lib/` | Build, sandbox, corpora, MCP client, result store, compare and report |
| `lib/groups/` | One module per measurement: `indexing`, `retrieval`, `query`, `parser` |
| `suite/corpora.toml` | The frozen corpora, pinned to commits |
| `suite/questions/` | Retrieval questions (`eulix.toml`) and latency-only questions per outside corpus |
| `suite/eulix.toml.tmpl` | The eulix config every version runs with |
| `suite/venv-cpu.txt` | CPU-only additions to the embedder's Python requirements |
| `tests/` | Unit tests (standard library `unittest`) |
| `results/` | Written-up comparisons: [v0.8.3 vs f9006ea](results/v0.8.3-vs-f9006ea.md) |

How it works, why, and the flag rules: [docs/Architecture/Benchmarking.md](../docs/Architecture/Benchmarking.md).
