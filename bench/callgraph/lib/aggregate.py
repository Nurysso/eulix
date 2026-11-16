"""Combine scored runs: the repeats of one corpus, and the corpora of one language.

Repeats. `--runs N` builds each graph N times. PRISM is deterministic, so the N
graphs should be byte-identical and every metric the same; `combine_runs`
records whether they were, the mean and the spread (max - min) of every summary
number, and the parser times. A non-zero spread means the output changed
between runs, which is a bug in itself.

Languages. `by_language` rolls the real corpora of one language up two ways:

  mean of repos   each project weighs the same: the mean of the per-project
                  precision, recall and F1 (a project whose value is undefined,
                  e.g. precision with no edge at all, is left out of that mean)
  pooled          the counts of all projects summed, then the metrics computed
                  once, so a large project weighs more
"""

from __future__ import annotations

from collections import Counter, defaultdict

from .score import metrics

COUNTS = ("TP", "FP", "FN", "TN", "TP_edges", "unseen", "unverifiable_edges")
RATES = ("precision", "recall", "recall_all", "f1", "accuracy", "specificity", "strict_precision")


ROLLED = ("precision", "recall", "recall_all", "f1")


def _mean(xs):
    xs = [x for x in xs if x is not None]
    return round(sum(xs) / len(xs), 4) if xs else None


def _mean_count(xs):
    """A count averaged over runs; stays an integer when the runs agree."""
    m = _mean(xs)
    return int(m) if m is not None and float(m).is_integer() else m


def _spread(xs):
    xs = [x for x in xs if x is not None]
    return round(max(xs) - min(xs), 4) if xs else None


def combine_runs(summaries: list[dict], parser_seconds: list[float], graph_hashes: list[str]) -> dict:
    """One corpus and PRISM version, N repeated runs."""
    keys = COUNTS + RATES
    return {
        "n": len(summaries),
        "identical_graphs": len(set(graph_hashes)) == 1,
        "graph_sha256": graph_hashes,
        "parser_seconds": parser_seconds,
        "parser_seconds_mean": _mean(parser_seconds),
        "summary_mean": {k: (_mean_count if k in COUNTS else _mean)([s.get(k) for s in summaries]) for k in keys},
        "summary_spread": {k: _spread([s.get(k) for s in summaries]) for k in keys},
    }


def repo_summary(r: dict) -> dict:
    """The summary that stands for one corpus: the mean over runs when there were several."""
    runs = r.get("runs")
    return runs["summary_mean"] if runs else r["score"]["summary"]


def by_language(results: list[dict]) -> list[dict]:
    """Real corpora only, grouped by (language, PRISM version), in first-seen order."""
    groups: dict[tuple, list[dict]] = defaultdict(list)
    for r in results:
        if "error" in r or r.get("set", "real") != "real":
            continue
        groups[(r["lang"], r["prism"])].append(r)
    out = []
    for (lang, prism), rs in groups.items():
        sums = [repo_summary(r) for r in rs]
        pooled = Counter()
        for s in sums:
            for k in COUNTS:
                pooled["unverifiable" if k == "unverifiable_edges" else k] += s.get(k) or 0
        out.append({
            "lang": lang,
            "prism": prism,
            "repos": [r["corpus"] for r in rs],
            "mean_of_repos": {k: _mean([s.get(k) for s in sums]) for k in ROLLED},
            "mean_of_repos_n": {k: sum(s.get(k) is not None for s in sums) for k in ROLLED},
            "min_max": {k: (min(v), max(v)) if (v := [s.get(k) for s in sums if s.get(k) is not None]) else None
                        for k in ROLLED},
            "pooled": metrics(pooled),
        })
    return out
