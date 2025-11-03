package storage

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/redis/go-redis/v9"
    "eulix/internal/query"
)

const (
    redisAddr     = "localhost:6379"
    redisPassword = ""
    redisDB       = 0
    keyPrefix     = "eulix:query:"
)

// Redis manages Redis cache
type Redis struct {
    client *redis.Client
    ctx    context.Context
}

// NewRedis creates a new Redis client
func NewRedis() (*Redis, error) {
    ctx := context.Background()

    client := redis.NewClient(&redis.Options{
        Addr:         redisAddr,
        Password:     redisPassword,
        DB:           redisDB,
        MaxRetries:   3,
        DialTimeout:  2 * time.Second,
        ReadTimeout:  2 * time.Second,
        WriteTimeout: 2 * time.Second,
    })

    // Test connection with timeout
    ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
    defer cancel()

    if err := client.Ping(ctx).Err(); err != nil {
        return nil, fmt.Errorf("failed to connect to Redis: %w", err)
    }

    return &Redis{
        client: client,
        ctx:    context.Background(),
    }, nil
}

// CacheQuery caches a query result
func (r *Redis) CacheQuery(queryText string, result *query.Result, ttl time.Duration) error {
    key := keyPrefix + queryText

    data, err := json.Marshal(result)
    if err != nil {
        return fmt.Errorf("failed to marshal result: %w", err)
    }

    return r.client.Set(r.ctx, key, data, ttl).Err()
}

// GetQuery retrieves a cached query result
func (r *Redis) GetQuery(queryText string) (*query.Result, error) {
    key := keyPrefix + queryText

    data, err := r.client.Get(r.ctx, key).Bytes()
    if err == redis.Nil {
        return nil, fmt.Errorf("query not found in cache")
    } else if err != nil {
        return nil, fmt.Errorf("failed to get query: %w", err)
    }

    var result query.Result
    if err := json.Unmarshal(data, &result); err != nil {
        return nil, fmt.Errorf("failed to unmarshal result: %w", err)
    }

    return &result, nil
}

// InvalidateCache invalidates all cached queries
func (r *Redis) InvalidateCache() error {
    iter := r.client.Scan(r.ctx, 0, keyPrefix+"*", 0).Iterator()
    for iter.Next(r.ctx) {
        if err := r.client.Del(r.ctx, iter.Val()).Err(); err != nil {
            return err
        }
    }
    return iter.Err()
}

// GetCacheStats returns cache statistics
func (r *Redis) GetCacheStats() (map[string]interface{}, error) {
    stats := make(map[string]interface{})

    // Count cached queries
    var count int64
    iter := r.client.Scan(r.ctx, 0, keyPrefix+"*", 0).Iterator()
    for iter.Next(r.ctx) {
        count++
    }
    if err := iter.Err(); err != nil {
        return nil, err
    }

    stats["cached_queries"] = count

    // Get Redis info
    info, err := r.client.Info(r.ctx, "memory").Result()
    if err == nil {
        stats["memory_info"] = info
    }

    return stats, nil
}

// Close closes the Redis connection
func (r *Redis) Close() error {
    return r.client.Close()
}
