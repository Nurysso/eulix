package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"eulix/internal/cache"
	"eulix/internal/config"
	"eulix/internal/llm"
	"eulix/internal/query"
	"eulix/internal/utils"
)

// ========== Repository & query logic ==========

// GetActiveRepo returns the currently active repository path.
func (s *Server) GetActiveRepo() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeRepo
}

// SetRepoPath binds or switches the server to a project repository. If the project has
// not been initialized with Eulix it returns an actionable error.
func (s *Server) SetRepoPath(repoPath string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setRepoPathLocked(repoPath)
}

func (s *Server) setRepoPathLocked(repoPath string) (string, error) {
	clean := strings.TrimSpace(repoPath)
	if clean == "" {
		return "", errors.New("repository path cannot be empty")
	}

	absPath, err := filepath.Abs(clean)
	if err != nil {
		return "", fmt.Errorf("invalid repository path %q: %w", repoPath, err)
	}

	fi, err := os.Stat(absPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("repository path does not exist: %s", absPath)
	case err != nil:
		return "", fmt.Errorf("cannot access repository path %s: %w", absPath, err)
	case !fi.IsDir():
		return "", fmt.Errorf("repository path is not a directory: %s", absPath)
	}

	eulixDir := filepath.Join(absPath, utils.EulixDir)
	kbPath := filepath.Join(eulixDir, "kb.json")
	if _, err := os.Stat(kbPath); errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("project at %q is not initialized by Eulix (no knowledge base found at %s). "+
			"Please run 'eulix analyze' in the project root in your terminal to generate it before querying",
			absPath, kbPath)
	}

	if s.activeRepo == absPath && s.router != nil {
		return "Repository is already set to: " + absPath, nil
	}

	// Build the new router BEFORE tearing down the old one, so a failure leaves the
	// server in its previous, working state.
	cfg, err := config.LoadFrom(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to load config from %s: %w", absPath, err)
	}
	llmClient, err := llm.MouthClient(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to initialize LLM client: %w", err)
	}
	cacheManager, _ := cache.CacheController(cfg)
	router, err := query.QueryTrafficController(eulixDir, cfg, llmClient, cacheManager)
	if err != nil {
		return "", fmt.Errorf("failed to initialize router for %s: %w", absPath, err)
	}

	if s.router != nil {
		s.log.Info("switching repository; freeing previous memory", "from", s.activeRepo, "to", absPath)
		if err := s.router.Close(); err != nil {
			s.log.Warn("failed to close previous router", "err", err)
		}
	}

	s.activeRepo = absPath
	s.eulixDir = eulixDir
	s.cfg = cfg
	s.router = router
	s.touchLocked()

	s.log.Info("active repository set", "repo", absPath)
	return "Active repository successfully set to: " + absPath, nil
}

// ExecuteQuery runs a query against the codebase. If repoPath is non-empty the server
// switches to that repository first. Switching and querying happen under one lock, so
// concurrent calls can never query the wrong repository.
func (s *Server) ExecuteQuery(ctx context.Context, repoPath, queryText string, known []string, includeCoT bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(repoPath) != "" {
		if _, err := s.setRepoPathLocked(repoPath); err != nil {
			return "", err
		}
	}

	if s.router == nil {
		return "", errors.New("repository path is not set. Provide the absolute path to your repository " +
			"via the 'repo_path' argument or the 'set_repo_path' tool. The project must be initialized " +
			"by running 'eulix analyze' in the project root in your terminal")
	}

	// Honor cancellation that happened while we waited for the lock.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	result, trace, err := s.retrieveLocked(ctx, queryText, known, includeCoT)
	if err != nil {
		return "", err
	}
	s.log.Info("query served", "repo", s.activeRepo, "retrieval", trace.String())

	if s.cfg != nil && s.cfg.Project.DebugConfig && s.eulixDir != "" {
		if err := s.writeDebugLogLocked(queryText, result, includeCoT, trace.String()); err != nil {
			s.log.Debug("failed to write debug log", "err", err)
		}
	}

	s.touchLocked()
	return result, nil
}

// ========== Idle handling ==========

// touchLocked records activity and (re)arms the idle timer. Caller must hold s.mu.
func (s *Server) touchLocked() {
	s.lastActivity = time.Now()
	if s.idleTimeout <= 0 {
		return
	}
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	s.idleTimer = time.AfterFunc(s.idleTimeout, s.onIdleTimeout)
}

func (s *Server) onIdleTimeout() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// The timer may have fired just before a query reset it; re-check real activity.
	if time.Since(s.lastActivity) < s.idleTimeout {
		return
	}
	if s.router == nil || !s.router.HasContextBuilder() {
		return
	}

	s.log.Info("inactivity timeout reached; releasing context builder", "timeout", s.idleTimeout)
	if err := s.router.CloseContextBuilder(); err != nil {
		s.log.Warn("failed to close context builder", "err", err)
		return
	}
	s.log.Info("context builder released; memory freed")
}

func (s *Server) writeDebugLogLocked(queryText, result string, includeCoT bool, trace string) error {
	logFile := filepath.Join(s.eulixDir, "debug", "mcp-query-debug.log")
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		return err
	}

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	sep := strings.Repeat("=", 80)
	_, werr := fmt.Fprintf(f,
		"\n%s\n=== MCP QUERY DEBUG ENTRY ===\nTimestamp: %s\nRepo: %s\nCoT: %v\nQuery: %s\nRetrieval: %s\nOutput:\n%s\n%s\n",
		sep, time.Now().Format("2006-01-02 15:04:05"), s.activeRepo, includeCoT, queryText, trace, result, sep)

	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// parseURIPath converts a file:// URI (as sent in MCP roots) into a local path.
func parseURIPath(rawURI string) string {
	raw := strings.TrimSpace(rawURI)
	if !strings.HasPrefix(raw, "file://") {
		return raw
	}
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		return filepath.FromSlash(u.Path)
	}
	return strings.TrimPrefix(raw, "file://")
}

// splitList parses a comma-separated argument (prompt arguments are plain strings).
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
