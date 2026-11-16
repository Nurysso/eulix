import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from lib.aggregate import by_language, combine_runs  # noqa: E402
from lib.score import metrics  # noqa: E402
from collections import Counter  # noqa: E402


def summary(tp, fp, fn, tn, unv=0):
    return metrics(Counter(TP=tp, FP=fp, FN=fn, TN=tn, unverifiable=unv))


def result(corpus, lang, prism, s, set_="real", runs=None):
    r = {"corpus": corpus, "lang": lang, "prism": prism, "set": set_, "score": {"summary": s}}
    if runs:
        r["runs"] = runs
    return r


class AggregateTest(unittest.TestCase):
    def test_identical_runs(self):
        s = summary(8, 2, 2, 5)
        runs = combine_runs([s, s, s], [0.1, 0.2, 0.3], ["h", "h", "h"])
        self.assertTrue(runs["identical_graphs"])
        self.assertEqual(runs["summary_mean"]["TP"], 8)
        self.assertIsInstance(runs["summary_mean"]["TP"], int)
        self.assertEqual(runs["summary_mean"]["precision"], 0.8)
        self.assertEqual(runs["summary_spread"]["recall"], 0)
        self.assertEqual(runs["parser_seconds_mean"], 0.2)

    def test_differing_runs_show_spread(self):
        runs = combine_runs([summary(8, 2, 2, 5), summary(6, 2, 4, 5)], [1, 1], ["a", "b"])
        self.assertFalse(runs["identical_graphs"])
        self.assertEqual(runs["summary_mean"]["TP"], 7)
        self.assertEqual(runs["summary_spread"]["TP"], 2)

    def test_language_mean_and_pooled(self):
        rs = [
            result("big", "go", 2, summary(90, 10, 0, 0)),   # P 0.9, R 1.0
            result("small", "go", 2, summary(1, 1, 1, 0)),   # P 0.5, R 0.5
            result("micro-go", "go", 2, summary(0, 9, 9, 0), set_="micro"),  # not a real corpus
            {"corpus": "broken", "lang": "go", "prism": 2, "set": "real", "error": "x"},
        ]
        (g,) = by_language(rs)
        self.assertEqual(g["repos"], ["big", "small"])
        self.assertEqual(g["mean_of_repos"]["precision"], 0.7)   # (0.9 + 0.5) / 2
        self.assertEqual(g["mean_of_repos"]["recall"], 0.75)     # (1.0 + 0.5) / 2
        self.assertEqual(g["pooled"]["TP"], 91)
        self.assertEqual(g["pooled"]["precision"], round(91 / 102, 4))
        self.assertEqual(g["min_max"]["recall"], (0.5, 1.0))

    def test_undefined_precision_left_out_of_mean(self):
        rs = [result("a", "java", 2, summary(0, 0, 5, 0)), result("b", "java", 2, summary(4, 1, 0, 0))]
        (g,) = by_language(rs)
        self.assertEqual(g["mean_of_repos"]["precision"], 0.8)
        self.assertEqual(g["mean_of_repos_n"]["precision"], 1)

    def test_run_means_stand_for_the_repo(self):
        s = summary(8, 2, 2, 5)
        runs = combine_runs([s, s], [1, 1], ["h", "h"])
        (g,) = by_language([result("a", "c", 1, summary(0, 0, 1, 0), runs=runs)])
        self.assertEqual(g["pooled"]["TP"], 8)


if __name__ == "__main__":
    unittest.main()
