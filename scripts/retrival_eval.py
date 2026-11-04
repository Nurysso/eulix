#!/usr/bin/env python3
"""Run a fixed query set against `eulix mcp` and dump the results as JSON.

Each query gets a fresh server so session memory cannot leak between queries.
Queries are grouped by the engine they were written to exercise; every group
runs against whichever engine eulix.toml selects.
Usage: python3 scripts/eval_mcp.py [out.json]   (default: .eulix/eval/<engine>_baseline.json)
"""
import json
import re
import subprocess
import sys
import tempfile
import tomllib
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BIN = ROOT / "build" / "eulix_linux_amd"
R = "eulix-cli/internal/query/retrieval/"

# group -> [(component, query, expected file prefixes)]
CASES = {}

# Written while developing EHNAS: mostly code vocabulary and symbol names.
CASES["ehnas"] = [
    ("go-cli", "How does applyBudget work", [R + "intent.go"]),
    ("go-cli", "How does the engine switch between etiya and ehnas", [R + "engine.go"]),
    ("go-cli", "How is the pre-MMR score floor applied to candidates", [R + "utils.go"]),
    ("go-cli", "How does structuralMMR measure redundancy between two chunks", [R + "mmr_ehnas.go"]),
    ("go-cli", "How are the seeds for the Monte Carlo walk selected", [R + "walk_diag.go"]),
    ("go-cli", "How are cross-language twin chunks found", [R + "twins.go"]),
    ("go-cli", "How is eulix.toml loaded and its values validated", ["eulix-cli/internal/config/config.go"]),
    ("go-cli", "How does pseudo relevance feedback expand the keyword query", [R + "prf.go"]),
    ("go-cli", "How does noveltyPack demote redundant chunks", [R + "ehnas_search.go"]),
    ("go-cli", "How are subsystems detected from the query", [R + "subsystem.go"]),
    ("go-cli", "How does the MCP server release the context builder after being idle", ["eulix-cli/internal/mcp/"]),
    ("go-cli", "How is source code hydrated to fit the token budget", [R + "source.go"]),
    ("rust-lsp", "How does the LSP server handle goto definition", ["eulix-lsp/src/backend.rs"]),
    ("rust-lsp", "How is a diagnostic created when a file fails to parse", ["eulix-lsp/src/diagnostics.rs"]),
    ("rust-lsp", "How does the language server answer hover requests", ["eulix-lsp/src/backend.rs"]),
    ("parser", "How does the parser spill intermediate results to disk", ["eulix-parser/src/spill.rs"]),
    ("libeulix", "How is the call graph built", ["libeulix/src/analyze/call_graph/"]),
    ("libeulix", "How does find_entry_points detect entry points and HTTP routes", ["libeulix/src/analyze/entry_points.rs"]),
    ("embed", "How does the ONNX embedder generate embeddings", ["eulix-embed/embedders/onnx_embed.py"]),
    ("embed", "How is a single file chunked for embedding", ["eulix-embed/chunking/chunker.py"]),
    # Names a 523-line function: exact matches must survive the long-chunk penalty (Fix 4).
    ("go-cli", "How does EhnasSearch work", [R + "ehnas_search.go"]),
]

# etiya adds query embeddings + vector search, so it should reach code that
# shares no words with the question. These describe behaviour in plain
# language and avoid the identifiers and terms used in the target code.
CASES["etiya"] = [
    ("go-cli", "Where does the tool find the secret token for talking to the language model provider",
     ["eulix-cli/internal/config/config.go"]),
    ("go-cli", "How does it notice that project files changed since the last indexing run",
     ["eulix-cli/internal/checksum/"]),
    ("go-cli", "How are vectors compressed to a single byte per dimension",
     [R + "loader.go", R + "mmap/"]),
    ("go-cli", "How are previous answers remembered so the same question is not asked twice",
     ["eulix-cli/internal/cache/"]),
    ("go-cli", "How are names that appear in nearly every file kept from affecting ranking",
     [R + "boilerPlate.go"]),
    ("go-cli", "How does reading vectors from disk avoid copying the whole file into memory",
     [R + "mmap/"]),
    ("go-cli", "How does nearest neighbour lookup avoid comparing the question against every vector",
     [R + "vector_ivf.go"]),
    ("go-cli", "How does it decide what kind of question the user asked",
     [R + "intent.go", "eulix-cli/internal/query/classifier/"]),
]

