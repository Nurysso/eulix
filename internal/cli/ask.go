package cli

import (
    "fmt"
    "strings"
    "time"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/bubbles/viewport"
    "github.com/charmbracelet/lipgloss"
    "eulix/internal/config"
    "eulix/internal/kb"
    "eulix/internal/query"
    "eulix/internal/storage"
)

var (
    spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

    promptStyle = lipgloss.NewStyle().
        Bold(true).
        Foreground(lipgloss.Color("63"))

    answerStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("252"))

    sourceStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("240")).
        Italic(true)

    askError = lipgloss.NewStyle().
        Foreground(lipgloss.Color("196")).
        Bold(true)

    scrollHintStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("241")).
        Italic(true)
)

type askModel struct {
    query    string
    verbose  bool

    // State
    loading  bool
    spinner  int
    result   *query.Result
    err      error
    stage    string

    // Viewport for scrolling
    viewport viewport.Model
    ready    bool

    // Storage
    storage  *storage.SQLite
    redis    *storage.Redis
}

type tickMsg time.Time
type resultMsg struct {
    result *query.Result
    err    error
}

func (m askModel) Init() tea.Cmd {
    return tea.Batch(
        tickCmd(),
        runQuery(m.query, m.verbose, m.storage, m.redis),
    )
}

func (m askModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    var cmd tea.Cmd

    switch msg := msg.(type) {
    case tea.KeyMsg:
        if m.ready {
            // Allow scrolling when result is ready
            switch msg.String() {
            case "q", "ctrl+c", "esc":
                return m, tea.Quit
            default:
                m.viewport, cmd = m.viewport.Update(msg)
                return m, cmd
            }
        } else if msg.String() == "q" || msg.String() == "ctrl+c" {
            return m, tea.Quit
        }

    case tea.WindowSizeMsg:
        if !m.ready {
            m.viewport = viewport.New(msg.Width, msg.Height-4)
            m.viewport.YPosition = 0
            m.ready = true
        } else {
            m.viewport.Width = msg.Width
            m.viewport.Height = msg.Height - 4
        }

    case tickMsg:
        m.spinner = (m.spinner + 1) % len(spinnerFrames)
        if m.loading {
            return m, tickCmd()
        }

    case resultMsg:
        m.loading = false
        m.result = msg.result
        m.err = msg.err

        if msg.result != nil {
            fmt.Printf("[DEBUG askModel] Got result with answer length: %d\n", len(msg.result.Answer))
        }
        if msg.err != nil {
            fmt.Printf("[DEBUG askModel] Got error: %v\n", msg.err)
        }

        // Set viewport content
        m.viewport.SetContent(m.buildResultView())

        return m, nil

    case tea.QuitMsg:
        return m, tea.Quit
    }

    return m, cmd
}

func (m askModel) View() string {
    if !m.ready {
        return "\n  Initializing..."
    }

    var s strings.Builder

    // Query header
    s.WriteString(promptStyle.Render("Query: "))
    s.WriteString(m.query)
    s.WriteString("\n\n")

    // Loading state
    if m.loading {
        s.WriteString(spinnerFrames[m.spinner])
        s.WriteString(" ")

        cfg := config.Load()
        if cfg.LLM.Provider == "openai" {
            s.WriteString(fmt.Sprintf("Querying OpenAI (%s)...", cfg.LLM.Model))
        } else {
            s.WriteString(fmt.Sprintf("Querying Ollama (%s)...", cfg.LLM.Model))
        }

        s.WriteString("\n")
        s.WriteString(sourceStyle.Render("This may take 10-30 seconds..."))
        return s.String()
    }

    // Show viewport with result
    s.WriteString(m.viewport.View())
    s.WriteString("\n")

    // Scroll hint
    if m.viewport.TotalLineCount() > m.viewport.Height {
        s.WriteString(scrollHintStyle.Render(fmt.Sprintf(
            "↑/↓: scroll • %d%% • q: quit",
            int(m.viewport.ScrollPercent()*100),
        )))
    } else {
        s.WriteString(scrollHintStyle.Render("Press q to quit"))
    }

    return s.String()
}

