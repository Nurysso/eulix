#!/usr/bin/env python3
"""Call-graph accuracy harness for eulix_parser.

Builds the call graph of one pinned corpus per language with eulix_parser and
scores it against call sites resolved by that language's own tooling (go/types,
javac, jedi, rust-analyzer, the TypeScript checker, libclang).

    python3 bench/callgraph/cg_eval.py                       # real corpora, PRISM v1 and v2
    python3 bench/callgraph/cg_eval.py --set micro           # tiny fixtures, seconds
    python3 bench/callgraph/cg_eval.py --lang go,java --prism 2
    python3 bench/callgraph/cg_eval.py --corpus gin --examples 40 --json
    python3 bench/callgraph/cg_eval.py --runs 3              # build each graph 3 times, report means

Results are also rolled up per language (real corpora only): the mean of the
per-project numbers, and the pooled counts of all projects of that language.

State lives in ~/.Eulix/bench/callgraph (snapshots, cached reference output,
reports). Exit code: 0 ok, 1 a corpus failed, 2 usage error.
"""

from __future__ import annotations

import argparse
import hashlib
import datetime as dt
import json
import os
import subprocess
import sys
from collections import Counter
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from lib import corpus as corpora  # noqa: E402
from lib import aggregate, eulix, refs, report  # noqa: E402
from lib.score import Site, score  # noqa: E402

REPO = Path(__file__).resolve().parents[2]
DEFAULT_PARSER = REPO / "eulix-parser" / "target" / "release" / "eulix_parser"


def log(msg: str) -> None:
    print(msg, file=sys.stderr, flush=True)


def load_sites(path: Path) -> list[Site]:
    with open(path) as fh:
        return [Site.from_json(json.loads(l)) for l in fh if l.strip()]


def source_line(root: Path, file: str, line: int, cache: dict) -> str:
    if file not in cache:
        try:
            cache[file] = (root / file).read_text(encoding="utf-8", errors="replace").splitlines()
        except OSError:
            cache[file] = []
    lines = cache[file]
    return lines[line - 1].strip()[:160] if 0 < line <= len(lines) else ""


