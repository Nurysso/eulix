# PRISM v2

PRISM v2 is the scope-aware call-graph builder in `libeulix`: it resolves each call by searching from the nearest scope outward, and it declines to guess when a name is too common.

> **Crate / path:** `libeulix/src/analyze/call_graph/prism_v2.rs` (plus `prism_v2/php.rs`, `prism_v2/receivers.rs`, `prism_v2/receivers/rust_paths.rs`, `prism_v2/receivers/typed.rs`)  
> **Language:** Rust  
> **Status:** the intended default; receiver-aware for Python, TypeScript, Rust, Go and Java, with dedicated rules for PHP  
> **Last verified against:** `923a577`; the Go/C kind rule and the Rust `impl` and cross-crate rules against the fixes of 2026-11-15; the Go and Java rules against the uncommitted PRISM-5 fix of 2026-11-15

## Overview

v2 keeps the same goal as [PRISM v1](PRISM-V1.md), which is to link every call to the definition it probably means. What changes is how it chooses. Instead of "the first function with that name anywhere", v2 asks "which function with that name is *closest* to where the call is made?" It looks in the same file first, then in what the file imports, then in the same directory, and only then across the whole project.

It also knows about classes. When code inside a method calls `self.validate()`, v2 starts at the method's own class and walks up through its parent classes to find a method with that name. (PHP also searches subclasses; see below.) And when a name is shared by too many definitions to guess responsibly, v2 draws no edge at all. A missing edge is usually better for retrieval than a wrong one.

The source documents a target of roughly 1.4 seconds and 3.1 GB of peak memory for a ~64,000-file polyglot repository. That number comes from a source comment and was not re-measured for this page.

## Why It Exists

v1's global lookup is wrong whenever two unrelated symbols share a name, and in real repositories that is the normal case (`__init__`, `run`, `handle`, `Get`). v2 exists to fix that by using information the parsers already provide: the file a call is in, the file's imports, the directory structure and the class hierarchy. None of it requires a type checker, so v2 stays fast enough to run on repositories with hundreds of thousands of symbols.

## How It Works

`build_with` runs in five phases. Almost all of the heavy work is parallel, and the single-threaded merge steps exist to keep the result deterministic.

1. **Sort and index the files.** Files are sorted by directory and then by path. This places files from the same directory next to each other, which is what makes the "same directory" lookup a cheap range search. In parallel, each file contributes its nodes (functions, methods, classes), the base classes of its classes, and its resolved imports.
2. **Merge.** The per-chunk results are merged on one thread. If two files produce the same id, the earlier file in sorted order wins, so every run gives the same answer.
3. **Build the indices.** A map from each short name to all of its definitions (the inverted symbol map), a sorted list of class methods for fast lookup, a module index for resolving imports, and, if the project has PHP files, an include index.
4. **Resolve calls.** For each function and method, every call is resolved by the scope ladder described below. Edges are collapsed so that the graph has at most one edge per caller and target within a file, keeping the first call-site line. An edge is marked `conditional` only if *every* call to that target is conditional.
5. **Write the output.** Nodes and edges are materialised, and each node's `call_count_estimate` is the number of resolved `call` edges pointing at it. Inheritance edges are deliberately left out of that count, because for "hot spot" questions the number of callers is what matters.

After the graph is built, `populate_called_by_from_graph` fills each function's `called_by` list from the finished graph. It does not re-run resolution, so it can also run on a graph that was saved and reloaded. Unlike v1, v2 never rewrites `kb.structure`, so `calls[].callee` keeps the names the parser wrote.

### Main Components

