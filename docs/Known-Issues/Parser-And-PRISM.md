# Parser And PRISM Issues

Known problems in `libeulix` and `eulix-parser`: how files are parsed, and how PRISM links calls to definitions.

> **Scope:** `libeulix`, `eulix-parser`  
> **Last verified against:** `ad744e9` (2026-11-03); PRISM-10, PRISM-11, PARSE-7 and PARSE-8 against `5ecd16d` (2026-11-15); PRISM-2, PRISM-5 and PRISM-10 re-measured on 2026-11-15 with the uncommitted PRISM-5 fix on top of `c294935`; PRISM-12, PRISM-13 and the 2026-11-16 additions to PARSE-3 and PARSE-9 against `c294935` plus the uncommitted `prism-go-java-receivers` branch

Entries marked *Reproduced* were checked on 2026-11-03 by building the parser from this commit and parsing a small multi-language project with `--prism 2` (and `--prism 1` for comparison). The test project had a Python class hierarchy, a Go type with methods, a Java class, a C++ struct, a JavaScript subclass, a Rust `impl` and a TypeScript class. Read [Overview](Overview.md) for the legend.

PRISM-10, PRISM-11, PARSE-7 and PARSE-8 were found on 2026-11-15 by the call-graph accuracy harness ([bench/callgraph](../../bench/callgraph/README.md)), which scores PRISM against each language's own tooling on one real project per language. Each was then reproduced on `5ecd16d` with a minimal project of a few lines, given in its entry.

## What the test project showed

For orientation, here is what PRISM v2 resolved and did not:

| Call in the test project | Language | v2 result |
| --- | --- | --- |
| `self.validate()` to a method of a parent class in another directory | Python | Edge to the parent's method. Fixed. |
| `this.step()` and `new A().run()` | TypeScript | Edges. Fixed. |
| `self.inner()` and `P::new()` | Rust | Edges. Fixed. |
| `m.Save()`, and `m.load()` inside a method | Go | No edge on 2026-11-03. Fixed 2026-11-15 (PRISM-5). |
| `baz()` inside a method, `new Foo()`, `f.bar()` | Java | No edge on 2026-11-03. Fixed 2026-11-15 (PRISM-5). |
| `s.get()` | C++ | **No edge.** |
| `this.hello()` to a parent's method | JavaScript | **No edge**, and no inheritance edge either. |
| `helper()` in Go with the only `helper` defined in a Python file | Go | Edge to the Python function on 2026-11-03. No edge since the PRISM-5 fix; other language pairs are still open (PRISM-4). |
| `f.save()` where `f` has no visible type | Python | No edge (by design, PRISM-3). |

PRISM v1, run on the same files, linked the Go, Java and C++ method calls, but also linked a TypeScript function to an unrelated Python function.

## Measured on real code