def examples(sites: list[Site], unverifiable: list[dict], nodes, root: Path, n: int) -> tuple[dict, dict]:
    cache: dict = {}
    out, totals = {}, Counter()
    for key in ("FN", "wrong_target", "FP_external"):
        picked = [s for s in sites if s.bucket == key]
        totals[key] = len(picked)
        rows = []
        for s in picked[:n]:
            exp = ", ".join(sorted(s.expected)) or "–"
            got = ", ".join(s.hit) or "no edge"
            detail = {"FN": f"expected → {exp}", "wrong_target": f"eulix → {got}; expected → {exp}",
                      "FP_external": f"external call, eulix → {got}"}[key]
            rows.append({"where": f"{s.file}:{s.line}", "name": s.name, "kind": s.kind, "caller": s.caller,
                         "detail": detail, "src": source_line(root, s.file, s.line, cache)})
        out[key] = rows
    totals["unverifiable"] = len(unverifiable)
    rows = []
    for u in unverifiable[:n]:
        f = nodes[u["from"]].file if u["from"] in nodes else "?"
        rows.append({"where": f"{f}:{u['line']}", "name": u["to"], "detail": u["why"],
                     "src": source_line(root, f, u["line"], cache)})
    out["unverifiable"] = rows
    return out, dict(totals)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--set", choices=["real", "micro", "all"], default="real")
    ap.add_argument("--lang", help="comma list: go,java,python,rust,ts,js,c,cpp")
    ap.add_argument("--corpus", help="comma list of corpus names")
    ap.add_argument("--prism", default="1,2", help="PRISM versions to score (default 1,2)")
    ap.add_argument("--parser", type=Path, default=DEFAULT_PARSER)
    ap.add_argument("--threads", type=int, default=os.cpu_count() or 4)
    ap.add_argument("--runs", type=int, default=1,
                    help="build and score each graph this many times; report means and check the graphs are identical")
    ap.add_argument("--refresh-ref", action="store_true", help="ignore cached reference output")
    ap.add_argument("--examples", type=int, default=15, help="examples per bucket in the report")
    ap.add_argument("--home", type=Path, default=Path(os.environ.get("EULIX_CG_HOME", Path.home() / ".Eulix" / "bench" / "callgraph")))
    ap.add_argument("--out", type=Path, help="report directory (default <home>/reports/<timestamp>)")
    ap.add_argument("--json", action="store_true", help="print results.json to stdout instead of the table")
    a = ap.parse_args()

    if not a.parser.exists():
        log(f"parser not found: {a.parser}  (build it: make parser-release, or pass --parser)")
        return 2
    prisms = [int(x) for x in a.prism.split(",")]
    sel = corpora.load()
    if a.set != "all":
        sel = [c for c in sel if c["set"] == a.set]
    if a.lang:
        langs = set(a.lang.split(","))
        sel = [c for c in sel if c["lang"] in langs]
    if a.corpus:
        names = set(a.corpus.split(","))
        sel = [c for c in sel if c["name"] in names]
    if not sel:
        log("no corpus selected")
        return 2

    started = dt.datetime.now().strftime("%Y%m%d-%H%M%S")
    out = a.out or a.home / "reports" / started
    out.mkdir(parents=True, exist_ok=True)
    version = subprocess.run([str(a.parser), "--version"], capture_output=True, text=True).stdout.strip()
    if a.runs < 1:
        log("--runs must be at least 1")
        return 2
    meta = {"parser": str(a.parser), "parser_version": version, "started": started, "prism": prisms,
            "runs": a.runs}

    results, failed = [], 0
    for c in sel:
        log(f"[{c['name']}] {c['lang']}")
        try:
            root = corpora.snapshot(c, a.home, log)
            refdir = a.home / "refs"
            refdir.mkdir(parents=True, exist_ok=True)
            ref_file = refdir / f"{corpora.key(c)}-{c['lang']}-{refs.tool_hash(c['lang'])}.jsonl"
            if c["source"] == "micro" or a.refresh_ref or not ref_file.exists():
                ref_meta = refs.run(c["lang"], root, ref_file, a.home, c["ref_args"], log)
                (ref_file.with_suffix(".meta.json")).write_text(json.dumps(ref_meta))
            else:
                ref_meta = json.loads(ref_file.with_suffix(".meta.json").read_text()) | {"cached": True}
        except Exception as e:  # noqa: BLE001
            log(f"  FAILED: {e}")
            failed += 1
            for v in prisms:
                results.append({"corpus": c["name"], "lang": c["lang"], "set": c["set"], "prism": v, "error": str(e)})
            continue
        for v in prisms:
            try:
                # Run 1 keeps the full detail (examples, sites file); the
                # others only contribute their summary, time and graph hash.
                summaries, seconds, hashes = [], [], []
                for i in range(1, a.runs + 1):
                    run_dir = a.home / "runs" / corpora.key(c) / (f"v{v}" if a.runs == 1 else f"v{v}-r{i}")
                    pm = eulix.run_parser(a.parser, root, run_dir, v, a.threads)
                    run_nodes, run_edges = eulix.load(run_dir)
                    run_sites = load_sites(ref_file)
                    run_sc = score(run_nodes, run_edges, run_sites, c["lang"])
                    summaries.append(run_sc["summary"])
                    seconds.append(pm["seconds"])
                    hashes.append(hashlib.sha256(eulix._kbfb().call_graph_path(run_dir).read_bytes()).hexdigest())
                    if i == 1:
                        pmeta, nodes, sites, sc = pm, run_nodes, run_sites, run_sc
                runs = aggregate.combine_runs(summaries, seconds, hashes) if a.runs > 1 else None
                if runs and not runs["identical_graphs"]:
                    log(f"  v{v}: WARNING the {a.runs} graphs differ; see runs.summary_spread in results.json")
                unv = sc.pop("_unverifiable")
                ex, totals = examples(sites, unv, nodes, root, a.examples)
                s = sc["summary"]
                log(f"  v{v}: P {report.pct(s['precision'])}  R {report.pct(s['recall'])}  F1 {report.pct(s['f1'])}"
                    f"  (TP {s['TP']} FP {s['FP']} FN {s['FN']} TN {s['TN']})")
                (out / f"{c['name']}-v{v}-sites.jsonl").write_text("".join(
                    json.dumps({"file": x.file, "line": x.line, "name": x.name, "kind": x.kind, "status": x.status,
                                "bucket": x.bucket, "caller": x.caller, "expected": sorted(x.expected), "eulix": x.hit}) + "\n"
                    for x in sites))
                results.append({"corpus": c["name"], "lang": c["lang"], "set": c["set"], "prism": v,
                                "commit": c.get("commit"), "tag": c.get("tag"), "subdir": c["subdir"],
                                "parser": pmeta, "ref": ref_meta, "score": sc, "examples": ex,
                                "example_totals": totals, **({"runs": runs} if runs else {})})
            except Exception as e:  # noqa: BLE001
                log(f"  v{v} FAILED: {e}")
                failed += 1
                results.append({"corpus": c["name"], "lang": c["lang"], "set": c["set"], "prism": v, "error": str(e)})

    langs = aggregate.by_language(results)
    doc = {"meta": meta, "results": results, "by_language": langs}
    (out / "results.json").write_text(json.dumps(doc, indent=1))
    md = report.write(results, meta, out, langs)
    if a.json:
        print(json.dumps(doc, indent=1))
    else:
        print(report.text_table(report.HEAD, report.summary_rows(results)))
        if langs:
            print("\nper language (real corpora): mean of repos, then pooled counts")
            print(report.text_table(report.LANG_HEAD, report.language_rows(langs)))
        if a.runs > 1:
            bad = [r for r in results if r.get("runs") and not r["runs"]["identical_graphs"]]
            print(f"\n{a.runs} runs per graph: " + ("all graphs identical across runs" if not bad else
                  "graphs DIFFER for " + ", ".join(f"{r['corpus']} v{r['prism']}" for r in bad)))
        for r in results:
            if "error" in r:
                print(f"ERROR {r['corpus']} v{r['prism']}: {r['error']}")
    log(f"\nreport:  {md}\nresults: {out / 'results.json'}  (per-site buckets: {out}/<corpus>-v<N>-sites.jsonl)")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
