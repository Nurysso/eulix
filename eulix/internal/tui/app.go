//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package embeddings provides the command-line interface implementation for EULIX.

// Main TUI model: chat transcript, input box, and markdown-ish response formatting.
package tui

import (
	"fmt"
	"regexp"
	"strings"

	"eulix/internal/cache"
	"eulix/internal/config"
	"eulix/internal/query"
	"eulix/internal/utils"

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
	state         AppState
	input         textinput.Model
	messages      []Message
	viewport      viewport.Model
	spinner       spinner.Model
	router        *query.Router
	config        *config.Config
	cacheManager  *cache.Manager
	width         int
	height        int
	processing    bool
	showReasoning bool
}

type queryResultMsg struct {
	query  string
	result string
	err    error
}

type switchToCacheViewerMsg struct{}

// Theme

var (
	primaryColor   = lipgloss.Color("#5EEAD4") // teal                —     accents, user, links
	secondaryColor = lipgloss.Color("#A78BFA") // violet              — chrome / titles
	successColor   = lipgloss.Color("#34D399") // emerald             — assistant badge
	errorColor     = lipgloss.Color("#F87171") // coral               — errors
	warningColor   = lipgloss.Color("#FBBF24") // amber               — warnings
	mutedColor     = lipgloss.Color("#7C8798") // slate               — secondary text
	textColor      = lipgloss.Color("#E7E9EE") // off-white           — body text
	borderColor    = lipgloss.Color("#2A3142") // deep slate          — borders
	codeColor      = lipgloss.Color("#F9E2AF") // warm gold           — code
	codeBgColor    = lipgloss.Color("#1A2030") // code block background
	highlightColor = lipgloss.Color("#C4B5FD") // light violet        — list bullets
	reasoningColor = lipgloss.Color("#5B6478") // dim slate-blue      — reasoning
	quoteColor     = lipgloss.Color("#8B95A8") // blockquote text
	titleBarBg     = lipgloss.Color("#1E1B4B") // deep indigo         — title bar bg
	tableHeaderBg  = lipgloss.Color("#232A3D") // header row background
)

var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)

var (
	numberedListRe = regexp.MustCompile(`^(\d+)\.\s+(.+)$`)
	inlineCodeRe   = regexp.MustCompile("`([^`]+)`")
	boldRe         = regexp.MustCompile(`(\*\*|__)([^*_]+)(\*\*|__)`)
	strikeRe       = regexp.MustCompile(`~~([^~]+)~~`)
	linkRe         = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	isNumberedRe   = regexp.MustCompile(`^\d+\.\s`)
	taskListRe     = regexp.MustCompile(`^[-*+]\s+\[([ xX])\]\s+(.+)$`)
	// tableRowRe     = regexp.MustCompile(`^\s*\|?.*\|.*\|?\s*$`)
	tableSepCellRe = regexp.MustCompile(`^:?-{1,}:?$`)
)

var (
	codeBlockStyle = lipgloss.NewStyle().
			Foreground(codeColor).
			Background(codeBgColor).
			Padding(0, 1)
	codeLangStyle   = lipgloss.NewStyle().Bold(true).Foreground(mutedColor)
	codeInlineStyle = lipgloss.NewStyle().Foreground(codeColor)

	boldStyle     = lipgloss.NewStyle().Bold(true).Foreground(textColor)
	strikeStyle   = lipgloss.NewStyle().Strikethrough(true).Foreground(mutedColor)
	linkTextStyle = lipgloss.NewStyle().Underline(true).Foreground(primaryColor)
	linkURLStyle  = lipgloss.NewStyle().Italic(true).Foreground(mutedColor)

	listStyle = lipgloss.NewStyle().Foreground(highlightColor)

	checkedBoxStyle   = lipgloss.NewStyle().Foreground(successColor).Bold(true)
	uncheckedBoxStyle = lipgloss.NewStyle().Foreground(mutedColor).Bold(true)
	taskDoneStyle     = lipgloss.NewStyle().Strikethrough(true).Foreground(mutedColor)

	h1Style = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(primaryColor)
	h2Style = lipgloss.NewStyle().Bold(true).Foreground(secondaryColor)
	h3Style = lipgloss.NewStyle().Bold(true).Foreground(highlightColor)

	quoteBarStyle = lipgloss.NewStyle().Foreground(borderColor)
	quoteStyle    = lipgloss.NewStyle().Italic(true).Foreground(quoteColor)
	hrStyle       = lipgloss.NewStyle().Foreground(borderColor)

	reasoningLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(reasoningColor)
	reasoningTextStyle  = lipgloss.NewStyle().Italic(true).Foreground(reasoningColor)
	reasoningBarStyle   = lipgloss.NewStyle().Foreground(borderColor)

	tableBorderStyle = lipgloss.NewStyle().Foreground(borderColor)
	tableHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(secondaryColor).Background(tableHeaderBg)
	tableCellStyle   = lipgloss.NewStyle().Foreground(textColor)

	titleBarStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			Background(titleBarBg).
			Padding(0, 2)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Padding(0, 2)

	scrollHintStyle = lipgloss.NewStyle().Foreground(mutedColor).Italic(true)

	keyHintStyle = lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true)

	helpBarStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Padding(0, 2)
)

