package tui

import (
	"fmt"
	"regexp"
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

// Color scheme
var (
	primaryColor   = lipgloss.Color("#00D9FF")
	secondaryColor = lipgloss.Color("#7C3AED")
	successColor   = lipgloss.Color("#10B981")
	errorColor     = lipgloss.Color("#EF4444")
	warningColor   = lipgloss.Color("#F59E0B")
	mutedColor     = lipgloss.Color("#6B7280")
	textColor      = lipgloss.Color("#F9FAFB")
	borderColor    = lipgloss.Color("#374151")
	codeColor      = lipgloss.Color("#FCD34D")
	highlightColor = lipgloss.Color("#8B5CF6")
)

func MainModel(router *query.Router, cfg *config.Config, cacheManager *cache.Manager) Model {
	ti := textinput.New()
	ti.Placeholder = "Ask a question or type /help for commands"
	ti.Focus()
	ti.CharLimit = 500
	ti.Width = 80
	ti.PromptStyle = lipgloss.NewStyle().Foreground(primaryColor).Bold(true)
	ti.TextStyle = lipgloss.NewStyle().Foreground(textColor)

	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(primaryColor)

	vp := viewport.New(80, 20)
	// Disable mouse in viewport to allow text selection
	vp.MouseWheelEnabled = false

	return Model{
		state:        StateIdle,
		input:        ti,
		viewport:     vp,
		spinner:      s,
		router:       router,
		config:       cfg,
		cacheManager: cacheManager,
		messages: []Message{
			{Role: "system", Content: "Welcome to Eulix AI Code Assistant\n\nI can help you understand and navigate your codebase.\n\nTry asking:\n  - What does this function do?\n  - Explain the authentication flow\n  - Show me error handling patterns\n\nType /help to see available commands"},
		},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		tea.DisableMouse, // Disable mouse capture to allow text selection
	)
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

			if strings.HasPrefix(query, "/") {
				return m.handleCommand(query)
			}

			m.messages = append(m.messages, Message{
				Role:    "user",
				Content: query,
			})

			m.input.SetValue("")
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

		m.viewport.SetContent(m.renderMessages())

	case switchToCacheViewerMsg:
		if m.cacheManager == nil {
			m.messages = append(m.messages, Message{
				Role:    "error",
				Content: "Cache is not enabled. Enable cache in eulix.toml to use this feature.",
			})
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
			return m, nil
		}

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

		cacheModel := HistoryView(entries, m.cacheManager)
		cacheModel.width = m.width
		cacheModel.height = m.height

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
			Content: "AVAILABLE COMMANDS\n\n  /help     Show this help message\n  /history  View cached queries and responses\n  /clear    Clear conversation history\n  /stats    Show system statistics\n  /quit     Exit the application\n\nKEYBOARD SHORTCUTS\n\n  Enter     Send message\n  Esc       Exit application\n  Ctrl+C    Force exit",
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
			Content: fmt.Sprintf("Unknown command: %s\n\nType /help to see available commands.", parts[0]),
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

	return fmt.Sprintf("SYSTEM STATISTICS\n\n  Total Messages    %d\n  Your Questions    %d\n  AI Responses      %d\n  Current State     %s\n  Cache Status      %s",
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

	// Header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(textColor).
		Background(secondaryColor).
		Padding(0, 2).
		Width(m.width).
		Align(lipgloss.Center)

	b.WriteString(headerStyle.Render("EULIX AI CODEBASE ASSISTANT"))
	b.WriteString("\n")

	// Viewport with messages
	viewportStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(m.width - 2).
		Height(m.viewport.Height)

	b.WriteString(viewportStyle.Render(m.viewport.View()))
	b.WriteString("\n")

	// Processing indicator
	if m.processing {
		processingStyle := lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true).
			Padding(0, 2)
		b.WriteString(processingStyle.Render(fmt.Sprintf("%s Processing your request...", m.spinner.View())))
		b.WriteString("\n")
	}

	// Input box
	inputContainerStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(primaryColor).
		Padding(0, 1).
		Width(m.width - 2)

	inputPrefix := lipgloss.NewStyle().
		Foreground(primaryColor).
		Bold(true).
		Render("")

	b.WriteString(inputContainerStyle.Render(inputPrefix + m.input.View()))
	b.WriteString("\n")

	// Footer help
	helpStyle := lipgloss.NewStyle().
		Foreground(mutedColor).
		Padding(0, 2)

	helpText := "Enter: send | Esc: quit | /help: commands | Mouse selection enabled"
	b.WriteString(helpStyle.Render(helpText))

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
		Foreground(primaryColor).
		Bold(true)

	assistantStyle := lipgloss.NewStyle().
		Foreground(successColor)

	systemStyle := lipgloss.NewStyle().
		Foreground(mutedColor)

	errorStyle := lipgloss.NewStyle().
		Foreground(errorColor).
		Bold(true)

	messagePadding := lipgloss.NewStyle().
		MarginBottom(1)

	wrapWidth := m.viewport.Width - 6
	if wrapWidth < 40 {
		wrapWidth = 40
	}

	for _, msg := range m.messages {
		var prefix string
		var style lipgloss.Style

		switch msg.Role {
		case "user":
			prefix = "[YOU]"
			style = userStyle
		case "assistant":
			prefix = "[EULIX]"
			style = assistantStyle
		case "system":
			prefix = "[SYSTEM]"
			style = systemStyle
		case "error":
			prefix = "[ERROR]"
			style = errorStyle
		}

		header := style.Render(prefix)

		// Format content based on role
		var content string
		if msg.Role == "assistant" {
			content = formatMarkdownResponse(msg.Content, wrapWidth)
		} else {
			content = formatSimpleText(msg.Content, wrapWidth)
		}

		fullMessage := fmt.Sprintf("%s\n%s", header, content)
		b.WriteString(messagePadding.Render(fullMessage))
		b.WriteString("\n")
	}

	return b.String()
}

// formatMarkdownResponse formats LLM responses with markdown-like styling
func formatMarkdownResponse(text string, width int) string {
	var result strings.Builder

	// Normalize line breaks - preserve intentional double newlines, convert single to space
	text = normalizeLineBreaks(text)

	lines := strings.Split(text, "\n")
	inCodeBlock := false
	inList := false

	codeBlockStyle := lipgloss.NewStyle().
		Foreground(codeColor).
		Background(lipgloss.Color("#1F2937")).
		Padding(0, 1)

	codeInlineStyle := lipgloss.NewStyle().
		Foreground(codeColor)

	boldStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(textColor)

	listStyle := lipgloss.NewStyle().
		Foreground(highlightColor)

	headingStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(primaryColor).
		Underline(true)

	for i, line := range lines {
		line = strings.TrimRight(line, " \t")

		// Empty line handling
		if line == "" {
			if inCodeBlock {
				result.WriteString("\n")
			} else if i > 0 && i < len(lines)-1 {
				result.WriteString("\n")
			}
			inList = false
			continue
		}

		// Code blocks (``` or ~~~)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inCodeBlock = !inCodeBlock
			if inCodeBlock {
				// Starting code block
				lang := strings.TrimPrefix(line, "```")
				lang = strings.TrimPrefix(lang, "~~~")
				lang = strings.TrimSpace(lang)
				if lang != "" {
					result.WriteString(headingStyle.Render(fmt.Sprintf("[%s]", strings.ToUpper(lang))))
					result.WriteString("\n")
				}
			} else {
				// Ending code block - add spacing
				result.WriteString("\n")
			}
			continue
		}

		if inCodeBlock {
			// Inside code block - preserve formatting
			result.WriteString(codeBlockStyle.Render(line))
			result.WriteString("\n")
			continue
		}

		// Headings (##, ###, etc)
		if match := regexp.MustCompile(`^(#{1,6})\s+(.+)$`).FindStringSubmatch(line); match != nil {
			heading := strings.TrimSpace(match[2])
			result.WriteString(headingStyle.Render(heading))
			result.WriteString("\n")
			continue
		}

		// Lists (-, *, +, or numbered)
		if isListItem(line) {
			formatted := formatListItem(line, width-4, listStyle, codeInlineStyle, boldStyle)
			result.WriteString("  " + formatted)
			result.WriteString("\n")
			inList = true
			continue
		}

		// Regular paragraph
		if inList && !isListItem(line) {
			result.WriteString("\n")
			inList = false
		}

		// Process inline markdown (bold, code, etc)
		formatted := processInlineMarkdown(line, width, codeInlineStyle, boldStyle)
		result.WriteString(formatted)
		result.WriteString("\n")
	}

	return strings.TrimRight(result.String(), "\n")
}

