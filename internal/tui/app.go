package tui

import (
	"fmt"
	"strings"

	"eulix/internal/config"
	"eulix/internal/query"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type AppState int

const (
	StateIdle AppState = iota
	StateTyping
	StateProcessing
	StateDisplaying
	StateError
)

type Message struct {
	Role    string
	Content string
}

type Model struct {
	state      AppState
	input      textinput.Model
	messages   []Message
	viewport   viewport.Model
	spinner    spinner.Model
	router     *query.Router
	config     *config.Config
	width      int
	height     int
	err        error
	processing bool
}

type queryResultMsg struct {
	result string
	err    error
}

func NewModel(router *query.Router, cfg *config.Config) Model {
	ti := textinput.New()
	ti.Placeholder = "Ask a question about your codebase..."
	ti.Focus()
	ti.CharLimit = 500
	ti.Width = 80

	s := spinner.New()
	s.Spinner = spinner.Dot

	vp := viewport.New(80, 20)

	return Model{
		state:    StateIdle,
		input:    ti,
		viewport: vp,
		spinner:  s,
		router:   router,
		config:   cfg,
		messages: []Message{
			{Role: "system", Content: "Welcome to Eulix! Ask me anything about your codebase."},
		},
	}
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit

		case "enter":
			if m.processing {
				return m, nil
			}

			query := strings.TrimSpace(m.input.Value())
			if query == "" {
				return m, nil
			}

			// Add user message
			m.messages = append(m.messages, Message{
				Role:    "user",
				Content: query,
			})

			// Clear input
			m.input.SetValue("")

			// Start processing
			m.processing = true
			m.state = StateProcessing

			return m, tea.Batch(
				m.spinner.Tick,
				m.processQuery(query),
			)
		}

	case queryResultMsg:
		m.processing = false

		if msg.err != nil {
			m.messages = append(m.messages, Message{
				Role:    "error",
				Content: fmt.Sprintf("Error: %v", msg.err),
			})
			m.state = StateError
		} else {
			m.messages = append(m.messages, Message{
				Role:    "assistant",
				Content: msg.result,
			})
			m.state = StateDisplaying
		}

		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()

		return m, nil

	case spinner.TickMsg:
		if m.processing {
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 5
		m.input.Width = msg.Width - 4
	}

	if !m.processing {
		m.input, cmd = m.input.Update(msg)
	}

	m.viewport, _ = m.viewport.Update(msg)

	return m, cmd
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	// Header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("39")).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("39")).
		Padding(0, 1).
		Width(m.width)

	b.WriteString(headerStyle.Render("Eulix - AI Code Assistant"))
	b.WriteString("\n\n")

	// Messages viewport
	b.WriteString(m.viewport.View())
	b.WriteString("\n\n")

	// Processing indicator
	if m.processing {
		spinnerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
		b.WriteString(spinnerStyle.Render(fmt.Sprintf("%s Analyzing...", m.spinner.View())))
		b.WriteString("\n\n")
	}

	// Input
	inputStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(0, 1)

	b.WriteString(inputStyle.Render(m.input.View()))
	b.WriteString("\n\n")

	// Help
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	b.WriteString(helpStyle.Render("Enter: Send • Esc/Ctrl+C: Quit"))

	return b.String()
}

func (m Model) processQuery(query string) tea.Cmd {
	return func() tea.Msg {
		result, err := m.router.Query(query)
		return queryResultMsg{result: result, err: err}
	}
}

func (m Model) renderMessages() string {
	var b strings.Builder

	userStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("39")).
		Bold(true)

	assistantStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("42"))

	systemStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Italic(true)

	errorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")).
		Bold(true)

	for _, msg := range m.messages {
		switch msg.Role {
		case "user":
			b.WriteString(userStyle.Render("You: "))
			b.WriteString(msg.Content)
		case "assistant":
			b.WriteString(assistantStyle.Render("Eulix: "))
			b.WriteString(msg.Content)
		case "system":
			b.WriteString(systemStyle.Render("ℹ " + msg.Content))
		case "error":
			b.WriteString(errorStyle.Render("❌ " + msg.Content))
		}
		b.WriteString("\n\n")
	}

	return b.String()
}
