package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisExactCache implements ExactCache using Redis.
type RedisExactCache struct {
	client *redis.Client
	prefix string
}

type RedisConfig struct {
	Prefix string
}

// NewRedisExactCache creates a Redis-backed cache.
func NewRedisExactCache(client *redis.Client, config RedisConfig) *RedisExactCache {
	return &RedisExactCache{
		client: client,
		prefix: config.Prefix,
	}
}

// key builds the final Redis key with prefix.
func (c *RedisExactCache) key(k string) string {
	if c.prefix == "" {
		return k
	}
	return c.prefix + ":" + k
}

// Get retrieves a value from Redis cache.
// On Redis error, it returns (nil, false, err) so caller can log and treat as miss.
func (c *RedisExactCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("context error: %w", err)
	}

	redisKey := c.key(key)

	res, err := c.client.Get(ctx, redisKey).Bytes()
	if err == redis.Nil {
		// Key does not exist – this is a clean miss.
		return nil, false, nil
	}
	if err != nil {
		// Caller (handler) should log and treat as miss.
		return nil, false, fmt.Errorf("redis get failed: %w", err)
	}

	return res, true, nil
}

// Set stores a value in Redis cache with TTL.
// If ttl <= 0, it does nothing (no caching).
func (c *RedisExactCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context error: %w", err)
	}

	if ttl <= 0 {
		return nil
	}

	redisKey := c.key(key)

	if err := c.client.Set(ctx, redisKey, value, ttl).Err(); err != nil {
		return fmt.Errorf("redis set failed: %w", err)
	}

	return nil
}

// Delete removes a key from cache
func (c *RedisExactCache) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context error: %w", err)
	}
	return c.client.Del(ctx, c.key(key)).Err()
}

// Exists checks if a key exists without retrieving the value.
func (c *RedisExactCache) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("context error: %w", err)
	}
	count, err := c.client.Exists(ctx, c.key(key)).Result()
	if err != nil {
		return false, fmt.Errorf("redis exists failed: %w", err)
	}
	return count > 0, nil
}

// Ping checks if Redis connection is healthy.
func (c *RedisExactCache) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context error: %w", err)
	}
	return c.client.Ping(ctx).Err()
}
