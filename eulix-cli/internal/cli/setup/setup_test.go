// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"eulix/internal/config"
)

func writeFile(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEnvFile(t *testing.T) {
	if env := LoadEnvFile(filepath.Join(t.TempDir(), ".env")); env.HasValues() || env.Changed() {
		t.Fatal("missing .env should load empty and unchanged")
	}

	path := filepath.Join(t.TempDir(), ".env")
	const original = "# comment\nA=1\n\nB = x=y \nnot a pair\nA=2\n"
	writeFile(t, path, original, 0o644)
	env := LoadEnvFile(path)
	if env.values["A"] != "2" || env.values["B"] != "x=y" || len(env.values) != 2 {
		t.Fatalf("parsed values = %v, want A=2 (last wins) and B=x=y", env.values)
	}

	env.Set("A", "2") // same value: not a change
	if env.Changed() {
		t.Fatal("setting an identical value marked the file changed")
	}
	if err := env.Save(); err != nil || readFile(t, path) != original {
		t.Fatalf("unchanged Save must not touch the file (err=%v)", err)
	}

	env.Set("C", "3")
	env.Set("B", "new")
	if err := env.Save(); err != nil {
		t.Fatal(err)
	}
	// Only B's line changes; comments, blanks, odd lines and untouched keys stay byte-for-byte.
	const want = "# comment\nA=1\n\nB=new\nnot a pair\nA=2\nC=3\n"
	if got := readFile(t, path); got != want {
		t.Fatalf("saved .env:\n%q\nwant:\n%q", got, want)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf(".env holds API keys, want mode 0600, got %o", info.Mode().Perm())
	}
}

func TestEuignoreSteps(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".euignore")
	if got := formatPatternList(readEuignorePatterns(path)); got != "  (none yet)" {
		t.Fatalf("missing file: got %q", got)
	}

	writeFile(t, path, "# comment\nnode_modules/\n\n*.log", 0o644) // no trailing newline
	if got := formatPatternList(readEuignorePatterns(path)); got != "  node_modules/\n  *.log" {
		t.Fatalf("pattern list = %q", got)
	}

	step := BuildEuignoreSteps(path)[0]
	if err := step.Apply("", nil, nil); err != nil || readFile(t, path) != "# comment\nnode_modules/\n\n*.log" {
		t.Fatalf("empty answer must leave the file alone (err=%v)", err)
	}
	if err := step.Apply(" dist/ ,, *.tmp,", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := readEuignorePatterns(path); strings.Join(got, " ") != "node_modules/ *.log dist/ *.tmp" {
		t.Fatalf("patterns after append = %q", got)
	}
}

// press feeds keys to the wizard the way bubbletea would.
func press(m *wizardModel, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "ctrl+u":
			msg = tea.KeyMsg{Type: tea.KeyCtrlU}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m.Update(msg)
	}
}

func (m *wizardModel) currentKey() string {
	if m.idx >= len(m.steps) {
		return ""
	}
	return m.steps[m.idx].Key
}

func TestWizardLocalFlow(t *testing.T) {
	cfg, env := config.DefaultConfig(), LoadEnvFile(filepath.Join(t.TempDir(), ".env"))
	m := newWizard(BuildConfigSteps(), cfg, env)
	m.Init()

	press(m, "enter", "enter", "enter") // edit config: Yes, threads: default, mode: Local
	press(m, "enter")                   // empty model name
	if m.err == nil || m.currentKey() != "llm_model_local" {
		t.Fatalf("empty model should be rejected and stay on the step, at %q err=%v", m.currentKey(), m.err)
	}
	press(m, "llama3.1:8b", "enter", "enter") // model, base URL: default
	// follow-up steps are spliced in before the already-queued run_analysis step
	if m.currentKey() != "run_analysis" {
		t.Fatalf("want run_analysis last, at %q", m.currentKey())
	}
	press(m, "n", "enter")

	if !m.done || m.cancelled || m.err != nil {
		t.Fatalf("done=%v cancelled=%v err=%v", m.done, m.cancelled, m.err)
	}
	if cfg.Parser.Threads != runtime.NumCPU() || !cfg.LLM.Local ||
		cfg.LLM.Model != "llama3.1:8b" || cfg.LLM.BaseURL != "http://localhost:11434" {
		t.Fatalf("config not applied: threads=%d llm=%+v", cfg.Parser.Threads, cfg.LLM)
	}
	if m.answers["run_analysis"] != "No" || env.Changed() {
		t.Fatalf("answers=%v env changed=%v", m.answers, env.Changed())
	}
}

func TestWizardRemoteFlow(t *testing.T) {
	cfg, env := config.DefaultConfig(), LoadEnvFile(filepath.Join(t.TempDir(), ".env"))
	cfg.LLM.BaseURL = "http://localhost:11434"
	m := newWizard(BuildConfigSteps(), cfg, env)
	m.Init()

	press(m, "enter", "ctrl+u", "0", "enter") // edit config: Yes, threads: 0
	if m.err == nil || m.currentKey() != "parser_threads" {
		t.Fatalf("threads=0 should be rejected, at %q err=%v", m.currentKey(), m.err)
	}
	press(m, "ctrl+u", "2", "enter")
	press(m, "down", "enter")             // mode: Remote
	press(m, "enter")                     // provider: first sorted option
	press(m, "claude-x", "enter")         // model
	press(m, "sk-test", "enter", "enter") // API key, run analysis: Yes

	if !m.done || m.err != nil {
		t.Fatalf("done=%v err=%v at %q", m.done, m.err, m.currentKey())
	}
	provider := providerNames()[0]
	if cfg.Parser.Threads != 2 || cfg.LLM.Local || cfg.LLM.BaseURL != "" ||
		cfg.LLM.Provider != provider || cfg.LLM.Model != "claude-x" {
		t.Fatalf("config not applied: threads=%d llm=%+v", cfg.Parser.Threads, cfg.LLM)
	}
	if got := env.values[providerEnvVar[provider]]; got != "sk-test" || cfg.LLM.APIKey == "sk-test" {
		t.Fatalf("API key must go to .env as %s only, env=%q", providerEnvVar[provider], got)
	}
}

func TestWizardDeclineAndCancel(t *testing.T) {
	cfg := config.DefaultConfig()
	m := newWizard(BuildConfigSteps(), cfg, nil)
	m.Init()
	press(m, "n", "enter")
	if !m.done || len(m.steps) != 1 || cfg.Parser.Threads != config.DefaultConfig().Parser.Threads {
		t.Fatalf("declining edit should finish without follow-ups, steps=%d", len(m.steps))
	}

	m = newWizard(BuildConfigSteps(), cfg, nil)
	m.Init()
	press(m, "esc")
	if !m.done || !m.cancelled {
		t.Fatal("esc should cancel the wizard")
	}
}
