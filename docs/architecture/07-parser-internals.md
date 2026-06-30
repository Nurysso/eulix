# Architecture: Parser Internals (`eulix_parser`)

> **Purpose:** Describes the internal structure of `eulix_parser` — the Rust
> binary responsible for turning source files into a structured knowledge base.
> Use this when adding language support, debugging parse failures, or understanding
> what data shapes the Go CLI actually receives.
>
> **Limitation:** Individual parser implementations (Python, Go, C) are not
> expanded at the per-node level. The call-graph construction algorithm inside
> `Analyzer` is shown at the phase level only. AST node type details live in
> `eulix-parser/src/parser/<lang>.rs`.

---

## Component Map

```mermaid
graph TD
    subgraph MAIN["src/main.rs — entry point"]
        ARGS["Args (clap)\n--root · --output · --threads · --languages · --euignore"]
        RAYON["rayon::ThreadPoolBuilder\nset global thread count"]
        PHASE1["Phase 1\nFile discovery + parallel parse"]
        PHASE2["Phase 2\nCall graph + index build"]
        PHASE3["Phase 3\nSummary generation"]
        PHASE4["Phase 4\nWrite output files"]
    end

    subgraph UTILS["src/utils/"]
        FW["file_walker.rs\nFileWalker\nrespects .euignore"]
    end

    subgraph PARSER["src/parser/"]
        LANG["language.rs\nLanguage enum\ndetect() from extension"]
        PY["python.rs\nparse_file()"]
        GO_P["go.rs\nparse_file()"]
        CP["c.rs\nparse_file()"]
        JS["javascript.rs\n⚠ not yet implemented"]
        TS["typescript.rs\n⚠ not yet implemented"]
        RS_P["rust.rs\n⚠ not yet implemented"]
        ANALYZE["analyze.rs\nAnalyzer\nanalyze_and_build()\ngenerate_summary()"]
    end

    subgraph KB_MOD["src/kb/"]
        TYPES["types.rs\nKnowledgeBase · FileData\nFunction · Class · CallGraph\nMetadata · Indices"]
    end

    subgraph OUTPUT["Output files (.eulix/)"]
        KB["kb.json"]
        CG["kb_call_graph.json"]
        IDX["kb_index.json"]
        SUM["kb_summary.json"]
    end

    ARGS --> RAYON --> PHASE1
    PHASE1 --> FW --> LANG
    LANG --> PY & GO_P & CP
    PY & GO_P & CP -->|"Vec<FileData>"| PHASE2
    PHASE2 --> ANALYZE
    ANALYZE --> PHASE3 --> PHASE4
    PHASE4 --> KB & CG & IDX & SUM
    TYPES -.->|"type definitions used by"| PY & GO_P & CP & ANALYZE
```

---

## Parallel Parsing Pipeline

```mermaid
flowchart LR
    FILES["Vec&lt;PathBuf&gt;\nall source files"]

    subgraph PAR["Rayon par_iter()"]
        T1["Thread 1\nparse_file()"]
        T2["Thread 2\nparse_file()"]
        T3["Thread 3\nparse_file()"]
        TN["Thread N…"]
    end

    STATS["Arc&lt;Mutex&lt;ParseStats&gt;&gt;\nparsed · skipped · failed"]

    MERGE["Collect results\nbuild HashMap&lt;path, FileData&gt;"]

    KB_BUILD["Build KnowledgeBase\nmetadata aggregation"]

    FILES --> T1 & T2 & T3 & TN
    T1 & T2 & T3 & TN -->|"Ok(FileData)"| MERGE
    T1 & T2 & T3 & TN -->|"stats"| STATS
    MERGE --> KB_BUILD
```

---

## `FileData` Schema (per source file)

```
FileData {
    language    : String           // "Python" | "Go" | "C"
    loc         : usize            // lines of code
    functions   : Vec<Function>
    classes     : Vec<Class>
}

Function {
    name        : String
    signature   : String
    line_start  : usize
    line_end    : usize
    docstring   : Option<String>
    calls       : Vec<CallRef>     // { callee, line, defined_in }
    complexity  : Option<usize>    // cyclomatic
    is_exported : bool
}

Class {
    name        : String
    line_start  : usize
    line_end    : usize
    docstring   : Option<String>
    methods     : Vec<Function>
}
```

---

## Call-Graph Construction (`Analyzer`)

```mermaid
flowchart TD
    KB_IN["KnowledgeBase\n(after parse phase)"]

    subgraph ANALYZE["Analyzer::analyze_and_build()"]
        IDX_BUILD["Build Indices\nFunctionsByName: name → Vec<location>\nTypesByName: name → Vec<location>\nFunctionsCalling: callee → Vec<caller>"]
        CG_BUILD["Build CallGraph\nfor each function:\n  node + outgoing edges\n  reverse map → CalledBy edges"]
        ENTRY["Find entry points\n(functions with no CalledBy)"]
        EXT["Collect external dependencies\n(calls not defined in codebase)"]
    end

    KB_OUT["KnowledgeBase\n(enriched with call_graph + indices)"]

    KB_IN --> IDX_BUILD --> CG_BUILD --> ENTRY --> EXT --> KB_OUT
```

---

## Language Support Status

| Language   | Extension | Parse | Call Graph | Complexity | Status                    |
| ---------- | --------- | ----- | ---------- | ---------- | ------------------------- |
| Python     | `.py`     | ✅    | ✅         | ✅         | Implemented               |
| Go         | `.go`     | ✅    | ✅         | ✅         | Implemented               |
| C          | `.c` `.h` | ✅    | ✅         | ✅         | Implemented               |
| Rust       | `.rs`     | ✅    | ✅         | ✅         | Implemented               |
| TypeScript | `.ts`     | ✅    | ✅         | ✅         | Implemented(need changes) |
| JavaScript | `.js`     | ❌    | ❌         | ❌         | Returns error             |

> **Note:** Even though JS/TS/Rust are listed in `collect_source_files`, any
> file of those types causes `parse_file()` to return `Err(...)`, which is
> silently counted as a parse failure. The file is skipped without user warning
> unless `--verbose` is passed.

---

## Performance Characteristics

| Metric                 | Value                        | Conditions                       |
| ---------------------- | ---------------------------- | -------------------------------- |
| Throughput             | ~9M LOC / 40s                | Single thread, mixed Python/Go/C |
| Parallelism            | Linear with `-t`             | Rayon work-stealing thread pool  |
| Memory                 | O(total functions + classes) | Full KB held in RAM              |
| Large codebase warning | > 10 000 files               | Printed only in `--verbose` mode |

---

## Limitations

| Limitation                                                     | Impact                                      | Notes                                             |
| -------------------------------------------------------------- | ------------------------------------------- | ------------------------------------------------- |
| JS/TS parsers not implemented/isn't stable                     | Those files fail                            | Highest priority: TS (dogfooding)                 |
| Call graph cross-file resolution depends on name matching      | Overloaded function names cause false edges | Qualify names with file path                      |
| Complexity is per-function cyclomatic; no module-level metrics | Cannot detect "hot" modules                 | Add file-level aggregation                        |
| `--no-analyze` skips call graph entirely                       | Dependency/usage queries return no results  | Warn user in `eulix chat` if call graph is absent |