| Item | Responsibility |
| --- | --- |
| `build_call_graph_v2(&structure)` | Entry point with default options. |
| `build_with(&structure, &CallGraphOptions)` | Entry point with your own options. |
| `populate_called_by_from_graph(&mut kb)` | Fills `called_by` from the finished graph. |
| `Resolver::lookup` | The scope ladder. |
| `Resolver::pick` | Chooses among candidates by kind, distance and a stable tiebreak. |
| `Resolver::resolve_call` | Interprets the shape of a call (`foo()`, `self.foo()`, `pkg.foo()`, `Cls.foo()`). |
| `receivers` module | The receiver-aware path for Python, TypeScript and Rust. |
| `receivers::typed` module | Go and Java: imports, packages, and receivers typed from declarations. |
| `php` module | PHP's own rules: namespaces, includes, traits, magic methods. |

### Data In, Data Out

The input is `&BTreeMap<String, FileData>`, the same as v1. The output is a `CallGraph`. Unlike v1, you do not run a pre-pass first.

## Key Algorithms

### PRISM v2 in Plain Words

Imagine you hear someone say "call Sam". You would not phone every Sam in the world. You would think of the Sam in the room first, then the Sam from your team, then the Sam from your building, and only after that consider any Sam in the company. If there were fifty Sams in the company and nothing else to go on, you would say "which Sam?" and not pick one.

v2 does exactly that for a function name. It checks the **same file**, then the files this file **imports**, then the **same directory**, and finally the **whole project**. The first place that has a match wins. In the whole-project step, if more than a handful of functions share the name, v2 gives up and draws no edge. For method calls it also looks through the class and its parents, because `self.save()` almost always means a method on the same class or a class it inherits from.

### The scope ladder

**The problem.** A short name like `parse` can exist in many files. Which one does a call mean?

**The approach.** The ladder has up to five rungs, and the first rung that yields a candidate decides the answer:

| Rung | Where it looks | Why it comes here |
| --- | --- | --- |
| 1. Same file | Definitions in the caller's own file | A name defined right next to the call is the most likely meaning. |
| 2. Explicit import | Files the caller imported the name from | The author said so directly. |
| 2b. PHP includes | Files pulled in with `include` / `require`, transitively | PHP's equivalent of an import. |
| 3. Same directory | Definitions in the caller's directory | In most languages a directory is a package. |
| 4. Whole project | Every definition with the name, but only if there are no more than `max_ambiguous` (default 8) | The last resort, and the only one that can abstain. |

When several candidates survive on one rung, `pick` prefers the better-fitting *kind* (a plain call prefers a function, then a class; a method call prefers a method, then a function). In Go and C a class is never a candidate for a call at all: neither language has constructor calls, and `T(x)` there is a conversion or a cast. For PHP a plain call can still fall back to a method when not strict. For Python, TypeScript and Rust a bare `foo()` never resolves to a method, then the candidate whose path shares more leading directories with the caller, and finally the lowest index, which makes the result repeatable.

**Why this way.** Nearer scopes are right more often than far ones, and checking them first costs almost nothing, because the candidate lists are sorted so each rung is a binary search and not a scan. The cap is the key design choice: if a name is that common and nothing nearer settled it, any pick would be a coin toss.

