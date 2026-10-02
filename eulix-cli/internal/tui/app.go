//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package tui provides terminal UI components, layouts, and views for the CLI.

// Main TUI model: chat transcript, input box, and markdown-ish response formatting.

package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"eulix/internal/cache"
	"eulix/internal/config"
	"eulix/internal/query"
	"eulix/internal/utils"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type AppState int

type tableAlign int

const (
	StateIdle AppState = iota
	StateTyping
	StateProcessing
	StateDisplaying
	StateError
)

const (
	alignLeft tableAlign = iota
	alignCenter
	alignRight
)

// Message is one transcript entry. The unexported fields are a render cache:
// assistant output is split into reasoning/answer once, and the styled
// result is reused until the width or the reasoning toggle changes.
type Message struct {
	Role    string
	Content string

	reasoning     string
	answer        string
	rendered      string
	renderedW     int
	renderedThink bool
}

func newMessage(role, content string) Message {
	msg := Message{Role: role, Content: content}
	if role == "assistant" {
		msg.reasoning, msg.answer = utils.SplitReasoningAndAnswer(content)
	}
	return msg
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

// Theme has been moved to utils package

var (
	headingRe      = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	numberedListRe = regexp.MustCompile(`^(\d+)\.\s+(.+)$`)
	taskContentRe  = regexp.MustCompile(`^\[([ xX])\]\s+(.+)$`)
	inlineCodeRe   = regexp.MustCompile("`([^`]+)`")
	boldStarRe     = regexp.MustCompile(`\*\*(.+?)\*\*`)
	boldUnderRe    = regexp.MustCompile(`\b__(.+?)__\b`)
	strikeRe       = regexp.MustCompile(`~~([^~]+)~~`)
	linkRe         = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	placeholderRe  = regexp.MustCompile("\x00(\\d+)\x00")
	tableSepCellRe = regexp.MustCompile(`^:?-{1,}:?$`)
)

var (
	codeBlockStyle = lipgloss.NewStyle().
			Foreground(utils.CodeColor).
			Background(utils.CodeBgColor).
			Padding(0, 1)
	codeLangStyle   = lipgloss.NewStyle().Bold(true).Foreground(utils.MutedColor)
	codeInlineStyle = lipgloss.NewStyle().Foreground(utils.CodeColor)

	boldStyle     = lipgloss.NewStyle().Bold(true).Foreground(utils.TextColor)
	strikeStyle   = lipgloss.NewStyle().Strikethrough(true).Foreground(utils.MutedColor)
	linkTextStyle = lipgloss.NewStyle().Underline(true).Foreground(utils.PrimaryColor)
	linkURLStyle  = lipgloss.NewStyle().Italic(true).Foreground(utils.MutedColor)

	listStyle = lipgloss.NewStyle().Foreground(utils.HighlightColor)

	checkedBoxStyle   = lipgloss.NewStyle().Foreground(utils.SecondaryColor).Bold(true)
	uncheckedBoxStyle = lipgloss.NewStyle().Foreground(utils.MutedColor).Bold(true)
	taskDoneStyle     = lipgloss.NewStyle().Strikethrough(true).Foreground(utils.MutedColor)

	h1Style = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(utils.PrimaryColor)
	h2Style = lipgloss.NewStyle().Bold(true).Foreground(utils.SecondaryColor)
	h3Style = lipgloss.NewStyle().Bold(true).Foreground(utils.HighlightColor)

	quoteBarStyle = lipgloss.NewStyle().Foreground(utils.BorderColor)
	quoteStyle    = lipgloss.NewStyle().Italic(true).Foreground(utils.QuoteColor)
	hrStyle       = lipgloss.NewStyle().Foreground(utils.BorderColor)

	reasoningLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(utils.ReasoningColor)
	reasoningTextStyle  = lipgloss.NewStyle().Italic(true).Foreground(utils.ReasoningColor)
	reasoningBarStyle   = lipgloss.NewStyle().Foreground(utils.BorderColor)

	tableBorderStyle = lipgloss.NewStyle().Foreground(utils.BorderColor)
	tableHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(utils.SecondaryColor).Background(utils.TableHeaderBg)
	tableCellStyle   = lipgloss.NewStyle().Foreground(utils.TextColor)

	titleBarStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(utils.PrimaryColor).
			Background(utils.TitleBarBg).
			Padding(0, 2)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(utils.MutedColor).
			Padding(0, 2)

	scrollHintStyle = lipgloss.NewStyle().Foreground(utils.MutedColor).Italic(true).Padding(0, 2)
	statusStyle     = lipgloss.NewStyle().Foreground(utils.PrimaryColor).Bold(true).Padding(0, 2)

	keyHintStyle = lipgloss.NewStyle().
			Foreground(utils.PrimaryColor).
			Bold(true)

	helpBarStyle = lipgloss.NewStyle().
			Foreground(utils.MutedColor).
			Padding(0, 2)

	// Hoisted from View(): built once, only Width/Height are applied per frame.
	viewportBoxStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(utils.BorderColor).
				Padding(0, 2)
	inputBoxStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(utils.PrimaryColor).
			Padding(0, 1)
)

type roleStyle struct {
	badge   string
	badgeSt lipgloss.Style
	textSt  lipgloss.Style
}

var roleStyles = map[string]roleStyle{
	"user": {
		"› YOU",
		lipgloss.NewStyle().Foreground(utils.PrimaryColor).Bold(true),
		lipgloss.NewStyle().Foreground(utils.TextColor),
	},
	"assistant": {
		"◆ EULIX",
		lipgloss.NewStyle().Foreground(utils.BorderColor).Bold(true),
		lipgloss.NewStyle().Foreground(utils.TextColor),
	},
	"system": {
		"◇ SYSTEM",
		lipgloss.NewStyle().Foreground(utils.HighlightColor).Bold(true),
		lipgloss.NewStyle().Foreground(utils.MutedColor),
	},
	"error": {
		"✖ ERROR",
		lipgloss.NewStyle().Foreground(utils.ErrorColor).Bold(true),
		lipgloss.NewStyle().Foreground(utils.ErrorColor),
	},
	"warning": {
		"▲ WARNING",
		lipgloss.NewStyle().Foreground(utils.WarningColor).Bold(true),
		lipgloss.NewStyle().Foreground(utils.WarningColor),
	},
}

// roleMeta returns the badge and content style for a message role.
func roleMeta(role string) (string, lipgloss.Style, lipgloss.Style) {
	if rs, ok := roleStyles[role]; ok {
		return rs.badge, rs.badgeSt, rs.textSt
	}
	return "● " + strings.ToUpper(role),
		lipgloss.NewStyle().Foreground(utils.MutedColor).Bold(true),
		lipgloss.NewStyle().Foreground(utils.TextColor)
}

func MainModel(router *query.Router, cfg *config.Config, cacheManager *cache.Manager) Model {
	ti := textinput.New()
	ti.Placeholder = "Ask a question or type /help for commands"
	ti.Focus()
	ti.CharLimit = 0
	ti.Width = 80
	ti.PromptStyle = lipgloss.NewStyle().Foreground(utils.PrimaryColor).Bold(true)
	ti.TextStyle = lipgloss.NewStyle().Foreground(utils.TextColor)
	ti.Prompt = "❯ "

	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(utils.PrimaryColor)

	vp := viewport.New(80, 20)
	vp.MouseWheelEnabled = false
	// Only arrows/page keys scroll. The default keymap binds space, f, b, d,
	// u, j, k, which also used to scrolled the transcript while typing in the input.
	vp.KeyMap = viewport.KeyMap{
		Up:       key.NewBinding(key.WithKeys("up")),
		Down:     key.NewBinding(key.WithKeys("down")),
		PageUp:   key.NewBinding(key.WithKeys("pgup")),
		PageDown: key.NewBinding(key.WithKeys("pgdown")),
	}

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
			newMessage("system",
				"Eulix initialized.\n\n"+
					"Your codebase is now a searchable book. I am your AI assistant dedicated to helping you navigate, understand, and query your code.\n\n"+
					"Try asking:\n"+
					"  - 'What does this function do?'\n"+
					"  - 'Explain the authentication flow in this module.'\n"+
					"  - 'Find where the database connection is initialized.'\n\n"+
					"Type /help to see available commands."),
		},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		tea.DisableMouse,
	)
}

