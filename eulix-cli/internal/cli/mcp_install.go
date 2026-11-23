// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// agentHint is the line that makes agents use Eulix. In the agent benchmark,
// Claude called Eulix in 5 of 50 sessions without it and 40 of 50 with it.
const agentHint = "This repository is indexed by Eulix (MCP server `eulix`). For who calls what, use its " +
	"`callers` / `callees` tools; for where a symbol is defined, `definition`; for how one function reaches " +
	"another across files, `trace`; for how a feature works, `search` (pass exact symbol names in " +
	"known_symbols). Each reply starts with a `complete: yes|no` line: when it says yes, do not re-check " +
	"with grep. Use grep for literal strings, config values and log messages."

const (
	hintBegin = "<!-- eulix:begin -->"
	hintEnd   = "<!-- eulix:end -->"
)

var mcpInstallCmd = &cobra.Command{
	Use:   "install <claude|cursor|cline> [path]",
	Short: "Register the Eulix MCP server with an agent and tell the agent when to use it",
	Long: `Registers 'eulix mcp' with an agent for the project at [path] (default: current
directory) and adds a short instruction telling the agent which questions Eulix
answers better than grep. Both steps are idempotent; re-running updates them.

  claude  writes .mcp.json and a block in CLAUDE.md
  cursor  writes .cursor/mcp.json and .cursor/rules/eulix.mdc
  cline   writes .clinerules/eulix.md and prints the server entry to paste into
          Cline's MCP settings (Cline keeps them outside the project)`,
	Args:      cobra.RangeArgs(1, 2),
	ValidArgs: []string{"claude", "cursor", "cline"},
	RunE: func(cmd *cobra.Command, args []string) error {
		root := "."
		if len(args) == 2 {
			root = args[1]
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		bin, err := os.Executable()
		if err != nil {
			bin = "eulix"
		}
		out := cmd.OutOrStdout()
		switch strings.ToLower(args[0]) {
		case "claude":
			if err := mergeMCPServers(filepath.Join(abs, ".mcp.json"), bin, abs); err != nil {
				return err
			}
			if err := upsertHintBlock(filepath.Join(abs, "CLAUDE.md"), agentHint); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Registered eulix in %s and added the usage hint to %s\n",
				filepath.Join(abs, ".mcp.json"), filepath.Join(abs, "CLAUDE.md"))
		case "cursor":
			if err := mergeMCPServers(filepath.Join(abs, ".cursor", "mcp.json"), bin, abs); err != nil {
				return err
			}
			rule := "---\ndescription: When to use the Eulix MCP server\nalwaysApply: true\n---\n\n" + agentHint + "\n"
			if err := writeFileMkdir(filepath.Join(abs, ".cursor", "rules", "eulix.mdc"), rule); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Registered eulix in %s and wrote %s\n",
				filepath.Join(abs, ".cursor", "mcp.json"), filepath.Join(abs, ".cursor", "rules", "eulix.mdc"))
		case "cline":
			if err := writeFileMkdir(filepath.Join(abs, ".clinerules", "eulix.md"), agentHint+"\n"); err != nil {
				return err
			}
			entry, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"eulix": serverEntry(bin, abs)}}, "", "  ")
			_, _ = fmt.Fprintf(out, "Wrote %s. Add this to Cline's MCP settings (MCP Servers → Configure):\n%s\n",
				filepath.Join(abs, ".clinerules", "eulix.md"), entry)
		default:
			return fmt.Errorf("unknown agent %q: use claude, cursor or cline", args[0])
		}
		if _, err := os.Stat(filepath.Join(abs, ".eulix")); errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintln(out, "Note: this project is not indexed yet; run 'eulix analyze' in it before the agent queries.")
		}
		return nil
	},
}

func init() {
	mcpCmd.AddCommand(mcpInstallCmd)
}

// serverEntry is the stdio server definition every client understands.
func serverEntry(bin, repo string) map[string]any {
	return map[string]any{"command": bin, "args": []string{"mcp", repo}}
}

// mergeMCPServers adds or replaces the "eulix" entry under "mcpServers" in
// path, keeping every other server and top-level key as it was.
func mergeMCPServers(path, bin, repo string) error {
	doc := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &doc); err != nil {
				return fmt.Errorf("%s is not valid JSON, leaving it untouched: %w", path, err)
			}
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["eulix"] = serverEntry(bin, repo)
	doc["mcpServers"] = servers
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeFileMkdir(path, string(out)+"\n")
}

// upsertHintBlock writes hint between eulix markers in path, replacing an
// earlier block and keeping everything else in the file.
func upsertHintBlock(path, hint string) error {
	block := hintBegin + "\n" + hint + "\n" + hintEnd
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	text := string(data)
	if i := strings.Index(text, hintBegin); i >= 0 {
		if j := strings.Index(text[i:], hintEnd); j >= 0 {
			return writeFileMkdir(path, text[:i]+block+text[i+j+len(hintEnd):])
		}
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" {
		text += "\n"
	}
	return writeFileMkdir(path, text+block+"\n")
}

func writeFileMkdir(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
