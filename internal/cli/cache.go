package cli

import (
    "fmt"
    "eulix/internal/storage"
)

// RunCacheStats shows cache statistics
func RunCacheStats() error {
    // SQLite stats
    store, err := storage.NewSQLite()
    if err != nil {
        return fmt.Errorf("failed to open database: %w", err)
    }
    defer store.Close()

    sqliteStats, err := store.GetStats()
    if err != nil {
        return fmt.Errorf("failed to get SQLite stats: %w", err)
    }

    fmt.Println("Cache Statistics")
    fmt.Println()
    fmt.Println("SQLite (Persistent Storage):")
    fmt.Printf("  Total Queries: %v\n", sqliteStats["total_queries"])
    fmt.Printf("  Average Duration: %.2fs\n", sqliteStats["avg_duration"])

    if byType, ok := sqliteStats["by_type"].(map[string]int); ok {
        fmt.Println("  By Type:")
        for qtype, count := range byType {
            fmt.Printf("    - %s: %d\n", qtype, count)
        }
    }

    // Redis stats
    fmt.Println()
    redis, err := storage.NewRedis()
    if err != nil {
        fmt.Printf("Redis Cache: Not available (%v)\n", err)
    } else {
        defer redis.Close()

        redisStats, err := redis.GetCacheStats()
        if err != nil {
            fmt.Printf("Redis Cache: Error getting stats (%v)\n", err)
        } else {
            fmt.Println("Redis Cache:")
            fmt.Printf("  Cached Queries: %v\n", redisStats["cached_queries"])
        }
    }

    return nil
}

// RunCacheClear clears the cache
func RunCacheClear() error {
    // Clear Redis
    redis, err := storage.NewRedis()
    if err != nil {
        fmt.Printf(":(  Redis not available: %v\n", err)
    } else {
        defer redis.Close()

        if err := redis.InvalidateCache(); err != nil {
            return fmt.Errorf("failed to clear Redis cache: %w", err)
        }
        fmt.Println("✓ Redis cache cleared")
    }

    // Option to clear SQLite history
    fmt.Print("\nClear SQLite history too? (y/N): ")
    var response string
    fmt.Scanln(&response)

    if response == "y" || response == "Y" {
        store, err := storage.NewSQLite()
        if err != nil {
            return fmt.Errorf("failed to open database: %w", err)
        }
        defer store.Close()

        if err := store.ClearHistory(); err != nil {
            return fmt.Errorf("failed to clear history: %w", err)
        }
        fmt.Println("✓ SQLite history cleared")
    }

    return nil
}
