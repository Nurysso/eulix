//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer mnae (Nurysso) contact - nurysso [at] proton.me

// package setup contains code related to init flow of eulix

package setup

import (
	"errors"
	"fmt"
	"strings"

	"eulix/internal/config"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Generic step framework.
//
// To add a new question:
//   1. Write a Step value (see wizard_steps.go for examples of each Kind).
//   2. Return it from BuildConfigSteps() to always ask it, or from another
//      step's Next() to ask it only when a previous answer calls for it
//      (e.g. "only ask for a base URL if the user picked 'local'").

// StepKind selects which small UI the engine renders for a Step.
type StepKind int

const (
	StepConfirm  StepKind = iota // Yes / No picker
	StepSelect                   // pick one of Options
	StepText                     // free text input
	StepPassword                 // free text input, masked
	StepAction                   // runs an arbitrary side effect, no prompt shown
)

// Answers accumulates every answer given so far, keyed by Step.Key, so that
// later steps (Skip/Next/Apply) can react to earlier ones.
type Answers map[string]string

// Step is one question (or side effect) in the wizard.
type Step struct {
	Key      string                                                      // unique id used to look the answer up in Answers
	Kind     StepKind                                                    // which UI (or side effect) to render
	Prompt   string                                                      // question text shown above the input; unused by StepAction
	Help     string                                                      // optional one-line hint under the prompt
	Options  []string                                                    // choices for StepSelect; ignored by other kinds
	Default  string                                                      // pre-filled value for StepText/StepPassword
	Validate func(string) error                                          // rejects bad input from StepText/StepPassword; non-nil error keeps the user on this step
	Skip     func(Answers) bool                                          // hides this step when it returns true, evaluated against answers so far
	Next     func(answer string, a Answers) []Step                       // injects follow-up steps right after this one, based on the answer just given
	Action   func(a Answers) (string, error)                             // side effect run for StepAction steps; string is shown as a transient status
	Apply    func(answer string, cfg *config.Config, env *EnvFile) error // persists the answer into config/.env immediately on submit
}

type wizardModel struct {
	steps     []Step
	idx       int
	answers   Answers
	cursor    int
	input     textinput.Model
	cfg       *config.Config
	env       *EnvFile
	status    string
	err       error
	done      bool
	cancelled bool
}

var ErrWizardCancelled = errors.New("wizard cancelled by user")

var (
	promptStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	helpStyle     = lipgloss.NewStyle().Faint(true)
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	statusStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	footerStyle   = lipgloss.NewStyle().Faint(true)
)

func newWizard(steps []Step, cfg *config.Config, env *EnvFile) *wizardModel {
	input := textinput.New()
	input.CharLimit = 256
	input.Width = 48
	return &wizardModel{
		steps:   steps,
		answers: Answers{},
		cfg:     cfg,
		env:     env,
		input:   input,
	}
}

func (m *wizardModel) Init() tea.Cmd {
	return m.enterCurrentStep()
}

// enterCurrentStep prepares whatever step is now at m.idx: it skips steps
// whose Skip() returns true, runs StepAction steps immediately, and stops
// once it reaches a step that needs user input (or the end of the list).
func (m *wizardModel) enterCurrentStep() tea.Cmd {
	for m.idx < len(m.steps) {
		step := m.steps[m.idx]

		if step.Skip != nil && step.Skip(m.answers) {
			m.idx++
			continue
		}

		switch step.Kind {
		case StepAction:
			return m.runActionStep(step)
		case StepText, StepPassword:
			return m.prepareTextInput(step)
		case StepConfirm, StepSelect:
			m.cursor = 0
			m.err = nil
			return nil
		}
	}
	m.done = true
	return tea.Quit
}

func (m *wizardModel) runActionStep(step Step) tea.Cmd {
	msg, err := step.Action(m.answers)
	if err != nil {
		m.err = err
		return tea.Quit
	}
	m.status = msg
	m.idx++
	return m.enterCurrentStep()
}

func (m *wizardModel) prepareTextInput(step Step) tea.Cmd {
	m.input.Reset()
	m.input.SetValue(step.Default)
	m.input.EchoMode = textinput.EchoNormal
	if step.Kind == StepPassword {
		m.input.EchoMode = textinput.EchoPassword
	}
	m.input.Focus()
	m.err = nil
	return textinput.Blink
}

// submit records the answer for the current step, applies it, splices in
// any follow-up steps it wants right after itself, then advances.
func (m *wizardModel) submit(answer string) tea.Cmd {
	step := m.steps[m.idx]

	if step.Validate != nil {
		if err := step.Validate(answer); err != nil {
			m.err = err
			return nil
		}
	}
	m.answers[step.Key] = answer

	if step.Apply != nil {
		if err := step.Apply(answer, m.cfg, m.env); err != nil {
			m.err = err
			return nil
		}
	}

	if step.Next != nil {
		m.spliceInFollowUpSteps(step.Next(answer, m.answers))
	}

	m.status = ""
	m.err = nil
	m.idx++
	return m.enterCurrentStep()
}

// spliceInFollowUpSteps inserts extra steps right after the current one,
// preserving any steps that were already queued behind it.
func (m *wizardModel) spliceInFollowUpSteps(extra []Step) {
	if len(extra) == 0 {
		return
	}
	remaining := append([]Step{}, m.steps[m.idx+1:]...)
	m.steps = append(append(m.steps[:m.idx+1], extra...), remaining...)
}

func (m *wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.idx >= len(m.steps) {
		return m, tea.Quit
	}

	if keyMsg.String() == "ctrl+c" || keyMsg.String() == "esc" {
		m.done = true
		m.cancelled = true
		return m, tea.Quit
	}

	step := m.steps[m.idx]
	switch step.Kind {
	case StepConfirm:
		return m.updateConfirm(keyMsg)
	case StepSelect:
		return m.updateSelect(keyMsg, step)
	case StepText, StepPassword:
		return m.updateTextInput(keyMsg)
	}
	return m, nil
}

func (m *wizardModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "left", "h", "y", "Y":
		m.cursor = 0
	case "right", "l", "n", "N":
		m.cursor = 1
	case "enter":
		return m, m.submit(m.confirmAnswer())
	}
	return m, nil
}

