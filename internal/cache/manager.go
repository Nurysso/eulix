package cache

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"eulix/internal/config"

	"github.com/redis/go-redis/v9"
	_ "github.com/mattn/go-sqlite3"
)

type Manager struct {
	config      *config.Config
	redisClient *redis.Client
	sqlDB       *sql.DB
	ctx         context.Context
}

type CacheEntry struct {
	QueryHash      string    `json:"query_hash"`
	Query          string    `json:"query"`
	Response       string    `json:"response"`
	ChecksumHash   string    `json:"checksum_hash"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func CacheController(cfg *config.Config) (*Manager, error) {
	m := &Manager{
		config: cfg,
		ctx:    context.Background(),
	}

	// Initialize Redis if enabled
	if cfg.Cache.Redis.Enabled {
		opt, err := redis.ParseURL(cfg.Cache.Redis.URL)
		if err != nil {
			return nil, fmt.Errorf("invalid redis URL: %w", err)
		}

		m.redisClient = redis.NewClient(opt)

		// Test connection
		if err := m.redisClient.Ping(m.ctx).Err(); err != nil {
			return nil, fmt.Errorf("redis connection failed: %w", err)
		}
	}

	// Initialize SQL if enabled
	if cfg.Cache.SQL.Enabled {
		dbPath := ".eulix/cache.db"
		if cfg.Cache.SQL.DSN != "" {
			dbPath = cfg.Cache.SQL.DSN
		}

		db, err := sql.Open("sqlite3", dbPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open SQL database: %w", err)
		}

		m.sqlDB = db

		// Create table if not exists
		if err := m.initSQLSchema(); err != nil {
			return nil, fmt.Errorf("failed to initialize SQL schema: %w", err)
		}
	}

	return m, nil
}

func (m *Manager) initSQLSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS cache_entries (
		query_hash TEXT PRIMARY KEY,
		query TEXT NOT NULL,
		response TEXT NOT NULL,
		checksum_hash TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		expires_at DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_checksum_hash ON cache_entries(checksum_hash);
	CREATE INDEX IF NOT EXISTS idx_expires_at ON cache_entries(expires_at);
	CREATE INDEX IF NOT EXISTS idx_created_at ON cache_entries(created_at);
	`

	_, err := m.sqlDB.Exec(schema)
	return err
}

// Get retrieves a cached response if it exists and the checksum matches
func (m *Manager) Get(query string, currentChecksumHash string) (string, bool, error) {
	queryHash := m.hashQuery(query)

	// Try Redis first (if enabled)
	if m.config.Cache.Redis.Enabled && m.redisClient != nil {
		if response, found, err := m.getFromRedis(queryHash, currentChecksumHash); err == nil && found {
			return response, true, nil
		}
	}

	// Try SQL (if enabled)
	if m.config.Cache.SQL.Enabled && m.sqlDB != nil {
		if response, found, err := m.getFromSQL(queryHash, currentChecksumHash); err == nil && found {
			return response, true, nil
		}
	}

	return "", false, nil
}

func (m *Manager) getFromRedis(queryHash, currentChecksumHash string) (string, bool, error) {
	key := fmt.Sprintf("eulix:query:%s", queryHash)

	data, err := m.redisClient.Get(m.ctx, key).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	var entry CacheEntry
	if err := json.Unmarshal([]byte(data), &entry); err != nil {
		return "", false, err
	}

	// Verify checksum matches
	if entry.ChecksumHash != currentChecksumHash {
		// Checksum mismatch - invalidate cache
		m.redisClient.Del(m.ctx, key)
		return "", false, nil
	}

	// Check expiration
	if time.Now().After(entry.ExpiresAt) {
		m.redisClient.Del(m.ctx, key)
		return "", false, nil
	}

	return entry.Response, true, nil
}

func (m *Manager) getFromSQL(queryHash, currentChecksumHash string) (string, bool, error) {
	var entry CacheEntry

	query := `
		SELECT query_hash, query, response, checksum_hash, created_at, expires_at
		FROM cache_entries
		WHERE query_hash = ? AND checksum_hash = ?
	`

	err := m.sqlDB.QueryRow(query, queryHash, currentChecksumHash).Scan(
		&entry.QueryHash,
		&entry.Query,
		&entry.Response,
		&entry.ChecksumHash,
		&entry.CreatedAt,
		&entry.ExpiresAt,
	)

	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	// Check expiration
	if time.Now().After(entry.ExpiresAt) {
		// Delete expired entry
		m.sqlDB.Exec("DELETE FROM cache_entries WHERE query_hash = ?", queryHash)
		return "", false, nil
	}

	return entry.Response, true, nil
}

// Set stores a response in cache with the current checksum
func (m *Manager) Set(query, response, checksumHash string) error {
	queryHash := m.hashQuery(query)

	entry := CacheEntry{
		QueryHash:    queryHash,
		Query:        query,
		Response:     response,
		ChecksumHash: checksumHash,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(m.getTTL()),
	}

	// Save to Redis
	if m.config.Cache.Redis.Enabled && m.redisClient != nil {
		if err := m.saveToRedis(&entry); err != nil {
			return fmt.Errorf("redis save failed: %w", err)
		}
	}

	// Save to SQL
	if m.config.Cache.SQL.Enabled && m.sqlDB != nil {
		if err := m.saveToSQL(&entry); err != nil {
			return fmt.Errorf("sql save failed: %w", err)
		}
	}

	return nil
}