// push appends a message and refreshes the viewport.
func (m *Model) push(role, content string) {
	m.messages = append(m.messages, newMessage(role, content))
	m.refresh()
}

// refresh re-renders the transcript (cached per message) and sticks to bottom.
func (m *Model) refresh() {
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "esc":
			// Clear the input first; only quit when it's already empty.
			if m.input.Value() != "" {
				m.input.SetValue("")
				return m, nil
			}
			return m, tea.Quit

		case "enter":
			// Typing stays enabled while processing; only submit is blocked.
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

			m.input.SetValue("")
			m.processing = true
			m.state = StateProcessing
			m.push("user", q)

			return m, tea.Batch(m.spinner.Tick, m.processQuery(q))
		}

	case queryResultMsg:
		m.processing = false

		if msg.err != nil {
			m.state = StateError
			m.push("error", msg.err.Error())
			return m, nil
		}

		m.state = StateDisplaying
		m.push("assistant", msg.result)

		if cm := m.cacheManager; cm != nil {
			last := m.messages[len(m.messages)-1]
			q, reasoning, answer := msg.query, last.reasoning, last.answer
			// Persist off the UI goroutine so disk I/O never blocks rendering.
			return m, func() tea.Msg {
				_, _ = cm.Save(q, reasoning, answer)
				return nil
			}
		}
		return m, nil

	case spinner.TickMsg:
		if m.processing {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.WindowSizeMsg:
		widthChanged := msg.Width != m.width
		m.width, m.height = msg.Width, msg.Height
		m.viewport.Width = max(10, msg.Width-6)
		m.viewport.Height = max(3, msg.Height-9)
		m.input.Width = max(10, msg.Width-8)
		if widthChanged {
			m.viewport.SetContent(m.renderMessages())
		}

	case switchToCacheViewerMsg:
		if m.cacheManager == nil {
			m.push("error", "Cache is not enabled. Enable cache in eulix.toml to use this feature.")
			return m, nil
		}

		entries, err := m.cacheManager.ListAll()
		if err != nil {
			m.push("error", fmt.Sprintf("Failed to load cache history: %v", err))
			return m, nil
		}

		if len(entries) == 0 {
			m.push("system", "No cache entries found. Your question history is empty.")
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

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m Model) handleCommand(command string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return m, nil
	}

	m.input.SetValue("")

	switch parts[0] {
	case "/help":
		m.push("system",
			"AVAILABLE COMMANDS\n\n"+
				"  /help     Show this help message\n"+
				"  /history  View cached queries and responses\n"+
				"  /think    Toggle visibility of reasoning traces\n"+
				"  /clear    Clear conversation history\n"+
				"  /stats    Show system statistics\n"+
				"  /quit     Exit the application\n\n"+
				"KEYBOARD SHORTCUTS\n\n"+
				"  Enter     Send message\n"+
				"  Esc       Clear input, or exit if empty\n"+
				"  Ctrl+C    Force exit\n"+
				"  ↑ / ↓     Scroll transcript\n"+
				"  PgUp/PgDn Scroll by page")
	case "/history":
		return m, func() tea.Msg { return switchToCacheViewerMsg{} }
	case "/think", "/reasoning":
		m.showReasoning = !m.showReasoning
		visibility := "hidden"
		if m.showReasoning {
			visibility = "visible"
		}
		m.push("system", fmt.Sprintf("Reasoning traces are now %s.", visibility))
	case "/clear":
		m.messages = []Message{newMessage("system", "Conversation cleared. How can I help you?")}
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoTop()
	case "/stats":
		m.push("system", m.getSystemStats())
	case "/quit":
		return m, tea.Quit
	default:
		m.push("error", fmt.Sprintf("Unknown command: %s\n\nType /help to see available commands.", parts[0]))
	}

	return m, nil
}

