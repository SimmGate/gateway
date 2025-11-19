package cache

import (
	"context"
	"strings"
	"time"

	"simmgate-gateway/internal/metrics"
	"simmgate-gateway/pkg/logging/logging"

	"go.uber.org/zap"
)

// LoggingExactCache wraps an ExactCache with logging + metrics.
type LoggingExactCache struct {
	inner ExactCache
}

// NewLoggingExactCache returns a cache that logs and records metrics.
func NewLoggingExactCache(inner ExactCache) ExactCache {
	return &LoggingExactCache{inner: inner}
}

func (c *LoggingExactCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	start := time.Now()
	value, ok, err := c.inner.Get(ctx, key)
	latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

	logger := loggerFromContext(ctx)

	result := "miss"
	if err != nil {
		result = "error"
	} else if ok {
		result = "hit"
		// Prometheus: count exact cache hits
		metrics.ExactHitsTotal.Inc()
	}

	fields := []zap.Field{
		zap.String("cache_tier", "exact"),
		zap.String("hash_key", key),
		zap.String("cache_result", result), // hit | miss | error
		zap.Float64("latency_ms", latencyMs),
	}

	if parts, ok := parseExactKey(key); ok {
		fields = append(fields,
			zap.String("user_id", parts.userID),
			zap.String("model_id", parts.modelID),
			zap.String("version_id", parts.versionID),
			zap.String("hash", parts.hash),
		)
	}

	if err != nil {
		logger.Error("exact_cache_get", append(fields, zap.Error(err))...)
	} else {
		logger.Info("exact_cache_get", fields...)
	}

	return value, ok, err
}

func (c *LoggingExactCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	start := time.Now()
	err := c.inner.Set(ctx, key, value, ttl)
	latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

	logger := loggerFromContext(ctx)

	fields := []zap.Field{
		zap.String("cache_tier", "exact"),
		zap.String("hash_key", key),
		zap.Float64("latency_ms", latencyMs),
	}

	if parts, ok := parseExactKey(key); ok {
		fields = append(fields,
			zap.String("user_id", parts.userID),
			zap.String("model_id", parts.modelID),
			zap.String("version_id", parts.versionID),
			zap.String("hash", parts.hash),
		)
	}

	if err != nil {
		logger.Error("exact_cache_set", append(fields, zap.Error(err))...)
	} else {
		logger.Info("exact_cache_set", fields...)
	}

	return err
}

func loggerFromContext(ctx context.Context) *zap.Logger {
	if l := logging.FromContext(ctx); l != nil {
		return l
	}
	return logging.L(ctx)
}

// --- helpers for parsing your ExactCacheKey.String() ---

type exactKeyParts struct {
	userID    string
	modelID   string
	versionID string
	hash      string
}

// Expecting: exact:<USER_ID>:<MODEL_ID>:<VERSION_ID>:<HASH>
func parseExactKey(key string) (exactKeyParts, bool) {
	parts := strings.Split(key, ":")
	if len(parts) != 5 || parts[0] != "exact" {
		return exactKeyParts{}, false
	}
	return exactKeyParts{
		userID:    parts[1],
		modelID:   parts[2],
		versionID: parts[3],
		hash:      parts[4],
	}, true
}
