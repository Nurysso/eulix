package cli

import (
    "fmt"
    "strings"
    "time"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
    "github.com/charmbracelet/bubbles/viewport"
    "eulix/internal/kb"
    "eulix/internal/query"
    "eulix/internal/storage"
)

var (
    chatTitleStyle = lipgloss.NewStyle().
        Bold(true).
        Foreground(lipgloss.Color("205"))

    userInputStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("86")).
        Bold(true)

    assistantStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("252"))

    timestampStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("240")).
        Italic(true)

    helpStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("241"))
)

type chatModel struct {
    // Configuration
    verbose bool

    // State
    messages     []ChatMessage
    input        string
    loading      bool
    spinner      int
    err          error
    quitting     bool
    sessionID    string

 	// Viewport for scrolling
    viewport     viewport.Model
    ready        bool

    // Dependencies
    kbLoader *kb.Loader
    router   *query.Router
    storage  *storage.SQLite
    redis    *storage.Redis
}

type ChatMessage struct {
    Role      string    // "user" or "assistant"
    Content   string
    Timestamp time.Time
    Sources   []string
}

type chatResultMsg struct {
    result *query.Result
    err    error
}

func NewChatModel(verbose bool) (*chatModel, error) {
    // Load KB
    kbLoader, err := kb.NewLoader()
    if err != nil {
        return nil, fmt.Errorf("failed to load knowledge base: %w", err)
    }

    // Initialize router
    router := query.NewRouter(kbLoader)

    // Initialize storage
    store, err := storage.NewSQLite()
    if err != nil {
        return nil, fmt.Errorf("failed to open database: %w", err)
    }
 // Try to initialize Redis
    redis, err := storage.NewRedis()
    if err != nil {
        fmt.Printf("Warning: Redis not available: %v\n", err)
        redis = nil
    }
    // Create new session
    sessionID := fmt.Sprintf("session_%d", time.Now().Unix())

    return &chatModel{
        verbose:   verbose,
        messages:  []ChatMessage{},
        sessionID: sessionID,
        kbLoader:  kbLoader,
        router:    router,
        storage:   store,
		redis:	   redis,
    }, nil
}

func (m chatModel) Init() tea.Cmd {
    return tea.Batch(
        tickCmd(),
        tea.EnterAltScreen,
    )
}

func (m chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch msg.Type {
        case tea.KeyCtrlC, tea.KeyEsc:
            m.quitting = true
            return m, tea.Quit

        case tea.KeyEnter:
            if m.loading || m.input == "" {
                return m, nil
            }

            // Special commands
            if m.input == "/quit" || m.input == "/exit" {
                m.quitting = true
                return m, tea.Quit
            }

            if m.input == "/clear" {
                m.messages = []ChatMessage{}
                m.input = ""
                return m, nil
            }

            if m.input == "/help" {
                m.messages = append(m.messages, ChatMessage{
                    Role:      "assistant",
                    Content:   m.getHelpText(),
                    Timestamp: time.Now(),
                })
                m.input = ""
                return m, nil
            }

            // Send query
            userQuery := m.input
            m.messages = append(m.messages, ChatMessage{
                Role:      "user",
                Content:   userQuery,
                Timestamp: time.Now(),
            })
            m.input = ""
            m.loading = true

            return m, runChatQuery(m.router, userQuery, m.redis)

        case tea.KeyBackspace:
            if len(m.input) > 0 {
                m.input = m.input[:len(m.input)-1]
            }

        default:
            if !m.loading {
                m.input += msg.String()
            }
        }

    case tickMsg:
        m.spinner = (m.spinner + 1) % len(spinnerFrames)
        if m.loading {
            return m, tickCmd()
        }

    case chatResultMsg:
        m.loading = false

        if msg.err != nil {
            m.err = msg.err
            m.messages = append(m.messages, ChatMessage{
                Role:      "assistant",
                Content:   fmt.Sprintf(" Error: %v", msg.err),
                Timestamp: time.Now(),
            })
        } else {
            m.messages = append(m.messages, ChatMessage{
                Role:      "assistant",
                Content:   msg.result.Answer,
                Timestamp: time.Now(),
                Sources:   msg.result.Sources,
            })

            // Save to storage
            lastUserMsg := ""
            for i := len(m.messages) - 2; i >= 0; i-- {
                if m.messages[i].Role == "user" {
                    lastUserMsg = m.messages[i].Content
                    break
                }
            }

            if lastUserMsg != "" {
                m.storage.SaveQuery(storage.QueryRecord{
                    SessionID: m.sessionID,
                    Query:     lastUserMsg,
                    Answer:    msg.result.Answer,
                    QueryType: msg.result.Type,
                    Source:    msg.result.Source,
                    Duration:  msg.result.Duration.Seconds(),
                    Timestamp: time.Now(),
                })
            }
        }

    case tea.WindowSizeMsg:
        // Handle window resize if needed
        return m, nil
    }

    return m, nil
}