func (m Model) getSystemStats() string {
	userMessages, aiMessages := 0, 0
	for _, msg := range m.messages {
		switch msg.Role {
		case "user":
			userMessages++
		case "assistant":
			aiMessages++
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
		len(m.messages), userMessages, aiMessages, m.getStateName(), cacheStatus, reasoningStatus,
	)
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
	b.Grow(m.width * (m.height + 4))

	// Header
	cacheStatus := "cache off"
	if m.cacheManager != nil {
		cacheStatus = "cache on"
	}
	reasoningStatus := "reasoning hidden"
	if m.showReasoning {
		reasoningStatus = "reasoning shown"
	}
	b.WriteString(titleBarStyle.Width(m.width).Render("◆ EULIX  —  AI CODEBASE ASSISTANT"))
	b.WriteByte('\n')
	b.WriteString(subtitleStyle.Width(m.width).Render(
		fmt.Sprintf("state: %s  •  messages: %d  •  %s  •  %s",
			m.getStateName(), len(m.messages), cacheStatus, reasoningStatus),
	))
	b.WriteByte('\n')

	// Transcript
	b.WriteString(viewportBoxStyle.
		Width(m.width - 2).
		Height(m.viewport.Height).
		Render(m.viewport.View()))
	b.WriteByte('\n')

	// Status line: always exactly one row so layout never shifts.
	switch {
	case m.processing:
		b.WriteString(statusStyle.Render(m.spinner.View() + " Thinking..."))
	case !m.viewport.AtTop() && !m.viewport.AtBottom():
		b.WriteString(scrollHintStyle.Render("▲▼ more above and below"))
	case !m.viewport.AtTop():
		b.WriteString(scrollHintStyle.Render("▲ more above"))
	case !m.viewport.AtBottom():
		b.WriteString(scrollHintStyle.Render("▼ more below"))
	default:
		b.WriteString(scrollHintStyle.Render(" "))
	}
	b.WriteByte('\n')

	// Input
	b.WriteString(inputBoxStyle.Width(m.width - 2).Render(m.input.View()))
	b.WriteByte('\n')

	// Footer key hints
	b.WriteString(helpBarStyle.Render(fmt.Sprintf(
		"%s send   %s quit   %s commands   %s toggle reasoning   %s scroll",
		keyHintStyle.Render("Enter"),
		keyHintStyle.Render("Esc"),
		keyHintStyle.Render("/help"),
		keyHintStyle.Render("/think"),
		keyHintStyle.Render("↑/↓"),
	)))

	return b.String()
}

func (m Model) processQuery(q string) tea.Cmd {
	router := m.router // don't capture the whole Model copy
	return func() tea.Msg {
		result, err := router.QueryEngine(q)
		return queryResultMsg{query: q, result: result, err: err}
	}
}

// renderMessages builds the transcript. Each message's styled output is
// cached and only recomputed when the width or reasoning toggle changes, so
// appending a message costs one render instead of re-rendering everything.
func (m Model) renderMessages() string {
	wrapWidth := max(20, m.viewport.Width)

	var b strings.Builder
	for i := range m.messages {
		msg := &m.messages[i] // shares the backing array, so the cache sticks
		if msg.rendered == "" || msg.renderedW != wrapWidth || msg.renderedThink != m.showReasoning {
			msg.rendered = renderMessage(msg, wrapWidth, m.showReasoning)
			msg.renderedW = wrapWidth
			msg.renderedThink = m.showReasoning
		}
		b.WriteString(msg.rendered)
	}
	return b.String()
}

func renderMessage(msg *Message, width int, showReasoning bool) string {
	badge, badgeStyle, contentStyle := roleMeta(msg.Role)

	var content string
	if msg.Role == "assistant" {
		content = renderAssistantMessage(msg.reasoning, msg.answer, width, showReasoning)
	} else {
		content = styleLines(contentStyle, wrapText(msg.Content, width))
	}

	return badgeStyle.Render(badge) + "\n" + content + "\n\n"
}

// renderAssistantMessage renders a full assistant turn: an optional
// reasoning block followed by the markdown-formatted answer.
func renderAssistantMessage(reasoning, answer string, width int, showReasoning bool) string {
	var b strings.Builder
	if reasoning != "" {
		b.WriteString(renderReasoningBlock(reasoning, width, showReasoning))
		b.WriteString("\n\n")
	}
	b.WriteString(renderMarkdownBody(answer, width))
	return b.String()
}

// renderReasoningBlock renders the model's reasoning trace. Collapsed by
// default so the answer stays front and center; /think expands it in place.
func renderReasoningBlock(reasoning string, width int, expanded bool) string {
	if !expanded {
		lines := strings.Count(strings.TrimSpace(reasoning), "\n") + 1
		return reasoningLabelStyle.Render("◔ REASONING") + "  " +
			reasoningTextStyle.Render(fmt.Sprintf("%d lines hidden — /think to expand", lines))
	}

	var b strings.Builder
	b.WriteString(reasoningLabelStyle.Render("◔ REASONING"))
	b.WriteByte('\n')

	bar := reasoningBarStyle.Render("│ ")
	for _, line := range strings.Split(wrapText(reasoning, clampWidth(width-2)), "\n") {
		b.WriteString(bar)
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
	fenceMarker := ""
	fenceIndent := 0
	inList := false
	prevWasParagraph := false

	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)

		// Fences are detected on the trimmed line, so fences nested in list
		// items ("   ```go") work. Inside a block only the same marker closes.
		if marker := fenceOf(trimmed); marker != "" && (!inCodeBlock || marker == fenceMarker) {
			inCodeBlock = !inCodeBlock
			if inCodeBlock {
				fenceMarker = marker
				fenceIndent = len(line) - len(strings.TrimLeft(line, " \t"))
				if lang := strings.TrimSpace(strings.TrimPrefix(trimmed, marker)); lang != "" {
					fmt.Fprintf(&b, "%s\n", codeLangStyle.Render("["+strings.ToUpper(lang)+"]"))
				}
			} else {
				fenceMarker = ""
				b.WriteByte('\n')
			}
			inList = false
			prevWasParagraph = false
			continue
		}

		// Code comes before the blank-line check so empty lines inside a
		// block keep the code background.
		if inCodeBlock {
			code := strings.ReplaceAll(raw, "\t", "    ")
			// Drop the fence's own indentation, keep relative indentation.
			strip := 0
			for strip < fenceIndent && strip < len(code) && code[strip] == ' ' {
				strip++
			}
			code = strings.TrimRight(code[strip:], " ")
			if code == "" {
				code = " "
			}
			for _, cl := range strings.Split(ansi.Wrap(code, clampWidth(width-2), ""), "\n") {
				if cl == "" {
					cl = " "
				}
				b.WriteString(codeBlockStyle.Render(cl))
				b.WriteByte('\n')
			}
			continue
		}

		if trimmed == "" {
			b.WriteByte('\n')
			inList = false
			prevWasParagraph = false
			continue
		}

		// Table: a row immediately followed by a separator row starts a GFM
		// table; consume all subsequent rows.
		if i+1 < len(lines) && isTableRow(line) && isTableSeparator(lines[i+1]) {
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
			b.WriteString(renderTable(tableLines, width))
			b.WriteByte('\n')
			i = j - 1
			inList = false
			prevWasParagraph = false
			continue
		}

		if m := headingRe.FindStringSubmatch(trimmed); m != nil {
			if prevWasParagraph {
				b.WriteByte('\n')
			}
			// Style the stripped text so inner ANSI resets can't cancel the
			// heading style, and wrap long headings.
			title := wrapText(stripInlineMarkers(strings.TrimSpace(m[2])), width)
			b.WriteString(styleLines(headingStyleFor(len(m[1])), title))
			b.WriteByte('\n')
			inList = false
			prevWasParagraph = false
			continue
		}

		if isHorizontalRule(trimmed) {
			b.WriteString(hrStyle.Render(strings.Repeat("─", clampWidth(width))))
			b.WriteByte('\n')
			inList = false
			prevWasParagraph = false
			continue
		}

		if isBlockquote(trimmed) {
			q := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			wrapped := wrapText(stripInlineMarkers(q), clampWidth(width-2))
			bar := quoteBarStyle.Render("┃ ")
			for _, l := range strings.Split(wrapped, "\n") {
				b.WriteString(bar)
				b.WriteString(quoteStyle.Render(l))
				b.WriteByte('\n')
			}
			inList = false
			prevWasParagraph = false
			continue
		}

		if indent, bullet, content, ok := parseListItem(line); ok {
			b.WriteString(formatListItem(indent, bullet, content, width))
			b.WriteByte('\n')
			inList = true
			prevWasParagraph = false
			continue
		}

		// Wrapped continuation line of a list item.
		if inList && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")) {
			b.WriteString("    ")
			b.WriteString(strings.ReplaceAll(processInlineMarkdown(trimmed, clampWidth(width-4)), "\n", "\n    "))
			b.WriteByte('\n')
			continue
		}

		if inList {
			b.WriteByte('\n')
			inList = false
		}

		b.WriteString(processInlineMarkdown(line, width))
		b.WriteByte('\n')
		prevWasParagraph = true
	}

	return strings.TrimRight(b.String(), "\n")
}

