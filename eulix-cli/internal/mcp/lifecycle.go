package mcp

import (
	"context"
	"errors"
	"io"
	"os"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// =========== Lifecycle ==========

// Serve runs the server until the client disconnects (stdin EOF) or ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	defer func() { _ = s.Close() }()

	repo := s.GetActiveRepo()
	if repo == "" {
		repo = "none (awaiting 'set_repo_path', 'repo_path' parameter, or client roots)"
	}
	s.log.Info("starting Eulix MCP server",
		"active_repo", repo, "idle_timeout", s.idleTimeout, "default_cot", s.defaultCoT)

	var transport mcpsdk.Transport = &mcpsdk.StdioTransport{}
	if s.in != nil || s.out != nil {
		in, out := s.in, s.out
		if in == nil {
			in = os.Stdin
		}
		if out == nil {
			out = os.Stdout
		}
		transport = &mcpsdk.IOTransport{
			Reader: io.NopCloser(in),
			Writer: nopWriteCloser{out},
		}
	}

	err := s.mcp.Run(ctx, transport)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		return err
	}
	s.log.Info("server stopped")
	return nil
}

// Close stops background timers and releases the router. It is safe to call repeatedly.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
	if s.router == nil {
		return nil
	}
	err := s.router.Close()
	s.router = nil
	return err
}