// normalizeLineBreaks intelligently handles line breaks
func normalizeLineBreaks(text string) string {
	// Replace CRLF with LF
	text = strings.ReplaceAll(text, "\r\n", "\n")

	// Preserve double newlines (paragraph breaks)
	text = strings.ReplaceAll(text, "\n\n", "<<PARAGRAPH_BREAK>>")

	// Replace single newlines with spaces (unless before list items, headings, or code blocks)
	lines := strings.Split(text, "\n")
	var normalized []string

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Check if next line is special (list, heading, code)
		if i < len(lines)-1 {
			nextLine := strings.TrimSpace(lines[i+1])
			if nextLine == "" || strings.HasPrefix(nextLine, "#") ||
			   strings.HasPrefix(nextLine, "```") || strings.HasPrefix(nextLine, "~~~") ||
			   isListItem(nextLine) {
				normalized = append(normalized, line)
				continue
			}
		}

		// Check if current line is special
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") ||
		   strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") ||
		   isListItem(trimmed) {
			normalized = append(normalized, line)
			continue
		}

		// Regular line - join with next
		if i < len(lines)-1 {
			normalized = append(normalized, line+" ")
		} else {
			normalized = append(normalized, line)
		}
	}

	text = strings.Join(normalized, "")
	text = strings.ReplaceAll(text, "<<PARAGRAPH_BREAK>>", "\n\n")

	return text
}

