package cli

import (
	"fmt"
	"time"

	"eulix/internal/cache"
	"eulix/internal/checksum"
	"eulix/internal/config"
	"eulix/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// CacheStats displays cache statistics
func CacheStats() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if !cfg.Cache.SQL.Enabled && !cfg.Cache.Redis.Enabled {
		fmt.Println("❌ No cache backends enabled")
		fmt.Println("💡 Enable cache in eulix.toml to use caching features")
		return nil
	}

	cacheManager, err := cache.CacheController(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize cache: %w", err)
	}
	defer cacheManager.Close()

	stats, err := cacheManager.GetStats()
	if err != nil {
		return fmt.Errorf("failed to get stats: %w", err)
	}

	fmt.Println("📊 Cache Statistics")
	fmt.Println("==================")

	if cfg.Cache.SQL.Enabled {
		fmt.Printf("\n✓ SQL Cache (SQLite)\n")
		fmt.Printf("  Path: %s\n", cfg.Cache.SQL.DSN)
		if total, ok := stats["sql_total_entries"].(int); ok {
			fmt.Printf("  Total entries: %d\n", total)
		}
		if valid, ok := stats["sql_valid_entries"].(int); ok {
			fmt.Printf("  Valid entries: %d\n", valid)
		}
	}

	if cfg.Cache.Redis.Enabled {
		fmt.Printf("\n✓ Redis Cache\n")
		fmt.Printf("  URL: %s\n", cfg.Cache.Redis.URL)
		if connected, ok := stats["redis_connected"].(bool); ok && connected {
			fmt.Printf("  Status: Connected\n")
		} else {
			fmt.Printf("  Status: Disconnected\n")
		}
		fmt.Printf("  TTL: %d hours\n", cfg.Cache.Redis.TTLHours)
	}

	// Show current checksum
	detector := checksum.HashHound(".")
	if current, err := detector.Calculate(); err == nil {
		fmt.Printf("\n🔍 Current Checksum\n")
		fmt.Printf("  Hash: %s\n", current.Hash[:16]+"...")
		fmt.Printf("  Files: %d\n", current.TotalFiles)
		fmt.Printf("  Lines: %d\n", current.TotalLines)
	}

	return nil
}

// CacheClear clears all cache entries
func CacheClear() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if !cfg.Cache.SQL.Enabled && !cfg.Cache.Redis.Enabled {
		fmt.Println("❌ No cache backends enabled")
		return nil
	}

	fmt.Print("⚠️  This will delete all cached queries. Continue? [y/N]: ")
	var response string
	fmt.Scanln(&response)

	if response != "y" && response != "yes" {
		fmt.Println("Cancelled")
		return nil
	}

	cacheManager, err := cache.CacheController(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize cache: %w", err)
	}
	defer cacheManager.Close()

	// Clear by invalidating all entries (pass empty checksum)
	if err := cacheManager.InvalidateByChecksum(""); err != nil {
		return fmt.Errorf("failed to clear cache: %w", err)
	}

	fmt.Println("✓ Cache cleared successfully")
	return nil
}

// CacheCleanup removes expired entries
func CacheCleanup() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if !cfg.Cache.SQL.Enabled {
		fmt.Println("❌ SQL cache not enabled")
		return nil
	}

	cacheManager, err := cache.CacheController(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize cache: %w", err)
	}
	defer cacheManager.Close()

	fmt.Println("🧹 Cleaning expired cache entries...")

	if err := cacheManager.CleanExpired(); err != nil {
		return fmt.Errorf("cleanup failed: %w", err)
	}

	fmt.Println("✓ Cleanup completed")
	return nil
}

// CacheTest tests cache functionality
func CacheTest() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if !cfg.Cache.SQL.Enabled && !cfg.Cache.Redis.Enabled {
		fmt.Println("❌ No cache backends enabled")
		return nil
	}

	cacheManager, err := cache.CacheController(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize cache: %w", err)
	}
	defer cacheManager.Close()

	// Get current checksum
	detector := checksum.HashHound(".")
	current, err := detector.Calculate()
	if err != nil {
		return fmt.Errorf("failed to calculate checksum: %w", err)
	}

	fmt.Println("🧪 Testing cache operations...")

	// Test write
	testQuery := "test query: what is the main function?"
	testResponse := "This is a test response"

	fmt.Print("  Writing test entry... ")
	if err := cacheManager.Set(testQuery, testResponse, current.Hash); err != nil {
		fmt.Printf("❌ Failed: %v\n", err)
		return err
	}
	fmt.Println("✓")

	// Test read
	fmt.Print("  Reading test entry... ")
	response, found, err := cacheManager.Get(testQuery, current.Hash)
	if err != nil {
		fmt.Printf("❌ Failed: %v\n", err)
		return err
	}
	if !found {
		fmt.Println("❌ Not found")
		return fmt.Errorf("cache entry not found")
	}
	if response != testResponse {
		fmt.Println("❌ Mismatch")
		return fmt.Errorf("response mismatch")
	}
	fmt.Println("✓")

	// Test checksum validation
	fmt.Print("  Testing checksum validation... ")
	_, found, _ = cacheManager.Get(testQuery, "invalid_checksum")
	if found {
		fmt.Println("❌ Should not find entry with wrong checksum")
		return fmt.Errorf("checksum validation failed")
	}
	fmt.Println("✓")

	fmt.Println("\n✅ All cache tests passed!")
	return nil
}

