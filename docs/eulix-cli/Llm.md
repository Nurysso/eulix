# llm

The `llm` package sends a prompt to a language-model provider and returns the reply.

> **Crate / path:** `eulix-cli/internal/llm/client.go`  
> **Language:** Go  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

Eulix works with many model providers, but they speak only four different "languages" on the wire. This package hides that behind one method, `LlmResponse(prompt)`. You choose a provider and model in `eulix.toml`, and the client takes care of addresses, authentication and request formats.

## Why It Exists

Users want to run a small local model one day and a hosted one the next, without changing how Eulix works. A thin client with a table of known providers makes the switch a two-line configuration change.

## How It Works

`MouthClient(cfg)` checks the configuration and returns a `Client` with a 5-minute HTTP timeout. Validation rules:

- `anthropic` and `gemini` require an API key.
- `ollama` needs no key.
- Providers on the OpenAI-compatible list need no key, because local servers often have none.
- An unknown provider name is accepted if you set `baseURL`, and is treated as OpenAI-compatible. Otherwise the error lists the supported names.
- `model` must be set.

If `provider` is blank and `local` is true, the provider is Ollama.

Sending a prompt picks one of four request formats: Anthropic messages, Gemini content, Ollama chat, or OpenAI chat completions. The last format covers OpenAI itself and Groq, Together, Mistral, DeepSeek, LM Studio, vLLM, OpenRouter and Fireworks.

The address is resolved in layers: the `baseURL` and `endpoint` you configured win, then the table of known providers, then the OpenAI default. For example, Ollama is `http://localhost:11434/api/chat`, LM Studio is `http://localhost:1234/v1/chat/completions` and Anthropic is `https://api.anthropic.com/v1/messages`.

When debugging is on, every prompt is appended to `.eulix/debug/llmdebug.log` with the provider and model.

### Main Components

| Item | Responsibility |
| --- | --- |
| `MouthClient` | Validates the config and creates the client. |
| `LlmResponse` | Sends a prompt, returns the reply text. |
| `BuildFullPrompt` | Turns a retrieved `ContextWindow` and a question into the context part of a prompt. |
| `knownProviders` | The base URL and path for each named provider. |

### Data In, Data Out

In: a prompt string. Out: the reply text. `BuildFullPrompt` renders the retrieved chunks ahead of the question. It first prints a one-line summary (source chunks, metadata chunks, tokens, files), then a `SOURCE` section whose entries look like `[1] path:start-end (score 0.87)`, then a `METADATA` section for chunks that carry only signatures, and finally `QUERY` and the question. The reasoning instructions are added afterwards by the [query router](query/Prompts.md).

## Key Algorithms

### Adding a provider without code

**The problem.** New providers appear constantly, and most copy OpenAI's request format.

**The approach.** A provider is just a name. If it speaks the OpenAI format, adding it to the compatibility map and the defaults table is enough. If it is not in either, setting `baseURL` is enough. No new request code is needed.

**Why this way.** The cost of supporting another provider stays at one line, or zero.

## Configuration

See `[llm]` in [Config](Config.md). The API key can come from `api_key`, a provider-specific environment variable, or `LLM_API_KEY`.

## Usage Examples

```toml
[llm]
local = false
provider = "openrouter"
model = "anthropic/claude-sonnet-5-5"
```

With `OPENROUTER_API_KEY` set, the client sends OpenAI-format requests to `https://openrouter.ai/api/v1/chat/completions`.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Replies are not streamed. | `LlmResponse` waits for the complete response and returns a string. The chat screen therefore shows nothing until the model has finished. | None yet. |
| The package comment mentions only Anthropic and Ollama. | It is out of date; four formats are supported. | Trust this page. |
| Token counting is approximate. | Only a few providers use a real tokenizer; others use length divided by four. See [Utils](Utils.md). | Leave headroom in `max_tokens`. |
| One 5-minute timeout for everything. | Hard-coded. | None. |

## Related Docs

- [Config](Config.md): provider settings.
- [Prompts](query/Prompts.md): the reasoning instructions added around the context.
- [Tui](tui/Overview.md): where replies are displayed.
