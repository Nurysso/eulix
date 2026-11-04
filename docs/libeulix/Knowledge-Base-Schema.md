# Knowledge Base Schema

This page describes the data that `libeulix` produces: what each type holds, how the types nest, and which fields you can rely on.

> **Crate / path:** `libeulix/src/struc/kb_struct.rs`  
> **Language:** Rust (all types derive `Serialize` and `Deserialize`)  
> **Status:** stable  
> **Last verified against:** `923a577`

## Overview

The schema is the contract between `libeulix` and everything that consumes it. The parser writes it to JSON, the CLI and the embedding tools read it back, and the language server holds it in memory. If you need to know what information Eulix has about a repository, this is the page to read.

There are two top-level types. `FileData` describes one parsed file. `KnowledgeBase` describes the whole project: a map from relative file path to `FileData`, plus the relationships that the analysis passes derive from it.

## Why It Exists

Keeping every type in one file, separate from the parsers and the analysis code, means that both sides depend on the schema and not on each other. A new language parser only has to fill in the same `FileData` shape, and a new analysis pass only has to read it. That is why adding a language never requires touching the call-graph code.

## How It Works

### The shape of a knowledge base

```text
KnowledgeBase
├── metadata               project name, hashes, counts, languages
├── structure              BTreeMap<relative path, FileData>
│   └── FileData
│       ├── language, loc
│       ├── imports[]
│       ├── functions[]    ── Function (calls, called_by, complexity, tags, ...)
│       ├── classes[]      ── Class (bases, methods[] of Function, attributes)
│       ├── global_vars[], todos[], security_notes[]
│       └── syntax         (runtime only, never serialized)
├── call_graph             nodes[] + edges[]
├── dependency_graph       nodes[] + edges[]   (always empty today)
├── indices                lookup maps
├── entry_points[]
├── external_dependencies[]
└── patterns
```

`structure` is a `BTreeMap`, so files iterate in path order. Both PRISM versions depend on that ordering to make their results repeatable.

### Main Components

| Type | Responsibility |
| --- | --- |
| `KnowledgeBase` | The whole project: metadata, files, call graph and derived reports. |
| `Metadata` | Version, git hash, project name and hash, parse time, languages and the totals for files, lines, functions, classes and methods. The parser binary fills it in, not the library. |
| `FileData` | One file: language, line count, imports, functions, classes, globals, TODOs and security notes. |
| `Function` | A function or method: id, name, signature, parameters, return type, docstring, line range, calls, callers, variables, control flow, exceptions, complexity, tags and an importance score. |
| `FunctionCall` | One call site: callee, optional receiver, the file it resolved to, line, arguments, and whether it sits inside a conditional or loop. |
| `Class` | A class, struct, trait or interface: id, name, base types, docstring, line range, methods, attributes and decorators. |
| `LanguageSpecificInfo` | Extra fields per language (Rust lifetimes, Go generics, PHP namespaces, and so on). Each parser fills only its own variant. |
| `CallGraph` | `CallGraphNode` and `CallGraphEdge` lists, built by PRISM. |
| `EntryPoint`, `ExternalDependency`, `PatternInfo` | Outputs of the smaller analysis passes. |
| `Indices` / `IndicesView` | Lookup maps. `Indices` owns its strings and is stored in the knowledge base; `IndicesView` borrows them and is cheaper to build. |
| `SyntaxHealth` | Counts of Tree-sitter error and missing nodes in a file, with the first error line. Not serialized. |

### Data In, Data Out

**Ids.** Every function, method and class has an `id` that is unique within the project. The parsers build them from a kind prefix, the name and the file path:

| Kind | Id format | Example |
| --- | --- | --- |
| Function | `func_{name}::{path}` | `func_helper::app/models.py` |
| Method | `method_{Class}_{name}::{path}` | `method_Form_save::app/models.py` |
| Class / struct | `class_{name}::{path}` or `struct_{name}::{path}` | `class_Form::app/models.py` |
| Nested function | `func_{outer}.{inner}::{path}`, tagged `nested` | `func_outer.helper::a.rs` |

Which prefix a language uses for its types varies (Go and C use `struct_`, JavaScript uses `class_`). Ids are how the call graph refers to nodes, so treat them as opaque strings and do not parse them in your own code.

**Edges.** A `CallGraphEdge` has `edge_type` set to `call` or `inheritance`, a `conditional` flag, and `call_site_line`. The type's source comment also mentions `uses`, but neither PRISM version emits it.

**Tags.** `Function::tags` holds short labels from the parser, such as `entry-point` and `nested`. The `nested` tag marks a function declared inside another function's body, and the call-graph code and the indices both treat such functions specially.

## Key Algorithms

This page describes data, not algorithms. The schema does carry two pieces of design worth knowing about.

### Skipped and optional fields

**The problem.** A knowledge base for a large repository can reach hundreds of megabytes of JSON, so every field costs real space.

**The approach.** Fields that only some languages need are optional and are skipped when empty. `FunctionCall::receiver` is omitted when absent, and each entry of `LanguageSpecificInfo` appears only for its own language. `FileData::syntax` is marked `#[serde(skip)]`, so parse-error information exists at runtime but never reaches the JSON.

**Why this way.** It keeps the output small and keeps old knowledge bases readable, because new optional fields default to nothing when an older file is loaded.

## Configuration

The schema has no settings. The only knob is that `serde_json` can be built with the `dev` feature (`arbitrary_precision`) when exact number handling is needed.

## Usage Examples

Reading a knowledge base produced by the parser:

```rust
use libeulix::struc::kb_struct::KnowledgeBase;

let json = std::fs::read_to_string("knowledge_base.json")?;
let kb: KnowledgeBase = serde_json::from_str(&json)?;

for (path, file) in &kb.structure {
    println!("{path}: {} functions", file.functions.len());
}
```

Expected result: one line per file with its function count.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| `dependency_graph` is always empty. | The type exists, but I found no pass in `libeulix` or `eulix-parser` that fills it; both only create it with `DependencyGraph::default()`. | Use `external_dependencies` and each file's `imports` instead. |
| `Metadata` is not filled by the library. | Counts, hashes and the git hash need information the library does not have. | The caller builds it; `eulix-parser` does this. |
| `EntryPoint::function` and `handler` always carry the same value today. | The detector sets both from the function name. | Use either one. |
| Some fields are free-form strings. | `entry_type`, `edge_type`, `scope` and similar fields are `String`, not enums. | Compare against the documented values listed in the type comments. |

## Related Docs

- [Public API](Public-API.md): the functions that create these types.
- [Analysis Passes](Analysis-Passes.md): which pass fills which field.
- [PRISM v1](PRISM-V1.md) and [PRISM v2](PRISM-V2.md): how `call_graph` and `called_by` are produced.