// fenceOf returns "```" or "~~~" if trimmed starts a/ends a fence, else "".
func fenceOf(trimmed string) string {
	switch {
	case strings.HasPrefix(trimmed, "```"):
		return "```"
	case strings.HasPrefix(trimmed, "~~~"):
		return "~~~"
	}
	return ""
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

// styleLines applies a style to each line separately so multi-line text
// doesn't get padded to a common width by lipgloss.
func styleLines(st lipgloss.Style, s string) string {
	if !strings.Contains(s, "\n") {
		return st.Render(s)
	}
	ls := strings.Split(s, "\n")
	for i, l := range ls {
		ls[i] = st.Render(l)
	}
	return strings.Join(ls, "\n")
}

// isHorizontalRule: 3+ of the same '-', '*' or '_' (spaces allowed), nothing
// else. Byte loop, no allocations.
func isHorizontalRule(line string) bool {
	var c byte
	n := 0
	for i := 0; i < len(line); i++ {
		b := line[i]
		if b == ' ' || b == '\t' {
			continue
		}
		if b != '-' && b != '*' && b != '_' {
			return false
		}
		if n == 0 {
			c = b
		} else if b != c {
			return false
		}
		n++
	}
	return n >= 3
}

func isBlockquote(trimmed string) bool {
	return trimmed == ">" || strings.HasPrefix(trimmed, "> ")
}

// parseListItem parses "- x", "* x", "+ x" and "1. x" (any indentation) and
// returns the indent depth, the bullet text and the item content.
func parseListItem(line string) (indent int, bullet, content string, ok bool) {
	for _, r := range line {
		if r == ' ' {
			indent++
		} else if r == '\t' {
			indent += 4
		} else {
			break
		}
	}
	t := strings.TrimSpace(line)
	if len(t) < 3 {
		return 0, "", "", false
	}

	if (t[0] == '-' || t[0] == '*' || t[0] == '+') && t[1] == ' ' {
		return indent, "•", strings.TrimSpace(t[2:]), true
	}
	if t[0] >= '0' && t[0] <= '9' {
		if m := numberedListRe.FindStringSubmatch(t); m != nil {
			return indent, m[1] + ".", m[2], true
		}
	}
	return 0, "", "", false
}

// formatListItem renders one list item with nesting and hanging indent, so
// wrapped lines align under the item text rather than column 0.
func formatListItem(indent int, bullet, content string, width int) string {
	var styledBullet string
	var task []string
	if bullet == "•" {
		task = taskContentRe.FindStringSubmatch(content)
	}

	checked := false
	if task != nil {
		checked = task[1] != " "
		content = task[2]
		if checked {
			styledBullet = checkedBoxStyle.Render("☑")
		} else {
			styledBullet = uncheckedBoxStyle.Render("☐")
		}
		bullet = "☐" // width 1
	} else {
		styledBullet = listStyle.Render(bullet)
	}

	lead := 2 + indent
	pad := strings.Repeat(" ", lead+lipgloss.Width(bullet)+1)
	avail := clampWidth(width - len(pad))

	var body string
	if checked {
		body = styleLines(taskDoneStyle, wrapText(stripInlineMarkers(content), avail))
	} else {
		body = processInlineMarkdown(content, avail)
	}
	body = strings.ReplaceAll(body, "\n", "\n"+pad)

	return strings.Repeat(" ", lead) + styledBullet + " " + body
}

// isTableRow reports whether line looks like a GFM table row.
func isTableRow(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" || !strings.Contains(t, "|") {
		return false
	}
	return len(splitTableRow(t)) >= 2
}

