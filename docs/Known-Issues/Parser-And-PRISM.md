# Parser And PRISM Issues

Known problems in `libeulix` and `eulix-parser`: how files are parsed, and how PRISM links calls to definitions.

> **Scope:** `libeulix`, `eulix-parser`  
> **Last verified against:** `ad744e9` (2026-11-03)

Entries marked *Reproduced* were checked on 2026-11-03 by building the parser from this commit and parsing a small multi-language project with `--prism 2` (and `--prism 1` for comparison). The test project had a Python class hierarchy, a Go type with methods, a Java class, a C++ struct, a JavaScript subclass, a Rust `impl` and a TypeScript class. Read [Overview](Overview.md) for the legend.

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

**Workaround.** See PRISM-2.

**Fix direction.** Have these parsers fill the receiver field and extend receiver-aware resolution to them, then strip `new ` for Java. This is the prerequisite for fixing PRISM-2.

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

## Related Docs

- [PRISM v1](../libeulix/PRISM-V1.md) and [PRISM v2](../libeulix/PRISM-V2.md): the algorithms.
- [Research write-up](../libeulix/Research/PRISM-V2-Methodology.md): the formal description, written against an older commit.
- [Fixed Issues](Fixed-Issues.md): what was fixed in method-call resolution.
