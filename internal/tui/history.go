package tui

import (
	"fmt"
	"strings"
	"time"

	"eulix/internal/cache"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type CacheViewerModel struct {
	list         list.Model
	viewport     viewport.Model
	entries      []cache.CacheEntry
	cacheManager *cache.Manager
	selected     int
	width        int
	height       int
	showDetail   bool
	quitting     bool
}

type cacheItem struct {
	entry cache.CacheEntry
	index int
}
type keyMap struct {
	Up     key.Binding
	Down   key.Binding
	Enter  key.Binding
	Delete key.Binding
	Back   key.Binding
	Quit   key.Binding
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "view details"),
	),
	Delete: key.NewBinding(
		key.WithKeys("d", "delete"),
		key.WithHelp("d", "delete entry"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc", "b"),
		key.WithHelp("esc", "back"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
}

func (i cacheItem) Title() string {
	query := i.entry.Query
	if len(query) > 60 {
		query = query[:57] + "..."
	}

	status := "✓"
	if time.Now().After(i.entry.ExpiresAt) {
		status = "⏱"
	}

	return fmt.Sprintf("%s [%d] %s", status, i.index+1, query)
}

func (i cacheItem) Description() string {
	return fmt.Sprintf("Created: %s • Expires: %s",
		i.entry.CreatedAt.Format("2006-01-02 15:04"),
		i.entry.ExpiresAt.Format("2006-01-02 15:04"))
}

func (i cacheItem) FilterValue() string {
	return i.entry.Query
}

func HistoryView(entries []cache.CacheEntry, manager *cache.Manager) CacheViewerModel {
	items := make([]list.Item, len(entries))
	for i, entry := range entries {
		items[i] = cacheItem{entry: entry, index: i}
	}

	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = "📚 Cache History"
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)

	vp := viewport.New(0, 0)

	return CacheViewerModel{
		list:         l,
		viewport:     vp,
		entries:      entries,
		cacheManager: manager,
		showDetail:   false,
	}
}

func (m CacheViewerModel) Init() tea.Cmd {
	return nil
}

func (m CacheViewerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		h, v := lipgloss.NewStyle().GetFrameSize()
		m.list.SetSize(msg.Width-h, msg.Height-v-4)
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - 6

	case tea.KeyMsg:
		if m.showDetail {
			switch msg.String() {
			case "esc", "b", "q":
				m.showDetail = false
				return m, nil
			case "d", "delete":
				return m, m.deleteCurrentEntry()
			}
		} else {
			switch msg.String() {
			case "q", "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "enter":
				m.selected = m.list.Index()
				m.showDetail = true
				m.viewport.SetContent(m.renderDetail())
				m.viewport.GotoTop()
				return m, nil
			case "d", "delete":
				return m, m.deleteCurrentEntry()
			}
		}
	}

	var cmd tea.Cmd
	if m.showDetail {
		m.viewport, cmd = m.viewport.Update(msg)
	} else {
		m.list, cmd = m.list.Update(msg)
	}

	return m, cmd
}

func (m CacheViewerModel) View() string {
	if m.quitting {
		return ""
	}

	if m.showDetail {
		return m.renderDetailView()
	}

	return m.renderListView()
}

func (m CacheViewerModel) renderListView() string {
	var b strings.Builder

	b.WriteString(m.list.View())
	b.WriteString("\n")

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Padding(1, 0)
	b.WriteString(helpStyle.Render("enter: view • d: delete • q: quit"))

	return b.String()
}

func (m CacheViewerModel) renderDetailView() string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("39")).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("39")).
		Padding(0, 1).
		Width(m.width - 2)

	contentStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1).
		Width(m.width - 4)

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	var b strings.Builder

	b.WriteString(titleStyle.Render("Cache Entry Details"))
	b.WriteString("\n\n")
	b.WriteString(contentStyle.Render(m.viewport.View()))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("esc: back • d: delete • q: quit"))

	return b.String()
}

func (m CacheViewerModel) renderDetail() string {
	if m.selected >= len(m.entries) {
		return "Invalid selection"
	}

	entry := m.entries[m.selected]

	var b strings.Builder

	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	expiredStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	validStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))

	// Status
	expired := time.Now().After(entry.ExpiresAt)
	if expired {
		b.WriteString(expiredStyle.Render("⏱ EXPIRED"))
	} else {
		b.WriteString(validStyle.Render("✓ VALID"))
	}
	b.WriteString("\n\n")

	// Query
	b.WriteString(labelStyle.Render("Query:"))
	b.WriteString("\n")
	b.WriteString(valueStyle.Render(wrapTextCache(entry.Query, m.width-8)))
	b.WriteString("\n\n")

	// Response
	b.WriteString(labelStyle.Render("Response:"))
	b.WriteString("\n")
	responsePreview := entry.Response
	if len(responsePreview) > 500 {
		responsePreview = responsePreview[:497] + "..."
	}
	b.WriteString(valueStyle.Render(wrapTextCache(responsePreview, m.width-8)))
	b.WriteString("\n\n")

	// Metadata
	b.WriteString(labelStyle.Render("Metadata:"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  Created:  %s\n", entry.CreatedAt.Format("2006-01-02 15:04:05")))
	b.WriteString(fmt.Sprintf("  Expires:  %s\n", entry.ExpiresAt.Format("2006-01-02 15:04:05")))

	if !expired {
		timeLeft := time.Until(entry.ExpiresAt)
		b.WriteString(fmt.Sprintf("  Time left: %s\n", formatDuration(timeLeft)))
	}

	b.WriteString(fmt.Sprintf("  Hash:     %s\n", entry.QueryHash))
	b.WriteString(fmt.Sprintf("  Checksum: %s\n", entry.ChecksumHash[:16]+"..."))

	return b.String()
}

func (m CacheViewerModel) deleteCurrentEntry() tea.Cmd {
	return func() tea.Msg {
		if m.selected >= len(m.entries) {
			return nil
		}

		entry := m.entries[m.selected]
		if err := m.cacheManager.Delete(entry.QueryHash); err != nil {
			return nil
		}

		// Remove from local list
		m.entries = append(m.entries[:m.selected], m.entries[m.selected+1:]...)

		// Update list items
		items := make([]list.Item, len(m.entries))
		for i, e := range m.entries {
			items[i] = cacheItem{entry: e, index: i}
		}
		m.list.SetItems(items)

		m.showDetail = false

		return nil
	}
}

func wrapTextCache(text string, width int) string {
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

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
