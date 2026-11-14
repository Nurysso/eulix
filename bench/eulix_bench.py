#!/usr/bin/env python3
# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Benchmark eulix versions against each other, from speed to retrieval accuracy.

    python3 bench/eulix_bench.py build REV...           build once per commit (cached)
    python3 bench/eulix_bench.py run REV... [opts]      measure and save results (cached)
    python3 bench/eulix_bench.py compare A B [opts]     measure what's missing, then diff
    python3 bench/eulix_bench.py list                   what has been measured
    python3 bench/eulix_bench.py clean [--work ...]     free disk space
    python3 bench/eulix_bench.py exec REV -- ARGS...    run that version's eulix in its sandbox

REV is a tag, branch, SHA, or `.` for the working tree (never cached).
State lives in ~/.Eulix/bench. See docs/Architecture/Benchmarking.md.
"""
import argparse
import sys
from pathlib import Path

if sys.version_info < (3, 11):
    sys.exit("eulix_bench needs Python 3.11+ (tomllib)")

sys.path.insert(0, str(Path(__file__).resolve().parent))

import re  # noqa: E402
from datetime import datetime  # noqa: E402

from lib import build, compare, corpora, gitrev, machine, paths, report, runner, sandbox, store, suite  # noqa: E402
from lib.common import SUITE, H, BenchError, say, write_json  # noqa: E402
from lib.groups import indexing, parser, query, retrieval  # noqa: E402

GROUPS = ["indexing", "retrieval", "query", "parser"]


def selected_corpora(opts):
    return corpora.select(corpora.load_all(), opts.corpus, opts.large)


def measure(side: runner.Side, opts) -> list[dict]:
    """Run every requested group on every selected corpus, skipping what's cached."""
    docs = []
    groups = [g for g in GROUPS if g in opts.groups]
    with sandbox.InstallGuard():
        for c in selected_corpora(opts):
            todo = [g for g in groups if g in c.measures]
            if not todo:
                continue
            say(f"{side.rev.label} [{side.device}] on {c.label}: {', '.join(todo)}")
            questions = suite.load(c.questions)
            if "retrieval" in todo or "query" in todo:
                corpora.preflight(c, questions)
            for g in todo:
                if g == "indexing":
                    docs.append(indexing.run(side, c, opts.repeat or c.index_repeats or 3, opts.rerun))
                elif g == "retrieval":  # on a CPU-built index whatever the device (EMB-1)
                    docs.extend(retrieval.run(side.accuracy_side(), c, questions, opts.query_timeout, opts.rerun))
                elif g == "query":
                    docs.extend(query.run(side, c, questions, opts.query_timeout, opts.rerun))
                elif g == "parser":
                    docs.append(parser.run(side, c, opts.rerun))
            if c.tier == "large" and not opts.keep_work:
                corpora.remove_tree(corpora.work_dir(c, side.rev.sha12, side.device))
    return docs


def cmd_build(args) -> int:
    with paths.Lock():
        for spec in args.revs:
            rev = gitrev.resolve(spec)
            meta = build.ensure(rev, rebuild=args.rebuild)
            print(f"{rev.label}\t{rev.sha[:12]}\t{build.binary(rev)}\tengines={','.join(meta['capabilities']['engines'])}")
    return 0


def cmd_exec(args) -> int:
    rev = gitrev.resolve(args.rev)
    with paths.Lock():
        side = runner.Side(rev, runner.resolve_device(args.cpu))
        with sandbox.InstallGuard():
            return side.sandbox.run(*args.args).returncode


def cmd_run(args) -> int:
    with paths.Lock():
        device = runner.resolve_device(args.cpu)
        for spec in args.revs:
            side = runner.Side(gitrev.resolve(spec), device, rebuild=args.rebuild)
            measure(side, args)
    return 0


