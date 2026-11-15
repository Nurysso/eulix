import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from lib.score import Node, Site, score  # noqa: E402

F = "a.go"


def nodes():
    ns = [
        Node("func_main::a.go", "main", "function", F, 10, 20),
        Node("method_S_Save::a.go", "Save", "method", F, 1, 3),
        Node("func_Save::a.go", "Save", "function", F, 4, 5),
        Node("func_helper::a.go", "helper", "function", F, 6, 7),
        Node("class_S::a.go", "S", "class", F, 1, 3),
    ]
    return {n.id: n for n in ns}


def site(line, name, status="project", tline=None, kind="method", lines=()):
    t = [{"file": F, "line": tline, "name": name}] if tline else []
    return Site(F, line, name, kind, status, t, list(lines))


class ScoreTest(unittest.TestCase):
    def test_buckets(self):
        sites = [
            site(11, "Save", tline=1),                       # TP
            site(12, "helper", tline=6, kind="function"),   # FN
            site(13, "Save", tline=1),                       # wrong target (eulix -> func_Save)
            site(14, "Println", status="external"),          # TN
            site(15, "Exit", status="external"),             # FP_external
            site(16, "cb", status="unknown"),                # ignored
            site(30, "helper", tline=6),                     # outside any function
        ]
        edges = [
            ("func_main::a.go", "method_S_Save::a.go", 11),
            ("func_main::a.go", "func_Save::a.go", 13),
            ("func_main::a.go", "func_helper::a.go", 15),   # name mismatch -> unverifiable
            ("func_main::a.go", "func_helper::a.go", 18),   # no call there -> unverifiable
        ]
        sites[4].name = "helper"  # eulix linked an external `helper` call
        r = score(nodes(), edges, sites)
        s = r["per_call_site"]["summary"]
        self.assertEqual((s["TP"], s["FP"], s["FN"], s["TN"]), (1, 2, 2, 1))
        # pairs: both Save calls from main are one (main, S.Save) pair -> TP; func_Save is
        # a wrong pair; main -> func_helper is correct as a pair (line 12 calls it), even
        # though v2 recorded it at the external `helper` call on line 15 -> that stays TN.
        p = r["summary"]
        self.assertEqual((p["TP"], p["FP"], p["FN"], p["TN"]), (2, 1, 0, 2))
        self.assertEqual(r["buckets"]["wrong_target"], 1)
        self.assertEqual(r["buckets"]["FP_external"], 1)
        self.assertEqual(r["buckets"]["out_of_scope"], 1)
        self.assertEqual(s["unverifiable_edges"], 1)
        self.assertAlmostEqual(s["precision"], 1 / 3, places=3)
        self.assertAlmostEqual(s["recall"], 1 / 3, places=3)

    def test_duplicates_and_extra_targets(self):
        sites = [site(11, "Save", tline=1)]
        edges = [("func_main::a.go", "method_S_Save::a.go", 11)] * 2 + [("func_main::a.go", "func_Save::a.go", 11)]
        r = score(nodes(), edges, sites)
        self.assertEqual(r["eulix"]["duplicate_edges"], 1)
        self.assertEqual((r["summary"]["TP"], r["summary"]["FP"]), (1, 1))  # extra ambiguous target

    def test_multiline_call_matches_nearby_line(self):
        sites = [site(14, "Save", tline=1)]
        r = score(nodes(), [("func_main::a.go", "method_S_Save::a.go", 12)], sites)
        self.assertEqual(r["summary"]["TP"], 1)
        self.assertEqual(r["eulix"]["fuzzy_line_matches"], 1)

    def test_constructor_accepts_class(self):
        s = site(11, "S", tline=1, kind="constructor")
        r = score(nodes(), [("func_main::a.go", "class_S::a.go", 11)], [s])
        self.assertEqual(r["summary"]["TP"], 1)

    def test_unmapped_callee_not_scored(self):
        s = site(11, "Gone", tline=50)
        r = score(nodes(), [], [s])
        self.assertEqual(r["buckets"].get("unmapped_callee"), 1)
        self.assertEqual(r["summary"]["FN"], 0)

    def test_two_calls_same_name_same_line(self):
        sites = [site(11, "Save", tline=1), site(11, "Save", tline=1)]
        r = score(nodes(), [("func_main::a.go", "method_S_Save::a.go", 11)], sites)
        self.assertEqual((r["per_call_site"]["summary"]["TP"], r["per_call_site"]["summary"]["FN"]), (1, 1))
        self.assertEqual((r["summary"]["TP"], r["summary"]["FN"]), (1, 0))  # v2 keeps one edge per pair

    def test_pair_recorded_at_another_call_of_the_same_name_is_correct(self):
        # main calls A.f (line 11) and B.f (line 14); v2 keeps one edge per pair, at
        # the first line, and links B.f there. The pair (main, B.f) is still right.
        ns = nodes() | {
            "method_A_f::a.go": Node("method_A_f::a.go", "f", "method", F, 30, 31),
            "method_B_f::a.go": Node("method_B_f::a.go", "f", "method", F, 32, 33),
        }
        sites = [site(11, "f", tline=30), site(14, "f", tline=32)]
        r = score(ns, [("func_main::a.go", "method_B_f::a.go", 11)], sites)
        p = r["summary"]
        self.assertEqual((p["TP"], p["FP"], p["FN"]), (1, 0, 1))
        self.assertEqual(r["eulix"]["pairs_correct_for_another_call_of_the_caller"], 1)
        # per call site the same edge is still judged by its line
        self.assertEqual(r["per_call_site"]["summary"]["FP"], 1)

    def test_wrong_pair_counts_once_even_if_it_lands_on_two_groups(self):
        sites = [site(11, "Save", tline=1), site(13, "Save", tline=1, kind="function")]
        sites[1].targets = [{"file": F, "line": 4, "name": "Save"}]
        edges = [("func_main::a.go", "func_helper::a.go", 11), ("func_main::a.go", "func_helper::a.go", 13)]
        for e in sites:
            e.name = "helper"
        r = score(nodes(), edges, sites)
        self.assertEqual(r["summary"]["FP"], 1)

    def test_unnamed_target_is_not_mapped_to_the_function_on_its_line(self):
        # `const isFunction = typeOfTest('function')`: the call runs the arrow
        # that typeOfTest returns, which has no eulix node of its own.
        s = Site(F, 11, "isFunction", "function", "project", [{"file": F, "line": 6, "name": ""}])
        r = score(nodes(), [], [s])
        self.assertEqual(r["buckets"].get("unmapped_callee"), 1)
        self.assertEqual(r["summary"]["FN"], 0)

    def test_other_language_edges_dropped(self):
        ns = nodes() | {"func_x::doc.js": Node("func_x::doc.js", "x", "function", "doc.js", 1, 9)}
        r = score(ns, [("func_x::doc.js", "func_x::doc.js", 2)], [], lang="go")
        self.assertEqual(r["eulix"]["other_language_edges"], 1)
        self.assertEqual(r["summary"]["unverifiable_edges"], 0)

    def test_qualified_cpp_names_match(self):
        ns = {"func_A::run::a.cpp": Node("func_A::run::a.cpp", "A::run", "function", "a.cpp", 1, 5),
              "func_A::go::a.cpp": Node("func_A::go::a.cpp", "A::go", "function", "a.cpp", 7, 9)}
        s = Site("a.cpp", 8, "run", "method", "project", [{"file": "a.cpp", "line": 1, "name": "run"}])
        r = score(ns, [("func_A::go::a.cpp", "func_A::run::a.cpp", 8)], [s], lang="cpp")
        self.assertEqual(r["summary"]["TP"], 1)


if __name__ == "__main__":
    unittest.main()