func (m askModel) buildResultView() string {
    var s strings.Builder

    // Error
    if m.err != nil {
        s.WriteString(askError.Render(fmt.Sprintf(" Error: %v", m.err)))
        s.WriteString("\n\n")
        s.WriteString(sourceStyle.Render("Troubleshooting:\n"))
        s.WriteString(sourceStyle.Render("  1. Check if Ollama is running: curl http://localhost:11434/api/tags\n"))
        s.WriteString(sourceStyle.Render("  2. Check if model exists: ollama list\n"))
        s.WriteString(sourceStyle.Render("  3. Pull model if needed: ollama pull llama3.2\n"))
        return s.String()
    }

    // Result
    if m.result != nil {
        // Show query type
        if m.verbose {
            s.WriteString(sourceStyle.Render(fmt.Sprintf("[%s query from %s]\n\n",
                m.result.Type, m.result.Source)))
        }

        // Answer
        if m.result.Answer != "" {
            s.WriteString("Answer:\n")
            s.WriteString(answerStyle.Render(m.result.Answer))
            s.WriteString("\n\n")
        } else {
            s.WriteString(askError.Render(":(  Received empty response from LLM\n\n"))
        }

        // Sources
        if len(m.result.Sources) > 0 {
            s.WriteString(sourceStyle.Render("≡ Sources:\n"))
            for _, src := range m.result.Sources {
                s.WriteString(sourceStyle.Render(fmt.Sprintf("  • %s\n", src)))
            }
            s.WriteString("\n")
        }

        // Timing
        s.WriteString(sourceStyle.Render(fmt.Sprintf("⏱ Response time: %.2fs", m.result.Duration.Seconds())))

        // Cache indicator
        if m.result.Source == "redis_cache" {
            s.WriteString(sourceStyle.Render(" [cached]"))
        }
        s.WriteString("\n")
    } else {
        s.WriteString(askError.Render(" No result received\n"))
    }

    return s.String()
}

func tickCmd() tea.Cmd {
    return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
        return tickMsg(t)
    })
}

func runQuery(queryText string, verbose bool, store *storage.SQLite, redis *storage.Redis) tea.Cmd {
    return func() tea.Msg {
        start := time.Now()

        fmt.Printf("[DEBUG runQuery] Starting query: %s\n", queryText)

        // Check Redis cache first
        if redis != nil {
            cached, err := redis.GetQuery(queryText)
            if err == nil && cached != nil {
                fmt.Printf("[DEBUG runQuery] Found in Redis cache\n")
                cached.Duration = time.Since(start)
                cached.Source = "redis_cache"
                return resultMsg{result: cached}
            }
        }

        // Load KB components
        kbLoader, err := kb.NewLoader()
        if err != nil {
            fmt.Printf("[DEBUG runQuery] KB load error: %v\n", err)
            return resultMsg{err: fmt.Errorf("failed to load knowledge base: %w", err)}
        }

        fmt.Printf("[DEBUG runQuery] KB loaded successfully\n")

        // Execute query
        router := query.NewRouter(kbLoader)
        result, err := router.Query(queryText)
        if err != nil {
            fmt.Printf("[DEBUG runQuery] Query error: %v\n", err)
            return resultMsg{err: err}
        }

        fmt.Printf("[DEBUG runQuery] Query completed, answer length: %d\n", len(result.Answer))

        result.Duration = time.Since(start)

        // Save to SQLite
        if store != nil {
            store.SaveQuery(storage.QueryRecord{
                SessionID: "ask",
                Query:     queryText,
                Answer:    result.Answer,
                QueryType: result.Type,
                Source:    result.Source,
                Duration:  result.Duration.Seconds(),
                Timestamp: time.Now(),
            })
        }

        // Cache in Redis (only for successful queries)
        if redis != nil && result.Answer != "" {
            redis.CacheQuery(queryText, result, 24*time.Hour)
        }

        return resultMsg{result: result}
    }
}

func RunAsk(queryText string, verbose bool) error {
    // Initialize storage
    store, err := storage.NewSQLite()
    if err != nil {
        fmt.Printf("Warning: Failed to initialize SQLite: %v\n", err)
        store = nil
    }

    // Initialize Redis (optional, will be nil if Redis not available)
    redis, err := storage.NewRedis()
    if err != nil {
        fmt.Printf("Warning: Redis not available: %v\n", err)
        redis = nil
    }

    m := askModel{
        query:   queryText,
        verbose: verbose,
        loading: true,
        stage:   "loading",
        storage: store,
        redis:   redis,
    }

    p := tea.NewProgram(
        m,
        tea.WithAltScreen(),
    )

    if _, err := p.Run(); err != nil {
        return err
    }

    return nil
}