// isListItem checks if a line is a list item
func isListItem(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}

	// Unordered lists: -, *, +
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") {
		return true
	}

	// Numbered lists: 1., 2., etc
	if matched, _ := regexp.MatchString(`^\d+\.\s`, line); matched {
		return true
	}

	return false
}

// formatListItem formats a list item with proper indentation
func formatListItem(line string, width int, listStyle, codeStyle, boldStyle lipgloss.Style) string {
	line = strings.TrimSpace(line)

	// Extract bullet/number and content
	var bullet, content string
	if strings.HasPrefix(line, "- ") {
		bullet = "•"
		content = strings.TrimPrefix(line, "- ")
	} else if strings.HasPrefix(line, "* ") {
		bullet = "•"
		content = strings.TrimPrefix(line, "* ")
	} else if strings.HasPrefix(line, "+ ") {
		bullet = "•"
		content = strings.TrimPrefix(line, "+ ")
	} else if match := regexp.MustCompile(`^(\d+)\.\s+(.+)$`).FindStringSubmatch(line); match != nil {
		bullet = match[1] + "."
		content = match[2]
	}

	content = processInlineMarkdown(content, width-4, codeStyle, boldStyle)

	return listStyle.Render(bullet) + " " + content
}

// processInlineMarkdown handles inline markdown like **bold** and `code`
func processInlineMarkdown(text string, width int, codeStyle, boldStyle lipgloss.Style) string {
	// Handle inline code first (`code`)
	codeRegex := regexp.MustCompile("`([^`]+)`")
	text = codeRegex.ReplaceAllStringFunc(text, func(match string) string {
		code := strings.Trim(match, "`")
		return codeStyle.Render(code)
	})

	// Handle bold (**text** or __text__)
	boldRegex := regexp.MustCompile(`(\*\*|__)([^*_]+)(\*\*|__)`)
	text = boldRegex.ReplaceAllStringFunc(text, func(match string) string {
		// Extract text between markers
		inner := regexp.MustCompile(`(\*\*|__)([^*_]+)(\*\*|__)`).FindStringSubmatch(match)
		if len(inner) > 2 {
			return boldStyle.Render(inner[2])
		}
		return match
	})

	// Wrap text
	return wrapText(text, width)
}

// formatSimpleText formats non-assistant messages (system, user, error)
func formatSimpleText(text string, width int) string {
	textStyle := lipgloss.NewStyle().Foreground(textColor)

	// Just wrap and style, no special formatting
	wrapped := wrapText(text, width)
	return textStyle.Render(wrapped)
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
		wordLen := lipgloss.Width(word) // Use lipgloss.Width to account for ANSI codes

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
