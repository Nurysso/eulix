package tui

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

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

// Compiled once — used in formatListItem and processInlineMarkdown.
var (
	headingRe      = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	numberedListRe = regexp.MustCompile(`^(\d+)\.\s+(.+)$`)
	inlineCodeRe   = regexp.MustCompile("`([^`]+)`")
	boldRe         = regexp.MustCompile(`(\*\*|__)([^*_]+)(\*\*|__)`)
	isNumberedRe   = regexp.MustCompile(`^\d+\.\s`)
)

// Package-level styles — constructed once per process lifetime.
var (
	codeBlockStyle = lipgloss.NewStyle().
			Foreground(codeColor).
			Background(lipgloss.Color("#1F2937")).
			Padding(0, 1)

	codeInlineStyle = lipgloss.NewStyle().
			Foreground(codeColor)

	boldStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(textColor)

	listStyle = lipgloss.NewStyle().
			Foreground(highlightColor)

	headingStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			Underline(true)
)

func MainModel(router *query.Router, cfg *config.Config, cacheManager *cache.Manager) Model {
	ti := textinput.New()
	ti.Placeholder = "Ask a question or type /help for commands"
	ti.Focus()
	ti.CharLimit = 0
	ti.Width = 80
	ti.PromptStyle = lipgloss.NewStyle().Foreground(primaryColor).Bold(true)
	ti.TextStyle = lipgloss.NewStyle().Foreground(textColor)

	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(primaryColor)

	vp := viewport.New(80, 20)
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
		tea.DisableMouse,
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

			q := strings.TrimSpace(m.input.Value())
			if q == "" {
				return m, nil
			}

			if strings.HasPrefix(q, "/") {
				return m.handleCommand(q)
			}

			m.messages = append(m.messages, Message{Role: "user", Content: q})
			m.input.SetValue("")
			m.processing = true
			m.state = StateProcessing

			return m, tea.Batch(m.spinner.Tick, m.processQuery(q))
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
		// reserve rows for header(2) + input(3) + spinner(1) + footer(1) + borders(2)
		m.viewport.Height = msg.Height - 9
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
	case "/history":
		return m, func() tea.Msg { return switchToCacheViewerMsg{} }
	case "/clear":
		m.messages = []Message{
			{Role: "system", Content: "Conversation cleared. How can I help you?"},
		}
		m.viewport.GotoTop()
		m.input.SetValue("")
		m.viewport.SetContent(m.renderMessages())
		return m, nil
	case "/stats":
		m.messages = append(m.messages, Message{Role: "system", Content: m.getSystemStats()})
	case "/quit":
		return m, tea.Quit
	default:
		m.messages = append(m.messages, Message{
			Role:    "error",
			Content: fmt.Sprintf("Unknown command: %s\n\nType /help to see available commands.", parts[0]),
		})
	}

	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
	m.input.SetValue("")
	return m, nil
}

func (m Model) getSystemStats() string {
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
		len(m.messages), userMessages, userMessages, m.getStateName(), cacheStatus)
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
		Foreground(textColor).
		Background(secondaryColor).
		Padding(0, 2).
		Width(m.width).
		Align(lipgloss.Center)
	b.WriteString(headerStyle.Render("EULIX AI CODEBASE ASSISTANT"))
	b.WriteString("\n")

	viewportStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(m.width - 2).
		Height(m.viewport.Height)
	b.WriteString(viewportStyle.Render(m.viewport.View()))
	b.WriteString("\n")

	if m.processing {
		processingStyle := lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true).
			Padding(0, 2)
		b.WriteString(processingStyle.Render(fmt.Sprintf("%s Processing your request...", m.spinner.View())))
		b.WriteString("\n")
	}

	inputContainerStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(primaryColor).
		Padding(0, 1).
		Width(m.width - 2)
	inputPrefix := lipgloss.NewStyle().Foreground(primaryColor).Bold(true).Render("")
	b.WriteString(inputContainerStyle.Render(inputPrefix + m.input.View()))
	b.WriteString("\n")

	helpStyle := lipgloss.NewStyle().Foreground(mutedColor).Padding(0, 2)
	b.WriteString(helpStyle.Render("Enter: send | Esc: quit | /help: commands | ↑/↓: scroll"))

	return b.String()
}

