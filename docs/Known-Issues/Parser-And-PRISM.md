# Parser And PRISM Issues

Known problems in `libeulix` and `eulix-parser`: how files are parsed, and how PRISM links calls to definitions.

> **Scope:** `libeulix`, `eulix-parser`  
> **Last verified against:** `ad744e9` (2026-11-03); PRISM-10, PRISM-11 and PARSE-5 to PARSE-8 against `5ecd16d` (2026-11-15)

Entries marked *Reproduced* were checked on 2026-11-03 by building the parser from this commit and parsing a small multi-language project with `--prism 2` (and `--prism 1` for comparison). The test project had a Python class hierarchy, a Go type with methods, a Java class, a C++ struct, a JavaScript subclass, a Rust `impl` and a TypeScript class. Read [Overview](Overview.md) for the legend.

PRISM-10, PRISM-11 and PARSE-5 to PARSE-8 were found on 2026-11-15 by the call-graph accuracy harness ([bench/callgraph](../../bench/callgraph/README.md)), which scores PRISM against each language's own tooling on one real project per language. Each was then reproduced on `5ecd16d` with a minimal project of a few lines, given in its entry.

## What the test project showed

For orientation, here is what PRISM v2 resolved and did not:

| Call in the test project | Language | v2 result |
| --- | --- | --- |
| `self.validate()` to a method of a parent class in another directory | Python | Edge to the parent's method. Fixed. |
| `this.step()` and `new A().run()` | TypeScript | Edges. Fixed. |
| `self.inner()` and `P::new()` | Rust | Edges. Fixed. |
| `m.Save()`, and `m.load()` inside a method | Go | **No edge.** |
| `baz()` inside a method, `new Foo()`, `f.bar()` | Java | **No edge.** |
| `s.get()` | C++ | **No edge.** |
| `this.hello()` to a parent's method | JavaScript | **No edge**, and no inheritance edge either. |
| `helper()` in Go with the only `helper` defined in a Python file | Go | **Edge to the Python function.** |
| `f.save()` where `f` has no visible type | Python | No edge (by design, PRISM-3). |

PRISM v1, run on the same files, linked the Go, Java and C++ method calls, but also linked a TypeScript function to an unrelated Python function.

## Measured on real code

The accuracy harness, run on `5ecd16d` (2026-11-15). Each count is a unique caller → callee pair, which is what v2 stores. Precision = correct edges / edges eulix made; recall = calls the language's own tooling resolves into the project that eulix links correctly. See the [harness README](../../bench/callgraph/README.md) for how sites are matched and what each reference tool can and cannot see.

| Corpus | Language | v2 precision | v2 recall | v2 F1 | v1 precision | v1 recall |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| gin 1.12.0 | Go | 55.8% | 44.1% | 49.2% | 59.5% | 78.9% |
| gson 2.15.0 | Java | – | 0.0% | – | 56.3% | 58.4% |
| flask 3.1.3 | Python | 98.2% | 75.0% | 85.0% | 62.0% | 75.0% |
| ripgrep 15.2.0 | Rust | 99.4% | 39.2% | 56.2% | 39.6% | 55.5% |
| class-validator 0.15.1 | TypeScript | 100.0% | 91.0% | 95.3% | 95.5% | 96.0% |
| axios 1.21.1 | JavaScript | 91.2% | 72.7% | 80.9% | 97.5% | 53.5% |
| cJSON 1.7.19 | C | 100.0% | 99.8% | 99.9% | 99.4% | 99.1% |
| tinyxml2 11.0.0 | C++ | 94.9% | 88.8% | 91.7% | 89.7% | 8.3% |

Recall only counts calls made from inside a function eulix knows about, and to a function eulix knows about. PARSE-5 to PARSE-7 hide whole groups of functions (on ripgrep, 7,345 of 15,066 call sites are inside functions eulix never extracted), so the real coverage is lower than these numbers. Reproduce with `make bench-callgraph`.

---

## PRISM-1: Call graphs are approximate

**Severity:** Medium · **Status:** By design · **Verification:** Carried over

