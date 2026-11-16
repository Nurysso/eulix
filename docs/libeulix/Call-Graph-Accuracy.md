# Call-Graph Accuracy

How often the call graph that `libeulix` builds is right, measured against each language's own compiler or type checker on one real project per language.

> **Crate / path:** `libeulix/src/analyze/call_graph/` (PRISM v1 and v2), measured with [`bench/callgraph`](../../bench/callgraph/README.md)  
> **Measured:** 2026-11-15, `eulix_parser` 0.9.0. "Baseline" is commit `01b1695`; "current" is `c294935` (which has the parser fixes listed in [What Changed](#what-changed-baseline--current)) plus the Go and Java receiver fix, [PRISM-5](../Known-Issues/Parser-And-PRISM.md#prism-5-go-and-java-receivers-never-reach-the-resolver-java-new-calls-never-resolve), not yet committed when this was written  
> **Benchmark checked:** yes, see [How The Benchmark Was Checked](#how-the-benchmark-was-checked); the numbers below are from the corrected harness  
> **Raw results:** `~/.Eulix/bench/callgraph/reports/2026-11-15-baseline-01b1695/` and `.../2026-11-15-fixes/` (before the PRISM-5 fix) on the machine that ran them; regenerate the current numbers with `make bench-callgraph`

## Overview

PRISM links each call to the function it probably means, from names, scopes and imports, without type checking (see [PRISM v2](PRISM-V2.md)). This page measures how good that guess is. For each language, a tool that really resolves calls (go/types, javac, jedi, rust-analyzer, the TypeScript checker, libclang) lists every call site in a pinned project and where it lands. The harness compares eulix's edges against that list.

**Short answer, PRISM v2 (the default):**

| Language | Precision | Recall | What to expect |
| --- | ---: | ---: | --- |
| C | 100% | 99.8% | Effectively exact for plain function calls. |
| Java | 100% | 97.3% | Close to exact: nearly every receiver has a declared type. |
| TypeScript | 100% | 90.3% | Exact for functions; half of the method calls are missed. |
| JavaScript | 91.5% | 82.8% | Functions are close to exact; some method calls on built-ins are linked wrongly. |
| Python | 98.2% | 74.0% | Function calls exact; calls on objects of unknown type are missed. |
| Go | 100% | 66.3% | Function calls exact; 39% of method calls, because a local assigned from a call has no declared type. |
| C++ | 90.0% | 56.0% | About half of all calls; overloads are confused. |
| Rust | 98.8% | 34.3% | Edges are almost always right, but most method calls are missed. |

**A v2 edge to a plain function is right 99–100% of the time in every language**, and v2 finds 80–100% of function calls everywhere except C++ (57%). Method calls are the weak part: v2 finds 96% of them in Java, and 25–76% elsewhere. [PRISM v1](PRISM-V1.md) finds more method calls in Go and Rust, but 53% and 71% of its method edges there are wrong.

Before the Go and Java receiver fix (PRISM-5), v2 linked no method call in either language: gin stood at 70.3% precision and 43.9% recall, and gson had no edges at all. See [The Go And Java Receiver Fix](#the-go-and-java-receiver-fix-prism-5).

## How It Is Measured

The [harness README](../../bench/callgraph/README.md) has the rules, and [Call-Graph Benchmark Scoring](../Architecture/Call-Graph-Scoring.md) explains each step with a worked example. In brief:

- **One fact per caller → callee pair.** v2 keeps one edge per pair (at the first call line), so a function calling `free` three times counts once. Per-call-site numbers are given separately in [Coverage And Edges](#coverage-and-edges).
- **TP:** the reference resolves a call into the project and eulix links the same caller to that callee, whichever of the caller's calls the edge's single line points at. **FN:** eulix has no such edge. **FP:** an eulix pair that is right for none of the caller's calls, judged by the call at its line: a project call there makes it a wrong target, a standard-library or dependency call a wrongly linked external one. **TN:** a call to the standard library or a dependency that eulix correctly leaves unlinked.
- **Precision** TP / (TP + FP). **Recall** TP / (TP + FN). **F1**. **Accuracy** (TP + TN) / everything scored. **Specificity** TN / (TN + FP); because wrong targets count as FPs it is not a pure "external calls" rate. **Strict precision** also counts as FPs the eulix pairs the reference cannot confirm (no reference call by that name at that line, or one the reference could not resolve).
- **Virtual calls:** a call through a project interface or base class accepts any implementation in the project (class-hierarchy analysis), so linking to one of them is a TP.
- **Not scored:** calls the reference cannot resolve (function values, missing dependencies), calls through a library interface or method that project types implement or override (the call may or may not run project code), calls outside any function eulix knows about, and calls to a function eulix has no node for (including unnamed functions, such as an arrow returned by a factory). Those are counted in [Coverage And Edges](#coverage-and-edges), because they cap what recall can mean.

### Corpora

| Corpus | Version | Language | Files | LOC | Scope | Reference tool |
| --- | --- | --- | ---: | ---: | --- | --- |
| gin | v1.12.0 | Go | 98 | 23,672 | whole repo | go/types through `golang.org/x/tools/go/packages` |
| gson | 2.15.0 | Java | 86 | 19,506 | `gson/src/main/java` | javac attribution (`JavacTask`) |
| flask | 3.1.3 | Python | 24 | 9,199 | `src/` | jedi 0.19.2 |
| ripgrep | 15.2.0 | Rust | 100 | 53,025 | whole repo | rust-analyzer LSIF |
| class-validator | 0.15.1 | TypeScript | 138 | 5,742 | `src/` | TypeScript 5.9.3 checker |
| axios | 1.21.1 | JavaScript | 70 | 9,560 | `lib/` | TypeScript 5.9.3 checker, `allowJs` |
| cJSON | 1.7.19 | C | 28 | 9,670 | repo minus `tests/unity`, `fuzzing` | libclang 23 |
| tinyxml2 | 11.0.0 | C++ | 68 | 10,184 | whole repo | libclang 23 |

Commits are pinned in `bench/callgraph/corpora.toml`; each snapshot was checked against the version string in its own sources. PHP is not measured: there is no PHP toolchain in the reference set.

## Results

Current parser, both PRISM versions, caller → callee pairs:

| Corpus | Language | PRISM | TP | FP | FN | TN | Precision | Recall | F1 | Accuracy | Specificity | Strict P |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| gin | Go | v2 | 1603 | 0 | 814 | 3194 | 100.0% | 66.3% | 79.8% | 85.5% | 100.0% | 100.0% |
| gin | Go | v1 | 1915 | 1168 | 502 | 2491 | 62.1% | 79.2% | 69.6% | 72.5% | 68.1% | 59.8% |
| gson | Java | v2 | 1059 | 0 | 29 | 755 | 100.0% | 97.3% | 98.7% | 98.4% | 100.0% | 98.3% |
| gson | Java | v1 | 678 | 360 | 410 | 561 | 65.3% | 62.3% | 63.8% | 61.7% | 60.9% | 47.3% |
| flask | Python | v2 | 165 | 3 | 58 | 487 | 98.2% | 74.0% | 84.4% | 91.4% | 99.4% | 94.8% |
| flask | Python | v1 | 167 | 99 | 56 | 416 | 62.8% | 74.9% | 68.3% | 79.0% | 80.8% | 50.8% |
| ripgrep | Rust | v2 | 1645 | 20 | 3153 | 3349 | 98.8% | 34.3% | 50.9% | 61.2% | 99.4% | 95.5% |
| ripgrep | Rust | v1 | 1781 | 3381 | 3017 | 2043 | 34.5% | 37.1% | 35.8% | 37.4% | 37.7% | 33.1% |
| class-validator | TypeScript | v2 | 364 | 0 | 39 | 119 | 100.0% | 90.3% | 94.9% | 92.5% | 100.0% | 100.0% |
| class-validator | TypeScript | v1 | 384 | 21 | 19 | 101 | 94.8% | 95.3% | 95.0% | 92.4% | 82.8% | 93.2% |
| axios | JavaScript | v2 | 270 | 25 | 56 | 409 | 91.5% | 82.8% | 87.0% | 89.3% | 94.2% | 84.9% |
| axios | JavaScript | v1 | 198 | 5 | 128 | 433 | 97.5% | 60.7% | 74.9% | 82.6% | 98.9% | 94.7% |
| cjson | C | v2 | 812 | 0 | 2 | 144 | 100.0% | 99.8% | 99.9% | 99.8% | 100.0% | 100.0% |
| cjson | C | v1 | 807 | 5 | 7 | 144 | 99.4% | 99.1% | 99.3% | 98.8% | 96.6% | 99.4% |
| tinyxml2 | C++ | v2 | 350 | 39 | 275 | 61 | 90.0% | 56.0% | 69.0% | 56.7% | 61.0% | 88.6% |
| tinyxml2 | C++ | v1 | 163 | 21 | 462 | 61 | 88.6% | 26.1% | 40.3% | 31.7% | 74.4% | 50.8% |

Accuracy is dominated by how many external calls a project makes (gin calls the standard library 5,051 times), so compare precision and recall, not accuracy, across projects.

### By Call Kind

PRISM v2, precision / recall, with the number of reference pairs in brackets. The last column is v1's method calls for comparison.

| Corpus | Language | Function calls | Method calls | Constructor calls | v1 method calls |
| --- | --- | ---: | ---: | ---: | ---: |
| gin | Go | 100.0% / 100.0% (1077) | 100.0% / 39.2% (1340) | – | 47.3% / 63.4% (1340) |
| gson | Java | 100.0% / 100.0% (200) | 100.0% / 96.2% (767) | 100.0% / 100.0% (121) | 59.9% / 66.0% (767) |
| flask | Python | 100.0% / 100.0% (46) | 97.2% / 67.3% (153) | 100.0% / 66.7% (24) | 58.8% / 83.0% (153) |
| ripgrep | Rust | 99.2% / 80.5% (781) | 98.6% / 25.3% (4017) | – | 29.3% / 33.0% (4017) |
| class-validator | TypeScript | 100.0% / 100.0% (343) | 100.0% / 47.7% (44) | – / 0.0% (16) | 66.1% / 93.2% (44) |
| axios | JavaScript | 100.0% / 96.6% (204) | 74.5% / 76.0% (96) | – / 0.0% (26) | – / 0.0% (96) |
| cjson | C | 100.0% / 99.8% (814) | – | – | – |
| tinyxml2 | C++ | 100.0% / 57.3% (75) | 88.1% / 55.3% (523) | 100.0% / 66.7% (27) | 84.8% / 22.4% (523) |

"Function" means a call the reference resolves to a free function or a static method; Java has no free functions, so its row counts static methods. A "–" precision means eulix made no edge of that kind.

## What Changed (Baseline → Current)

Fixes made on 2026-11-15 (details and guarding tests in [Fixed Issues](../Known-Issues/Fixed-Issues.md#parser-and-prism)):

1. Rust items inside inline `mod` blocks, including every `#[cfg(test)] mod tests`, are extracted (was PARSE-5).
2. Rust `impl` blocks in another file than their type keep their methods, bound to the type.
3. A Rust type imported from another crate no longer resolves to a homonym in that crate's `tests/` or in an unrelated crate; the imported crate name picks the package.
4. C++ headers named `.h` are parsed as C++, and `class EXPORT_MACRO Name` no longer hides the class (was PARSE-6).
5. C and C++ look inside `#if`/`#ifdef` blocks (include guards) and `extern "C" { ... }`.
6. Go and C calls never land on a struct type (part of PRISM-10).

PRISM v2, caller → callee pairs, both parsers scored by the same corrected harness. This table stops before the Go and Java receiver fix, which has its own section below; the gin and gson rows here are therefore not the current numbers:

| Corpus | Language | Correct pairs | Wrong pairs | Missed pairs | Precision | Recall | F1 |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| gin | Go | 1060 | 832 → **448** | 1357 | 56.0% → **70.3%** | 43.9% | 49.2% → **54.0%** |
| gson | Java | 0 | 0 | 1088 | – | 0.0% | – |
| flask | Python | 165 | 3 | 58 | 98.2% | 74.0% | 84.4% |
| ripgrep | Rust | 967 → **1645** | 6 → **20** | 1499 → **3153** | 99.4% → **98.8%** | 39.2% → **34.3%** | 56.2% → **50.9%** |
| class-validator | TypeScript | 364 | 0 | 39 | 100.0% | 90.3% | 94.9% |
| axios | JavaScript | 270 | 25 | 56 | 91.5% | 82.8% | 87.0% |
| cjson | C | 785 → **812** | 0 | 2 | 100.0% | 99.8% | 99.9% |
| tinyxml2 | C++ | 286 → **350** | 13 → **39** | 26 → **275** | 95.7% → **90.0%** | 91.7% → **56.0%** | 93.6% → **69.0%** |

**Read the ripgrep and tinyxml2 rows carefully.** Both found many more correct edges (+70% and +22%), yet recall fell. Before the fixes, thousands of calls sat inside functions eulix did not know about, so they were not scored at all. Now they are visible, and v2 cannot yet resolve most of them, so the denominator grew faster than the hits. The baseline percentages were measuring a smaller, easier slice of each project. The new tinyxml2 FPs are of the same kind: calls inside header methods that were invisible before, now linked to the wrong overload.

## The Go And Java Receiver Fix (PRISM-5)

Made on 2026-11-15, after the fixes above. The Go and Java parsers now pass the receiver of every call, the parser keeps the imports and the declared types of variables and fields until the call graph is built, and v2 resolves both languages through receivers typed from declarations (rules in [PRISM v2](PRISM-V2.md#go-and-java-receivers-typed-from-declarations)). A receiver with no declared type still gets no edge.

PRISM v2, caller → callee pairs, before → after:

| Corpus | Measure | Before | After |
| --- | --- | ---: | ---: |
| gin (Go) | Precision | 70.3% | **100.0%** |
| gin (Go) | Recall | 43.9% | **66.3%** |
| gin (Go) | F1 | 54.0% | **79.8%** |
| gin (Go) | Correct / wrong / missed pairs | 1060 / 448 / 1357 | **1603 / 0 / 814** |
| gin (Go) | Method calls, precision / recall | 0.0% / 0.0% | **100.0% / 39.2%** |
| gin (Go) | Function calls, precision / recall | 94.8% / 98.4% | **100.0% / 100.0%** |
| gson (Java) | Precision | – | **100.0%** |
| gson (Java) | Recall | 0.0% | **97.3%** |
| gson (Java) | F1 | – | **98.7%** |
| gson (Java) | Correct / wrong / missed pairs | 0 / 0 / 1088 | **1059 / 0 / 29** |
| gson (Java) | Method calls, precision / recall | – / 0.0% | **100.0% / 96.2%** |
| gson (Java) | Static-method calls, precision / recall | – / 0.0% | **100.0% / 100.0%** |
| gson (Java) | Constructor calls, precision / recall | – / 0.0% | **100.0% / 100.0%** |

- **The other six corpora did not move.** For flask, ripgrep, class-validator, axios, cJSON and tinyxml2, the parser output (all eight JSON files, PRISM v1 and v2) is byte-identical before and after, and so is every per-call-site verdict.
- **Every gin and gson edge v2 now makes is confirmed by the reference**, except 18 gson edges at an anonymous class (`new TypeAdapter<T>() { ... }`), which v2 links to the constructor or interface being extended and the reference names differently. They are counted as unverifiable: strict precision on gson is 98.3%.
- **What Go still misses.** Of the 814 method-call pairs v2 does not link on gin, about three quarters (624, by the nearest declaration of the receiver) are calls on a local assigned from a call, such as `router := New()` or `c, _ := CreateTestContext(w)`, most of them in tests. 46 are call chains (`engine().Run()`), 45 go through a field or package variable whose type is an interface or is not recorded (`c.Writer.Write`, `json.API.Marshal`), and the rest are names declared with two types in one function, or package-level variables. All of these are receivers without a declared type ([PRISM-3](../Known-Issues/Parser-And-PRISM.md#prism-3-calls-on-a-receiver-of-unknown-type-get-no-edge-in-v2)).
- **What Java still misses.** 29 pairs: calls on a call result (`getAsSingleElement().getAsFloat()`, `delegate().read(in)`), and calls made inside an anonymous class to a method it inherits.
- **v1 moved slightly** because the parser output changed: gin 1900 → 1915 correct pairs (a generic call `getTyped[string](c, key)` is now recorded as `getTyped`), gson 637 → 678 (a constructor call is recorded as `Foo`, not `new Foo`).

## Coverage And Edges

What the scores leave out, and how big the graph is. An arrow shows a change from the baseline; on the gin and gson rows of the second table it shows the change made by the Go and Java receiver fix.

| Corpus | Call sites | Project / external / unknown | Outside any eulix function | Callee has no eulix node | Shared ids (PARSE-8) |
| --- | ---: | ---: | ---: | ---: | ---: |
| gin | 8745 | 3498 / 5051 / 196 | 36 | 0 | 2 |
| gson | 3157 | 1701 / 1240 / 216 | 468 | 14 | 46 |
| flask | 1168 | 253 / 645 / 270 | 97 | 0 | 10 |
| ripgrep | 15066 | 9579 / 5430 / 57 | 7345 → **2370** | 30 → **0** | 17 → **20** |
| class-validator | 743 | 456 / 153 / 134 | 77 | 15 | 0 |
| axios | 2076 | 909 / 690 / 477 | 554 | 222 | 1 |
| cjson | 2331 | 1322 / 225 / 784 | 12 → **0** | 39 → **0** | 0 |
| tinyxml2 | 2167 | 2045 / 122 / 0 | 121 → **2** | 685 → **26** | 13 → **50** |

| Corpus | eulix nodes | v2 edges | v1 edges | Methods with a v2 caller | v2 per call site: P / R | Parser time, v2 (s) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| gin | 1495 | 1508 → **1603** | 4684 → 4699 | 0 → **176** / 471 | 70.2% / 30.3% → **100.0% / 45.9%** | 0.12 |
| gson | 821 | 0 → **1077** | 1883 → 2036 | 0 → **383** / 708 | – / 0.0% → **100.0% / 74.7%** | 0.05 |
| flask | 399 | 174 | 371 | 70 / 281 | 98.2% / 67.3% | 0.05 |
| ripgrep | 2686 → 3151 | 1722 | 7994 | 367 / 2060 | 98.8% / 22.3% | 0.15 |
| class-validator | 284 | 364 | 432 | 16 / 30 | 100.0% / 86.5% | 0.02 |
| axios | 266 | 318 | 267 | 4 / 49 | 91.2% / 63.6% | 0.05 |
| cjson | 373 → 378 | 812 | 1314 | 0 / 0 | 100.0% / 61.4% | 0.08 |
| tinyxml2 | 190 → 354 | 395 | 1302 | 0 / 144 | 87.6% / 16.9% | 0.11 |

- **Outside any eulix function** is the main coverage gap now. On ripgrep, 2,200 of the remaining 2,370 are in `tests/*.rs`, whose test functions are generated by the `rgtest!` macro, so their bodies belong to no function node; on gson, 468 are in anonymous classes and enum-constant bodies ([PARSE-7](../Known-Issues/Parser-And-PRISM.md#parse-7-java-methods-inside-anonymous-classes-are-not-extracted)); on axios, 554 are in function expressions that are neither declared nor bound to a name ([PARSE-9](../Known-Issues/Parser-And-PRISM.md#parse-9-javascript-functions-that-are-not-declared-or-bound-to-a-name-have-no-node)).
- **Unknown** calls are not scored. Most are dynamic: function values, untyped parameters, `current_app`-style proxies, or calls into dependencies that are not installed (`click` in flask; jest in class-validator). cJSON's 784 are the unity test macros, whose headers were removed from the corpus. gin's 101 and gson's 216 extra are calls through a library interface or method that project types implement (`err.Error()`, `list.get()`), which may or may not run project code.
- **Callee has no eulix node** includes axios's 178 calls to unnamed functions, mostly helpers that a factory returns (`const isFunction = typeOfTest('function')`): the call runs an arrow inside the factory, which has no node of its own.
- **Per call site** counts every call separately. Because v2 keeps one edge per caller → callee pair, its per-site recall is lower by design (cJSON: a test function calling `cJSON_Delete` five times has one edge).
- **Methods with a v2 caller** is the PRISM-2 symptom: the methods defined inside C++ class bodies get no incoming edge at all, and until the receiver fix neither did any Go or Java method. (C++ out-of-class definitions are `func_` nodes and do get callers, which is why tinyxml2's method calls score 55.3% recall here.)
- **Speed:** the whole parse plus call graph takes 0.02–0.15 s per project with 12 threads; the fixes added about 0.04 s on ripgrep, for 465 more nodes. No cross-language edges were produced on any corpus.

## Where The Errors Come From

The largest groups of PRISM v2 errors on each corpus, current parser. Counts are call sites.

| Corpus | Main false positives | Main misses | Issue |
| --- | --- | --- | --- |
| gin (Go) | none (before the receiver fix: `router.GET(...)` → the package-level `func GET` in `ginS` (89), `c.Bind` → `func Bind` (56), `router.Use` → `func Use` (51), `errors.New` → gin's `func New` (48)) | method calls on a local assigned from a call (`router := New(); router.GET(...)`, `c, _ := CreateTestContext(w); c.Set(...)`), call chains (`engine().Run()`), interface fields (`c.Writer.Write`) | [PRISM-3](../Known-Issues/Parser-And-PRISM.md#prism-3-calls-on-a-receiver-of-unknown-type-get-no-edge-in-v2) |
| gson (Java) | none | methods on a call result (`getAsSingleElement().getAsFloat()`, `delegate().read(in)`), calls inside anonymous classes to inherited methods | PRISM-3, [PARSE-7](../Known-Issues/Parser-And-PRISM.md#parse-7-java-methods-inside-anonymous-classes-are-not-extracted) |
| flask (Python) | `current_app.make_response(...)` linked to the module function of the same name; stdlib `json.dumps`/`json.loads` linked to flask's own (3 in all) | calls on objects of unknown type (`self.serializer.tag(...)`, `super().__init__(...)`), raising project exceptions (`NoAppException`) | [PRISM-3](../Known-Issues/Parser-And-PRISM.md#prism-3-calls-on-a-receiver-of-unknown-type-get-no-edge-in-v2) |
| ripgrep (Rust) | same-name functions in `#[cfg]` variants and tests (`empty`, `from_iter`) | method calls on locals of unknown type (`builder.build()`, `.path()`, `.line_number()`): 4,362 sites | PRISM-3 |
| class-validator (TS) | none | `new ValidationMetadata(...)`, methods on a call result (`getMetadataStorage().addValidationMetadata(...)`) | PRISM-3 |
| axios (JS) | `list.forEach` → `utils.forEach` (13), `regex.test` → a local `test` (7), `str.trim` → `utils.trim` (3) | `new AxiosError(...)` (31), static methods (`AxiosHeaders.from`) | [PRISM-11](../Known-Issues/Parser-And-PRISM.md#prism-11-javascript-calls-on-built-in-objects-link-to-project-functions-of-the-same-name) |
| cJSON (C) | none | 2 pairs | – |
| tinyxml2 (C++) | wrong overload or class: `attr->Value()` → `XMLNode::Value` (11), `Write(const char*)` vs `Write(const char*, size_t)` (9) | method calls through pointers of unknown type (`doc.FirstChildElement()`, `->Parse()`) | PRISM-2, [PARSE-8](../Known-Issues/Parser-And-PRISM.md#parse-8-overloads-and-same-name-definitions-in-one-file-share-one-id) |

The pattern is the same everywhere: **PRISM v2 is precise because it declines to guess the type of a receiver, and its recall is limited by exactly that.** Go and Java show both halves. Passing their receivers through to the resolver (PRISM-5) removed every wrong edge on gin and gson. Java, where almost every receiver has a declared type, went to 97% recall; Go, where locals are usually declared with `:=` from a call, stops at 66%.

## Micro Fixtures

Each language also has a fixture of a few lines under `bench/callgraph/micro/`, which runs in seconds and doubles as a check that the reference tools work. They show the same picture at a glance:

| Fixture | v1 precision / recall | v2 precision / recall | v2 found / missed |
| --- | ---: | ---: | ---: |
| Go | 100% / 100% | 100% / 100% | 4 / 0 |
| Java | 100% / 66.7% | 100% / 83.3% | 5 / 1 (the miss is `twice()` calling `area()`: both are declared on one source line of the fixture, so the harness cannot tell the callers apart) |
| Python | 100% / 75.0% | 100% / 75.0% | 3 / 1 |
| Rust | 100% / 100% | 100% / 71.4% | 5 / 2 |
| TypeScript | 100% / 80.0% | 100% / 40.0% | 2 / 3 |
| JavaScript | 100% / 20.0% | 100% / 20.0% | 1 / 4 |
| C | 100% / 100% | 100% / 100% | 2 / 0 |
| C++ | 100% / 83.3% | 100% / 33.3% | 2 / 4 |

## Which Version To Use

| You need | Use |
| --- | --- |
| Edges you can trust in Python, TypeScript, Rust, C, Go, Java | v2. Precision is 98–100%. |
| Java | v2: 97% of all calls at 100% precision. |
| More method calls in Go than v2's 39% | v1, and filter: it finds 63% of method calls, but 53% of its method edges are wrong. |
| JavaScript | v2: more correct edges than v1 (270 vs 198), at slightly lower precision. |
| C++ | v2: twice v1's correct edges (350 vs 163) at higher precision. |
| A complete list of callers of something | Neither alone. Combine the graph with a text search; v2 recall is 34–100% depending on language. |

## How The Benchmark Was Checked

The harness was audited on 2026-11-15, after the first write-up, and the numbers on this page are from the corrected version. The checks, and what they found:

| Check | Result |
| --- | --- |
| Corpora are the pinned releases | All 8 match their pinned commit and the version string in their own sources. |
| The baseline and current binaries are what they claim | The baseline misses a Rust inline-module function that the current parser extracts; parser output is identical between repeated runs (all 16 graphs). |
| A second, independent scorer | Written separately, with no line matching at all (pure caller → callee comparison). It agrees with the harness on every TP and FN in all 32 runs (8 corpora × 2 PRISM versions × 2 parsers). The only differences are a few FPs on calls the reference cannot resolve, where the line-free scorer guesses by name and the harness, correctly, calls them unverifiable. |
| Go reference vs. an independent tool | Compared with `golang.org/x/tools/cmd/callgraph -algo=static` on gin: 2,783 project calls in common, all to the same callee. The reference has 467 more, all inside function literals, which the static algorithm does not visit; the tool has a few calls to closures, which the reference deliberately leaves unscored. |
| Reading verdicts by hand | A fixed random sample of TPs, FNs, wrong targets, wrongly linked externals, TNs, unverifiable edges and v1 FPs on every corpus (about 140 calls) was checked against the source. The verdicts held, apart from the problems listed below. Nearby-line matches (multi-line builder chains) were checked the same way. |

What the audit fixed:

- **Scoring:** v2 stores one line per caller → callee pair. When that line held another call of the same name (another overload, or `errors.New` before `gin.New` in the same function), a correct pair was counted as a wrong target and a miss. Pairs are now correct if they are right for any of the caller's calls, and each wrong pair counts once. This mostly changed tinyxml2 (10 more correct pairs, 9 fewer wrong ones) and v1's FP counts.
- **Unnamed targets:** a call to an arrow returned by a factory was mapped onto the factory, because it starts on the same line. Such targets are now unmapped. axios v2 recall went from 73.0% to 82.8%.
- **Python reference:** jedi bundles typeshed stubs for some packages, flask among them, so a few calls into flask resolved to the stub and counted as external. They now resolve to the source.
- **TypeScript reference:** a call to an overloaded function resolved to an overload signature with no body and counted as unknown. It now resolves to the implementation.
- **Go reference:** `testdata/` packages are now loaded (39 call sites), and a method of a library interface called through a project interface that embeds it (`w.Flush()` on gin's `ResponseWriter`) now targets the project's implementations.
- **Go and Java references:** calls through a library interface or method that project types implement (`err.Error()`, `list.get()`) are now unknown instead of external, so linking them to a project implementation is no longer counted as a false positive.
- **C reference:** calls to functions that are only implicitly declared (macros whose headers are missing) are unknown, not project calls.

## Limitations Of The Measurement

- **The references are static too.** A virtual call lists every override in the project, so a v2 edge to any one of them is a TP; Rust calls through a trait bound resolve only to the trait's declaration. Dynamic calls (`getattr`, callbacks, function pointers) are "unknown" and not scored.
- **No dependencies are installed** for any corpus, so calls into libraries the reference cannot see come out "unknown" rather than "external"; TNs are a lower bound.
- **Line matching:** eulix records one line per edge. When the edge's line has no same-name call, a same-name call up to 15 lines below it in the same caller is used (the start of a multi-line builder chain); checked by hand on ripgrep, where v1 relies on it most.
- **One project per language** is a sample, not a population. The numbers describe these projects; the pattern (functions good, methods limited by receivers of unknown type) held on every fixture as well.

## Reproducing

```sh
make parser-release
make bench-callgraph                       # all real corpora, PRISM v1 and v2 (~3 min with cached references)
make bench-callgraph-micro                 # fixtures only
python3 bench/callgraph/cg_eval.py --parser /path/to/other/eulix_parser --out /tmp/other
```

The report (`report.md`), `results.json`, and one `<corpus>-v<N>-sites.jsonl` per run (every reference call with its bucket, expected targets and eulix's edges) are written to `~/.Eulix/bench/callgraph/reports/<timestamp>/`. The baseline numbers above were produced by building `eulix_parser` from `01b1695` in a separate worktree and passing it with `--parser`.

## Related Docs

- [PRISM v2](PRISM-V2.md) and [PRISM v1](PRISM-V1.md): the algorithms being measured.
- [Harness README](../../bench/callgraph/README.md): scoring rules and reference tools.
- [Call-Graph Benchmark Scoring](../Architecture/Call-Graph-Scoring.md): how each number is computed, step by step.
- [Parser And PRISM Issues](../Known-Issues/Parser-And-PRISM.md): the open problems behind the misses.
- [Fixed Issues](../Known-Issues/Fixed-Issues.md#parser-and-prism): what changed between baseline and current.