func (m Model) processQuery(q string) tea.Cmd {
	return func() tea.Msg {
		result, err := m.router.Query(q)
		return queryResultMsg{result: result, err: err}
	}
}

func (m Model) renderMessages() string {
	var b strings.Builder

	userStyle := lipgloss.NewStyle().Foreground(primaryColor).Bold(true)
	assistantStyle := lipgloss.NewStyle().Foreground(successColor)
	systemStyle := lipgloss.NewStyle().Foreground(mutedColor)
	errorStyle := lipgloss.NewStyle().Foreground(errorColor).Bold(true)
	messagePadding := lipgloss.NewStyle().MarginBottom(1)

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

		var content string
		if msg.Role == "assistant" {
			content = formatMarkdownResponse(msg.Content, wrapWidth)
		} else {
			content = formatSimpleText(msg.Content, wrapWidth)
		}

		b.WriteString(messagePadding.Render(fmt.Sprintf("%s\n%s", header, content)))
		b.WriteString("\n")
	}

	return b.String()
}

// formatMarkdownResponse formats LLM responses with markdown-like styling.
func formatMarkdownResponse(text string, width int) string {
	// normalise away newlines preserve the LLM's line breaks
	// so long responses are never truncated mid-sentence.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	var b strings.Builder
	b.Grow(len(text) + 256)

	inCodeBlock := false
	inList := false
	prevWasParagraph := false

	for _, line := range lines {
		line = strings.TrimRight(line, " \t")

		// Empty line
		if line == "" {
			b.WriteByte('\n')
			inList = false
			prevWasParagraph = false
			continue
		}

		// Code-fence toggle
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inCodeBlock = !inCodeBlock
			if inCodeBlock {
				lang := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "```"), "~~~"))
				if lang != "" {
					fmt.Fprintf(&b, "%s\n", headingStyle.Render("["+strings.ToUpper(lang)+"]"))
				}
			} else {
				b.WriteByte('\n')
			}
			prevWasParagraph = false
			continue
		}

		// Inside code block — render verbatim, never wrap
		if inCodeBlock {
			fmt.Fprintf(&b, "%s\n", codeBlockStyle.Render(line))
			continue
		}

		// Heading
		if m := headingRe.FindStringSubmatch(line); m != nil {
			if prevWasParagraph {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "%s\n", headingStyle.Render(strings.TrimSpace(m[2])))
			inList = false
			prevWasParagraph = false
			continue
		}

		// List item
		if isListItem(line) {
			formatted := formatListItem(line, width-4)
			fmt.Fprintf(&b, "  %s\n", formatted)
			inList = true
			prevWasParagraph = false
			continue
		}

		// List continuation (indented line following a list item)
		if inList && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")) {
			trimmed := strings.TrimLeft(line, " \t")
			formatted := processInlineMarkdown(trimmed, width-4)
			fmt.Fprintf(&b, "    %s\n", formatted)
			continue
		}

		// Paragraph
		if inList {
			b.WriteByte('\n')
			inList = false
		}

		formatted := processInlineMarkdown(line, width)
		fmt.Fprintf(&b, "%s\n", formatted)
		prevWasParagraph = true
	}

	return strings.TrimRight(b.String(), "\n")
}

// normalizeLineBreaks is kept only for explicit callers that want soft-wrap
// joining. formatMarkdownResponse no longer calls it.
func normalizeLineBreaks(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n\n", "<<PARAGRAPH_BREAK>>")

	lines := strings.Split(text, "\n")
	var out []string

	for i, line := range lines {
		if i < len(lines)-1 {
			next := strings.TrimSpace(lines[i+1])
			if next == "" || strings.HasPrefix(next, "#") ||
				strings.HasPrefix(next, "```") || strings.HasPrefix(next, "~~~") ||
				isListItem(next) {
				out = append(out, line)
				continue
			}
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") ||
			isListItem(trimmed) {
			out = append(out, line)
			continue
		}
		if i < len(lines)-1 {
			out = append(out, line+" ")
		} else {
			out = append(out, line)
		}
	}

	text = strings.Join(out, "")
	return strings.ReplaceAll(text, "<<PARAGRAPH_BREAK>>", "\n\n")
}

func isListItem(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}

	// Unordered lists
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") {
		return true
	}

	// Corrected: MatchString only returns one value
	return isNumberedRe.MatchString(line)
}