def cmd_compare(args) -> int:
    groups = [g for g in GROUPS if g in args.groups]
    with paths.Lock():
        device = runner.resolve_device(args.cpu)
        side_a = runner.Side(gitrev.resolve(args.a), device)
        side_b = runner.Side(gitrev.resolve(args.b), device)
        if not args.no_run:
            measure(side_a, args)
            measure(side_b, args)
        cells = compare.build_cells(side_a, side_b, selected_corpora(args), groups)
    warnings = compare.environment_warnings(cells)
    print(report.terminal(cells, side_a, side_b, warnings))
    suite_info = {"questions": H({p.name: p.read_text() for p in sorted((SUITE / "questions").glob("*.toml"))})[:8],
                  "corpora": H((SUITE / "corpora.toml").read_text())[:8]}
    stamp = datetime.now().strftime("%Y%m%d-%H%M%S")
    safe = lambda s: re.sub(r"[^A-Za-z0-9._-]+", "_", s)  # noqa: E731
    path = Path(args.report) if args.report else paths.sub(
        "reports", f"{safe(side_a.rev.label)}_vs_{safe(side_b.rev.label)}-{stamp}.md")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(report.markdown(cells, side_a, side_b, warnings, suite_info))
    if args.json:
        write_json(path.with_suffix(".json"), [
            {"group": c.group, "corpus": c.corpus, "engine": c.engine, "status": c.status, "note": c.note,
             "flags": c.flags, "movers": c.movers,
             "rows": [{"name": r.name, "unit": r.unit, "a": r.a, "b": r.b, "delta": r.delta, "flag": r.flag}
                      for r in c.rows]} for c in cells])
    say(f"report: {path}")
    return compare.verdict(cells)


def cmd_list(args) -> int:
    fp = {machine.fingerprint(False), machine.fingerprint(True)}
    rows = []
    for d in store.scan():
        if args.rev and not d["sha"].startswith(gitrev.resolve(args.rev).sha):
            continue
        samples = max((len(m.get("samples", [])) for m in d["metrics"]), default=0)
        cover = ""
        if d["group"] == "retrieval":
            cover = f"{len(d['details']['questions'])} q"
        mfp = d["key_fields"].get("machine_fp")
        rows.append((d["label"], d["sha"][:12], d["group"], d["corpus"]["name"], d.get("engine") or "",
                     d["device"], "" if mfp is None else ("this" if mfp in fp else "other"),
                     str(samples or ""), cover, d.get("created", "")[:16]))
    head = ("label", "sha", "group", "corpus", "engine", "device", "machine", "samples", "coverage", "created")
    rows.sort(key=lambda r: (r[9], r[0], r[2], r[3], r[4]))
    widths = [max(len(x) for x in col) for col in zip(head, *rows)]
    for r in [head, *rows]:
        print("  ".join(x.ljust(w) for x, w in zip(r, widths)).rstrip())
    return 0


def cmd_clean(args) -> int:
    targets = []
    if args.work or args.all:
        targets.append(paths.sub("work"))
    if args.builds is not None or args.all:
        if args.builds:
            for spec in args.builds:
                rev = gitrev.resolve(spec)
                targets += [paths.sub("builds", rev.sha), *paths.sub("homes").glob(f"{rev.sha12}*")]
        else:
            targets += [paths.sub("builds"), paths.sub("homes")]
    if args.corpora or args.all:
        targets.append(paths.sub("corpora"))
    if args.venvs or args.all:
        targets.append(paths.sub("venvs"))
    if args.logs or args.all:
        targets.append(paths.sub("logs"))
    if args.results:
        if not args.yes:
            raise BenchError("--results deletes every saved measurement; add --yes to confirm")
        targets.append(paths.sub("results"))
    if not targets:
        raise BenchError("nothing selected; see clean --help")
    with paths.Lock():
        for t in targets:
            if not t.exists():
                continue
            say(f"{'would remove' if args.dry_run else 'removing'} {t}")
            if not args.dry_run:
                corpora.remove_tree(t)
    return 0


