# prompts

The prompt builders wrap the retrieved context in instructions that make the model reason carefully and admit what it cannot see.

> **Crate / path:** `eulix-cli/internal/query/prompts.go`, `router_handlers.go` (`cotHeader`, `BuildPromptString`)  
> **Language:** Go  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

A language model will happily answer a question about code it has not seen. The prompts are built to prevent that. Every prompt that goes to the model has the same shape: the retrieved context, then a reasoning header, then a task description specific to the question type, then a short footer. The shared parts guarantee consistent behaviour across all question types.

## Why It Exists

The main failure of retrieval-based assistants is confident invention. Eulix gives the model only partial code, often signatures without bodies, so it matters that the model keeps "what I can see" apart from "what I guess". The prompts enforce this with a fixed vocabulary of claim strength and a rule to cite sources.

## How It Works

A full prompt is assembled in this order:

1. **Context.** From `BuildFullPrompt`: a summary line, source chunks, metadata-only chunks, then the question. See [Llm](../Llm.md).
2. **Reasoning header (`cotHeader`).** Tells the model whether it has real source code (and roughly what share, from `code_to_ast_ratio`) or only metadata. It lists the relevant symbols and keywords, states the question type and confidence, and then sets out four steps to follow inside `<reasoning>` tags: *Inventory* (list the relevant symbols and what is available for each), *Evidence* (cite specific lines or signatures), *Gaps* (say plainly what cannot be determined), and *Answer* (inside `<answer>` tags, grounded in the evidence).
3. **Task body (`getTaskBody`).** One body per question type, with three shared blocks put in front of it.
4. **Footer (`cotFooter`).** A final honesty reminder.

### The three shared blocks

- **Language grounding.** Tells the model to take its vocabulary from the language of the retrieved code (nil, null or None; exceptions or error values; interfaces or traits) instead of defaulting to whichever language it saw most in training. Adding a new supported language needs no change to the prompts.
- **Confidence legend.** Every claim must be labelled `CONFIRMED` (visible in retrieved source), `INFERRED` (deduced from a signature, type or call edge, but the body was not retrieved) or `UNKNOWN`.
- **Citation contract (the "honesty contract").** Cite `file:line` for every factual claim, tag each claim with a label, say plainly if the answer is not in the context instead of filling the gap from outside knowledge, and do not invent code bodies or behaviour.

### Task bodies

Each question type has its own text. For example, *Understanding* asks the model to explain what each relevant symbol does, trace at least one concrete call path from an entry point to a leaf, note visible design choices, and list what remains unclear, and it suggests an answer shape (summary, per-symbol bullets, call path, open questions). Some bodies pull in runtime data, such as the files in the index that define the symbols mentioned in the question.

### Main Components

| Item | Responsibility |
| --- | --- |
| `BuildPromptString` | Header + task + footer. |
| `cotHeader`, `cotFooter` | The universal preamble and closing reminder. |
| `getTaskBody` | Picks the task text and prepends the shared blocks. |
| `taskBodies` | Map from question type to a function returning its text. |
| `buildFullPromptWithContext` | Context + reasoning prompt, used by every model-calling handler. |

### Data In, Data Out

In: the question, its classification, whether source code is available, and the code-to-AST ratio. Out: one prompt string.

## Key Algorithms

### Telling the model what it actually has

**The problem.** If only some chunks include real code, a model that assumes all of them do will invent bodies for the rest.

**The approach.** The header states which situation applies. With source, it says that source blocks are ground truth and metadata is only structural hints. Without source, it says that only metadata is available and that implementation details must not be invented. `hasSourceCode` checks the context to decide.

**Why this way.** The instruction matches the evidence. The same honesty contract then makes the model label each claim, so a reader can see which parts are solid.

## Configuration

`retrievalConfig.code_to_ast_ratio` changes the percentage quoted in the header. The MCP `include_cot` option turns the reasoning parts off while keeping the context.

## Usage Examples

With reasoning off (`query_without_cot`), the reply is only the context block. With it on, the reply ends with text like:

```text
=== CHAIN-OF-THOUGHT INSTRUCTIONS ===
Step 1 — INVENTORY ...
Step 2 — EVIDENCE ...
Step 3 — GAPS ...
Step 4 — ANSWER ...
```

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| The instructions are suggestions, not guarantees. | A model may ignore them. | Check cited lines. |
| Tags may be missing in a reply. | Models sometimes skip `<answer>`. | The splitter treats an untagged reply as the answer. |
| The task bodies are long English text in code. | They are string constants. | Edit with care. |

## Related Docs

- [query Overview](Overview.md): where prompts are used.
- [Llm](../Llm.md): the context block.
- [MCP Server And Tools](../mcp/Server-And-Tools.md): the `include_cot` switch.