PRISM infers calls from names, scopes and imports. It is not a compiler front end, so edges can be missing, extra, or pointing at the wrong target when names are ambiguous. Treat edges as navigation evidence, not proof. See [PRISM v1](../libeulix/PRISM-V1.md) and [PRISM v2](../libeulix/PRISM-V2.md).

The earlier maintainer measurement on a 64,460-file Linux-kernel-sized checkout (12 threads, same input to both versions):

| Version | Edges | Analysis time | Peak memory | Character |
| --- | ---: | ---: | ---: | --- |
| v1 | 2,093,881 | 2.79 s | 4,622.8 MB | More edges, more over-approximation. |
| v2 | 1,510,341 | 1.34 s | 2,727.6 MB | About 28% fewer, better-qualified edges; about 41% less memory. |

I did not repeat this measurement. **Guidance:** use v2 (the default) for precision and inheritance awareness. Use v1 only when you want the largest possible edge set and will filter it yourself. Be aware of PRISM-2 below, where v1 currently finds method calls that v2 misses.

---

## PRISM-2: v2 never links calls to methods in Go, Java, JavaScript, C and C++

**Severity:** High · **Status:** Partly fixed · **Verification:** Reproduced

**Symptom.** In those languages, a call to a method gets no edge, and the method's `called_by` list is empty. Examples from the test project: Go `m.Save()`, Java `baz()`, C++ `s.get()`, JavaScript `this.hello()`.

