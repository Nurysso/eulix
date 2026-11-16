# Walkthrough scene 4: register eulix with Claude Code. The agent question
# that follows in the video is the dark teaser take (teaser.tape), reused so
# the demo costs one Claude query per color scheme.
Hide
Type 'cd "$WORK/flask" && clear'
Enter
Show

Sleep 700ms
Type "claude mcp add --scope project eulix -- eulix mcp"
Sleep 400ms
Enter
Wait+Screen@30s /Added/
Sleep 3s
