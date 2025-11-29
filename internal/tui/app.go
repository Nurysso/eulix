package tui

import (
	"fmt"
	"strings"

	"eulix/internal/cache"
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
	state        AppState
	input        textinput.Model
	messages     []Message
	viewport     viewport.Model
	spinner      spinner.Model
	router       *query.Router
	config       *config.Config
	cacheManager *cache.Manager
	width        int
	height       int
	err          error
	processing   bool
}

type queryResultMsg struct {
	result string
	err    error
}

type switchToCacheViewerMsg struct{}

func MainModel(router *query.Router, cfg *config.Config, cacheManager *cache.Manager) Model {
	ti := textinput.New()
	ti.Placeholder = "Ask a question... (type /help for commands)"
	ti.Focus()
	ti.CharLimit = 500
	ti.Width = 80

	s := spinner.New()
	s.Spinner = spinner.Dot

	vp := viewport.New(80, 20)

	return Model{
		state:        StateIdle,
		input:        ti,
		viewport:     vp,
		spinner:      s,
		router:       router,
		config:       cfg,
		cacheManager: cacheManager,
		messages: []Message{
			{Role: "system", Content: "Welcome to Eulix AI Code Assistant! \n\nIt can help you understand and navigate your codebase. Try typing:\n  • \"What does this function do?\"\n  • \"Explain the authentication flow\"\n\nType /help to see available commands."},
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

			// Handle commands
			if strings.HasPrefix(query, "/") {
				return m.handleCommand(query)
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
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - 10
		m.input.Width = msg.Width - 8

		// Re-render messages with new width
		m.viewport.SetContent(m.renderMessages())

	case switchToCacheViewerMsg:
		// Switch to cache viewer
		if m.cacheManager == nil {
			m.messages = append(m.messages, Message{
				Role:    "error",
				Content: "Cache is not enabled. Enable cache in eulix.toml to use this feature.",
			})
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
			return m, nil
		}

		// Get cache entries
		entries, err := m.cacheManager.ListAll()
		if err != nil {
			m.messages = append(m.messages, Message{
				Role:    "error",
				Content: fmt.Sprintf("Failed to load cache history: %v", err),
			})
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
			return m, nil
		}

		if len(entries) == 0 {
			m.messages = append(m.messages, Message{
				Role:    "system",
				Content: "No cache entries found. Your question history is empty.",
			})
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
			return m, nil
		}

		// Create cache viewer and pass current window size
		cacheModel := HistoryView(entries, m.cacheManager)

		// Initialize with current window dimensions
		cacheModel.width = m.width
		cacheModel.height = m.height

		// Set sizes for list and viewport
		h, v := lipgloss.NewStyle().GetFrameSize()
		cacheModel.list.SetSize(m.width-h, m.height-v-4)
		cacheModel.viewport.Width = m.width - 4
		cacheModel.viewport.Height = m.height - 6

		return cacheModel, cacheModel.Init()
	}

	if !m.processing {
		m.input, cmd = m.input.Update(msg)
	}

	m.viewport, _ = m.viewport.Update(msg)

	return m, cmd
}

func (m Model) handleCommand(command string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return m, nil
	}

	switch parts[0] {
	case "/help":
		m.messages = append(m.messages, Message{
			Role:    "system",
			Content: "Available Commands:\n\n  /help     - Show this help message\n  /history  - View cached queries and responses\n  /clear    - Clear conversation history\n  /stats    - Show system statistics\n  /quit     - Exit the application\n\nKeyboard Shortcuts:\n  Enter     - Send message,\n  Esc       - Exit application\n  Ctrl+C    - Exit application",
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		m.input.SetValue("")
		return m, nil

	case "/history":
		return m, func() tea.Msg {
			return switchToCacheViewerMsg{}
		}

	case "/clear":
		m.messages = []Message{
			{Role: "system", Content: "Conversation cleared. How can I help you?"},
		}
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoTop()
		m.input.SetValue("")
		return m, nil

	case "/stats":
		statsMsg := m.getSystemStats()
		m.messages = append(m.messages, Message{
			Role:    "system",
			Content: statsMsg,
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		m.input.SetValue("")
		return m, nil

	case "/quit":
		return m, tea.Quit

	default:
		m.messages = append(m.messages, Message{
			Role:    "error",
			Content: fmt.Sprintf("Unknown command: %s\nType /help to see available commands.", parts[0]),
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		m.input.SetValue("")
		return m, nil
	}
}

func (m Model) getSystemStats() string {
	conversationLength := len(m.messages)
	userMessages := 0
	for _, msg := range m.messages {
		if msg.Role == "user" {
			userMessages++
		}
	}

	cacheStatus := "Disabled"
	if m.cacheManager != nil {
		cacheStatus = "Enabled"
	}

	return fmt.Sprintf("System Statistics:\n\n Total Messages: %d\n  Your Questions: %d\n  AI Responses: %d\n State: %s\n  Cache: %s",
		conversationLength,
		userMessages,
		userMessages,
		m.getStateName(),
		cacheStatus)
}

func (m Model) getStateName() string {
	switch m.state {
	case StateIdle:
		return "Idle"
	case StateTyping:
		return "Typing"
	case StateProcessing:
		return "Processing"
	case StateDisplaying:
		return "Displaying"
	case StateError:
		return "Error"
	default:
		return "Unknown"
	}
}

func (m Model) View() string {
	if m.width == 0 {
		return "Initializing..."
	}

	var b strings.Builder

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FAFAFA")).
		Background(lipgloss.Color("#7D56F4")).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7D56F4")).
		Padding(0, 2).
		Width(m.width - 2).
		Align(lipgloss.Center)

	b.WriteString(headerStyle.Render("Eulix AI Codebase Assistant"))
	b.WriteString("\n\n")

	viewportStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7D56F4")).
		Padding(0, 1).
		Width(m.width - 2)

	b.WriteString(viewportStyle.Render(m.viewport.View()))
	b.WriteString("\n")

	if m.processing {
		spinnerStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6B9D")).
			Bold(true).
			Padding(0, 1)
		b.WriteString(spinnerStyle.Render(fmt.Sprintf("%s Thinking...", m.spinner.View())))
		b.WriteString("\n")
	}

	inputBoxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#04B575")).
		Padding(0, 1).
		Width(m.width - 4)

	inputLabel := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#04B575")).
		Bold(true).
		Render(" ")

	b.WriteString(inputBoxStyle.Render(inputLabel + m.input.View()))
	b.WriteString("\n")

	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#626262")).
		Padding(0, 1)

	helpParts := []string{
		lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4")).Render("Enter"),
		"send",
		lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4")).Render("Esc"),
		"quit",
		lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4")).Render("/help"),
		"commands",
	}

	b.WriteString(helpStyle.Render(strings.Join(helpParts, " • ")))

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
		Foreground(lipgloss.Color("#04B575")).
		Bold(true)

	assistantStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7D56F4"))

	systemStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#626262")).
		Italic(true)

	errorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF6B9D")).
		Bold(true)

	messageBubble := lipgloss.NewStyle().
		Padding(0, 1).
		MarginBottom(1)

	wrapWidth := m.viewport.Width - 6
	if wrapWidth < 40 {
		wrapWidth = 40
	}

	for _, msg := range m.messages {
		var prefix, content string
		var style lipgloss.Style

		switch msg.Role {
		case "user":
			prefix = "You: "
			content = msg.Content
			style = userStyle
		case "assistant":
			prefix = "Eulix: "
			content = msg.Content
			style = assistantStyle
		case "system":
			prefix = "[!] "
			content = msg.Content
			style = systemStyle
		case "error":
			prefix = "[x] "
			content = msg.Content
			style = errorStyle
		}

		fullMessage := style.Render(prefix) + wrapText(content, wrapWidth-len(prefix))
		b.WriteString(messageBubble.Render(fullMessage))
		b.WriteString("\n")
	}

	return b.String()
}

func wrapText(text string, width int) string {
	if width <= 0 {
		width = 80
	}

	var result strings.Builder
	var currentLine strings.Builder
	currentLength := 0

	words := strings.Fields(text)
	for i, word := range words {
		wordLen := len(word)

		if currentLength > 0 && currentLength+1+wordLen > width {
			result.WriteString(currentLine.String())
			result.WriteString("\n")
			currentLine.Reset()
			currentLength = 0
		}

		if currentLength > 0 {
			currentLine.WriteString(" ")
			currentLength++
		}

		currentLine.WriteString(word)
		currentLength += wordLen

		if i == len(words)-1 {
			result.WriteString(currentLine.String())
		}
	}

	return result.String()
}