func (m chatModel) View() string {
    if m.quitting {
        return "Goodbye!\n"
    }

    var s strings.Builder

    // Header
    s.WriteString(chatTitleStyle.Render(" Eulix Chat "))
    s.WriteString("\n\n")

    // Messages
    for _, msg := range m.messages {
        if msg.Role == "user" {
            s.WriteString(userInputStyle.Render("You: "))
            s.WriteString(msg.Content)
            s.WriteString("\n")
        } else {
            s.WriteString(assistantStyle.Render("Eulix: "))
            s.WriteString(msg.Content)
            s.WriteString("\n")

            // Show sources
            if len(msg.Sources) > 0 && m.verbose {
                s.WriteString(sourceStyle.Render("  Sources: "))
                s.WriteString(sourceStyle.Render(strings.Join(msg.Sources, ", ")))
                s.WriteString("\n")
            }
        }

        // Timestamp
        if m.verbose {
            s.WriteString(timestampStyle.Render(fmt.Sprintf("  [%s]",
                msg.Timestamp.Format("15:04:05"))))
            s.WriteString("\n")
        }

        s.WriteString("\n")
    }

    // Loading indicator
    if m.loading {
        s.WriteString(spinnerFrames[m.spinner])
        s.WriteString(" Thinking...\n\n")
    }

    // Input prompt
    s.WriteString(promptStyle.Render("❯ "))
    s.WriteString(m.input)
    s.WriteString("▌\n\n")

    // Help text
    // s.WriteString(helpStyle.Render("Commands: /help /clear /quit | Ctrl+C to exit"))

    return s.String()
}

func (m chatModel) getHelpText() string {
    return `Available commands:
  /help   - Show this help message
  /clear  - Clear chat history
  /quit   - Exit chat mode

Tips:
  • Ask "where is X" for quick lookups
  • Ask "how does X work" for explanations
  • Ask "explain the architecture" for overviews
  • Press Ctrl+C or type /quit to exit`
}


func runChatQuery(router *query.Router, queryText string, redis *storage.Redis) tea.Cmd {
    return func() tea.Msg {
        // Check Redis cache first
        if redis != nil {
            cached, err := redis.GetQuery(queryText)
            if err == nil && cached != nil {
                cached.Source = "redis_cache"
                return chatResultMsg{result: cached}
            }
        }

        result, err := router.Query(queryText)
        if err != nil {
            return chatResultMsg{err: err}
        }

        // Cache result
        if redis != nil && result.Answer != "" {
            redis.CacheQuery(queryText, result, 24*time.Hour)
        }

        return chatResultMsg{result: result}
    }
}

func RunChat(verbose bool) error {
    m, err := NewChatModel(verbose)
    if err != nil {
        return err
    }

    p := tea.NewProgram(m, tea.WithAltScreen())
    if _, err := p.Run(); err != nil {
        return err
    }

    return nil
}
