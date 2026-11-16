# README teaser: Claude Code asks eulix one question over MCP and answers
# from the ranked context. record.sh speeds up the wait and adds the badge.
#
# Startup stays off camera: the trust prompt shows the full folder path and
# defaults to "No, exit" (Down + Enter picks "Yes"; when the folder is
# already trusted the two keys do nothing).
Hide
Type 'source "$REPO/.github/readme/demo/claude.zsh" && cd "$WORK/flask" && clear'
Enter
Type "claude"
Enter
Sleep 8s
Down
Sleep 300ms
Enter
Wait+Screen@60s /Claude Code v/
Sleep 3s
Show

Sleep 800ms
Type "@Q_AGENT@"
Sleep 600ms
Enter
# Claude Code prints "✻ <verb> for 16s · done <time>" when the turn ends.
Wait+Screen@240s / for \d+(m \d+)?s · done/
Sleep 4s

Hide
Ctrl+C
Sleep 500ms
Ctrl+C
Sleep 1s