Measured on 2026-11-15 (`5ecd16d`, see [Measured on real code](#measured-on-real-code)):

- **Java gets no call edges at all**, not just no method edges. Every Java function is a method, static ones included, so v2 linked none of the 1,088 resolvable caller → callee pairs in gson; a static `util()` called from `run()` in the same class gets no edge either.
- **Go:** v2 linked 0 of 1,315 method-call pairs in gin; plain function calls were found (98%). Worse, many of those method calls are linked to a struct of the same name instead: see PRISM-10.
- **JavaScript:** `this.hello()` and `c.greet()` (on `const c = new Child()`) get no edge from v1 either, so the workaround below does not help for JavaScript.

**Cause.** The parsers write method ids as `method_{Class}_{name}::{path}`. For languages without receiver-aware resolution, v2 still indexes a method under the name taken from its id, which is `{Class}_{name}` and which no call site is spelled as. The fix for this (commit `95f1288`) indexes methods by their declared name, but only for the languages that keep a separate receiver on each call: **Python, TypeScript and Rust**. PHP has always had its own rules. The other languages still use the older path, so their methods can never be found by name.

**Workaround.** For method-heavy code in Go, Java, JavaScript, C or C++, use `--prism 1`. It links method calls by first-match name lookup (with the false positives described in [PRISM v1](../libeulix/PRISM-V1.md)).

**Fix direction.** Index these languages' methods by declared name as well, once their parsers pass the receiver (PRISM-5).

---

## PRISM-3: Calls on a receiver of unknown type get no edge in v2

**Severity:** Medium · **Status:** By design · **Verification:** Reproduced

**Symptom.** In Python, TypeScript and Rust, `f.save()` links nowhere when `f` is a parameter or a variable whose type PRISM cannot see. In the test project, `go(f): f.save()` and `run(): f = Form(); f.save()` both got no edge to `Form.save`. Calls on `self`, `this` and `Self`, and on a type named directly (`P::new()`), do resolve.

**Cause.** PRISM has no type inference. A commit made for precision (`02f3674`) turned off guessing "the only method with this name" for unknown receivers, because it produced wrong edges.

**Workaround.** None needed if you accept missing edges. v1 will link these calls by name, with the usual risk of a wrong target.

---

## PRISM-4: Calls resolve across languages

**Severity:** High · **Status:** Open · **Verification:** Reproduced

**Symptom.** v2 keeps PHP apart from everything else, but puts all other languages in one name space. In the test project, a Go function that calls `helper()` was linked to a Python function named `helper`, with no Go `helper` anywhere. v1 does the same, and in one case linked a TypeScript function to a Python one.

An earlier measurement on the Eulix repository itself found 125 of 1,932 call edges (6.5%) crossing languages, none of which could be real because the repository has no foreign-function calls between its languages. For example, a Rust call to `max` linked to a Go function. I did not repeat that count.

**Cause.** `Resolver::pick` only skips a candidate when its file and the caller's file differ in being PHP. There is no check for other languages.

**Workaround.** None. Filter edges whose two ends have different languages (the earlier document included a short script for counting them).

**Fix direction.** Compare language groups instead of only PHP-ness when choosing candidates. Treat JavaScript with TypeScript, and C with C++, as one group each.

---

## PRISM-5: Go and Java receivers never reach the resolver; Java `new` calls never resolve

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced

**Symptom and cause.** Python, TypeScript and Rust parsers store the receiver of a call in its own field. Go and Java store it in `defined_in` instead (`m.Save()` is recorded as callee `Save` with `defined_in = "m"`), and v2 does not read `defined_in`, so the resolver never sees `m`. Java constructor calls are recorded as `new Foo`, and only the PHP path removes the `new `. JavaScript keeps the qualified text (`this.hello`) in the callee, which the older path cannot use.

**False edges in Go (added 2026-11-10).** The same gap also creates wrong edges. For `fmt.Println`, the Go parser stores only `Println` as the callee and puts `fmt` in `defined_in` (`libeulix/src/grammar_files/go.rs`). `eulix-parser`'s slim step then clears `defined_in` (`eulix-parser/src/parse/slim.rs`), and for Go it also clears the imports, so PRISM v1 and v2 resolve the call by its bare name. In a small module, `os.Exit(1)` links to a local `func Exit`, `sub.Helper()` links to a local `Helper` instead of the one in `sub/`, and `s.Close()` links to a top-level `func Close` instead of `(*Store).Close`. On cobra with `-p 2`, four `exec.Command(...)` calls in the tests link to `struct_Command`. v0.8.3 does the same, so this is not a regression. Reported in review, not re-run for this page.

**Workaround.** See PRISM-2.

On gin the same bare-name lookup produces hundreds of wrong edges, most of them to struct types; that is tracked separately as PRISM-10.

**Fix direction.** Have these parsers fill the receiver field, keep Go imports through slim, and extend receiver-aware resolution to them, then strip `new ` for Java. This is the prerequisite for fixing PRISM-2.

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

**Severity:** High · **Status:** Open · **Verification:** Reproduced (2026-11-15, `5ecd16d`)

**Symptom.** In Go, v2 links a method call to a **struct** that has the method's name, and links a call into another package to a project function that only shares its name. On gin, v2 made 837 wrong caller → callee pairs next to 1,055 right ones, so 44% of its Go edges are wrong. The largest groups: `err.Error()` on the built-in `error` interface linked to gin's `struct_Error` (118 call sites, plus 13 calls to gin's own `c.Error`), `String()` calls linked to the `render.String` struct (166), and `errors.New`, `reflect.New` and `validator.New` linked to gin's own `func New` (48). Minimal reproduction:

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

**Workaround.** None in v2. v1 links `c.JSON` to the method, with its own name-collision false positives.

**Fix direction.** Do not offer type nodes as call targets in languages without constructor calls (Go has none: `T(x)` is a conversion, `T{}` a literal). Then fix PRISM-5, so that the receiver and the package qualifier rule out look-alikes.

---

## PRISM-11: JavaScript calls on built-in objects link to project functions of the same name

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced (2026-11-15, `5ecd16d`)

**Symptom.** A method call on an array, string, regular expression or function links to any project function with that name. On axios, v2's 26 false edges were all of this kind: `list.forEach(...)` to `utils.forEach` (13 call sites), `regex.test(...)` to a `test` function in `adapters/fetch.js` (7), `str.trim()` to `utils.trim` (3). v1 links these less often. Minimal reproduction:

```js
// utils.js
export function forEach(obj, fn) { for (const k in obj) fn(obj[k]); }
// app.js
export function run(list) { list.forEach(x => x); }   // v2: edge run -> utils.forEach
```

**Cause.** Not traced. The call has a receiver (`list`) whose type PRISM cannot see; in Python, TypeScript and Rust such calls get no edge by design (PRISM-3), but on the JavaScript path the bare name `forEach` still resolves.

**Fix direction.** Treat JavaScript like TypeScript: a call on a receiver that is not `this`, a class, or an imported module binding gets no edge.

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

---

## PARSE-4: `dependency_graph` is never filled; some Cargo features do nothing

**Severity:** Low · **Status:** Open · **Verification:** Read in code

The knowledge base has a `dependency_graph` field, but neither `libeulix` nor `eulix-parser` populates it; it is always empty. The `libeulix` Cargo features `secure` and `system` are declared but nothing in the library source uses them. The `main.rs` header comment of `eulix-parser` lists four old artifact names. None of these affects output; they mislead readers. See [Knowledge Base Schema](../libeulix/Knowledge-Base-Schema.md).

---

## PARSE-5: Rust items inside inline `mod` blocks are not extracted

**Severity:** High · **Status:** Open · **Verification:** Reproduced (2026-11-15, `5ecd16d`)

**Symptom.** Functions, structs and `impl` blocks written inside `mod name { ... }` never reach the knowledge base, so they have no node, no calls and no callers. This includes every `#[cfg(test)] mod tests` block. On ripgrep, 7,345 of the 15,066 call sites the reference found are inside functions eulix never extracted; `crates/printer/src/standard.rs` alone has 1,377. Minimal reproduction:

```rust
pub fn top() { inner::a(); }
mod inner {
    pub fn a() { b(); }   // not extracted
    fn b() {}             // not extracted
    pub struct S;         // not extracted
    impl S { pub fn x(&self) {} }   // not extracted
}
#[cfg(test)]
mod tests {
    #[test]
    fn t() { super::top(); }   // not extracted
}
```

The knowledge base has only `func_top`, and the call graph has no edges.

**Cause.** `extract_functions` in `libeulix/src/grammar_files/rust.rs` loops over the direct children of the file's root and looks for `function_item`; a `mod_item`'s `declaration_list` is never entered. Structs, enums and `impl` blocks inside a module are missed the same way.

**Fix direction.** Recurse into `mod_item` bodies (iteratively, as the rest of the parser walks the tree) and carry the module path into the ids so that `a::run` and `b::run` in one file stay distinct (see PARSE-8).

---

## PARSE-6: C++ code in `.h` headers is parsed as C

**Severity:** High · **Status:** Open · **Verification:** Reproduced (2026-11-15, `5ecd16d`)

**Symptom.** A `.h` file is always parsed with the C grammar, even in a C++ project. Classes in it are not recognised, and methods defined inside the class body (the usual style for small methods, and the whole of a header-only library) get no node. In tinyxml2, `tinyxml2.h` produced no classes and no functions; 661 of the 2,167 call sites target methods defined there. Minimal reproduction:

```cpp
// box.h
class Box {
public:
  int size() const { return n; }
  int twice() const { return size() * 2; }
private:
  int n = 1;
};
// main.cpp
#include "box.h"
int run() { Box b; return b.twice(); }
```

`box.h` is recorded with `language: c`, one bogus function `func_Box` spanning the class, and no methods, so `run -> Box::twice` and `twice -> size` are both missing.

**Cause.** `libeulix/src/grammar_files/language.rs` maps the `h` extension to `Language::C`; `.hpp`, `.hxx` and `.h++` go to C++.

**Workaround.** None without renaming files. Headers named `.hpp` parse as C++.

**Fix direction.** Decide per header: parse a `.h` file as C++ when the project has C++ sources and the header uses C++ syntax (`class`, `namespace`, `template`, `::`), or when it fails to parse as C but parses as C++.

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

## Related Docs

- [PRISM v1](../libeulix/PRISM-V1.md) and [PRISM v2](../libeulix/PRISM-V2.md): the algorithms.
- [Research write-up](../libeulix/Research/PRISM-V2-Methodology.md): the formal description, written against an older commit.
- [Fixed Issues](Fixed-Issues.md): what was fixed in method-call resolution.
- [Call-graph accuracy harness](../../bench/callgraph/README.md): how the numbers in [Measured on real code](#measured-on-real-code) are produced.
