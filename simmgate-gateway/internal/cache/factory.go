package cache

import (
	"time"

	"github.com/redis/go-redis/v9"
)

type Config struct {
	Backend string
	TTL     time.Duration
	Prefix  string
}

func NewExactCache(cfg Config, redisClient *redis.Client) ExactCache {
	switch cfg.Backend {
	case "redis":
		return NewRedisExactCache(redisClient, RedisConfig{
			Prefix: cfg.Prefix,
		})
	default:
		return NewMemoryExactCache(cfg.TTL)
	}
}
