package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"eulix/internal/config"
	"eulix/internal/kbfile"
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
	canonicalPath, err := filepath.EvalSymlinks(absPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("repository path does not exist: %s", absPath)
	case err != nil:
		return "", fmt.Errorf("cannot resolve repository path %s: %w", absPath, err)
	}
	absPath = canonicalPath

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
	kbPath := filepath.Join(eulixDir, kbfile.KBFile)
	if _, err := os.Stat(kbPath); errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("project at %q is not initialized by Eulix (no knowledge base found at %s). "+
			"To index it (no prompts, no Python), run in the project root: eulix init --yes --engine ehnas --analyze",
			absPath, kbPath)
	}

	if s.activeRepo == absPath && s.router != nil && (s.activeInfo == nil || os.SameFile(fi, s.activeInfo)) {
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
	// No history cache: MCP never reads or writes chat history, and an open
	// history.db stays locked, breaking 'eulix chat' and 'eulix history'.
	router, err := query.QueryTrafficController(eulixDir, cfg, llmClient, nil)
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
	s.activeInfo = fi
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
	return s.executeQuery(ctx, repoPath, queryText, known, queryOpts{cot: includeCoT, route: true})
}

// queryOpts are the per-call knobs of the free-text tools.
type queryOpts struct {
	cot       bool
	maxTokens int  // 0 = sized by question type
	route     bool // answer callers / definition questions from the index first
	fresh     bool // resend chunks already sent this session
	answer    string
	// legacy reproduces the pre-budget output exactly (router-sized context,
	// no routing, no header): the retrieval benchmark compares versions on it.
	legacy bool
	// lean sends the best chunk in full and the others one line each (see
	// leanSearch). The cascade still scores the full rendering.
	lean bool
}

// executeQuery is the body of 'query' and 'search'. With route set, a
// question the index answers exactly (who calls X, where is X defined) is
// answered from the call graph or definitions and never reaches ranked
// retrieval; everything else runs the cascade with a budget sized by
// question type.
func (s *Server) executeQuery(ctx context.Context, repoPath, queryText string, known []string, o queryOpts) (string, error) {
	var out string
	err := s.withRepo(ctx, repoPath, func() error {
		ro := retrieveOpts{cot: o.cot, fresh: o.fresh, answer: o.answer}
		if s.router != nil && !o.legacy {
			ac := s.router.ClassifyForAgent(queryText)
			if o.route {
				if text, ok := s.answerExactLocked(ac, o); ok {
					out = text
					return nil
				}
				ro.want = ac.Intent
			}
			if ro.answer == "" {
				ro.answer = string(ac.Intent)
			}
			ro.budget = s.budgetFor(ac.Intent, o.maxTokens)
		}
		result, trace, err := s.retrieveWith(ctx, queryText, known, ro)
		if err != nil {
			return err
		}
		if o.lean {
			result = leanSearch(result, focusTerms(queryText))
		}
		if !o.legacy {
			// Only chunks sent in full count as sent.
			s.markSentLocked(result)
		}
		s.log.Info("query served", "repo", s.activeRepo, "retrieval", trace.String())
		s.debugLocked(queryText, result, o.cot, trace.String())
		out = result
		return nil
	})
	return out, err
}

// answerExactLocked answers a callers/callees/definition question from the
// index. ok is false when the question is another kind or the symbol is not
// in the index, so the caller falls through to ranked retrieval.
func (s *Server) answerExactLocked(ac query.AgentClassification, o queryOpts) (string, bool) {
	if ac.Symbol == "" || o.cot {
		return "", false
	}
	switch ac.Intent {
	case query.IntentCallersQ, query.IntentCalleesQ:
		rep, err := s.router.Calls(ac.Symbol, ac.Intent == query.IntentCalleesQ, 1, callSiteLimit)
		if err != nil || !rep.Found {
			return "", false
		}
		return renderCalls(rep), true
	case query.IntentDefinitionQ:
		defs, _, err := s.router.Definitions(ac.Symbol, definitionBodyLines, 3)
		if err != nil || len(defs) == 0 {
			return "", false
		}
		return renderDefinitions(ac.Symbol, defs, nil), true
	}
	return "", false
}

// withRepo binds repoPath (when given), checks the server is ready, and runs
// fn under s.mu. Every tool goes through it, so switching and querying can
// never interleave.
func (s *Server) withRepo(ctx context.Context, repoPath string, fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(repoPath) != "" {
		if _, err := s.setRepoPathLocked(repoPath); err != nil {
			return err
		}
	}

	if s.router == nil {
		return errors.New("repository path is not set. Provide the absolute path to your repository " +
			"via the 'repo_path' argument or the 'set_repo_path' tool. The project must be initialized " +
			"in its root with: eulix init --yes --engine ehnas --analyze")
	}

	// A replaced folder would serve another repository's files against this index.
	if s.activeInfo != nil {
		if fi, err := os.Lstat(s.activeRepo); err != nil || !os.SameFile(s.activeInfo, fi) {
			return fmt.Errorf("repository folder %s changed after it was bound; call set_repo_path again", s.activeRepo)
		}
	}

	// Honor cancellation that happened while we waited for the lock.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fn(); err != nil {
		return err
	}
	s.touchLocked()
	return nil
}

// debugLocked writes the debug log entry when [project] DebugConfig is on.
func (s *Server) debugLocked(queryText, result string, cot bool, trace string) {
	if s.cfg != nil && s.cfg.Project.DebugConfig && s.eulixDir != "" {
		if err := s.writeDebugLogLocked(queryText, result, cot, trace); err != nil {
			s.log.Debug("failed to write debug log", "err", err)
		}
	}
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
	f, err := utils.OpenEulixFile(s.eulixDir, filepath.Join("debug", "mcp-query-debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY)
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
	return uriPath(rawURI, runtime.GOOS == "windows")
}

func uriPath(rawURI string, windows bool) string {
	raw := strings.TrimSpace(rawURI)
	if !strings.HasPrefix(raw, "file://") {
		return raw
	}
	// url.Parse rejects the encoded \\?\ prefix in the host position.
	if windows && len(raw) >= 11 && strings.EqualFold(raw[:11], "file://%3F/") {
		raw = "file:///%3F/" + raw[11:]
	}
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		p := u.Path
		if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
			p = "//" + u.Host + "/" + strings.TrimPrefix(p, "/")
		} else if windows {
			switch {
			case strings.HasPrefix(p, "/?/"):
				// file:///%3F/C:/repo is the URI form of \\?\C:\repo.
				p = "/" + p
			case len(p) >= 3 && p[0] == '/' && p[2] == ':':
				// VS Code also percent-encodes the drive colon.
				p = p[1:]
			}
		}
		return filepath.FromSlash(p)
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
