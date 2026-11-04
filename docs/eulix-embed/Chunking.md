# Chunking

This page explains how `kb.json` is cut into the text pieces that get embedded, and what those pieces actually contain today.

> **Crate / path:** `eulix-embed/chunking/` (`chunker.py`, `formatters.py`, `cleaners.py`)  
> **Language:** Python  
> **Status:** beta, with known problems  
> **Last verified against:** `ad744e9`

## Overview

An embedding model reads text, not JSON structures, and it has a length limit. So each file entry in the knowledge base is turned into several small *chunks*, and each chunk is rendered as a short text "card". The card is what the model sees, so its content decides what the vectors mean.

## Why It Exists

Retrieval works best when the unit of search is the unit of meaning: a function, a method, a class. Embedding a whole file as one vector blurs everything in it together. Small, well-labelled cards give precise matches.

## How It Works

`chunk_one_file` produces, for one file:

| Chunk | One per | Id | Importance | Tags |
| --- | --- | --- | --- | --- |
| function | function | the parser's id | from the file, default 0.5 | `function`, plus `async`, `complex` (complexity above 10), `api`, `test` from decorators |
| class | class | the parser's id | 0.7 | `class`, language |
| method | method of a class | the parser's id | from the file, default 0.5 | `method` and the same extras |
| file | file with any content | `file:<path>` | 0.5 | `file`, language |

Duplicate ids are skipped, as a guard against malformed input. Each card is cut to at most 2,000 characters (`--max-chunk` can only lower this), ending at the last whole line if one exists, with `...` appended.

Before chunking, `drop_docstrings` removes every `docstring` field from the file's functions, classes and methods. The comment in the code says the effect on retrieval was never evaluated.

### The cards

The formatters write comment-like headers, in this layout:

```text
// File: app/models.py
// Class: Form
// Method: save
// Inherits from: Base

// File: app/models.py
// Function: save
// Lines: 6-8
// Complexity: 0

def save()
```

A function card is designed to continue with parameters and return type, up to ten outgoing calls and five callers, control-flow counts, and raised and handled exceptions. A class card lists attributes and method names. A file card lists imports, functions (up to ten) and classes.

### What the cards really contain

I ran the chunker on real parser output (`--prism 2`) for a small Python file. Two things differ from the design above.

1. **The cards have no line breaks.** The formatters write each line with a function that does not add a newline, so a whole card is one line: `// File: app/models.py// Function: helper// Lines: 10-11// Complexity: 0def helper()`. See [EMB-6](../Known-Issues/Embeddings.md#emb-6-embedded-text-has-no-line-breaks).
2. **The cards are thin.** The parser's `kb.json` is a slim view ([Output Files](../eulix-parser/Output-Files.md)). It has no calls, callers, complexity, control flow, exceptions or imports, and the chunker has just removed the docstrings. What remains is the name, the line range, the signature, parameters, return type, class bases and, for a class, its method names. "Complexity: 0" is printed for every function. See [EMB-7](../Known-Issues/Embeddings.md#emb-7-cards-omit-calls-complexity-exceptions-imports-and-docstrings).

### Main Components

| Item | Responsibility |
| --- | --- |
| `chunk_one_file` | Builds all chunks for one file. |
| `fmt_function_with_context`, `fmt_method_with_class_ctx`, `fmt_class_overview`, `fmt_file_summary` | Render cards. |
| `_truncate_content`, `_generate_tags` | Length limit and tags. |
| `drop_docstrings` | Removes docstrings in place. |

### Data In, Data Out

In: one file entry (a dict) from the streamed knowledge base. Out: a list of `Chunk` records with an id, type, text, metadata (file, language, lines, name, complexity), tags and an importance score. Only the id and text are embedded; the rest is metadata.

## Key Algorithms

### Truncating on a line boundary

**The problem.** A card longer than the limit must be shortened, but cutting mid-line leaves broken text.

**The approach.** Cut at the last newline before the limit, if there is one.

**Why this way.** It keeps whole lines. Because of EMB-6 there are usually no newlines, so in practice the cut falls at the character limit.

## Configuration

`--max-chunk` (default 2,000 characters; the code also caps it at 2,000).

## Usage Examples

To see the cards for a knowledge base:

```python
from chunking.chunker import chunk_one_file
import json
kb = json.load(open(".eulix/kb.json"))
for path, fs in kb["structure"].items():
    for c in chunk_one_file(path, fs, 2000, set()):
        print(c.id, "\n", c.content)
```

Run it from the `eulix-embed` folder with the Eulix virtual environment.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| No line breaks in cards. | A write helper that omits `\n`. | [EMB-6](../Known-Issues/Embeddings.md#emb-6-embedded-text-has-no-line-breaks). |
| Calls, callers, complexity, exceptions and imports are missing. | The knowledge base no longer carries them. | [EMB-7](../Known-Issues/Embeddings.md#emb-7-cards-omit-calls-complexity-exceptions-imports-and-docstrings). |
| Docstrings are removed on purpose. | A design experiment never evaluated. | None. |
| Classes and methods use the file's order. | No sorting. | None. |

## Related Docs

- [Pipeline](Pipeline.md): what happens to the chunks next.
- [Output Files](../eulix-parser/Output-Files.md): why `kb.json` is slim.
- [Index Loading](../eulix-cli/query/retrieval/Index-Loading.md): the retrieval side builds its *own* chunk text from the same file; the two are not the same text.
