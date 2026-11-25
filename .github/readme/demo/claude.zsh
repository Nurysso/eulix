# Sourced (off camera) by teaser.tape and sidebyside.tape.
#
# Runs Claude Code the way a new user would see it: no personal plugins,
# hooks or claude.ai connectors (user settings and other MCP configs are
# skipped), default permission mode, and eulix as the only MCP server, read
# from the .mcp.json that `claude mcp add --scope project` wrote. The eulix
# MCP tools are pre-allowed (whole server, like the benchmark's eulix arm) so no
# permission prompt interrupts the take, whichever eulix tool the task reaches for.
# The theme is set per take (CLIP_SCHEME comes from record.sh): the ANSI
# themes draw with the terminal's colors, so the demo themes apply. Skipping
# user settings would otherwise leave Claude on its default RGB theme, whose
# inline-code color is unreadable on the light background.
# `claude mcp ...` passes through unchanged so it can be run on camera.
claude() {
  if [[ $1 == mcp ]]; then
    command claude "$@"
  else
    command claude --setting-sources project,local --strict-mcp-config \
      --mcp-config .mcp.json --permission-mode default \
      --settings "{\"theme\":\"${CLIP_SCHEME:-dark}-ansi\"}" \
      --allowedTools mcp__eulix "$@"
  fi
}