// CacheHistory displays cache history (simple text view)
func CacheHistory() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if !cfg.Cache.SQL.Enabled && !cfg.Cache.Redis.Enabled {
		fmt.Println("❌ No cache backends enabled")
		return nil
	}

	cacheManager, err := cache.CacheController(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize cache: %w", err)
	}
	defer cacheManager.Close()

	// Get all entries
	entries, err := cacheManager.ListAll()
	if err != nil {
		return fmt.Errorf("failed to list cache entries: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("📭 No cache entries found")
		return nil
	}

	fmt.Printf("📚 Cache History (%d entries)\n", len(entries))
	fmt.Println("==================")

	for i, entry := range entries {
		expired := time.Now().After(entry.ExpiresAt)
		status := "✓"
		if expired {
			status = "⏱"
		}

		// Truncate query for display
		query := entry.Query
		if len(query) > 60 {
			query = query[:57] + "..."
		}

		fmt.Printf("\n%s [%d] %s\n", status, i+1, query)
		fmt.Printf("   Created: %s\n", entry.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("   Expires: %s\n", entry.ExpiresAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("   Hash: %s\n", entry.QueryHash[:16]+"...")
	}

	fmt.Println("\n💡 Use 'eulix cache view' for interactive viewer")

	return nil
}

// CacheView launches interactive TUI cache viewer
func CacheView() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if !cfg.Cache.SQL.Enabled && !cfg.Cache.Redis.Enabled {
		fmt.Println("❌ No cache backends enabled")
		return nil
	}

	cacheManager, err := cache.CacheController(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize cache: %w", err)
	}
	defer cacheManager.Close()

	// Get all entries
	entries, err := cacheManager.ListAll()
	if err != nil {
		return fmt.Errorf("failed to list cache entries: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("📭 No cache entries found")
		return nil
	}

	// Launch TUI
	model := tui.HistoryView(entries, cacheManager)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

// CacheDelete deletes a specific cache entry by index or hash
func CacheDelete(identifier string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if !cfg.Cache.SQL.Enabled && !cfg.Cache.Redis.Enabled {
		fmt.Println("❌ No cache backends enabled")
		return nil
	}

	cacheManager, err := cache.CacheController(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize cache: %w", err)
	}
	defer cacheManager.Close()

	// Get all entries to find the one to delete
	entries, err := cacheManager.ListAll()
	if err != nil {
		return fmt.Errorf("failed to list cache entries: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("📭 No cache entries found")
		return nil
	}

	// Try to parse as index
	var queryHash string
	var index int
	_, err = fmt.Sscanf(identifier, "%d", &index)
	if err == nil {
		// It's an index
		if index < 1 || index > len(entries) {
			return fmt.Errorf("invalid index: %d (valid range: 1-%d)", index, len(entries))
		}
		queryHash = entries[index-1].QueryHash
	} else {
		// It's a hash
		queryHash = identifier
	}

	// Find and display the entry
	var found *cache.CacheEntry
	for i := range entries {
		if entries[i].QueryHash == queryHash || entries[i].QueryHash[:16] == queryHash {
			found = &entries[i]
			break
		}
	}

	if found == nil {
		return fmt.Errorf("cache entry not found: %s", identifier)
	}

	// Display entry details
	fmt.Println("🗑️  Deleting cache entry:")
	fmt.Printf("   Query: %s\n", found.Query)
	fmt.Printf("   Created: %s\n", found.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("   Hash: %s\n", found.QueryHash[:16]+"...")
	fmt.Println()

	fmt.Print("⚠️  Continue? [y/N]: ")
	var response string
	fmt.Scanln(&response)

	if response != "y" && response != "yes" {
		fmt.Println("Cancelled")
		return nil
	}

	// Delete from both backends
	if err := cacheManager.Delete(found.QueryHash); err != nil {
		return fmt.Errorf("failed to delete entry: %w", err)
	}

	fmt.Println("✓ Cache entry deleted successfully")
	return nil
}