// roleMeta returns the badge and content style for a message role.
func roleMeta(role string) (badge string, badgeStyle, contentStyle lipgloss.Style) {
	switch role {
	case "user":
		return "› YOU", lipgloss.NewStyle().Foreground(primaryColor).Bold(true), lipgloss.NewStyle().Foreground(textColor)
	case "assistant":
		return "◆ EULIX", lipgloss.NewStyle().Foreground(successColor).Bold(true), lipgloss.NewStyle().Foreground(textColor)
	case "system":
		return "◇ SYSTEM", lipgloss.NewStyle().Foreground(highlightColor).Bold(true), lipgloss.NewStyle().Foreground(mutedColor)
	case "error":
		return "✖ ERROR", lipgloss.NewStyle().Foreground(errorColor).Bold(true), lipgloss.NewStyle().Foreground(errorColor)
	case "warning":
		return "▲ WARNING", lipgloss.NewStyle().Foreground(warningColor).Bold(true), lipgloss.NewStyle().Foreground(warningColor)
	default:
		return "● " + strings.ToUpper(role), lipgloss.NewStyle().Foreground(mutedColor).Bold(true), lipgloss.NewStyle().Foreground(textColor)
	}
}

func MainModel(router *query.Router, cfg *config.Config, cacheManager *cache.Manager) Model {
	ti := textinput.New()
	ti.Placeholder = "Ask a question or type /help for commands"
	ti.Focus()
	ti.CharLimit = 0
	ti.Width = 80
	ti.PromptStyle = lipgloss.NewStyle().Foreground(primaryColor).Bold(true)
	ti.TextStyle = lipgloss.NewStyle().Foreground(textColor)
	ti.Prompt = "❯ "

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
			// TODO make these dynamic
			{
				Role: "system",
				Content: "Eulix initialized.\n\n" +
					"Your codebase is now a searchable book. I am your AI assistant dedicated to helping you navigate, understand, and query your code.\n\n" +
					"Try asking:\n" +
					"  - 'What does this function do?'\n" +
					"  - 'Explain the authentication flow in this module.'\n" +
					"  - 'Find where the database connection is initialized.'\n\n" +
					"Type /help to see available commands.",
			},
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
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()

			return m, tea.Batch(m.spinner.Tick, m.processQuery(q))
		}

	case queryResultMsg:
		m.processing = false

		if msg.err != nil {
			m.messages = append(m.messages, Message{
				Role:    "error",
				Content: fmt.Sprintf("%v", msg.err),
			})
			m.state = StateError
		} else {
			m.messages = append(m.messages, Message{
				Role:    "assistant",
				Content: msg.result,
			})
			m.state = StateDisplaying

			if m.cacheManager != nil {
				reasoning, answer := utils.SplitReasoningAndAnswer(msg.result)
				_, _ = m.cacheManager.Save(msg.query, reasoning, answer)
			}
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
		// reserve rows for header(3) + input(3) + spinner(1) + footer(1) + borders(2)
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
			Role: "system",
			Content: "AVAILABLE COMMANDS\n\n" +
				"  /help     Show this help message\n" +
				"  /history  View cached queries and responses\n" +
				"  /think    Toggle visibility of reasoning traces\n" +
				"  /clear    Clear conversation history\n" +
				"  /stats    Show system statistics\n" +
				"  /quit     Exit the application\n\n" +
				"KEYBOARD SHORTCUTS\n\n" +
				"  Enter     Send message\n" +
				"  Esc       Exit application\n" +
				"  Ctrl+C    Force exit\n" +
				"  ↑ / ↓     Scroll transcript",
		})
	case "/history":
		return m, func() tea.Msg { return switchToCacheViewerMsg{} }
	case "/think", "/reasoning":
		m.showReasoning = !m.showReasoning
		visibility := "hidden"
		if m.showReasoning {
			visibility = "visible"
		}
		m.messages = append(m.messages, Message{
			Role:    "system",
			Content: fmt.Sprintf("Reasoning traces are now %s.", visibility),
		})
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
	reasoningStatus := "Hidden"
	if m.showReasoning {
		reasoningStatus = "Visible"
	}
	return fmt.Sprintf(
		"SYSTEM STATISTICS\n\n"+
			"  Total Messages    %d\n"+
			"  Your Questions    %d\n"+
			"  AI Responses      %d\n"+
			"  Current State     %s\n"+
			"  Cache Status      %s\n"+
			"  Reasoning         %s",
		len(m.messages), userMessages, userMessages, m.getStateName(), cacheStatus, reasoningStatus)
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

	// Header: title bar + live status subtitle
	title := titleBarStyle.Width(m.width).Render("◆ EULIX  —  AI CODEBASE ASSISTANT")
	cacheStatus := "cache off"
	if m.cacheManager != nil {
		cacheStatus = "cache on"
	}
	reasoningStatus := "reasoning hidden"
	if m.showReasoning {
		reasoningStatus = "reasoning shown"
	}
	subtitle := subtitleStyle.Width(m.width).Render(
		fmt.Sprintf("state: %s  •  messages: %d  •  %s  •  %s", m.getStateName(), len(m.messages), cacheStatus, reasoningStatus))
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(subtitle)
	b.WriteString("\n")

	// Transcript
	scrollHint := ""
	if !m.viewport.AtTop() {
		scrollHint = "▲ more above"
	} else if !m.viewport.AtBottom() {
		scrollHint = "▼ more below"
	}
	viewportStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(m.width - 2).
		Height(m.viewport.Height)
	b.WriteString(viewportStyle.Render(m.viewport.View()))
	if scrollHint != "" {
		b.WriteString("\n")
		b.WriteString(scrollHintStyle.Padding(0, 2).Render(scrollHint))
	}
	b.WriteString("\n")

	// Processing pill
	if m.processing {
		processingStyle := lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(primaryColor).
			Padding(0, 1)
		b.WriteString(processingStyle.Render(fmt.Sprintf("%s Thinking...", m.spinner.View())))
		b.WriteString("\n")
	}

	// Input
	inputContainerStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(primaryColor).
		Padding(0, 1).
		Width(m.width - 2)
	b.WriteString(inputContainerStyle.Render(m.input.View()))
	b.WriteString("\n")

	// Footer key hints
	help := fmt.Sprintf(
		"%s send   %s quit   %s commands   %s toggle reasoning   %s scroll",
		keyHintStyle.Render("Enter"),
		keyHintStyle.Render("Esc"),
		keyHintStyle.Render("/help"),
		keyHintStyle.Render("/think"),
		keyHintStyle.Render("↑/↓"),
	)
	b.WriteString(helpBarStyle.Render(help))

	return b.String()
}