The accuracy harness on `5ecd16d`, the parser before the fixes of 2026-11-15, scored by the harness as corrected after its audit (see [How The Benchmark Was Checked](../libeulix/Call-Graph-Accuracy.md#how-the-benchmark-was-checked)). Each count is a unique caller → callee pair, which is what v2 stores. Precision = correct edges / edges eulix made; recall = calls the language's own tooling resolves into the project that eulix links correctly. Current numbers, after the fixes, are in [Call-Graph Accuracy](../libeulix/Call-Graph-Accuracy.md).

| Corpus | Language | v2 precision | v2 recall | v2 F1 | v1 precision | v1 recall |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| gin 1.12.0 | Go | 56.0% | 43.9% | 49.2% | 61.9% | 78.6% |
| gson 2.15.0 | Java | – | 0.0% | – | 64.0% | 58.6% |
| flask 3.1.3 | Python | 98.2% | 74.0% | 84.4% | 62.8% | 74.9% |
| ripgrep 15.2.0 | Rust | 99.4% | 39.2% | 56.2% | 41.2% | 55.7% |
| class-validator 0.15.1 | TypeScript | 100.0% | 90.3% | 94.9% | 94.8% | 95.3% |
| axios 1.21.1 | JavaScript | 91.5% | 82.8% | 87.0% | 97.5% | 60.7% |
| cJSON 1.7.19 | C | 100.0% | 99.8% | 99.9% | 99.4% | 99.1% |
| tinyxml2 11.0.0 | C++ | 95.7% | 91.7% | 93.6% | 90.0% | 8.6% |

Recall only counts calls made from inside a function eulix knows about, and to a function eulix knows about. At the time, Rust inline modules and C/C++ preprocessor blocks hid whole groups of functions (on ripgrep, 7,345 of 15,066 call sites were inside functions eulix never extracted), so the real coverage was lower than these numbers. Those, and several other gaps found the same day, are now fixed (see [Fixed Issues](Fixed-Issues.md#parser-and-prism)); this table is the baseline they were measured against. Reproduce current numbers with `make bench-callgraph`.

---

## PRISM-1: Call graphs are approximate

**Severity:** Medium · **Status:** By design · **Verification:** Carried over

PRISM infers calls from names, scopes and imports. It is not a compiler front end, so edges can be missing, extra, or pointing at the wrong target when names are ambiguous. Treat edges as navigation evidence, not proof. See [PRISM v1](../libeulix/PRISM-V1.md) and [PRISM v2](../libeulix/PRISM-V2.md).

The earlier maintainer measurement on a 64,460-file Linux-kernel-sized checkout (12 threads, same input to both versions):

| Version | Edges | Analysis time | Peak memory | Character |
| --- | ---: | ---: | ---: | --- |
| v1 | 2,093,881 | 2.79 s | 4,622.8 MB | More edges, more over-approximation. |
| v2 | 1,510,341 | 1.34 s | 2,727.6 MB | About 28% fewer, better-qualified edges; about 41% less memory. |

I did not repeat this measurement. **Guidance:** use v2 (the default) for precision and inheritance awareness. Use v1 only when you want the largest possible edge set and will filter it yourself. Be aware of PRISM-2 below: in C and C++, v1 currently finds method calls that v2 misses.

---

## PRISM-2: v2 never links calls to methods in Go, Java, JavaScript, C and C++

**Severity:** High · **Status:** Partly fixed (Go and Java fixed 2026-11-15; JavaScript, C and C++ open) · **Verification:** Reproduced

**Symptom.** In JavaScript, C and C++, a call to a method gets no edge, and the method's `called_by` list is empty. Examples from the test project: C++ `s.get()`, JavaScript `this.hello()`. `this.hello()` and `c.greet()` (on `const c = new Child()`) get no edge from v1 either, so the workaround below does not help for JavaScript.

**Go and Java are fixed.** Until 2026-11-15 the same was true there: v2 linked 0 of 1,340 method-call pairs in gin, and no Java call at all (every Java function is a method, static ones included, so gson had 0 of 1,088 pairs). Both parsers now pass the receiver ([PRISM-5](#prism-5-go-and-java-receivers-never-reach-the-resolver-java-new-calls-never-resolve)), and v2 indexes their methods by declared name and types the receiver from declarations (see [PRISM v2](../libeulix/PRISM-V2.md#go-and-java-receivers-typed-from-declarations)). Measured with the accuracy harness after the fix:

| Corpus | Method-call pairs linked | Method-call precision | All pairs: precision / recall |
| --- | ---: | ---: | ---: |
| gin (Go) | 526 of 1,340 (39.2%) | 100.0% | 100.0% / 66.3% |
| gson (Java) | 738 of 767 (96.2%) | 100.0% | 100.0% / 97.3% |

What Go still misses is by design: receivers whose type no declaration states, mostly locals assigned from a call (`router := New()`, `c, _ := CreateTestContext(w)`), which is [PRISM-3](#prism-3-calls-on-a-receiver-of-unknown-type-get-no-edge-in-v2). Guarded by `libeulix/tests/go_java_receivers.rs`.

**Cause (JavaScript, C, C++).** The parsers write method ids as `method_{Class}_{name}::{path}`. For languages without receiver-aware resolution, v2 still indexes a method under the name taken from its id, which is `{Class}_{name}` and which no call site is spelled as. Methods are indexed by their declared name only for the languages that keep a separate receiver on each call: Python, TypeScript and Rust (commit `95f1288`), and now Go and Java. PHP has always had its own rules.

**Workaround.** For method-heavy C or C++ code, use `--prism 1`. It links method calls by first-match name lookup (with the false positives described in [PRISM v1](../libeulix/PRISM-V1.md)).

**Fix direction.** Have the JavaScript, C and C++ parsers pass the receiver, as Go and Java now do, then index their methods by declared name.

---

## PRISM-3: Calls on a receiver of unknown type get no edge in v2

**Severity:** Medium · **Status:** By design · **Verification:** Reproduced

**Symptom.** In Python, TypeScript, Rust, Go and Java, `f.save()` links nowhere when `f` is a parameter or a variable whose type PRISM cannot see. Go and Java state most types in the source, so there this means a local assigned from a call (`r := New()`, `var x = make()`), a range variable, a call chain (`a.b().c()`), or a name declared twice with two types in one function; a variable or field with a declared type does resolve. On gin, about three quarters of the 814 method-call pairs v2 misses are calls on a local assigned from a call. In the test project, `go(f): f.save()` and `run(): f = Form(); f.save()` both got no edge to `Form.save`. Calls on `self`, `this` and `Self`, and on a type named directly (`P::new()`), do resolve.

**Cause.** PRISM has no type inference. A commit made for precision (`02f3674`) turned off guessing "the only method with this name" for unknown receivers, because it produced wrong edges.

**Workaround.** None needed if you accept missing edges. v1 will link these calls by name, with the usual risk of a wrong target.

---

## PRISM-4: Calls resolve across languages

**Severity:** High · **Status:** Open · **Verification:** Reproduced

**Symptom.** v2 keeps PHP apart from everything else, but puts all other languages in one name space. In the test project, a Go function that calls `helper()` was linked to a Python function named `helper`, with no Go `helper` anywhere. (Since the PRISM-5 fix of 2026-11-15, calls made *from* Go and Java files are looked up among declarations of their own language only, so that example no longer produces an edge. Calls from Python, TypeScript, Rust, JavaScript, C and C++ are unchanged.) v1 does the same, and in one case linked a TypeScript function to a Python one.

An earlier measurement on the Eulix repository itself found 125 of 1,932 call edges (6.5%) crossing languages, none of which could be real because the repository has no foreign-function calls between its languages. For example, a Rust call to `max` linked to a Go function. I did not repeat that count.

**Cause.** `Resolver::pick` only skips a candidate when its file and the caller's file differ in being PHP. There is no check for other languages.

**Workaround.** None. Filter edges whose two ends have different languages (the earlier document included a short script for counting them).

**Fix direction.** Compare language groups instead of only PHP-ness when choosing candidates. Treat JavaScript with TypeScript, and C with C++, as one group each.

---

## PRISM-5: Go and Java receivers never reach the resolver; Java `new` calls never resolve

**Severity:** Medium · **Status:** Fixed (2026-11-15, not yet committed) · **Verification:** Reproduced, then re-measured with the accuracy harness

**What was wrong.** Python, TypeScript and Rust parsers store the receiver of a call in its own field. Go and Java stored it in `defined_in` instead (`m.Save()` was recorded as callee `Save` with `defined_in = "m"`), and v2 does not read `defined_in`, so the resolver never saw `m`. `eulix-parser`'s slim step then cleared `defined_in`, the imports of both languages, and every declared type (parameter types, local variables, struct fields), so nothing was left to say what `m` or `fmt` is. Java constructor calls were recorded as `new Foo`, and only the PHP path removed the `new `.

**False edges in Go (added 2026-11-10).** The same gap also created wrong edges, because calls were resolved by their bare name. In a small module, `os.Exit(1)` linked to a local `func Exit`, `sub.Helper()` linked to a local `Helper` instead of the one in `sub/`, and `s.Close()` linked to a top-level `func Close` instead of `(*Store).Close`. On gin this produced 448 wrong caller → callee pairs (see PRISM-10).

**The fix.**

- The Go and Java parsers fill `receiver` and leave `defined_in` empty, as the other parsers do. A Java `new Foo<T>(...)` is recorded as callee `Foo`.
- The slim step keeps, for Go and Java only: the imports, the name and declared type of every variable of a function, the name and type of every field, and the name and type of a Go method's receiver.
- v2 resolves Go and Java through `prism_v2/receivers/typed.rs`: a Go import maps to one package directory through the module path in `go.mod`, a Java import to the file of its class; a receiver is typed from its declaration; an unknown receiver gets no edge. The rules are in [PRISM v2](../libeulix/PRISM-V2.md#go-and-java-receivers-typed-from-declarations).

Three parser bugs found on the way are fixed with it: Go struct fields were never extracted (the field list was read from a `body` field the grammar does not have), an alias in a grouped Go import (`import ( x "a/b" )`) was dropped, and a generic call `f[T](a, b)` was recorded under the name `f[T]`.

**Result.** gin: precision 70.3% → 100.0%, recall 43.9% → 66.3%. gson: no edges → precision 100.0%, recall 97.3%. Details in [Call-Graph Accuracy](../libeulix/Call-Graph-Accuracy.md). Guarded by `libeulix/tests/go_java_receivers.rs`, the `slimming_keeps_what_*_receivers_need` tests in `eulix-parser/src/parse/method_calls_tests.rs`, and `go_calls_resolve_through_receivers_and_the_module_path` in `eulix-parser/src/parse/file.rs`.

**Still open.** JavaScript keeps the qualified text (`this.hello`) in the callee, which the older path cannot use; that is the JavaScript part of PRISM-2. The `eulix-lsp` qualifier for Go and Java call sites is now read from `receiver`.

---

## PRISM-6: JavaScript classes record no base class

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced

**Symptom.** `class Child extends Base` produces a class with an empty `bases` list and no inheritance edge. TypeScript is unaffected.

**Cause.** The JavaScript parser looks for a `superclass` field that the tree-sitter JavaScript grammar does not have. The base class is in the `class_heritage` child.

**Fix direction.** Read `class_heritage`, as the TypeScript parser does.

---

## PRISM-7: C++ calls are recorded twice

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced

**Symptom.** The C++ parser records `s.get()` as two calls, `get` and `s.get`, and `ns::f()` as `f` and `ns::f`. Any consumer of a function's `calls` sees every C++ call doubled. In the test project, v2 still produced a single edge for `ns::f()`, because it removes duplicate caller-to-target pairs, so the damage is mainly to data that reads `calls` directly. I did not measure the effect on the lookup indices.

**Cause.** A fallback chain in the C++ parser's `resolve_callee` never runs, so both spellings are emitted.

**Fix direction.** Emit one call per call site.

---

## PRISM-8: v2 is very slow on large Rust workspaces with shared type names

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced

**Symptom.** With `--prism 2`, a Rust workspace in which many crates each define a type with the same name (`Error`, `Config`, `Builder`) and use each other's through `use` can spend seconds to minutes in the call-graph phase. The output is correct; only the time grows, quadratically: doubling either the number of crates or their size multiplies the time by about four.

I ran `scripts/check_rust_workspace_perf.sh` on this commit (each crate defines `Error` and calls a sibling's `Error::new()`):

| Workspace | Time on 2026-11-03 | Earlier measurement at `dcc1633` | Same workload before the receiver work (`32f62f0`) |
| --- | ---: | ---: | ---: |
| 40 crates × 80 modules | 2.60 s | 2.64 s | 0.07 s |
| 80 crates × 160 modules | 42.05 s | 42.7 s | 0.20 s |

In these runs no edges came out of the extra work, because the candidate count exceeds `max_ambiguous`.

**Cause.** `use other_crate::Type` binds the type, and every `Type::f()` call site calls `rust_type_outside_crate` (`libeulix/src/analyze/call_graph/prism_v2/receivers/rust_paths.rs`). For each project node with that name it recomputes both crate roots and walks the crate's module graph twice. Nothing is cached across call sites, so the cost is call sites × same-named types × crate size.

**Not affected:** `--prism 1`, other languages with `--prism 2`, paths rooted at `std`, `core`, `alloc` or `proc_macro`, and types defined only once.

**Workaround.** Use `--prism 1`, or `--no-analyze`.

**Fix direction.** Compute each Rust file's crate once, before resolution, so the filter becomes an integer comparison per candidate. Keep the lib/main and `tests/`/`examples/` distinctions. Add a test that bounds the many-crate case so this does not return.

---

## PRISM-9: PHP call graph is partial for dynamic code

**Severity:** Low · **Status:** Limitation · **Verification:** Carried over

PHP is resolved by PHP's own rules wherever the code states them, and a call is left unresolved rather than guessed. What cannot be resolved:

- classes the project does not declare (built-ins, an excluded `vendor/`), and runtime aliases made with `class_alias()`;
- include paths built at runtime with no literal file name (`$page . '.php'`); paths with a literal tail are matched by their trailing segments, and ties resolve to nothing;
- method calls on a receiver whose class is not visible: they link only when the project defines exactly one method of that name;
- return types across files (`$repo->find()->save()`), `$fn()`, `call_user_func('name')`, `new $class`, `$obj->$method()`, methods inherited from outside the project, and `parent::` inside traits.

A class that lacks the called method resolves to its `__call` or `__callStatic`; on Laravel this makes `Facade::__callStatic` a hub for every facade call. Files must be UTF-8, so a Latin-1 PHP file fails to parse (other languages replace bad bytes and report the file as partial; see [Parsing Pipeline](../eulix-parser/Parsing-Pipeline.md)).

The earlier measurement on WordPress 7.2-alpha (1,885 files) reported that 99.86% of resolved function, `new` and static calls pointed at the correct code, and 99.96% of calls to project functions resolved. I did not repeat it.

---

## PRISM-10: Go calls link to same-named struct types and to look-alike functions

**Severity:** High · **Status:** Fixed (2026-11-15; the second part not yet committed) · **Verification:** Reproduced (2026-11-15, `5ecd16d`), then re-measured

**Fixed, part 1.** Calls from Go (and C) files no longer land on struct types: on gin, v2's false caller → callee pairs fell from 832 to 448 and precision rose from 56.0% to 70.3%. Guarded by `go_calls_never_land_on_a_struct_of_the_same_name` and `c_calls_never_land_on_a_struct_of_the_same_name` in `libeulix/tests/method_precision.rs`.

**Fixed, part 2.** The look-alike functions went with [PRISM-5](#prism-5-go-and-java-receivers-never-reach-the-resolver-java-new-calls-never-resolve): `router.GET(...)` no longer links to the package-level `func GET` in `ginS` (89 call sites), nor `errors.New` to gin's `func New` (48). A Go call now needs a receiver of known type, a package that maps to a project directory, or a function of the caller's own package. On gin the false pairs fell from 448 to 0, and precision is 100.0%. Guarded by `go_standard_library_call_gets_no_edge_even_with_a_local_homonym`, `go_package_call_resolves_inside_the_imported_directory` and `go_bare_call_never_reaches_a_method_or_another_package` in `libeulix/tests/go_java_receivers.rs`.

The rest of this entry describes the problem as it was.

**Symptom (before the fixes).** In Go, v2 linked a method call to a **struct** that has the method's name, and links a call into another package to a project function that only shares its name. On gin, v2 made 832 wrong caller → callee pairs next to 1,060 right ones, so 44% of its Go edges were wrong. The largest groups: `err.Error()` on the built-in `error` interface linked to gin's `struct_Error` (118 call sites, plus 13 calls to gin's own `c.Error`), `String()` calls linked to the `render.String` struct (166), and `errors.New`, `reflect.New` and `validator.New` linked to gin's own `func New` (48). Minimal reproduction:

```go
// render/json.go
package render
type JSON struct{ Data any }

// ctx.go
package demo
import "net/http"
type Ctx struct{}
func (c *Ctx) JSON(code int) {}
func Dir(s string) string { return s }
func handle(c *Ctx) {
	c.JSON(200)        // v2: edge to struct_JSON::render/json.go, none to Ctx.JSON
	_ = http.Dir("/tmp") // v2: edge to the local func Dir (this is a type conversion in net/http)
}
```

**Cause.** `bare_name` (`libeulix/src/analyze/call_graph/prism_v2.rs`) indexes struct ids by their bare name, so `struct_JSON::render/json.go` is a candidate for any call spelled `JSON`. Go methods are indexed as `Ctx_JSON` (PRISM-2) and Go receivers and imports never reach the resolver (PRISM-5), so for `c.JSON(...)` the struct is the only candidate left, and for `http.Dir(...)` the local `Dir` wins.

**Fix.** Type nodes are no longer offered as call targets for Go and C, and the receiver and the package qualifier now reach the resolver (PRISM-5), which rules out the look-alikes. In the reproduction above, `c.JSON(200)` now links to `Ctx.JSON` and `http.Dir("/tmp")` gets no edge.

---

## PRISM-11: JavaScript calls on built-in objects link to project functions of the same name

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced (2026-11-15, `5ecd16d`)

**Symptom.** A method call on an array, string, regular expression or function links to any project function with that name. On axios, most of v2's 25 false caller → callee pairs are of this kind: `list.forEach(...)` to `utils.forEach` (13 call sites), `regex.test(...)` to a `test` function in `adapters/fetch.js` (7), `str.trim()` to `utils.trim` (3). v1 links these less often. Minimal reproduction:

```js
// utils.js
export function forEach(obj, fn) { for (const k in obj) fn(obj[k]); }
// app.js
export function run(list) { list.forEach(x => x); }   // v2: edge run -> utils.forEach
```

**Cause.** Not traced. The call has a receiver (`list`) whose type PRISM cannot see; in Python, TypeScript and Rust such calls get no edge by design (PRISM-3), but on the JavaScript path the bare name `forEach` still resolves.

**Fix direction.** Treat JavaScript like TypeScript: a call on a receiver that is not `this`, a class, or an imported module binding gets no edge.

---

## PRISM-12: TypeScript ignores declared types; `new` calls never link

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced (2026-11-16, `c294935` plus the uncommitted `prism-go-java-receivers` branch)

**Symptom.** In TypeScript, a method call gets an edge only when the receiver's class is visible from the shape of the call: `this.m()`, `Cls.m()`, `new Cls().m()`, or an imported class. A receiver whose type is *written down* still gets no edge: a typed local, a local initialised with `new`, a typed parameter. The instance creation `new Cls()` gets no edge either, to the constructor or to the class. On the harness corpora v2 stays at 100% precision in TypeScript, but recall is 90.3% on class-validator, 82.3% on immer and 39.1% on class-transformer (v1: 87.0%). The class-transformer misses that were inspected are of exactly this kind: `const executor = new TransformOperationExecutor(...); executor.transform(...)`, `const classTransformer: ClassTransformer = new ClassTransformer(); classTransformer.plainToInstance(...)`, and calls on an exported instance (`defaultMetadataStorage.findExposeMetadata(...)`). Constructor-call recall is 0 of 16 pairs on class-validator. Minimal reproduction:

```ts
export class Store { save(): number { return 1; } }
export function typedLocal()        { const s: Store = new Store(); return s.save(); }  // no edge
export function newLocal()          { const s = new Store(); return s.save(); }         // no edge
export function typedParam(s: Store) { return s.save(); }                                 // no edge
export function direct()            { return new Store().save(); }                        // edge to Store.save
// and no edge for any of the `new Store()` calls themselves
```

**Cause.** The TypeScript path classifies a receiver from syntax alone (PRISM-3). The parser does not pass variable and parameter types for TypeScript, and v2 does not read them. Go and Java do both since the PRISM-5 fix ([Go and Java: receivers typed from declarations](../libeulix/PRISM-V2.md#go-and-java-receivers-typed-from-declarations)). Why `new Cls()` itself gets no edge was not traced.

**Fix direction.** Do for TypeScript what was done for Go and Java: have the parser keep declared types of parameters, locals and class fields (and the class named by a `new` initialiser), and let v2 type the receiver from them. Link `new Cls()` to the constructor, or to the class when it declares none. `any`, unions and inferred types stay unknown, so precision should not drop.

---

## PRISM-13: C++ member calls on library objects link to project methods of the same name

**Severity:** High · **Status:** Open · **Verification:** Reproduced (2026-11-16, `c294935` plus the uncommitted `prism-go-java-receivers` branch)

**Symptom.** In C++, a member call on a standard-library object (`s.size()` on a `std::string`, `v.empty()`, `it.begin()`) links to any project method of that name that is defined outside its class body. On yaml-cpp, v2 precision is 66.1% with 288 wrong caller → callee pairs: 149 call sites are library calls linked to project methods (`m_data.size()` to `Node::size`), and 123 of the 139 wrong-target sites are calls on one project type linked to another type's method of the same name (`node_data::size` expected, `Node::size` linked). tinyxml2 (90.0%) and pugixml (100.0%) are affected far less; why was not measured. Minimal reproduction:

```cpp
#include <string>
struct Node { int size() const; bool empty() const; };
int Node::size() const { return 0; }
bool Node::empty() const { return true; }
int count(const std::string& s) { return s.size(); }   // v2: edge count -> Node::size
bool none(const std::string& s) { return s.empty(); }  // v2: edge none -> Node::empty
```

**Cause.** C++ is still name-based (PRISM-2): the parser does not pass the receiver, so `s.size()` reaches v2 as the bare name `size`. A method defined outside its class body (`int Node::size() const {...}`) is recorded as a function, `func_Node::size`, and is found by that bare name. This is the C++ form of [PRISM-11](#prism-11-javascript-calls-on-built-in-objects-link-to-project-functions-of-the-same-name).

**Workaround.** None in v2. v1 makes the same mistake.

**Fix direction.** Short term: when a C++ call has a receiver, do not let its bare name reach functions recorded from out-of-class definitions; this trades these false edges for missing ones, as in the other receiver-aware languages. Long term: pass the receiver and the declared types, as for Go and Java. C++ is harder than either: `auto`, templates, overloads and operator calls leave many receivers without a written type.

---

## PARSE-1: Unsupported languages

**Severity:** Medium · **Status:** Limitation · **Verification:** Read in code

Only C, C++, Go, Java, JavaScript, PHP, Python, Rust and TypeScript are parsed. Ruby, C# and others are not, despite a Ruby row in an old README table (removed). Additional languages are planned but not scheduled.

---

## PARSE-2: `.jsx` and `.tsx` files are rejected

**Severity:** High · **Status:** Open · **Verification:** Reproduced

**Symptom.** Parsing a `.tsx` file fails with `Unsupported language: Tsx`. The parser run records the file as failed, so React code is missing from the knowledge base. `.jsx` takes the same path.

**Cause.** The language detector knows `Jsx` and `Tsx`, but the dispatch in `grammar_files::parse_source` has no arm for them, so they fall through.

**Fix direction.** Dispatch `Jsx` to the JavaScript parser and `Tsx` to the TypeScript parser (using a TSX-capable grammar), then add a test for each extension.

---

## PARSE-3: C and C++ files parse only partly

**Severity:** Low · **Status:** By design · **Verification:** Carried over

On real production C and C++ code, files almost always parse partially, because of preprocessor macros that cannot be expanded without a C preprocessor, which is out of scope. Macro-defined variables are skipped on purpose. On a Linux-kernel-sized checkout, the earlier run had zero failed files, with partial parses almost all in C. Partial files stay in the knowledge base and their symbols remain usable. The partial count is shown at the end of each run, and `--parse-report` lists the files.

**Functions written as a macro call have no node.** A test case written as `TEST_XML(name, "<node/>") { ... }` is parsed as a call followed by a block, so the calls in its body have no caller and are lost. On pugixml, 14,321 of 20,568 call sites lie outside any function eulix knows: 11,967 in test files written this way, and 2,354 in `src/pugixml.cpp`, which were not traced (a plain macro prefix such as `PUGI_IMPL_FN int f()` and a namespace opened by a macro are both handled). Reproduced on 2026-11-16 with `#define TEST_XML(name, xml) void test_##name()` followed by `TEST_XML(a, "<x/>") { g(); }`: no node, no edge to `g`. A macro without a string argument, `TEST(one) { g(); }`, becomes a function named `TEST`.

**Every `#ifdef` branch is in the graph.** The parser does not choose a configuration, so a call in each branch of an `#ifdef` / `#else` gets an edge. The graph is the union over all configurations, which is what code navigation usually wants, but a compiler building one configuration sees only one of the calls. A macro that expands to nothing in a normal build (`lua_assert(f(x))`) still yields an edge to `f`. Neither kind of edge can be confirmed by the accuracy harness, which compiles one configuration with libclang, so they count as unverifiable and lower strict precision only: on zlib, 60 unverifiable edges, mostly in the `DYNAMIC_CRC_TABLE` / `MAKECRCH` blocks of `crc32.c` (precision 97.6%, strict 80.9%); on lua, 369, mostly inside `lua_assert(...)` (precision 100.0%, strict 88.1%). Reproduced on 2026-11-16:

```c
int fast(void) { return 1; }
int slow(void) { return 2; }
int pick(void) {
#ifdef USE_FAST
    return fast();     // edge pick -> fast
#else
    return slow();     // edge pick -> slow as well
#endif
}
```

---

## PARSE-4: `dependency_graph` is never filled; some Cargo features do nothing

**Severity:** Low · **Status:** Open · **Verification:** Read in code

The knowledge base has a `dependency_graph` field, but neither `libeulix` nor `eulix-parser` populates it; it is always empty. The `libeulix` Cargo features `secure` and `system` are declared but nothing in the library source uses them. The `main.rs` header comment of `eulix-parser` lists four old artifact names. None of these affects output; they mislead readers. See [Knowledge Base Schema](../libeulix/Knowledge-Base-Schema.md).

---

## PARSE-7: Java methods inside anonymous classes are not extracted

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced (2026-11-15, `5ecd16d`)

**Symptom.** Methods in an anonymous class body (`new Runnable() { public void run() { ... } }`) get no node, so the calls they make and the calls made to them are lost. Enum constants with a body (`IDENTITY { @Override String translateName(...) {...} }`) are lost the same way. In gson, `internal/bind/TypeAdapters.java` defines most of its adapters as anonymous classes and has only 22 nodes; 468 of gson's 3,157 call sites lie outside any function eulix knows, 249 of them in that file and 54 in the enum bodies of `FieldNamingPolicy` and `ToNumberPolicy`. Minimal reproduction:

```java
public class App {
    void g() {}
    Runnable r() {
        return new Runnable() {
            public void run() { g(); }   // no node for run
        };
    }
}
```

**Cause.** Read in code: the Java parser extracts methods from `class_declaration` (and the other declared type kinds). An `object_creation_expression` with a class body is recorded only as the call `new Runnable`; its body is not visited for methods. Lambdas were not checked.

**Fix direction.** Visit the `class_body` of an `object_creation_expression` and emit its methods with an id that names the enclosing method and position, so two anonymous classes in one method do not collide (PARSE-8).

---

## PARSE-8: Overloads and same-name definitions in one file share one id

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced (2026-11-15, `5ecd16d`)

**Symptom.** Two definitions with the same name in the same file get the same id: C++ and Java overloads (gson's `Gson.toJson`, `fromJson`, constructors), Python `@overload` stubs and property getter/setter pairs (flask's `locate_app`, `App.debug`), Rust `fmt` written in both `impl Display` and `impl Debug`, and `#[cfg(unix)]` / `#[cfg(windows)]` variants of one function (ripgrep's `is_hidden`). The call graph keeps one node for all of them and merges their calls under it, so a caller of one overload appears to call everything the other overloads call. Counts of shared ids on the harness corpora: gson 46, ripgrep 17, tinyxml2 13, flask 10, gin 2. Minimal reproduction:

```cpp
int g() { return 1; }
int h() { return 2; }
struct A { int f(int x); int f(const char* s); };
int A::f(int x) { return g(); }
int A::f(const char* s) { return h(); }
```

The knowledge base lists `func_A::f::a.cpp` twice (lines 4 and 5); the call graph has one `func_A::f::a.cpp` node with edges to both `g` and `h`.

**Cause.** Ids are `{prefix}_{name}::{path}` with no signature or position. `build_call_graph_v2` keeps the first node for an id; the test `colliding_function_ids_across_files_silently_dedupe_to_one_node` in `libeulix/src/analyze/call_graph/mod.rs` documents the collapse but assumes it "shouldn't happen in practice".

**Fix direction.** Add a disambiguator to colliding ids (the parameter types where the parser has them, otherwise the start line), keeping the plain id for the first definition so that existing references stay valid.

---

## PARSE-9: JavaScript functions that are not declared or bound to a name have no node

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced (2026-11-15)

**Symptom.** A function declaration (`function f() {}`) and a function assigned to a variable (`const f = () => ...`, `const f = function g() {}`) get a node. A function expression anywhere else does not: a callback passed as an argument, a function in an `export default` expression, or an immediately invoked function. Inside another function, the calls such a function makes are credited to the enclosing function, which is close enough. At module top level there is no enclosing function, so the calls are lost. On axios, 554 of 2,076 call sites are outside any function eulix knows, 289 of them in `adapters/http.js`, whose adapter is `export default isHttpAdapterSupported && function httpAdapter(config) { ... }`. Minimal reproduction:

```js
function helper(x) { return x; }
export default cond && function adapter(config) { return helper(config); }   // no node; edge lost
const curried = (({a}) => (obj) => helper(obj))(Object.prototype);           // no node; edge lost
[1].map(function cb(v) { return helper(v); });                               // no node; edge lost
```

**Functions assigned to a property have no node either.** `app.use = function use(fn) {...}` and `module.exports.run = function run() {...}` are both lost, and so are the calls inside them. Libraries written in this style are mostly invisible: in express 5.3.0 (`lib/`), 401 of the 461 call sites lie outside any function eulix knows, eulix has 18 nodes for 6 files, and only 3 caller → callee pairs could be scored. The harness dropped express for this reason (see `bench/callgraph/corpora.toml`). Reproduced on 2026-11-16:

```js
function helper(x) { return x; }
const app = {};
app.use = function use(fn) { return helper(fn); };            // no node; edge lost
module.exports.run = function run() { return helper(1); };    // no node; edge lost
```

**Cause.** Not traced in code. The JavaScript parser extracts `function_declaration` nodes and functions in a `variable_declarator`; other `function_expression` and `arrow_function` nodes, including the right-hand side of an `assignment_expression`, are not visited as definitions.

**Fix direction.** Give a function assigned to a property a node under the property name (`use`, `run`), owned by the object when it is a known class or prototype. Give any other named function expression a node under its own name, and an unnamed one at module top level a node named after its position (`<anonymous@12>`), so that the calls inside it have a caller.

---

## Related Docs

- [PRISM v1](../libeulix/PRISM-V1.md) and [PRISM v2](../libeulix/PRISM-V2.md): the algorithms.
- [Research write-up](../libeulix/Research/PRISM-V2-Methodology.md): the formal description, written against an older commit.
- [Fixed Issues](Fixed-Issues.md): what was fixed in method-call resolution.
- [Call-graph accuracy harness](../../bench/callgraph/README.md): how the numbers in [Measured on real code](#measured-on-real-code) are produced.
- [Call-Graph Accuracy](../libeulix/Call-Graph-Accuracy.md): the current measurements, by language and call kind.
