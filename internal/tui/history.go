//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package embeddings provides the command-line interface implementation for EULIX.

// Cache history browser: list + detail views over cached query/response pairs.
package tui

import (
	"fmt"
	"strings"
	"time"

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

	status := lipgloss.NewStyle().Foreground(successColor).Render("✓")
	if time.Now().After(i.entry.ExpiresAt) {
		status = lipgloss.NewStyle().Foreground(warningColor).Render("⏱")
	}

	return fmt.Sprintf("%s  [%d] %s", status, i.index+1, query)
}

func (i cacheItem) Description() string {
	return fmt.Sprintf("Created %s   •   Expires %s",
		i.entry.CreatedAt.Format("2006-01-02 15:04"),
		i.entry.ExpiresAt.Format("2006-01-02 15:04"))
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
	l.Title = "◆ Eulix Cache History"
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
	b.WriteString(titleStyle.Render("◆ Cache Entry Details"))
	b.WriteString("\n\n")
	b.WriteString(contentStyle.Render(m.viewport.View()))
	b.WriteString("\n")

	help := fmt.Sprintf(
		"%s back   %s delete   %s quit",
		keyHintStyle.Render("Esc"),
		keyHintStyle.Render("d"),
		keyHintStyle.Render("q"),
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
	statusOkStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(successColor).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(successColor).
		Padding(0, 1)
	statusExpiredStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(warningColor).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(warningColor).
		Padding(0, 1)

	wrapWidth := m.width - 8
	if wrapWidth < 30 {
		wrapWidth = 30
	}

	var b strings.Builder

	expired := time.Now().After(entry.ExpiresAt)
	if expired {
		b.WriteString(statusExpiredStyle.Render("⏱ EXPIRED"))
	} else {
		b.WriteString(statusOkStyle.Render("✓ VALID"))
	}
	b.WriteString("\n\n")

	b.WriteString(labelStyle.Render("Query"))
	b.WriteString("\n")
	b.WriteString(valueStyle.Render(wrapText(entry.Query, wrapWidth)))
	b.WriteString("\n\n")

	b.WriteString(labelStyle.Render("Response"))
	b.WriteString("\n")
	responsePreview := entry.Response
	if len(responsePreview) > 500 {
		responsePreview = responsePreview[:497] + "..."
	}
	b.WriteString(valueStyle.Render(wrapText(responsePreview, wrapWidth)))
	b.WriteString("\n\n")

	b.WriteString(labelStyle.Render("Metadata"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s Created:   %s\n", metaStyle.Render("•"), entry.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "%s Expires:   %s\n", metaStyle.Render("•"), entry.ExpiresAt.Format("2006-01-02 15:04:05"))

	if !expired {
		timeLeft := time.Until(entry.ExpiresAt)
		fmt.Fprintf(&b, "%s Time left: %s\n", metaStyle.Render("•"), formatDuration(timeLeft))
	}

	fmt.Fprintf(&b, "%s Hash:      %s\n", metaStyle.Render("•"), entry.QueryHash)
	fmt.Fprintf(&b, "%s Checksum:  %s\n", metaStyle.Render("•"), entry.ChecksumHash[:16]+"...")

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