func (m Model) processQuery(q string) tea.Cmd {
	return func() tea.Msg {
		result, err := m.router.QueryEngine(q)
		return queryResultMsg{result: result, err: err}
	}
}

func (m Model) renderMessages() string {
	var b strings.Builder

	messagePadding := lipgloss.NewStyle().MarginBottom(1)
	wrapWidth := m.viewport.Width - 6
	if wrapWidth < 40 {
		wrapWidth = 40
	}

	for _, msg := range m.messages {
		badge, badgeStyle, contentStyle := roleMeta(msg.Role)
		header := badgeStyle.Render(badge)

		var content string
		if msg.Role == "assistant" {
			content = renderAssistantMessage(msg.Content, wrapWidth, m.showReasoning)
		} else {
			content = contentStyle.Render(wrapText(msg.Content, wrapWidth))
		}

		b.WriteString(messagePadding.Render(fmt.Sprintf("%s\n%s", header, content)))
		b.WriteString("\n")
	}

	return b.String()
}

// renderAssistantMessage renders a full assistant turn: an optional
// reasoning block followed by the markdown-formatted answer.
func renderAssistantMessage(raw string, width int, showReasoning bool) string {
	reasoning, answer := utils.SplitReasoningAndAnswer(raw)

	var b strings.Builder
	if reasoning != "" {
		b.WriteString(renderReasoningBlock(reasoning, width, showReasoning))
		b.WriteString("\n\n")
	}
	b.WriteString(renderMarkdownBody(answer, width))

	return b.String()
}

