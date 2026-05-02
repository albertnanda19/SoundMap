package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	geov1 "github.com/soundmap/soundmap/gen/go/geo/v1"
)

// CellCache defines the interface for caching hex cell data
type CellCache interface {
	GetCell(ctx context.Context, h3Index string) (*geov1.HexCell, error)
	SetCell(ctx context.Context, h3Index string, cell *geov1.HexCell, expiry time.Duration) error
	GetCells(ctx context.Context, h3Indexes []string) (map[string]*geov1.HexCell, error)
	SetCells(ctx context.Context, cells map[string]*geov1.HexCell, expiry time.Duration) error
}

// RedisCache implements CellCache using Redis
type RedisCache struct {
	client *redis.Client
	logger *slog.Logger
}

// NewRedisCache creates a new Redis cache
func NewRedisCache(addr, password string, logger *slog.Logger) *RedisCache {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0,
	})

	return &RedisCache{
		client: client,
		logger: logger,
	}
}

// GetCell retrieves a single cell from cache
func (c *RedisCache) GetCell(ctx context.Context, h3Index string) (*geov1.HexCell, error) {
	key := fmt.Sprintf("cell:%s", h3Index)
	data, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			// Cache miss - not an error
			return nil, nil
		}
		// Log cache error but don't fail the query
		c.logger.Error("Redis cache get error", slog.String("error", err.Error()))
		return nil, fmt.Errorf("cache get error: %w", err)
	}

	var cell geov1.HexCell
	if err := json.Unmarshal(data, &cell); err != nil {
		c.logger.Error("Failed to unmarshal cached cell", slog.String("error", err.Error()))
		return nil, fmt.Errorf("cache unmarshal error: %w", err)
	}

	return &cell, nil
}

// SetCell stores a single cell in cache
func (c *RedisCache) SetCell(ctx context.Context, h3Index string, cell *geov1.HexCell, expiry time.Duration) error {
	key := fmt.Sprintf("cell:%s", h3Index)
	data, err := json.Marshal(cell)
	if err != nil {
		return fmt.Errorf("cache marshal error: %w", err)
	}

	if err := c.client.Set(ctx, key, data, expiry).Err(); err != nil {
		c.logger.Error("Redis cache set error", slog.String("error", err.Error()))
		return fmt.Errorf("cache set error: %w", err)
	}

	return nil
}

// GetCells retrieves multiple cells from cache (batch get)
func (c *RedisCache) GetCells(ctx context.Context, h3Indexes []string) (map[string]*geov1.HexCell, error) {
	if len(h3Indexes) == 0 {
		return nil, nil
	}

	// Build keys
	keys := make([]string, len(h3Indexes))
	for i, h3Index := range h3Indexes {
		keys[i] = fmt.Sprintf("cell:%s", h3Index)
	}

	// Use MGET for efficient batch retrieval
	results, err := c.client.MGet(ctx, keys...).Result()
	if err != nil {
		c.logger.Error("Redis cache MGet error", slog.String("error", err.Error()))
		// Fall through to return empty map (cache-aside pattern)
		return make(map[string]*geov1.HexCell), nil
	}

	found := make(map[string]*geov1.HexCell)
	for i, result := range results {
		if result == nil {
			continue // Cache miss
		}

		data, ok := result.(string)
		if !ok {
			continue
		}

		var cell geov1.HexCell
		if err := json.Unmarshal([]byte(data), &cell); err != nil {
			c.logger.Error("Failed to unmarshal cached cell",
				slog.String("h3_index", h3Indexes[i]),
				slog.String("error", err.Error()))
			continue
		}

		found[h3Indexes[i]] = &cell
	}

	return found, nil
}

// SetCells stores multiple cells in cache using pipeline
func (c *RedisCache) SetCells(ctx context.Context, cells map[string]*geov1.HexCell, expiry time.Duration) error {
	if len(cells) == 0 {
		return nil
	}

	pipe := c.client.Pipeline()
	for h3Index, cell := range cells {
		key := fmt.Sprintf("cell:%s", h3Index)
		data, err := json.Marshal(cell)
		if err != nil {
			c.logger.Error("Failed to marshal cell for cache",
				slog.String("h3_index", h3Index),
				slog.String("error", err.Error()))
			continue
		}
		pipe.Set(ctx, key, data, expiry)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		c.logger.Error("Redis pipeline error", slog.String("error", err.Error()))
		return fmt.Errorf("cache pipeline error: %w", err)
	}

	return nil
}

// Close closes the Redis client
func (c *RedisCache) Close() error {
	return c.client.Close()
}
