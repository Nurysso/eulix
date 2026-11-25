# Eulix from the shell

Part of the `eulix` skill. Use it only when the MCP tools are not available: each `eulix query`
reloads the index, so past a few questions registering the MCP server (see
[setup.md](setup.md)) is cheaper.

```bash
cd /path/to/repo      # any sub-folder works; eulix walks up to .eulix/
script -qc 'eulix query "who calls applyBudget?"' /dev/null
```

Wrap it in `script -qc '…' /dev/null` when stdout is piped; without a TTY it can hang. Don't put
backticks in the question here: the shell runs them as a command. Code-shaped names
(`applyBudget`, `Server.run`, `run(`) are recognised without them.

- **Factual questions get a direct answer from the index:** "where is X defined?", "what calls X?", "what does X call?", entry points, dependencies, TODOs, file structure, metrics.
- **"What calls X?" and "what does X call?" print X's call graph:** its file, its callers, and its outbound calls as a nested tree. There is no `complete:` header and no line numbers; grep `X(` in the listed files for those.
- **For a path from A to B, ask "what does A call?"** and follow the tree; ask again from the deepest name if B isn't in it. "How does A reach B?" gets a context prompt, not a path.
- **Everything else prints retrieved context plus reasoning instructions.** Nothing is sent to a model; read the context yourself.
- Output starts with a stray escape sequence (`]11;?`) from the terminal query; ignore it.
- For call graphs ask "who calls `X`?", not "call graph of `X`": with a lowercase or `::` name the latter can look up the word "of".
- The phrasing rules in the skill's "Asking `search`" section apply here too.
