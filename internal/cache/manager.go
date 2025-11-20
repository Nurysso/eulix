package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"eulix/internal/config"
)

type Manager struct {
	config      *config.Config
	redisClient interface{} // Placeholder for Redis client
	sqlDB       interface{} // Placeholder for SQL DB
}

func NewManager(cfg *config.Config) (*Manager, error) {
	m := &Manager{
		config: cfg,
	}

	// Initialize Redis if enabled
	if cfg.Cache.Redis.Enabled {
		// Placeholder - would initialize Redis client
		_ = time.Now() // use time so the import isn't unused
	}

	// Initialize SQL if enabled
	if cfg.Cache.SQL.Enabled {
		// Placeholder - would initialize SQL DB
	}

	return m, nil
}

func (m *Manager) Get(query string) (string, error) {
	queryHash := m.hashQuery(query)

	// Placeholder use so variable is not unused
	_ = queryHash

	// Try Redis first
	if m.config.Cache.Redis.Enabled && m.redisClient != nil {
		// Placeholder - would query Redis using queryHash
	}

	// Try SQL
	if m.config.Cache.SQL.Enabled && m.sqlDB != nil {
		// Placeholder - would query SQL using queryHash
	}

	return "", nil
}

func (m *Manager) Set(query, response string) error {
	queryHash := m.hashQuery(query)

	// Placeholder use so variable is not unused
	_ = queryHash

	timestamp := time.Now() // you intend to use this with TTL later
	_ = timestamp

	// Save to Redis
	if m.config.Cache.Redis.Enabled && m.redisClient != nil {
		// Placeholder - would save to Redis with queryHash + timestamp
	}

	// Save to SQL
	if m.config.Cache.SQL.Enabled && m.sqlDB != nil {
		// Placeholder - would save to SQL with queryHash + timestamp
	}

	return nil
}

func (m *Manager) hashQuery(query string) string {
	h := sha256.New()
	h.Write([]byte(query))
	return hex.EncodeToString(h.Sum(nil))
}