// isTableSeparator reports whether line is a GFM header separator row,
// e.g. "|---|:---:|---:|" or "--- | ---".
func isTableSeparator(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" || !strings.Contains(t, "-") {
		return false
	}
	cells := splitTableRow(t)
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if !tableSepCellRe.MatchString(strings.TrimSpace(c)) {
			return false
		}
	}
	return true
}

// splitTableRow splits a table row on unescaped "|" characters, trimming
// the empty cells produced by outer pipes.
func splitTableRow(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")

	cells := make([]string, 0, strings.Count(t, "|")+1)
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
		c = strings.TrimSpace(c)
		if strings.Contains(c, `\|`) {
			c = strings.ReplaceAll(c, `\|`, "|")
		}
		cells[i] = c
	}
	return cells
}

// parseTableAlignment reads a separator cell like ":---", "---:", ":--:".
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

// renderTable renders a GFM table as a bordered, column-aligned block that
// is guaranteed not to exceed width (when width allows minimum columns).
func renderTable(tableLines []string, width int) string {
	header := splitTableRow(tableLines[0])
	numCols := len(header)
	if numCols == 0 {
		return ""
	}

	aligns := make([]tableAlign, numCols)
	sepCells := splitTableRow(tableLines[1])
	for i := range aligns {
		if i < len(sepCells) {
			aligns[i] = parseTableAlignment(sepCells[i])
		}
	}

	// Single pass over cells: strip markers once for width measurement.
	plainHeader := make([]string, numCols)
	colWidth := make([]int, numCols)
	for i, h := range header {
		plainHeader[i] = stripInlineMarkers(h)
		colWidth[i] = lipgloss.Width(plainHeader[i])
	}

	bodyRows := make([][]string, 0, len(tableLines)-2)
	for _, l := range tableLines[2:] {
		row := splitTableRow(l)
		if len(row) > numCols {
			row = row[:numCols]
		}
		for len(row) < numCols {
			row = append(row, "")
		}
		for i, c := range row {
			if w := lipgloss.Width(stripInlineMarkers(c)); w > colWidth[i] {
				colWidth[i] = w
			}
		}
		bodyRows = append(bodyRows, row)
	}

	const minColWidth = 3
	const maxColWidth = 40
	total := 1 + numCols*3 // borders and cell padding
	for i := range colWidth {
		colWidth[i] = min(max(colWidth[i], minColWidth), maxColWidth)
		total += colWidth[i]
	}

	// Shave the widest column one cell at a time until it fits.
	for excess := total - width; excess > 0; excess-- {
		widest := 0
		for i := range colWidth {
			if colWidth[i] > colWidth[widest] {
				widest = i
			}
		}
		if colWidth[widest] <= minColWidth {
			break
		}
		colWidth[widest]--
	}

	var b strings.Builder
	vbar := tableBorderStyle.Render("│")

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

	writeRow := func(cells []string, isHeader bool) {
		wrapped := make([][]string, numCols)
		maxLines := 1
		for i, c := range cells {
			var s string
			if isHeader {
				s = wrapText(c, colWidth[i]) // c is already plain text
			} else {
				s = processInlineMarkdown(c, colWidth[i])
			}
			ls := strings.Split(s, "\n")
			wrapped[i] = ls
			if len(ls) > maxLines {
				maxLines = len(ls)
			}
		}

		style := tableCellStyle
		if isHeader {
			style = tableHeaderStyle
		}

		for line := 0; line < maxLines; line++ {
			b.WriteString(vbar)
			for i := 0; i < numCols; i++ {
				cellLine := ""
				if line < len(wrapped[i]) {
					cellLine = wrapped[i][line]
				}
				pad := max(0, colWidth[i]-lipgloss.Width(cellLine))
				var padded string
				switch aligns[i] {
				case alignRight:
					padded = strings.Repeat(" ", pad) + cellLine
				case alignCenter:
					l := pad / 2
					padded = strings.Repeat(" ", l) + cellLine + strings.Repeat(" ", pad-l)
				default:
					padded = cellLine + strings.Repeat(" ", pad)
				}
				b.WriteByte(' ')
				b.WriteString(style.Render(padded))
				b.WriteByte(' ')
				b.WriteString(vbar)
			}
			b.WriteByte('\n')
		}
	}

	writeBorder("┌", "┬", "┐")
	writeRow(plainHeader, true)
	writeBorder("├", "┼", "┤")
	for _, row := range bodyRows {
		writeRow(row, false)
	}
	writeBorder("└", "┴", "┘")

	return strings.TrimRight(b.String(), "\n")
}