def add_measure_options(p):
    p.add_argument("--groups", type=lambda s: [g.strip() for g in s.split(",") if g.strip()], default=GROUPS,
                   help=f"comma-separated subset of {','.join(GROUPS)}")
    p.add_argument("--corpus", action="append", help="limit to a corpus (repeatable)")
    p.add_argument("--repeat", type=int, help="indexing runs per corpus (default: corpus setting or 3)")
    p.add_argument("--rerun", action="store_true", help="ignore cached results and measure again")
    p.add_argument("--large", action="store_true", help="include large-tier corpora (Linux kernel)")
    p.add_argument("--cpu", action="store_true",
                   help="embed on CPU (the fallback; default is the GPU through your eulix venv)")
    p.add_argument("--keep-work", action="store_true", help="keep large-tier work dirs after the run")
    p.add_argument("--query-timeout", type=float, default=120, help="seconds per MCP call")


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(prog="eulix_bench", description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    sp = ap.add_subparsers(dest="cmd", required=True)

    p = sp.add_parser("build", help="build revisions (cached per commit)")
    p.add_argument("revs", nargs="+")
    p.add_argument("--rebuild", action="store_true", help="ignore the cached build")
    p.set_defaults(fn=cmd_build)

    p = sp.add_parser("run", help="measure revisions and save the results")
    p.add_argument("revs", nargs="+")
    p.add_argument("--rebuild", action="store_true", help="ignore the cached build")
    add_measure_options(p)
    p.set_defaults(fn=cmd_run)

    p = sp.add_parser("compare", help="measure what's missing for A and B, then report the differences")
    p.add_argument("a")
    p.add_argument("b")
    add_measure_options(p)
    p.add_argument("--no-run", action="store_true", help="only use saved results; missing cells exit 2")
    p.add_argument("--report", help="Markdown report path (default: ~/.Eulix/bench/reports/...)")
    p.add_argument("--json", action="store_true", help="also write the comparison as JSON next to the report")
    p.set_defaults(fn=cmd_compare)

    p = sp.add_parser("list", help="show saved results")
    p.add_argument("--rev", help="only this revision")
    p.set_defaults(fn=cmd_list)

    p = sp.add_parser("clean", help="free disk space")
    p.add_argument("--work", action="store_true", help="per-version corpus copies and their indexes")
    p.add_argument("--builds", nargs="*", metavar="REV", help="built binaries and sandboxes (all, or these revs)")
    p.add_argument("--corpora", action="store_true", help="fetched corpus snapshots")
    p.add_argument("--venvs", action="store_true", help="benchmark Python environments")
    p.add_argument("--logs", action="store_true", help="raw command output")
    p.add_argument("--all", action="store_true", help="everything above (not results)")
    p.add_argument("--results", action="store_true", help="saved measurements (needs --yes)")
    p.add_argument("--yes", action="store_true")
    p.add_argument("--dry-run", action="store_true")
    p.set_defaults(fn=cmd_clean)

    p = sp.add_parser("exec", help="run a version's eulix inside its sandbox")
    p.add_argument("rev")
    p.add_argument("args", nargs=argparse.REMAINDER, help="arguments for eulix (after --)")
    p.add_argument("--cpu", action="store_true", help="use the CPU-only venv instead of your eulix venv")
    p.set_defaults(fn=cmd_exec)

    args = ap.parse_args(argv)
    bad = [g for g in getattr(args, "groups", []) if g not in GROUPS]
    if bad:
        ap.error(f"unknown group(s) {', '.join(bad)}; choose from {', '.join(GROUPS)}")
    if getattr(args, "args", None) and args.args[0] == "--":
        args.args = args.args[1:]
    try:
        return args.fn(args)
    except BenchError as e:
        say(f"error: {e}")
        return 2
    except KeyboardInterrupt:
        say("interrupted")
        return 130


if __name__ == "__main__":
    sys.exit(main())
