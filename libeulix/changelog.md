# Changelog

## libeulix 0.9.0 - 2026-11-17

Changes since 0.8.2, the version that shipped with Eulix v0.8.3. This is a breaking release: `LanguageSpecificInfo` has a new `PhpInfo` variant, serialized maps are now `BTreeMap`/`BTreeSet`, `FunctionCall` has a new `receiver` field, `CallGraphOptions` has a new `go_modules` field, and some symbol IDs changed shape (see Changed).

### Added

- **PHP parser** (#82) for `.php`, `.phtml` and `.inc` files that hold PHP code, on `tree-sitter-php` 0.25 with the `LANGUAGE_PHP` grammar, so templates that mix HTML and `<?php ?>` parse as one tree.
  - Functions, methods, classes, interfaces, traits and enums with parameters, access modifier, `static`/`abstract`/`final`, docblocks and attributes (new `PhpInfo` in `LanguageSpecificInfo`).
  - `include`/`require` become imports: `include` for literal paths (including `__DIR__` and `dirname(__FILE__)` anchors), `include_dynamic` with the expression for paths built at runtime. `use` imports are read too. Include imports count as project files, not packages.
  - Class names are written fully qualified by PHP's rules (`use` imports and aliases, the current namespace, `namespace\`). Receivers are typed from `new`, type hints, `@param`/`@var`/`@global` docblocks, closure parameters, constructor injection and the class's own return types.
  - Top-level code goes into a synthetic `{main}` function per file, tagged `SCRIPT_TAG`. Functions under an `if (!function_exists('name'))` guard and no-op stubs are tagged `FALLBACK_TAG`. In a file with several namespaces, IDs carry the namespace, so `A\f` and `B\f` stay two nodes.
  - Globals, complexity, TODOs (`#` comments included) and security notes: `eval`, shell execution, `unserialize`, and SQL built from variables in `mysql_query`/`mysqli_query`.
  - Legacy `$s{0}` string offsets, which the grammar doesn't know: a file that fails to parse is retried with brackets in their place, and text is still read from the original.
  - Tree walks are iterative and recursion is bounded, since template code can nest deep enough to overflow a worker stack.
  - `grammar_files::parse_source` refuses a `.inc` file without a PHP open tag (`has_open_tag`), because C, C++ and assembler projects use the extension too.
- **PHP call resolution in PRISM v2** (#82), in a new `prism_v2::php` module. Other languages keep their own resolution path, and PHP and non-PHP symbols never resolve to each other.
  - Classes match by exact, case-insensitive qualified name. A class the project doesn't declare (built-ins, vendor code) gets no edge.
  - `foo()` tries the current namespace, then the global function.
  - `X::m()`, typed `$x->m()`, `$this->m()`, `self::` and `static::` search the class, its traits and its `extends` chain, then interfaces, then `__call`/`__callStatic`. `parent::` starts at the `extends` class, and `self::` in a trait is the class using it.
  - An untyped `$x->m()` resolves only when the project defines exactly one method of that name, and never to a static method.
  - Files reached through `include`/`require`, followed transitively, are a new scope tier between imports and the same directory.
  - When guessing, fallback-tagged functions lose to a real definition, and win when there are too many real ones to choose from.
- **Receiver-aware method resolution for Python, TypeScript and Rust** in PRISM v2 (#152). Each call keeps its receiver text in a new optional `FunctionCall` field, omitted when empty. Callee and node IDs don't change.
  - `self`, `this`, `cls` and `Self` resolve in the enclosing class and its parents, including methods inherited from another file.
  - Dotted Python imports and TypeScript re-exports are followed up to two levels.
  - Crate-local Rust paths resolve before the lexical fallback. Only `std`, `core`, `alloc`, `proc_macro` and the primitive types count as external roots, so calls into a sibling workspace crate keep the name lookup.
  - Local definitions win over imports. A method on an unknown receiver is no longer guessed by unique name in these languages.
  - JavaScript keeps the legacy name and method indexes, so it can't see the new short method names.
- **Go and Java receivers typed from declarations** in PRISM v2 (#167, PRISM-5), in a new `prism_v2::receivers::typed` module. Before, v2 linked no method call in either language.
  - The Go and Java parsers pass the receiver of every call in `FunctionCall::receiver`, and keep the declared types of variables and fields.
  - A receiver has a known type when it is the method's own object (`s` in `func (s *Store)`, `this`, `super`), a parameter or local with a declared type, a local whose initializer spells the type out (`m := &Store{}`, `new Foo(...)`, a cast), a field of a known type, or a package or class named directly. Anything else gets no edge (PRISM-3).
  - Go import paths map to package directories through the project's modules, passed in `CallGraphOptions::go_modules`. Imports from outside the module are external, so `fmt.Println` never links to a project `Println`.
  - Java `import a.b.C`, `import a.b.*` and `import static` are followed. `new Foo(...)` links to the declared constructor, or to the class when it has none.
  - On gin, precision goes from 70.3% to 100% and recall from 43.9% to 66.3%. On gson, recall goes from 0% to 97.3%.
- **Nested functions** (#146) in Rust, Python, TypeScript and JavaScript are extracted with scoped IDs such as `func_outer.inner::file.rs`, with an `@line` suffix for duplicates. The outer function's walk, metrics and analysis stop at the nested boundary, and nested functions stay out of the global name indexes.
- A shared iterative syntax-tree walker in the grammar utilities, with a heap-backed context stack (#133).
- `FileWalker::with_threads`, defaulting to the available parallelism (#147).

### Changed

- Every recursive tree walk in the C, C++, Go, Java, JavaScript, TypeScript, Python and Rust grammars now uses the iterative walker (#133). A 10,000-term expression chain no longer overflows a 2 MiB stack. Output is unchanged on redis, spdlog, gin, cobra, express, hono, zod, flask, requests and ripgrep.
- Serialized types use `BTreeMap`/`BTreeSet` instead of hash maps: `KnowledgeBase.structure`, `Indices`, the summary categories, the dependency maps and `used_by` sets, and the call-graph view maps (#149). The same input now gives the same output order on every run. Internal lookup maps are still hash maps.
- `FileWalker` hashes size and mtime instead of file contents, so each file is read once, by the parser. The unused `lines` field and `total_lines` are gone from the checksum (#147).
- TypeScript interface IDs include the file path (`interface_Name::path`), like class IDs, so same-named interfaces in different files are no longer merged into one node (#134).
- `classify_file` returns `&'static str` instead of allocating a `String` (#141).
- **Rust:** an `impl` block whose type is declared in another file, or another crate, is kept as an impl-only `Class`, `impl_{Type}::{path}`, with the decorator `impl` and `item_kind` `impl`, instead of being dropped with its methods. A blanket impl (`impl<T> Tr for T`) is `impl_<T>::{path}`. PRISM v2 gives the entry's methods, and `self` inside them, to the type it resolves to.
- **C/C++:** `parse_source` sends a `.h` file with C++-only syntax (a class with a body, a namespace, a template, an access specifier, `::`) to the C++ parser (`cpp::looks_like_cpp`). `Language::detect` still maps `.h` to C.
- **PRISM v2:** a call from a Go or C file never resolves to a type, since `T(x)` is a conversion or a cast there, so `c.JSON(...)` no longer links to `type JSON struct` (part of PRISM-10). On gin, wrong caller → callee pairs fall from 832 to 448.
- **PRISM v2, Rust:** a type imported from another crate prefers the package whose directory matches the crate name (`grep_regex` → `crates/regex`), and never resolves into another crate's `tests/`, `benches/` or `examples/`.

### Deprecated

- `analyze_and_build`. eulix_parser now runs its own analysis pipeline (`7cc429f`).

### Fixed

- **C++:** inline member functions, which tree-sitter names with a `field_identifier`, were dropped, leaving only constructors and destructors in the class (#109). Template classes, template functions and member templates were skipped (#117).
- **Python:** methods with a decorator (`@property`, `@staticmethod`, `@classmethod`, ...) were dropped from their class. `is_async` was always false. TODOs in `#` comments are now read, but not inside strings (#114).
- **TypeScript:** functions bound to variables (`export const f = () => ...`, `const f = function () {}`) were not extracted. On Hono's `src`, extracted functions go from 159 to 409 (#126).
- **Rust:** methods in generic or path-qualified impls (`Wrapper<T>`, `crate::m::Wrapper<T>`) were dropped. On ripgrep, extracted methods go from 1,676 to 1,972 (#134). A malformed attribute such as `#[derive)(]`, common while someone is typing, no longer panics and takes the whole run down (#111).
- **PRISM v1:** every node had `file_idx` 0, and class bases (`Base`, `models.Base`, `Base<T>`) never produced inheritance edges (#118). The call graph changed from run to run; on requests, ten runs gave ten different edge sets, and now give one (#134). Callers are deduplicated by name and line (#152).
- **PRISM v2:** a call to one of the caller's own parameters (`def dynamic(helper): helper()`) was bound to a project function with the same name (#135). A symbol declared twice in one file (`#ifdef` branches) got its own copy of every edge per declaration (#82).
- **Indices:** `files_by_category` was empty and method calls were missing from `functions_calling` (#141). `functions_calling` lists a caller once per callee and line (#152).
- **Summary:** `key_features` in `kb_summary.json` changed on every run. Files are now walked in path order, an opener is the first sentence of the first paragraph, tag-only docblocks (`@param`, `@see`, `{@inheritdoc}`) are skipped, and paragraphs split on CRLF and CR line endings too (#88).
- **FileWalker:** one unreadable file stopped the walk. Errors are now collected per file and the walk goes on; failed paths still reach the parser, so they are recorded as failed (#147).
- **Rust:** items inside inline `mod` blocks, including every `#[cfg(test)] mod tests`, were not extracted (PARSE-5). On ripgrep, PRISM v2's correct caller → callee pairs go from 967 to 1,645.
- **C and C++:** everything inside `#if`/`#ifdef`/`#else` blocks, which include guards wrap around every header, and inside `extern "C" { ... }` was skipped (`c_top_level_items`). On tinyxml2, calls into functions eulix had no node for fall from 685 to 26.
- **C++:** `class EXPORT_MACRO Name : Base {` was read as a function `Name` (PARSE-6); the annotation is now blanked before parsing, keeping offsets. Global variable names no longer include their initializer (`counter = 0`).

These were found with the call-graph accuracy harness in `bench/callgraph/`; measurements are in [Call-Graph Accuracy](../docs/libeulix/Call-Graph-Accuracy.md).

_Earlier libeulix changes were tracked with eulix_parser; see the root [changelog](../changelog.md)._