// renderReasoningBlock renders the model's reasoning trace. Collapsed by
// default so the answer stays front and center; /think expands it in
// place with a dim left rule to keep it visually subordinate to the answer.
func renderReasoningBlock(reasoning string, width int, expanded bool) string {
	lines := strings.Count(strings.TrimSpace(reasoning), "\n") + 1

	if !expanded {
		return reasoningLabelStyle.Render("◔ REASONING") + "  " +
			reasoningTextStyle.Render(fmt.Sprintf("%d lines hidden — /think to expand", lines))
	}

	var b strings.Builder
	b.WriteString(reasoningLabelStyle.Render("◔ REASONING"))
	b.WriteByte('\n')

	wrapped := wrapText(reasoning, width-2)
	for _, line := range strings.Split(wrapped, "\n") {
		b.WriteString(reasoningBarStyle.Render("│ "))
		b.WriteString(reasoningTextStyle.Render(line))
		b.WriteByte('\n')
	}

	return strings.TrimRight(b.String(), "\n")
}

// renderMarkdownBody formats markdown-ish text (headings, lists, code
// blocks, blockquotes, rules, links, tables, task lists, and
// bold/strikethrough/inline code) for terminal display.
func renderMarkdownBody(text string, width int) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	var b strings.Builder
	b.Grow(len(text) + 256)

	inCodeBlock := false
	inList := false
	inQuote := false
	prevWasParagraph := false

	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")

		if line == "" {
			b.WriteByte('\n')
			inList = false
			inQuote = false
			prevWasParagraph = false
			continue
		}

		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inCodeBlock = !inCodeBlock
			if inCodeBlock {
				lang := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "```"), "~~~"))
				if lang != "" {
					fmt.Fprintf(&b, "%s\n", codeLangStyle.Render("["+strings.ToUpper(lang)+"]"))
				}
			} else {
				b.WriteByte('\n')
			}
			prevWasParagraph = false
			continue
		}

		if inCodeBlock {
			fmt.Fprintf(&b, "%s\n", codeBlockStyle.Render(line))
			continue
		}

		// Table: a line containing a pipe, immediately followed by a
		// valid separator row (e.g. |---|:--:|---|), starts a GFM table.
		// Consume every subsequent row line as part of the table.
		if isTableRow(line) && i+1 < len(lines) && isTableSeparator(lines[i+1]) {
			tableLines := []string{line, lines[i+1]}
			j := i + 2
			for j < len(lines) {
				candidate := strings.TrimRight(lines[j], " \t")
				if candidate == "" || !isTableRow(candidate) {
					break
				}
				tableLines = append(tableLines, candidate)
				j++
			}
			fmt.Fprintf(&b, "%s\n", renderTable(tableLines, width))
			i = j - 1
			inList = false
			inQuote = false
			prevWasParagraph = false
			continue
		}

		if m := headingRe.FindStringSubmatch(line); m != nil {
			if prevWasParagraph {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "%s\n", headingStyleFor(len(m[1])).Render(strings.TrimSpace(m[2])))
			inList = false
			inQuote = false
			prevWasParagraph = false
			continue
		}

		if isHorizontalRule(line) {
			fmt.Fprintf(&b, "%s\n", hrStyle.Render(strings.Repeat("─", clampWidth(width))))
			inList = false
			inQuote = false
			prevWasParagraph = false
			continue
		}

		if isBlockquote(line) {
			quoteText := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), ">"))
			formatted := processInlineMarkdown(quoteText, width-4)
			for _, l := range strings.Split(formatted, "\n") {
				fmt.Fprintf(&b, "%s%s\n", quoteBarStyle.Render("┃ "), quoteStyle.Render(l))
			}
			inList = false
			inQuote = true
			prevWasParagraph = false
			continue
		}

		if m := taskListRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			fmt.Fprintf(&b, "  %s\n", formatTaskItem(m[1], m[2], width-4))
			inList = true
			inQuote = false
			prevWasParagraph = false
			continue
		}

		if isListItem(line) {
			formatted := formatListItem(line, width-4)
			fmt.Fprintf(&b, "  %s\n", formatted)
			inList = true
			inQuote = false
			prevWasParagraph = false
			continue
		}

		if inList && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")) {
			trimmed := strings.TrimLeft(line, " \t")
			formatted := processInlineMarkdown(trimmed, width-4)
			fmt.Fprintf(&b, "    %s\n", formatted)
			continue
		}

		if inList {
			b.WriteByte('\n')
			inList = false
		}
		if inQuote {
			inQuote = false
		}

		formatted := processInlineMarkdown(line, width)
		fmt.Fprintf(&b, "%s\n", formatted)
		prevWasParagraph = true
	}

	return strings.TrimRight(b.String(), "\n")
}

