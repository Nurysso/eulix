package cli

import (
    "fmt"
    "os"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
    "eulix/internal/config"
)

var (
    titleStyle = lipgloss.NewStyle().
        Bold(true).
        Foreground(lipgloss.Color("205")).
        PaddingTop(1).
        PaddingBottom(1)

    successStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("42"))

    errorStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("196"))
)

type initModel struct {
    step     int
    steps    []string
    complete bool
    err      error
}

func (m initModel) Init() tea.Cmd {
    return runInitSteps
}

func (m initModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        if msg.String() == "q" || msg.String() == "ctrl+c" {
            return m, tea.Quit
        }
        if m.complete {
            return m, tea.Quit
        }

    case initStepMsg:
        m.step = msg.step
        if msg.err != nil {
            m.err = msg.err
            m.complete = true
            return m, tea.Quit
        }
        if msg.step >= len(m.steps) {
            m.complete = true
            return m, tea.Quit
        }
        return m, runInitSteps

    case tea.QuitMsg:
        return m, tea.Quit
    }

    return m, nil
}

func (m initModel) View() string {
    if m.err != nil {
        return errorStyle.Render(fmt.Sprintf(" Initialization failed: %v\n", m.err))
    }

    s := titleStyle.Render("Initializing Eulix") + "\n\n"

    for i, step := range m.steps {
        if i < m.step {
            s += successStyle.Render("✓ ") + step + "\n"
        } else if i == m.step {
            s += " " + step + "...\n"
        } else {
            s += "  " + step + "\n"
        }
    }

    if m.complete {
        s += "\n" + successStyle.Render("✨ Eulix initialized successfully!\n")
        s += "\nNext steps:\n"
        s += "  1. Review .euignore to customize ignore patterns\n"
        s += "  2. Run: eulix parse\n"
        s += "  3. Ask: eulix ask \"where is the main function?\"\n"
    }

    return s
}

type initStepMsg struct {
    step int
    err  error
}

func runInitSteps() tea.Msg {
    // Step 0: Create .eulix directory
    if err := os.MkdirAll(".eulix", 0755); err != nil {
        return initStepMsg{step: 0, err: err}
    }

    // Step 1: Create .euignore
    if err := createEuignore(); err != nil {
        return initStepMsg{step: 1, err: err}
    }

    // Step 2: Create config.toml
    cfg := config.Load()
    if err := config.Save(cfg); err != nil {
        return initStepMsg{step: 2, err: err}
    }

    // Step 3: Create cache.db placeholder
    f, err := os.Create(".eulix/cache.db")
    if err != nil {
        return initStepMsg{step: 3, err: err}
    }
    f.Close()

    return initStepMsg{step: 4, err: nil}
}

func RunInit() error {
    if config.IsInitialized() {
        fmt.Println(":(  Eulix is already initialized in this directory")
        return nil
    }

    m := initModel{
        step: 0,
        steps: []string{
            "Create .eulix directory",
            "Create .euignore file",
            "Create config.toml",
            "Initialize cache database",
        },
    }

    p := tea.NewProgram(m)
    if _, err := p.Run(); err != nil {
        return err
    }

    return nil
}

func createEuignore() error {
    defaultIgnore := `# Eulix ignore patterns

# Version control
.git/
.svn/

# Dependencies
node_modules/
.venv/
venv/
__pycache__/

# Build outputs
dist/
build/
target/
*.o
*.so

# IDE
.vscode/
.idea/
*.swp

# Logs
*.log

# Eulix metadata
.eulix/
`

    return os.WriteFile(".euignore", []byte(defaultIgnore), 0644)
}
