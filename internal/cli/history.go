package cli

import (
    "fmt"

    "eulix/internal/storage"
)

// RunHistory displays query history
func RunHistory() error {
    store, err := storage.NewSQLite()
    if err != nil {
        return fmt.Errorf("failed to open database: %w", err)
    }
    defer store.Close()

    records, err := store.GetHistory(50)
    if err != nil {
        return fmt.Errorf("failed to get history: %w", err)
    }

    if len(records) == 0 {
        fmt.Println("No query history found")
        return nil
    }

    fmt.Printf("Query History (last %d queries)\n\n", len(records))

    for i, record := range records {
        fmt.Printf("─────────────────────────────────────────\n")
        fmt.Printf("%d. [%s] %s\n", i+1, record.Timestamp.Format("2006-01-02 15:04:05"), record.Query)
        fmt.Printf("   Type: %s | Source: %s | Duration: %.2fs\n",
            record.QueryType, record.Source, record.Duration)

        // Show first 100 chars of answer
        answer := record.Answer
        if len(answer) > 100 {
            answer = answer[:100] + "..."
        }
        fmt.Printf("   Answer: %s\n", answer)
    }

    fmt.Printf("─────────────────────────────────────────\n")

    return nil
}