func (m *wizardModel) confirmAnswer() string {
	if m.cursor == 0 {
		return "Yes"
	}
	return "No"
}

func (m *wizardModel) updateSelect(msg tea.KeyMsg, step Step) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(step.Options)-1 {
			m.cursor++
		}
	case "enter":
		return m, m.submit(step.Options[m.cursor])
	}
	return m, nil
}

func (m *wizardModel) updateTextInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "enter" {
		return m, m.submit(strings.TrimSpace(m.input.Value()))
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *wizardModel) View() string {
	if m.idx >= len(m.steps) {
		return ""
	}
	step := m.steps[m.idx]

	var b strings.Builder
	b.WriteString(promptStyle.Render(step.Prompt))
	b.WriteString("\n")
	if step.Help != "" {
		b.WriteString(helpStyle.Render(step.Help))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	switch step.Kind {
	case StepConfirm:
		b.WriteString(m.renderConfirm())
	case StepSelect:
		b.WriteString(m.renderSelect(step.Options))
	case StepText, StepPassword:
		b.WriteString(m.renderTextInput())
	}

	if m.err != nil {
		b.WriteString("\n\n")
		b.WriteString(errStyle.Render("✗ " + m.err.Error()))
	}
	if m.status != "" {
		b.WriteString("\n\n")
		b.WriteString(statusStyle.Render(m.status))
	}
	return "\n" + b.String() + "\n"
}

func (m *wizardModel) renderConfirm() string {
	yes, no := "Yes", "No"
	if m.cursor == 0 {
		yes = selectedStyle.Render("> " + yes)
		no = "  " + no
	} else {
		yes = "  " + yes
		no = selectedStyle.Render("> " + no)
	}
	footer := footerStyle.Render("← / → to choose · enter to confirm · esc to skip the rest")
	return yes + "    " + no + "\n" + footer
}

func (m *wizardModel) renderSelect(options []string) string {
	var b strings.Builder
	for i, opt := range options {
		if i == m.cursor {
			b.WriteString(cursorStyle.Render("> "))
			b.WriteString(selectedStyle.Render(opt))
		} else {
			b.WriteString("  ")
			b.WriteString(opt)
		}
		b.WriteString("\n")
	}
	b.WriteString(footerStyle.Render("↑ / ↓ to choose · enter to confirm · esc to skip the rest"))
	return b.String()
}

func (m *wizardModel) renderTextInput() string {
	footer := footerStyle.Render("enter to confirm · esc to skip the rest")
	return m.input.View() + "\n" + footer
}

// RunWizard drives a set of steps to completion, mutating cfg/env as the
// user answers. Pressing esc/ctrl+c at any point stops the wizard early
// without losing answers already applied.
func RunWizard(steps []Step, cfg *config.Config, env *EnvFile) error {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	final, err := tea.NewProgram(newWizard(steps, cfg, env)).Run()
	if err != nil {
		return fmt.Errorf("wizard failed: %w", err)
	}
	model, ok := final.(*wizardModel)
	if !ok {
		return nil
	}
	if model.err != nil {
		return model.err
	}
	if model.cancelled {
		return ErrWizardCancelled
	}
	return nil
}
