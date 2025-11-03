package cli

import (
    "fmt"
    "strings"
    "time"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
    "eulix/internal/parser"
)

var (
    progressStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("205"))

    statsStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("86"))
)

type parseModel struct {
    // State
    parsing   bool
    progress  float64
    status    string
    stats     *parser.ParseStats
    err       error
    startTime time.Time
}

type parseProgressMsg struct {
    progress float64
    status   string
}

type parseCompleteMsg struct {
    stats *parser.ParseStats
    err   error
}

func (m parseModel) Init() tea.Cmd {
    return tea.Batch(
        tickCmd(),
        runParser(),
    )
}

func (m parseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        if msg.String() == "q" || msg.String() == "ctrl+c" {
            return m, tea.Quit
        }

    case tickMsg:
        if m.parsing {
            // Fake progress animation (real progress from parser would be better)
            if m.progress < 0.95 {
                m.progress += 0.05
            }
            return m, tickCmd()
        }

    case parseProgressMsg:
        m.progress = msg.progress
        m.status = msg.status

    case parseCompleteMsg:
        m.parsing = false
        m.stats = msg.stats
        m.err = msg.err
        m.progress = 1.0
        return m, tea.Quit

    case tea.QuitMsg:
        return m, tea.Quit
    }

    return m, nil
}

func (m parseModel) View() string {
    var s strings.Builder

    s.WriteString(titleStyle.Render("Parsing Codebase"))
    s.WriteString("\n")

    if m.err != nil {
        s.WriteString(errorStyle.Render(fmt.Sprintf(" Parse failed: %v\n", m.err)))
        return s.String()
    }

    if m.parsing {
        // Progress bar
        s.WriteString(m.renderProgressBar())
        s.WriteString("\n\n")
        s.WriteString(m.status)
        s.WriteString("\n")
    } else if m.stats != nil {
        // Parse complete - show stats
        s.WriteString(successStyle.Render("Parse complete!\n\n"))
        s.WriteString(m.renderStats())
    }

    return s.String()
}

func (m parseModel) renderProgressBar() string {
    width := 40
    filled := int(m.progress * float64(width))
    bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)

    percentage := int(m.progress * 100)

    return progressStyle.Render(fmt.Sprintf("[%s] %d%%", bar, percentage))
}

func (m parseModel) renderStats() string {
    if m.stats == nil {
        return ""
    }

    duration := time.Since(m.startTime)
    locPerSec := 0.0
    if duration.Seconds() > 0 {
        locPerSec = float64(m.stats.TotalLOC) / duration.Seconds()
    }

    var s strings.Builder

    s.WriteString(statsStyle.Render("┌─────────────────────────────────────┐\n"))
    s.WriteString(statsStyle.Render("│          Parse Summary              │\n"))
    s.WriteString(statsStyle.Render("├─────────────────────────────────────┤\n"))
    s.WriteString(statsStyle.Render("│ Files:         %6d                  │\n", m.stats.Files))
    s.WriteString(statsStyle.Render("│ Lines:         %6d                  │\n", m.stats.TotalLOC))
    s.WriteString(statsStyle.Render("│ Functions:     %6d                  │\n", m.stats.Functions))
    s.WriteString(statsStyle.Render("│ Classes:       %6d                  │\n", m.stats.Classes))
    s.WriteString(statsStyle.Render("│ Methods:       %6d                  │\n", m.stats.Methods))
    s.WriteString(statsStyle.Render("├─────────────────────────────────────┤\n"))
    s.WriteString(statsStyle.Render("│ Duration:      %6.2fs               │\n", duration.Seconds()))
    s.WriteString(statsStyle.Render("│ Speed:         %6.0f LOC/s          │\n", locPerSec))
    s.WriteString(statsStyle.Render("└─────────────────────────────────────┘\n"))

    s.WriteString("\n")
    s.WriteString("Generated files:\n")
    s.WriteString("  • .eulix/knowledge_base.json\n")
    s.WriteString("  • .eulix/index.json\n")
    s.WriteString("  • .eulix/summary.json\n")

    return s.String()
}

func runParser() tea.Cmd {
    return func() tea.Msg {
        stats, err := parser.RunParserWithStats()
        return parseCompleteMsg{stats: stats, err: err}
    }
}

func RunParse() error {
    m := parseModel{
        parsing:   true,
        progress:  0.0,
        status:    "Initializing...",
        startTime: time.Now(),
    }

    p := tea.NewProgram(m)
    if _, err := p.Run(); err != nil {
        return err
    }

    return nil
}