func (m *Manager) saveToRedis(entry *CacheEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("eulix:query:%s", entry.QueryHash)
	ttl := time.Until(entry.ExpiresAt)

	return m.redisClient.Set(m.ctx, key, data, ttl).Err()
}

func (m *Manager) saveToSQL(entry *CacheEntry) error {
	query := `
		INSERT OR REPLACE INTO cache_entries
		(query_hash, query, response, checksum_hash, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`

	_, err := m.sqlDB.Exec(
		query,
		entry.QueryHash,
		entry.Query,
		entry.Response,
		entry.ChecksumHash,
		entry.CreatedAt,
		entry.ExpiresAt,
	)

	return err
}

// Delete removes a specific cache entry from both backends
func (m *Manager) Delete(queryHash string) error {
	// Delete from Redis
	if m.config.Cache.Redis.Enabled && m.redisClient != nil {
		key := fmt.Sprintf("eulix:query:%s", queryHash)
		if err := m.redisClient.Del(m.ctx, key).Err(); err != nil {
			return fmt.Errorf("redis delete failed: %w", err)
		}
	}

	// Delete from SQL
	if m.config.Cache.SQL.Enabled && m.sqlDB != nil {
		_, err := m.sqlDB.Exec("DELETE FROM cache_entries WHERE query_hash = ?", queryHash)
		if err != nil {
			return fmt.Errorf("sql delete failed: %w", err)
		}
	}

	return nil
}

// ListAll returns all cache entries sorted by creation time
func (m *Manager) ListAll() ([]CacheEntry, error) {
	var entries []CacheEntry

	// Get from SQL (primary source of truth)
	if m.config.Cache.SQL.Enabled && m.sqlDB != nil {
		rows, err := m.sqlDB.Query(`
			SELECT query_hash, query, response, checksum_hash, created_at, expires_at
			FROM cache_entries
			ORDER BY created_at DESC
		`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		for rows.Next() {
			var entry CacheEntry
			err := rows.Scan(
				&entry.QueryHash,
				&entry.Query,
				&entry.Response,
				&entry.ChecksumHash,
				&entry.CreatedAt,
				&entry.ExpiresAt,
			)
			if err != nil {
				continue
			}
			entries = append(entries, entry)
		}
	}

	// If no SQL, try Redis
	if len(entries) == 0 && m.config.Cache.Redis.Enabled && m.redisClient != nil {
		keys, err := m.redisClient.Keys(m.ctx, "eulix:query:*").Result()
		if err != nil {
			return nil, err
		}

		for _, key := range keys {
			data, err := m.redisClient.Get(m.ctx, key).Result()
			if err != nil {
				continue
			}

			var entry CacheEntry
			if err := json.Unmarshal([]byte(data), &entry); err != nil {
				continue
			}
			entries = append(entries, entry)
		}

		// Sort by creation time
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		})
	}

	return entries, nil
}

// InvalidateByChecksum removes all cache entries with a different checksum
func (m *Manager) InvalidateByChecksum(currentChecksumHash string) error {
	// Invalidate in SQL
	if m.config.Cache.SQL.Enabled && m.sqlDB != nil {
		_, err := m.sqlDB.Exec(
			"DELETE FROM cache_entries WHERE checksum_hash != ?",
			currentChecksumHash,
		)
		if err != nil {
			return err
		}
	}

	// For Redis, we'd need to scan and delete, which is expensive
	// Instead, we rely on checksum verification during Get()

	return nil
}

// CleanExpired removes all expired cache entries
func (m *Manager) CleanExpired() error {
	if m.config.Cache.SQL.Enabled && m.sqlDB != nil {
		_, err := m.sqlDB.Exec(
			"DELETE FROM cache_entries WHERE expires_at < ?",
			time.Now(),
		)
		return err
	}
	return nil
}

// GetStats returns cache statistics
func (m *Manager) GetStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	if m.config.Cache.SQL.Enabled && m.sqlDB != nil {
		var totalEntries, validEntries int

		m.sqlDB.QueryRow("SELECT COUNT(*) FROM cache_entries").Scan(&totalEntries)
		m.sqlDB.QueryRow(
			"SELECT COUNT(*) FROM cache_entries WHERE expires_at > ?",
			time.Now(),
		).Scan(&validEntries)

		stats["sql_total_entries"] = totalEntries
		stats["sql_valid_entries"] = validEntries
	}

	if m.config.Cache.Redis.Enabled && m.redisClient != nil {
		info, err := m.redisClient.Info(m.ctx, "stats").Result()
		if err == nil {
			stats["redis_connected"] = true
			stats["redis_info"] = info
		}
	}

	return stats, nil
}

// Close closes all connections
func (m *Manager) Close() error {
	if m.redisClient != nil {
		if err := m.redisClient.Close(); err != nil {
			return err
		}
	}

	if m.sqlDB != nil {
		if err := m.sqlDB.Close(); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) hashQuery(query string) string {
	h := sha256.New()
	h.Write([]byte(query))
	return hex.EncodeToString(h.Sum(nil))
}

func (m *Manager) getTTL() time.Duration {
	// Default 24 hours
	ttl := 24 * time.Hour

	// Use Redis TTL if enabled
	if m.config.Cache.Redis.Enabled && m.config.Cache.Redis.TTLHours > 0 {
		ttl = time.Duration(m.config.Cache.Redis.TTLHours) * time.Hour
	}

	return ttl
}
