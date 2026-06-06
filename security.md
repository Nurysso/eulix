# Security Policy

## Supported Versions

Eulix is currently in **beta** (pre-1.0). Security updates are provided for the latest release only.

| Version    | Supported     |
| ---------- | ------------- |
| 0.x (beta) | (latest only) |

Stable release (1.0) expected **late June / July 2026** – after that, we will maintain the last two minor versions.

---

## Reporting a Vulnerability

**Do NOT report security issues via public GitHub issues.**

Please report vulnerabilities to: **[nurysso@proton.me](mailto:nurysso@proton.me)**

### What to include

- **Description** of the vulnerability
- **Steps to reproduce** (minimal, concrete)
- **Potential impact** – what could an attacker do?
- **Environment** – OS, versions, configuration
- **Suggested fix** (if you have one)

### What to expect

1. **Acknowledgement** within 48 hours
2. **Initial assessment** within 5 days
3. **Fix timeline** – critical issues patched within 2 weeks; lower priority within next release
4. **Disclosure** – coordinated release with advisory

We will keep you informed throughout the process.

## Security Considerations

### Code Privacy

**Eulix is designed to keep your code local:**

- All parsing, embedding, and LLM inference happens on your machine
- No code is sent to external APIs by default
- The only external calls are for downloading models (Hugging Face) – optional and configurable

**If you use external LLM APIs** (e.g., OpenAI, Anthropic):

- Code snippets are sent as part of prompts
- Use with proprietary/regulated code only if compliant with your policies
- Consider using local models (Ollama, LM Studio) instead

### Supply Chain Security

We take supply chain risks seriously:

**Go dependencies:** `go.sum` pins checksums – verify with `go mod verify`

**Rust dependencies:** `Cargo.lock` pins versions – review with `cargo audit`

**Python dependencies:** `requirements.txt` + `uv.lock` (when added) – use `safety check`

**To audit dependencies:**

```bash
# Go
go list -m all | xargs go mod why

# Rust
cargo audit

# Python
pip-audit
```

### Binary Integrity

Releases (when available) will include:

- Checksums (SHA256)
- GPG signatures (key ID to be published)

Verify before running:

```bash
sha256sum eulix-linux-amd64
gpg --verify eulix-linux-amd64.asc
```

### Reporting Process Details

```
[You] → Report to nurysso@proton.me
   ↓
[Maintainer] → Acknowledge (48h), assess (5 days)
   ↓
[Maintainer + You] → Collaborate on fix (if needed)
   ↓
[Maintainer] → Prepare patch, test across supported versions
   ↓
[Coordinated Disclosure] → Release advisory + patch
```

---

## Known Limitations (Security‑Relevant)

### Call Graph Approximation (PRISM)

Eulix uses **PRISM** – an approximate call graph algorithm. This may:

- Miss indirect calls (function pointers, reflection, dynamic dispatch)
- Produce false positives (similar function names across packages)
- Not fully capture all code paths for security analysis

**Recommendation:** Use Eulix as a **starting point** for security reviews, not as the sole source of truth. Manual verification is recommended for critical paths.

### Embedding Storage

Embeddings are stored as binary files (`.bin`) in `.eulix/`. These contain vector representations of your code – not raw source, but could potentially leak structural information.

**Best practices:**

- Treat `.eulix/` as sensitive (add to `.gitignore`)
- Use `.euignore` to exclude proprietary files from analysis
- Delete `.eulix/` when sharing code (or regenerate on target machine)

### Local Model Security

If you download models from Hugging Face:

- Models may contain arbitrary code (PyTorch pickle files)
- Only use trusted models (official or widely used)
- Consider running in isolated environment (container, VM)

**Recommended models:**

- `sentence-transformers/all-MiniLM-L6-v2`
- `BAAI/bge-small-en-v1.5`
- `Qwen/Qwen2.5-7B-Instruct` (GGUF format, not pickle)

### Cache Poisoning

Eulix caches query results (Redis/SQL). In shared environments:

- Cache keys include project checksum – mitigates cross-project pollution
- Redis should be authentication-protected
- SQLite caches are local only (no network exposure)

---

## Safe Usage Guide

### For Local Development (Default)

```bash
# All local – safest
eulix analyze
eulix chat  # uses Ollama/local LLM but can also use Cloud llm dependig upon eulix.toml config
```

### For CI/CD Pipelines

```bash
# Analyze only (no LLM)
eulix analyze

# Or with container isolation
docker run --rm -v $(pwd):/code eulix analyze
```

### For Teams Sharing Analysis

**Option 1: Regenerate on each machine**

```bash
# In CI or on each developer machine
eulix analyze  # each machine parses locally
```

**Option 2: Use eulix server**

> Currently working, it will be a basic server that communicates via http.

---

## Out of Scope (What We Don't Cover)

- **Network security** – if you expose Eulix as a service, secure it yourself
- **Authentication/authorization** – Eulix has no built-in access control
- **Encryption at rest** – `.eulix/` files are plaintext; encrypt at filesystem level if needed
- **Side-channel attacks** – not relevant for local analysis tool

---

## Responsible Disclosure Recognition

We thank researchers who report vulnerabilities responsibly. With your permission, we will acknowledge you in the advisory and `SECURITY.md` (unless you prefer anonymity).

**Hall of Fame** (to be populated):

_No reports yet – be the first!_

---

## Updates to This Policy

This policy may change as Eulix evolves. Significant changes will be announced via:

- GitHub Releases

**Last updated:** June 2026

---

**Report vulnerabilities to:** [nurysso@proton.me](mailto:nurysso@proton.me)
**PGP Key:** (to be published before stable release)

Thank you for helping keep Eulix secure! 🔒