// stripInlineMarkers removes markdown syntax markers for width measurement
// and for text that gets wrapped in an outer style.
func stripInlineMarkers(s string) string {
	if !strings.ContainsAny(s, "`*_~[") {
		return s
	}
	s = linkRe.ReplaceAllString(s, "$1")
	s = inlineCodeRe.ReplaceAllString(s, "$1")
	s = boldStarRe.ReplaceAllString(s, "$1")
	s = boldUnderRe.ReplaceAllString(s, "$1")
	s = strikeRe.ReplaceAllString(s, "$1")
	return s
}

// processInlineMarkdown handles [links](url), `code`, **bold**, __bold__ and
// ~~strikethrough~~ spans, then wraps the result. Each styled span is swapped
// for a placeholder so later patterns can't match inside already-styled text
// or its ANSI codes; placeholders are restored just before wrapping.
func processInlineMarkdown(text string, width int) string {
	if !strings.ContainsAny(text, "`*_~[") { // fast path: plain prose
		return wrapText(text, width)
	}
	text = strings.ReplaceAll(text, "\x00", "")

	var stash []string
	hold := func(s string) string {
		stash = append(stash, s)
		return "\x00" + strconv.Itoa(len(stash)-1) + "\x00"
	}

	if strings.Contains(text, "`") {
		text = inlineCodeRe.ReplaceAllStringFunc(text, func(m string) string {
			return hold(codeInlineStyle.Render(m[1 : len(m)-1]))
		})
	}
	if strings.Contains(text, "](") {
		text = linkRe.ReplaceAllStringFunc(text, func(m string) string {
			sm := linkRe.FindStringSubmatch(m)
			if len(sm) < 3 {
				return m
			}
			return hold(linkTextStyle.Render(sm[1]) + " " + linkURLStyle.Render("("+sm[2]+")"))
		})
	}
	if strings.Contains(text, "**") {
		text = boldStarRe.ReplaceAllStringFunc(text, func(m string) string {
			return hold(boldStyle.Render(m[2 : len(m)-2]))
		})
	}
	if strings.Contains(text, "__") {
		text = boldUnderRe.ReplaceAllStringFunc(text, func(m string) string {
			return hold(boldStyle.Render(m[2 : len(m)-2]))
		})
	}
	if strings.Contains(text, "~~") {
		text = strikeRe.ReplaceAllStringFunc(text, func(m string) string {
			return hold(strikeStyle.Render(m[2 : len(m)-2]))
		})
	}

	// Restore placeholders (bold may contain a code placeholder, so loop).
	for n := 0; n < 4 && strings.Contains(text, "\x00"); n++ {
		text = placeholderRe.ReplaceAllStringFunc(text, func(m string) string {
			idx, err := strconv.Atoi(m[1 : len(m)-1])
			if err != nil || idx < 0 || idx >= len(stash) {
				return ""
			}
			return stash[idx]
		})
	}

	return wrapText(text, width)
}

// wrapText wraps text to width using ANSI-aware measurement. It preserves
// existing newlines and leading indentation, keeps styled spans intact, and
// hard-breaks words longer than the width (paths, URLs).
func wrapText(text string, width int) string {
	if width <= 0 {
		width = 80
	}
	return ansi.Wrap(text, width, "")
}
