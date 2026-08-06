# Welcome to Eulix! 🚀

> **Turn your codebase into something you can talk to.**

If you are looking for a quick starting point beyond the main README, you are in the right place. This guide is designed to help you get `eulix` installed, indexed, and queried in minutes, while explaining how everything fits together under the hood.

---

## 🛠️ Quick Start Guide

### 1. Installation

Grab the latest release or build from source via the [GitHub repository](https://github.com/Nurysso/eulix).

Make sure you have your toolchain ready depending on how you plan to run it:

* **Go** (for the orchestrator/CLI)
* **Rust** (for the static analyzer parser)
* **Python + PyTorch** (for the embedder)

### 2. Initialize in Your Project

Navigate to the root directory of any repository you want to analyze and run:

```bash
eulix init

```

This generates a local configuration file tailored to your project.

### 3. Analyze and Index

Run the static analysis engine to parse symbols, build call graphs, and map control flow:

```bash
eulix analyze

```

*Depending on your codebase size, the Rust parser moves at roughly 26 million lines per minute, parallelized across your CPU cores.*

### 4. Start Chatting

Launch the interactive terminal UI (TUI) to begin asking questions:

```bash
eulix chat

```

---

## 💡 Example Queries to Try

Because Eulix uses structural retrieval (combining exact symbol lookup, BM25, semantic vectors, and call graph expansion) rather than relying purely on embedding proximity, you can ask deeper architectural questions:

* **Debugging & Call Stacks:**
> *"What calls foo function, and what breaks if I change its signature?"*


* **Onboarding:**
> *"Trace foo request end-to-end through the module."*


* **Security & Auditing:**
> *"Find every caller of foo sensitive authentication method."*


* **Refactoring:**
> *"Show me the blast radius of modifying foo base class method."*



---

## 📂 Architecture Overview

Eulix is split into three core binaries working together:

| Component | Language | Role |
| --- | --- | --- |
| **`eulix`** | Go | The orchestrator: CLI, config, retrieval pipeline, LLM integration, and TUI. |
| **`eulix_parser`** | Rust | The static analyzer: handles symbols, call graphs, control flow, and complexity metrics. |
| **`eulix_embed`** | Python | The embedder: transformer models via PyTorch (with CUDA/ROCm support). |

---

## ⚙️ Configuration & Tips

```toml
# Eulix Configuration
[project]
path = "." # keep this as dot
Max_Lines = 300 # sets max lines to be added in retrieval after hydration
DebugConfig = false # debug infos

[parser]
threads = 4 # use it to increase parsing speed
prismVersion = 2 # Prism is eulix Call graph approximation algorithm, v1 is basic and tested well, v2 adds inheritance tracking and more accurate results

[embeddings]
model = "microsoft/codebert-base" # model to embedd file(kb.json) prepared by eulix_parser
dimension = 768 # model dimensions, exists to make sure query and embedding use same model and dim. 

[llm]
local = false # switch between local or cloud
provider = "fireworks" # llm provider
model = "accounts/fireworks/models/deepseek-v4-pro" # llm model name
max_tokens = 10000 # max tokens to be used for answering
temperature = 0.1 # keep this low so that llm dont make answer
# baseURL = "http://localhost:11434"

# Need to work on cache features
[cache]
[cache.redis]
enabled = false
url = "redis://localhost:6379"
ttl_hours = 6

[cache.sql]
enabled = true
driver = "sqlite"
dsn = ".eulix/history.db"

[checksum]
change_threshold = 0.10 # triggers analyze pipeline when codebase is changed after this percentage
force_reanalyze_threshold = 0.30 # force re-analyze when code base is changed significantly
```

* **Local-First Privacy:** Your code stays local by default. If you want to connect an external LLM (OpenAI, Anthropic, Gemini, etc.), it's a simple config line switch—entirely opt-in.
* **Speeding Up Cold Starts:** The initial query can take a few seconds while PyTorch initializes and warms up the cache. To keep retrieval blazing fast (under 50ms), run `eulix_embed serve` as a daemon or fire a quick warm-up query at startup.
* **Supported Languages:** Python, Go, C, C++, Rust, and TypeScript are fully stable.

---

## 🤝 Contributing & Feedback

Since Eulix is currently in **beta**, your feedback shapes the roadmap directly!

* Found a bug or rough edge? Open an issue on [GitHub](https://github.com/Nurysso/eulix/issues).
* Want to contribute? Docs contributions, bug reports, and hardware benchmark numbers (especially embedding speeds on different GPUs) are always welcome.
