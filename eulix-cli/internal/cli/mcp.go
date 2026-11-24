// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package cli provides the command-line interface implementation for EULIX.

package cli

import (
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"eulix/internal/kbfile"
	"eulix/internal/mcp"
	"eulix/internal/utils"

	"github.com/spf13/cobra"
)

var (
	mcpIdleTimeout time.Duration
	mcpDefaultCoT  bool
	mcpRepoPath    string
	mcpLegacyTools bool
)

var mcpCmd = &cobra.Command{
	Use:   "mcp [path]",
	Short: "[BETA] Start Model Context Protocol (MCP) server for AI code editors",
	Long: `Starts a Model Context Protocol (MCP) server over standard I/O (stdin/stdout).
AI code editors (Cursor, Windsurf, Claude Desktop, VS Code Cline) can connect to this
server to query codebases efficiently.

The MCP server can run globally across multiple repositories. Started without a repository,
it lists a 'set_repo_path' tool the agent uses to choose one. Started on a repository (an
argument or --repo), it does not list that tool; the 'repo_path' argument of each tool can
still switch.

Unlike the one-off 'query' command, the MCP server keeps the knowledge base and
ContextBuilder cached in memory across queries for near-instant responses, releasing
them only after a configurable period of inactivity.

Tools:
  - callers / callees: exact call sites from the call graph, one line each
  - definition: location, signature, docstring and a short body of a symbol
  - trace: the call path from one function to another, then the code for it
  - search: ranked code for a concept, within a token budget ([mcp] reply_tokens)

--legacy-tools (or EULIX_MCP_LEGACY_TOOLS=1) also lists the free-text tools of
earlier releases, query, query_with_cot and query_without_cot, which the
retrieval benchmark uses. Every listed tool costs the agent tokens on every turn.

Every reply starts with "answer: ... · complete: yes|no|n/a". Use
'eulix mcp install claude|cursor|cline' to register the server with an agent.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		targetRepo := mcpRepoPath
		if len(args) > 0 {
			targetRepo = args[0]
		} else if targetRepo == "" {
			// If cwd is an initialized eulix project, default to it
			if cwd, err := os.Getwd(); err == nil {
				kbPath := filepath.Join(cwd, utils.EulixDir, kbfile.KBFile)
				if _, err := os.Stat(kbPath); err == nil {
					targetRepo = cwd
				}
			}
		}

		// Cancel on Ctrl-C / SIGTERM so the server shuts down and frees memory.
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		srv := mcp.NewServer(
			nil, nil, "",
			mcp.WithInitialRepo(targetRepo),
			mcp.WithIdleTimeout(mcpIdleTimeout),
			mcp.WithDefaultCoT(mcpDefaultCoT),
			mcp.WithLegacyTools(mcpLegacyTools || os.Getenv(mcp.LegacyToolsEnv) == "1"),
		)

		return srv.Serve(ctx)
	},
}

func init() {
	mcpCmd.Flags().StringVar(&mcpRepoPath, "repo", "", "Initial repository path to query (optional; can also be set dynamically by AI agents)")
	mcpCmd.Flags().DurationVar(&mcpIdleTimeout, "timeout", mcp.DefaultIdleTimeout, "Idle timeout before releasing context builder from memory (e.g. 5m, 10m)")
	mcpCmd.Flags().BoolVar(&mcpDefaultCoT, "cot", false, "Default to Chain-of-Thought headers when 'include_cot' is not specified")
	mcpCmd.Flags().BoolVar(&mcpLegacyTools, "legacy-tools", false,
		"Also list the legacy free-text tools (query, query_with_cot, query_without_cot); same as "+mcp.LegacyToolsEnv+"=1")
}
