//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

// History browser: list + detail views over the query/response history log.
package tui

import (
	"fmt"
	"strings"

	"eulix/internal/cache"

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

func (i cacheItem) Title() string {
	query := i.entry.Query
	if len(query) > 60 {
		query = query[:57] + "..."
	}

	hasReasoning := lipgloss.NewStyle().Foreground(mutedColor).Render("·")
	if i.entry.Reasoning != "" {
		hasReasoning = lipgloss.NewStyle().Foreground(highlightColor).Render("◈")
	}

	return fmt.Sprintf("%s  [%d] %s", hasReasoning, i.entry.ID, query)
}

func (i cacheItem) Description() string {
	answerPreview := i.entry.Answer
	if len(answerPreview) > 72 {
		answerPreview = answerPreview[:69] + "..."
	}
	return fmt.Sprintf("%s  •  %s",
		i.entry.CreatedAt.Format("2006-01-02 15:04"),
		answerPreview,
	)
}

func (i cacheItem) FilterValue() string {
	return i.entry.Query
}

// themedDelegate returns a list delegate styled to match the chat view's theme.
func themedDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()

	d.Styles.SelectedTitle = d.Styles.SelectedTitle.Copy().
		Foreground(primaryColor).
		BorderForeground(primaryColor).
		Bold(true)
	d.Styles.SelectedDesc = d.Styles.SelectedDesc.Copy().
		Foreground(highlightColor).
		BorderForeground(primaryColor)
	d.Styles.NormalTitle = d.Styles.NormalTitle.Copy().Foreground(textColor)
	d.Styles.NormalDesc = d.Styles.NormalDesc.Copy().Foreground(mutedColor)
	d.Styles.DimmedTitle = d.Styles.DimmedTitle.Copy().Foreground(mutedColor)
	d.Styles.DimmedDesc = d.Styles.DimmedDesc.Copy().Foreground(mutedColor)

	return d
}

func HistoryView(entries []cache.CacheEntry, manager *cache.Manager) CacheViewerModel {
	items := make([]list.Item, len(entries))
	for i, entry := range entries {
		items[i] = cacheItem{entry: entry, index: i}
	}

	l := list.New(items, themedDelegate(), 0, 0)
	l.Title = "◆ Eulix Query History"
	l.Styles.Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(textColor).
		Background(secondaryColor).
		Padding(0, 2)
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

	listStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1)
	b.WriteString(listStyle.Render(m.list.View()))
	b.WriteString("\n")

	help := fmt.Sprintf(
		"%s view   %s delete   %s quit",
		keyHintStyle.Render("Enter"),
		keyHintStyle.Render("d"),
		keyHintStyle.Render("q"),
	)
	b.WriteString(helpBarStyle.Render(help))

	return b.String()
}

func (m CacheViewerModel) renderDetailView() string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(textColor).
		Background(secondaryColor).
		Padding(0, 2).
		Width(m.width - 2)

	contentStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(m.width - 4)

	var b strings.Builder
	b.WriteString(titleStyle.Render("◆ History Entry"))
	b.WriteString("\n\n")
	b.WriteString(contentStyle.Render(m.viewport.View()))
	b.WriteString("\n")

	help := fmt.Sprintf(
		"%s back   %s delete   %s/%s scroll",
		keyHintStyle.Render("Esc"),
		keyHintStyle.Render("d"),
		keyHintStyle.Render("↑"),
		keyHintStyle.Render("↓"),
	)
	b.WriteString(helpBarStyle.Render(help))

	return b.String()
}

func (m CacheViewerModel) renderDetail() string {
	if m.selected >= len(m.entries) {
		return "Invalid selection"
	}

	entry := m.entries[m.selected]

	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(primaryColor)
	valueStyle := lipgloss.NewStyle().Foreground(textColor)
	metaStyle := lipgloss.NewStyle().Foreground(mutedColor)
	dimStyle := lipgloss.NewStyle().Foreground(mutedColor).Italic(true)

	wrapWidth := m.width - 8
	if wrapWidth < 30 {
		wrapWidth = 30
	}

	var b strings.Builder

	// Metadata header
	fmt.Fprintf(&b, "%s %s\n", metaStyle.Render("ID:"), metaStyle.Render(fmt.Sprintf("%d", entry.ID)))
	fmt.Fprintf(&b, "%s %s\n\n", metaStyle.Render("Logged:"), metaStyle.Render(entry.CreatedAt.Format("2006-01-02 15:04:05")))

	// Query
	b.WriteString(labelStyle.Render("Query"))
	b.WriteString("\n")
	b.WriteString(valueStyle.Render(wrapText(entry.Query, wrapWidth)))
	b.WriteString("\n\n")

	// Reasoning (optional)
	b.WriteString(labelStyle.Render("Reasoning"))
	b.WriteString("\n")
	if entry.Reasoning == "" {
		b.WriteString(dimStyle.Render("(none)"))
	} else {
		b.WriteString(valueStyle.Render(wrapText(entry.Reasoning, wrapWidth)))
	}
	b.WriteString("\n\n")

	// Answer
	b.WriteString(labelStyle.Render("Answer"))
	b.WriteString("\n")
	b.WriteString(valueStyle.Render(wrapText(entry.Answer, wrapWidth)))
	b.WriteString("\n")

	return b.String()
}

func (m CacheViewerModel) deleteCurrentEntry() tea.Cmd {
	return func() tea.Msg {
		if m.selected >= len(m.entries) {
			return nil
		}

		entry := m.entries[m.selected]
		if err := m.cacheManager.Delete(entry.ID); err != nil {
			return nil
		}

		m.entries = append(m.entries[:m.selected], m.entries[m.selected+1:]...)

		items := make([]list.Item, len(m.entries))
		for i, e := range m.entries {
			items[i] = cacheItem{entry: e, index: i}
		}
		m.list.SetItems(items)

		m.showDetail = false
		return nil
	}
}