func headingStyleFor(level int) lipgloss.Style {
	switch level {
	case 1:
		return h1Style
	case 2:
		return h2Style
	default:
		return h3Style
	}
}

func clampWidth(width int) int {
	if width < 8 {
		return 8
	}
	return width
}

func isHorizontalRule(line string) bool {
	t := strings.TrimSpace(line)
	if len(t) < 3 {
		return false
	}
	for _, r := range []byte{'-', '*', '_'} {
		if strings.Count(t, string(r)) == len(t) {
			return true
		}
	}
	return false
}

func isBlockquote(line string) bool {
	t := strings.TrimSpace(line)
	return t == ">" || strings.HasPrefix(t, "> ")
}

func isListItem(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") {
		return true
	}
	return isNumberedRe.MatchString(line)
}

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

// formatTaskItem renders a GFM task-list item: "- [ ] foo" / "- [x] bar".
// Checked items get a filled box and strikethrough text; unchecked get an
// empty box in muted styling.
func formatTaskItem(mark, content string, width int) string {
	checked := mark == "x" || mark == "X"

	box := uncheckedBoxStyle.Render("☐")
	text := processInlineMarkdown(content, width-4)
	if checked {
		box = checkedBoxStyle.Render("☑")
		text = taskDoneStyle.Render(text)
	}

	return box + " " + text
}

// isTableRow reports whether line looks like a GFM table row: it must
// contain at least one unescaped pipe that isn't just inline code/text
// with a stray "|" in it. We require at least one "|" outside of a code
// span, and at least one non-empty cell.
func isTableRow(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" || !strings.Contains(t, "|") {
		return false
	}
	cells := splitTableRow(t)
	return len(cells) >= 2
}

// isTableSeparator reports whether line is a GFM header separator row,
// e.g. "|---|:---:|---:|" or "--- | ---".
func isTableSeparator(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	cells := splitTableRow(t)
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		c = strings.TrimSpace(c)
		if c == "" || !tableSepCellRe.MatchString(c) {
			return false
		}
	}
	return true
}

