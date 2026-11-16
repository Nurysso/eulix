# Walkthrough scene 3: ask eulix from the CLI and step through the ranked
# chunks (each one: file, line range, score). `query` sends nothing to a
# model; it prints the context an LLM would get. A `less` search for the
# chunk headers jumps from one chunk to the next; paging would only show the
# first chunk's code.
Hide
Type 'cd "$WORK/flask" && clear'
Enter
Show

Sleep 700ms
Type 'eulix query "@Q_CLI@" | less -R'
Sleep 400ms
Enter
Wait+Screen@120s /Context:/
Sleep 3s
Type '/^\[[0-9]'
Sleep 500ms
Enter
Sleep 2s
Type "n"
Sleep 1.6s
Type "n"
Sleep 1.6s
Type "n"
Sleep 1.6s
Type "n"
Sleep 1.6s
Type "n"
Sleep 2s
Type "q"
Sleep 1s
