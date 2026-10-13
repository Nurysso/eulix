package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// handleSetRepo implements the 'set_repo_path' tool.
func (s *Server) handleSetRepo(_ context.Context, _ *mcpsdk.CallToolRequest, a setRepoArgs) (*mcpsdk.CallToolResult, any, error) {
	msg, err := s.SetRepoPath(a.RepoPath)
	if err != nil {
		return nil, nil, err // the SDK turns this into an IsError tool result
	}
	return textResult(msg), nil, nil
}

// toolQuery is the shared body of all query tools.
func (s *Server) toolQuery(ctx context.Context, repoPath, q string, known []string, cot bool) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(q) == "" {
		return nil, nil, errors.New("argument 'query' must be a non-empty string")
	}
	out, err := s.ExecuteQuery(ctx, repoPath, q, known, cot)
	if err != nil {
		return nil, nil, fmt.Errorf("query execution failed: %w", err)
	}
	return textResult(out), nil, nil
}

func (s *Server) promptHandler(cot bool) mcpsdk.PromptHandler {
	return func(ctx context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
		args := req.Params.Arguments
		q := strings.TrimSpace(args["query"])
		if q == "" {
			return nil, errors.New("missing required argument 'query'")
		}
		out, err := s.ExecuteQuery(ctx, args["repo_path"], q, splitList(args["known_symbols"]), cot)
		if err != nil {
			return nil, fmt.Errorf("failed to generate prompt: %w", err)
		}
		return &mcpsdk.GetPromptResult{
			Description: "Eulix context for: " + q,
			Messages: []*mcpsdk.PromptMessage{
				{Role: "user", Content: &mcpsdk.TextContent{Text: out}},
			},
		}, nil
	}
}

func textResult(text string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}}}
}
