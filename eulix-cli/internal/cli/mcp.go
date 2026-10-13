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

	"eulix/internal/mcp"
	"eulix/internal/utils"

	"github.com/spf13/cobra"
)

var (
	mcpIdleTimeout time.Duration
	mcpDefaultCoT  bool
	mcpRepoPath    string
)

var mcpCmd = &cobra.Command{
	Use:   "mcp [path]",
	Short: "[BETA] Start Model Context Protocol (MCP) server for AI code editors",
	Long: `Starts a Model Context Protocol (MCP) server over standard I/O (stdin/stdout).
AI code editors (Cursor, Windsurf, Claude Desktop, VS Code Cline) can connect to this
server to query codebases efficiently.

The MCP server can run globally across multiple repositories. You can specify a repository
path via argument or flag, or the connected AI agent can specify/switch repositories dynamically
using the 'set_repo_path' tool or the 'repo_path' argument in queries.

Unlike the one-off 'query' command, the MCP server keeps the knowledge base and
ContextBuilder cached in memory across queries for near-instant responses, releasing
them only after a configurable period of inactivity.

Communication modes:
  - query: searches the codebase with optional 'include_cot' flag
  - query_with_cot: includes Chain-of-Thought reasoning headers
  - query_without_cot: clean context without Chain-of-Thought headers`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		targetRepo := mcpRepoPath
		if len(args) > 0 {
			targetRepo = args[0]
		} else if targetRepo == "" {
			// If cwd is an initialized eulix project, default to it
			if cwd, err := os.Getwd(); err == nil {
				kbPath := filepath.Join(cwd, utils.EulixDir, "kb.json")
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
		)

		return srv.Serve(ctx)
	},
}

func init() {
	mcpCmd.Flags().StringVar(&mcpRepoPath, "repo", "", "Initial repository path to query (optional; can also be set dynamically by AI agents)")
	mcpCmd.Flags().DurationVar(&mcpIdleTimeout, "timeout", mcp.DefaultIdleTimeout, "Idle timeout before releasing context builder from memory (e.g. 5m, 10m)")
	mcpCmd.Flags().BoolVar(&mcpDefaultCoT, "cot", false, "Default to Chain-of-Thought headers when 'include_cot' is not specified")
}