// splitTableRow splits a table row on unescaped "|" characters, trimming
// a leading/trailing empty cell produced by outer pipes (e.g. "| a | b |").
func splitTableRow(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")

	// Split on "|" that isn't escaped with a backslash.
	var cells []string
	var cur strings.Builder
	escaped := false
	for _, r := range t {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
			cur.WriteRune(r)
		case r == '|':
			cells = append(cells, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	cells = append(cells, cur.String())

	for i, c := range cells {
		cells[i] = strings.TrimSpace(strings.ReplaceAll(c, `\|`, "|"))
	}
	return cells
}

type tableAlign int

const (
	alignLeft tableAlign = iota
	alignCenter
	alignRight
)

// parseTableAlignment reads a separator cell like ":---", "---:", ":--:",
// or "---" and returns the column alignment.
func parseTableAlignment(cell string) tableAlign {
	cell = strings.TrimSpace(cell)
	left := strings.HasPrefix(cell, ":")
	right := strings.HasSuffix(cell, ":")
	switch {
	case left && right:
		return alignCenter
	case right:
		return alignRight
	default:
		return alignLeft
	}
}

// renderTable renders a GFM table (header + separator + body rows) as a
// bordered, column-aligned block using box-drawing characters, wrapping
// cell content and the whole table to fit within width.
func renderTable(tableLines []string, width int) string {
	header := splitTableRow(tableLines[0])
	aligns := make([]tableAlign, len(header))
	if len(tableLines) > 1 {
		sepCells := splitTableRow(tableLines[1])
		for i := range header {
			if i < len(sepCells) {
				aligns[i] = parseTableAlignment(sepCells[i])
			}
		}
	}

	var bodyRows [][]string
	for _, l := range tableLines[2:] {
		row := splitTableRow(l)
		// Normalize row length to header length.
		for len(row) < len(header) {
			row = append(row, "")
		}
		if len(row) > len(header) {
			row = row[:len(header)]
		}
		bodyRows = append(bodyRows, row)
	}

	numCols := len(header)
	if numCols == 0 {
		return ""
	}

	// Compute natural column widths from content (rendered, inline-formatted
	// so bold/code markers don't inflate width incorrectly — we measure the
	// raw text width, which is a good-enough proxy since ANSI codes are
	// added after wrapping).
	colWidth := make([]int, numCols)
	for i, h := range header {
		colWidth[i] = lipgloss.Width(strings.TrimSpace(stripInlineMarkers(h)))
	}
	for _, row := range bodyRows {
		for i, c := range row {
			w := lipgloss.Width(strings.TrimSpace(stripInlineMarkers(c)))
			if w > colWidth[i] {
				colWidth[i] = w
			}
		}
	}

	const minColWidth = 3
	const maxColWidth = 40
	for i := range colWidth {
		if colWidth[i] < minColWidth {
			colWidth[i] = minColWidth
		}
		if colWidth[i] > maxColWidth {
			colWidth[i] = maxColWidth
		}
	}

	// Shrink columns proportionally if the table is wider than the
	// available width (accounting for borders: 1 + numCols*3 + sum(widths)).
	overhead := 1 + numCols*3
	total := overhead
	for _, w := range colWidth {
		total += w
	}
	if total > width && width > overhead+numCols*minColWidth {
		budget := width - overhead
		sum := 0
		for _, w := range colWidth {
			sum += w
		}
		remaining := budget
		for i := range colWidth {
			share := colWidth[i] * budget / sum
			if share < minColWidth {
				share = minColWidth
			}
			colWidth[i] = share
			remaining -= share
		}
		// Dump any leftover budget into the widest column.
		if remaining > 0 {
			widest := 0
			for i := range colWidth {
				if colWidth[i] > colWidth[widest] {
					widest = i
				}
			}
			colWidth[widest] += remaining
		}
	}

	var b strings.Builder

	writeBorder := func(left, mid, right string) {
		b.WriteString(tableBorderStyle.Render(left))
		for i, w := range colWidth {
			b.WriteString(tableBorderStyle.Render(strings.Repeat("─", w+2)))
			if i < numCols-1 {
				b.WriteString(tableBorderStyle.Render(mid))
			}
		}
		b.WriteString(tableBorderStyle.Render(right))
		b.WriteByte('\n')
	}

	writeRow := func(cells []string, style lipgloss.Style) {
		// Wrap each cell to its column width, then render row-by-row so
		// multi-line cells stay aligned across the row.
		wrapped := make([][]string, numCols)
		maxLines := 1
		for i, c := range cells {
			c = processInlineMarkdown(c, colWidth[i])
			ls := strings.Split(c, "\n")
			wrapped[i] = ls
			if len(ls) > maxLines {
				maxLines = len(ls)
			}
		}
		for line := 0; line < maxLines; line++ {
			b.WriteString(tableBorderStyle.Render("│"))
			for i := 0; i < numCols; i++ {
				var cellLine string
				if line < len(wrapped[i]) {
					cellLine = wrapped[i][line]
				}
				pad := colWidth[i] - lipgloss.Width(cellLine)
				if pad < 0 {
					pad = 0
				}
				var padded string
				switch aligns[i] {
				case alignRight:
					padded = strings.Repeat(" ", pad) + cellLine
				case alignCenter:
					l := pad / 2
					r := pad - l
					padded = strings.Repeat(" ", l) + cellLine + strings.Repeat(" ", r)
				default:
					padded = cellLine + strings.Repeat(" ", pad)
				}
				b.WriteString(" ")
				b.WriteString(style.Render(padded))
				b.WriteString(" ")
				b.WriteString(tableBorderStyle.Render("│"))
			}
			b.WriteByte('\n')
		}
	}

	writeBorder("┌", "┬", "┐")
	writeRow(header, tableHeaderStyle)
	writeBorder("├", "┼", "┤")
	for _, row := range bodyRows {
		writeRow(row, tableCellStyle)
	}
	writeBorder("└", "┴", "┘")

	return strings.TrimRight(b.String(), "\n")
}

// stripInlineMarkers gives a rough plain-text width estimate for a markdown
// cell by removing syntax markers, without doing full styled rendering.
// Used only for column-width sizing, not final output.
func stripInlineMarkers(s string) string {
	s = linkRe.ReplaceAllString(s, "$1")
	s = inlineCodeRe.ReplaceAllString(s, "$1")
	s = boldRe.ReplaceAllString(s, "$2")
	s = strikeRe.ReplaceAllString(s, "$1")
	return s
}

// processInlineMarkdown handles [links](url), `code`, **bold**, and
// ~~strikethrough~~ spans, then word-wraps the result.
func processInlineMarkdown(text string, width int) string {
	text = linkRe.ReplaceAllStringFunc(text, func(match string) string {
		m := linkRe.FindStringSubmatch(match)
		if len(m) < 3 {
			return match
		}
		return linkTextStyle.Render(m[1]) + " " + linkURLStyle.Render("("+m[2]+")")
	})

	text = inlineCodeRe.ReplaceAllStringFunc(text, func(match string) string {
		return codeInlineStyle.Render(strings.Trim(match, "`"))
	})

	text = boldRe.ReplaceAllStringFunc(text, func(match string) string {
		if inner := boldRe.FindStringSubmatch(match); len(inner) > 2 {
			return boldStyle.Render(inner[2])
		}
		return match
	})

	text = strikeRe.ReplaceAllStringFunc(text, func(match string) string {
		if inner := strikeRe.FindStringSubmatch(match); len(inner) > 1 {
			return strikeStyle.Render(inner[1])
		}
		return match
	})

	return wrapText(text, width)
}

// wrapText wraps text at word boundaries using ANSI-aware widths, preserving
// existing newlines rather than collapsing them (as strings.Fields would).
func wrapText(text string, width int) string {
	if width <= 0 {
		width = 80
	}

	inputLines := strings.Split(text, "\n")
	out := make([]string, 0, len(inputLines))

	for _, inputLine := range inputLines {
		if inputLine == "" {
			out = append(out, "")
			continue
		}

		var curLine strings.Builder
		curLen := 0

		words := strings.Split(inputLine, " ")
		for i, word := range words {
			wLen := lipgloss.Width(word)
			if wLen == 0 {
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

			if i == len(words)-1 {
				out = append(out, curLine.String())
			}
		}

		if len(words) == 0 {
			out = append(out, "")
		}
	}

	return strings.Join(out, "\n")
}