**Go and Java stop earlier.** Both languages say where a name can come from, so their calls never reach rung 4. An unqualified Go call is looked up in the caller's file and then in its package directory, and nowhere else. A Java class is looked up in the caller's file, then its imports, then its package directory. See [Go and Java](#go-and-java-receivers-typed-from-declarations).

### Imports and `strict_imports`

If a name comes from an import that points *outside* the project (the standard library or a third-party package), then with `strict_imports` on, v2 stops there and draws no edge, unless the same name is also an internal import. Without that rule, `os.path.join()` could be linked to some unrelated project function called `join`. Turn it off if TypeScript path aliases such as `@app/...` hide your internal modules from the resolver.

### Class hierarchy

**The problem.** `self.validate()` should resolve to a method of the enclosing class or something it inherits from, not to any function named `validate`.

**The approach.** For Python, TypeScript, Rust, Go and Java, a call on a receiver of known class (`self`, `this`, the receiver of a Go method, or a variable with a declared type) looks only in the class's own methods and then its ancestors, with a breadth-first walk (`receiver_method` in `prism_v2/receivers.rs`). A method found only in a subclass does not count. In Go, the "ancestors" of a struct are the types it embeds, and those of an interface are the interfaces it embeds; in Java they are the superclass and the implemented interfaces. PHP is different: `$this->m()` also searches descendants, so an override in a subclass is found, while `X::m()` and `self::m()` look only upwards (`prism_v2/php.rs`). The walk is capped at 64 levels, and a visited set guards against cycles.

**Why this way.** Inherited methods are the common case, so ancestors come first. A subclass override does not tell the resolver what a receiver's class has, so outside PHP it is not used; PHP keeps the descendant search because `$this` there can be any subclass at run time. The cap protects against cyclic or adversarial hierarchies.

### Receiver-aware resolution

Python, TypeScript, Rust, Go and Java parsers keep the *receiver* of a call (the `x` in `x.foo()`) as a separate field, and methods in these languages are indexed under their declared name. For Python, TypeScript and Rust, v2 uses that receiver together with the file's imports, including Rust `use` trees, relative Python imports and relative TypeScript modules, to decide where a call goes. Go and Java go one step further and read the declared type of the receiver, described in the next section. JavaScript, C and C++ use the older, name-based path, where methods are still indexed under a name derived from the node id.

### Go and Java: receivers typed from declarations

Both languages write down the type of every parameter and field and of most local variables. v2 reads those declarations; it does not infer anything. A receiver has a known type when it is:

| Receiver | Go | Java |
| --- | --- | --- |
| The method's own object | `s` in `func (s *Store) Save()` | `this`, `Outer.this`, and `super` (ancestors only) |
| A parameter or local with a declared type | `m *Store`, `var m Store`, a closure parameter, a named result | `Foo f`, a loop or `catch` variable, a typed lambda parameter, `o instanceof Foo f` |
| A local whose initializer spells the type out | `m := Store{}`, `m := &Store{}`, `m := new(Store)` | `new Foo(...)` used directly as the receiver, a cast `((Foo) x)` |
| A field of a known type | `c.engine.Reset()`, and an embedded field by its type name (`e.Group.Use()`) | `field.m()`, `this.field.m()`, a field of an ancestor or of an enclosing class |
| A package or a class named directly | `pkg.Func()` | `Util.helper()`, `Outer.Inner.m()` |

Everything else has no known type and **gets no edge**: a variable assigned from a call (`r := New()`, `var x = make()`), a range variable, an untyped lambda parameter, a call chain (`a.b().c()`), a package-level Go variable. There is no fallback to "the only method of that name"; this is the same rule as for Python, TypeScript and Rust ([PRISM-3](../Known-Issues/Parser-And-PRISM.md#prism-3-calls-on-a-receiver-of-unknown-type-get-no-edge-in-v2)).

A function's variables are kept in one table per function, with no block scopes. So a name that the body declares twice with different types (a parameter shadowed by a range variable, two loops reusing `x`) is treated as having no type. A local of unknown type also hides a field, a package or a class of the same name.

**Go packages and imports.** A Go package is a directory. An import path maps to exactly one directory when the module path is known: `build_with` takes the project's Go modules in `CallGraphOptions::go_modules`, and `eulix_parser` fills it from every `go.mod` above a Go file. `import "example.com/demo/sub"` in module `example.com/demo` binds `sub` (or the alias) to the directory `sub/`, and `sub.Helper()` is looked up in that directory only. Any import from outside the module is external, so `fmt.Println`, `errors.New` and `os.Exit` never link to a project function of the same name. Without a known module, an import path of several segments is matched against directories by suffix, as before, and a single-segment or standard-library path is external. A blank (`_`) or dot (`.`) import binds nothing.

**Go methods.** A method is declared outside its type, possibly in another file of the package, and possibly on a type that is not a struct (`type Chain []int`). v2 gives every method to the type named in its receiver, pointer or not, by package directory and type name. A bare call `Close()` inside a method is a package function, never a sibling method.

**Java classes and imports.** `import a.b.C` binds `C` to the file `…/a/b/C.java`, and `import a.b.*` makes every class of that package directory visible. Classes of the caller's own directory are visible without an import. `java.*`, `javax.*` and any package with no files in the project are external: a class imported from there hides a project class of the same name. `import static a.b.C.m` binds `m` to that method of `C`.

**Java calls.** Every Java function is a method. `m()` with no receiver is looked up in the class, its ancestors and its enclosing classes, then among static imports. The parser records `new Foo(...)` under the class name; it links to the declared constructor (`ctor_Foo_Foo`), or to the class node when the class declares none. `this(...)` and `super(...)` link the same way.

**Interfaces.** A call on a receiver whose declared type is an interface links to the interface's own method declaration (`ifacemethod_Save`, or the Java interface method). v2 does not list the implementers.

**Rust `impl` blocks in another file.** The Rust parser records an `impl` block whose type is declared in another file as an impl-only entry, `impl_{Type}::{path}` (see [Knowledge Base Schema](Knowledge-Base-Schema.md)). v2 resolves `{Type}` from that file, as it would a base class, and indexes the entry's methods under the type, so `self.x()` in either file and `Type::x()` find them. A blanket impl (`impl<T> Tr for T`) is keyed `<T>` and binds to nothing.

**Rust types from other crates.** A type imported from another crate (`use grep_regex::RegexMatcher`) is looked up outside the caller's crate. Files under another crate's `tests/`, `benches/` or `examples/` are never candidates, because no other crate can import them. Without Cargo metadata the package name is unknown, so a package whose directory matches the imported crate name (`grep_regex` or `regex` → `crates/regex`) is preferred over other crates' homonyms.

### Two details that avoid wrong edges

A call to something that is actually one of the caller's own **parameters** is skipped, so `def f(helper): helper()` does not link to a global function named `helper`. A **nested** function (one declared inside another function) can only be called by its parent, and it is invisible to every other file and function.

### PHP

PHP gets its own module because its rules are different. Names are case-insensitive and namespace-qualified, each call shape (`foo()`, `$x->foo()`, `X::foo()`, `new X`) can reach only one kind of target, and `include` / `require` add a scope. Method calls on a receiver whose class is unknown need the *only* method of that name in the project. PHP and non-PHP symbols never resolve to each other. A cap of 2,048 files limits how far one file's include chain can reach.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `max_ambiguous` | `CallGraphOptions` | `8` | A whole-project lookup gives no edge when more than this many definitions share the name. Raise for more coverage, lower for more precision. |
| `strict_imports` | `CallGraphOptions` | `true` | Refuses to fall back to project definitions for names imported from outside the project. Turn off if TypeScript path aliases do not resolve. Go and Java imports are always strict. |
| `go_modules` | `CallGraphOptions` | empty | The Go modules of the project, as (directory of the `go.mod` relative to the project root, module path). Lets a Go import path map to one directory and marks every other import as external. `eulix_parser` fills it from the `go.mod` files it finds; set it yourself when you call `build_with` on a Go project. |

## Usage Examples

```rust
use libeulix::analyze::call_graph::prism_v2::{self, CallGraphOptions};

let opts = CallGraphOptions {
    // (directory of go.mod, module path); only needed for Go projects
    go_modules: vec![(String::new(), "example.com/demo".to_string())],
    ..Default::default()
};
kb.call_graph = prism_v2::build_with(&kb.structure, &opts);
prism_v2::populate_called_by_from_graph(&mut kb);
```

Expected result: `kb.call_graph` holds nodes and edges, and every resolved callee's `called_by` is filled. `prism_v2::build_call_graph_v2(&kb.structure)` does the same with default options.

## Performance Notes

Resolution cost is dominated by sorting the files, building the name index, and, per call, a handful of binary searches over candidate lists. See [Overview](Overview.md) for the documented target profile. The formal complexity analysis is in the [research write-up](Research/PRISM-V2-Methodology.md).

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Dynamic calls get no edge (`getattr`, `$this->$m()`, `obj[name]()`). | The target is not visible in the syntax. | None. |
| A virtual call gets one override, not all of them. | The walk returns the first match. | None. |
| Multiple inheritance follows breadth-first order by index, not the language's own method-resolution order. | Base-class order is not kept. | None yet. |
| "Same directory" means exactly the same directory. | A sibling package is only reachable by the whole-project rung, under its cap. | Raise `max_ambiguous`. |
| Resolution across languages is not blocked, except for PHP and for calls made from Go or Java files. | PHP is kept apart, and a Go or Java call is only looked up among declarations of its own language. Calls from the other languages can still land on any of them. | [PRISM-4](../Known-Issues/Parser-And-PRISM.md#prism-4-calls-resolve-across-languages). |
| Calls to methods in JavaScript, C and C++ classes resolve by name only, and C++ methods defined inside a class body get no caller. | These languages still use the older path, which indexes methods under an id-derived name no call is spelled as. Measured on 2026-11-15: JavaScript finds 76% and C++ out-of-class methods 55% through name lookup. | Use [PRISM v1](PRISM-V1.md) for method-heavy C++ code. See [PRISM-2](../Known-Issues/Parser-And-PRISM.md#prism-2-v2-never-links-calls-to-methods-in-go-java-javascript-c-and-c) and [Call-Graph Accuracy](Call-Graph-Accuracy.md). |
| Calls on a receiver of unknown type get no edge, in Python, TypeScript, Rust, Go and Java. | PRISM has no type inference and no longer guesses by unique name. In Go this is most locals, which are declared with `:=` from a call: v2 links 39% of gin's method calls (all of them correctly) and 96% of gson's. | [PRISM-3](../Known-Issues/Parser-And-PRISM.md#prism-3-calls-on-a-receiver-of-unknown-type-get-no-edge-in-v2). |
| The result type of a call is not followed in Go and Java. | `r := New(); r.Run()` and `a.b().c()` would need the declared return type of `New` and `b`; v2 reads only the declarations of variables and fields. | None. |
| A Go or Java variable declared twice with two types in one function has no type. | Variables are kept in one table per function, without block scopes, so `c` as a parameter and `c` as a range variable cannot be told apart. | None. |
| A call on a Go or Java interface links to the interface's method only. | Implementers are not listed, and a method that an interface gets from an embedded interface outside the project (`http.ResponseWriter`) has no node. | None. |
| Go package-level variables, dot imports and one-argument generic calls (`f[T](x)`) get no edge. | Global variables are not kept for the call graph; a dot import's names are unknown; the grammar reads `f[T](x)` as a conversion to the type `f[T]`. | None. |
| A Java call inside an anonymous class is credited to the enclosing method and resolved in the enclosing class. | Anonymous-class methods have no node ([PARSE-7](../Known-Issues/Parser-And-PRISM.md#parse-7-java-methods-inside-anonymous-classes-are-not-extracted)), so a method the anonymous class inherits is not found. | None. |
| A Java import is matched by path suffix, not by package declaration. | `import util.Text` matches `…/util/Text.java` in any source root, because the `package` line is not kept for the call graph. | None. |
| No call graph above 100,000 files via `analyze_and_build`. | Memory guard in the deprecated pipeline function. | Call `build_with` directly. |

Other known problems (cross-language edges, JavaScript base classes, doubled C++ calls and the slow Rust-workspace case) are listed with reproductions in [Parser And PRISM Issues](../Known-Issues/Parser-And-PRISM.md).

## Related Docs

- [PRISM v1](PRISM-V1.md): the simpler, faster version.
- [Call-Graph Accuracy](Call-Graph-Accuracy.md): measured precision and recall of v1 and v2, per language and call kind.
- [Research write-up](Research/PRISM-V2-Methodology.md): the formal description, for readers who want the maths.
- [Public API](Public-API.md): the functions and options.
- [Knowledge Base Schema](Knowledge-Base-Schema.md): the node and edge types.