// formatListItem no longer accepts style args — uses package-level vars.
func formatListItem(line string, width int) string {
	line = strings.TrimSpace(line)

	var bullet, content string
	switch {
	case strings.HasPrefix(line, "- "):
		bullet = "•"
		content = strings.TrimPrefix(line, "- ")
	case strings.HasPrefix(line, "* "):
		bullet = "•"
		content = strings.TrimPrefix(line, "* ")
	case strings.HasPrefix(line, "+ "):
		bullet = "•"
		content = strings.TrimPrefix(line, "+ ")
	default:
		if m := numberedListRe.FindStringSubmatch(line); m != nil {
			bullet = m[1] + "."
			content = m[2]
		}
	}

	return listStyle.Render(bullet) + " " + processInlineMarkdown(content, width-4)
}

// processInlineMarkdown handles `code` and **bold** spans, then word-wraps.
func processInlineMarkdown(text string, width int) string {
	// Inline code
	text = inlineCodeRe.ReplaceAllStringFunc(text, func(match string) string {
		return codeInlineStyle.Render(strings.Trim(match, "`"))
	})

	// Bold
	text = boldRe.ReplaceAllStringFunc(text, func(match string) string {
		if inner := boldRe.FindStringSubmatch(match); len(inner) > 2 {
			return boldStyle.Render(inner[2])
		}
		return match
	})

	return wrapText(text, width)
}

func formatSimpleText(text string, width int) string {
	return lipgloss.NewStyle().Foreground(textColor).Render(wrapText(text, width))
}

// wrapText wraps text at word boundaries, preserving existing newlines.
// previous version used strings.Fields which silently dropped all
// embedded newlines, causing multi-line responses to be cut off.
func wrapText(text string, width int) string {
	if width <= 0 {
		width = 80
	}

	// Process each existing line independently so we never collapse
	// intentional line breaks from the LLM output.
	inputLines := strings.Split(text, "\n")
	out := make([]string, 0, len(inputLines))

	for _, inputLine := range inputLines {
		if inputLine == "" {
			out = append(out, "")
			continue
		}

		var (
			curLine strings.Builder
			curLen  int
		)

		// Split on spaces while keeping ANSI-aware width via lipgloss.Width.
		words := strings.Split(inputLine, " ")
		for i, word := range words {
			// lipgloss.Width correctly measures visible width of styled spans.
			wLen := lipgloss.Width(word)
			if wLen == 0 {
				// Zero-width (e.g. empty string between consecutive spaces):
				// re-emit the space rather than dropping it.
				if curLen > 0 {
					curLine.WriteByte(' ')
					curLen++
				}
				continue
			}

			needsSpace := curLen > 0
			advance := wLen
			if needsSpace {
				advance++
			}

			if curLen > 0 && curLen+advance > width {
				out = append(out, curLine.String())
				curLine.Reset()
				curLen = 0
				needsSpace = false
				advance = wLen
			}

			if needsSpace {
				curLine.WriteByte(' ')
			}
			curLine.WriteString(word)
			curLen += advance

			// Flush on last word of this input line.
			if i == len(words)-1 {
				out = append(out, curLine.String())
			}
		}

		// Edge case: single word exactly fills the line — already flushed above.
		// But if curLine was never flushed (empty words slice), emit it anyway.
		if len(words) == 0 {
			out = append(out, "")
		}
	}

	return strings.Join(out, "\n")
}

// visibleLen returns the number of visible rune positions in s,
// skipping ANSI escape sequences. Used as a fallback when lipgloss.Width
// is unavailable (e.g. in unit tests without a TTY).
func visibleLen(s string) int {
	n := 0
	esc := false
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r == '\x1b' {
			esc = true
			continue
		}
		if esc {
			if r == 'm' {
				esc = false
			}
			continue
		}
		n++
	}
	return n
}