# "[1] path:10-20 (score 0.90)" for retrieval results, "### path:10-20" for the lexical fallback.
HEADER = re.compile(r"^(?:\[\d+\] |### )(\S+?):(\d+)-(\d+)(?: \(score ([\d.]+)\))?", re.M)
TRACE = re.compile(r'retrieval="(.*?)"')


def run(query):
    msgs = [
        {"jsonrpc": "2.0", "id": 1, "method": "initialize",
         "params": {"protocolVersion": "2024-11-05", "capabilities": {}, "clientInfo": {"name": "eval", "version": "0"}}},
        {"jsonrpc": "2.0", "method": "notifications/initialized"},
        {"jsonrpc": "2.0", "id": 2, "method": "tools/call",
         "params": {"name": "query_without_cot", "arguments": {"query": query}}},
    ]
    with tempfile.TemporaryFile("w+") as err:
        p = subprocess.Popen([BIN, "mcp", "--repo", ROOT], cwd=ROOT, text=True,
                             stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=err)
        p.stdin.write("".join(json.dumps(m) + "\n" for m in msgs))
        p.stdin.flush()
        for line in p.stdout:
            resp = json.loads(line)
            if resp.get("id") == 2:
                break
        else:
            raise RuntimeError(f"server exited without answering: {query}")
        p.stdin.close()
        p.wait(timeout=30)
        err.seek(0)
        trace = TRACE.search(err.read())
    result = resp["result"]
    text = "".join(c.get("text", "") for c in result["content"])
    if result.get("isError"):
        raise RuntimeError(f"tool error for {query!r}: {text}")
    return text, trace[1] if trace else None


def summary(rows):
    n = len(rows)
    return {"queries": n,
            "ranked_first": sum(r["rank"] == 1 for r in rows),
            "returned": sum(r["rank"] is not None for r in rows),
            "mrr": round(sum(1 / r["rank"] for r in rows if r["rank"]) / n, 3),
            "grep_fallback": sum("chosen=lexical" in (r["trace"] or "") for r in rows)}


engine = tomllib.loads((ROOT / "eulix.toml").read_text()).get("project", {}).get("engine") or "etiya"
groups = {}
for group, cases in CASES.items():
    print(f"== {group} queries (engine: {engine})")
    rows = []
    for component, query, expected in cases:
        text, trace = run(query)
        results = [{"id": f"{f}:{a}-{b}", "file": f, "lines": [int(a), int(b)], "importance": float(s) if s else None}
                   for f, a, b, s in HEADER.findall(text)]
        rank = next((i for i, r in enumerate(results, 1) if r["file"].startswith(tuple(expected))), None)
        rows.append({"component": component, "query": query, "expected": expected, "rank": rank,
                     "trace": trace, "results": results})
        print(f"{rank or '-':>3}  {query}")
    groups[group] = {"summary": summary(rows), "results": rows}

# Under .eulix/: the grep fallback skips hidden dirs, so results can't feed later queries.
dest = Path(sys.argv[1] if len(sys.argv) > 1 else ROOT / ".eulix" / "eval" / f"{engine}_baseline.json")
dest.parent.mkdir(parents=True, exist_ok=True)
dest.write_text(json.dumps({"engine": engine, "groups": groups}, indent=1) + "\n")
for group, g in groups.items():
    s = g["summary"]
    print(f"{group:6} Hit@1 {s['ranked_first']}/{s['queries']}  found {s['returned']}/{s['queries']}  "
          f"MRR {s['mrr']}  grep {s['grep_fallback']}")
print(f"-> {dest}")
