package cache

import (
	"context"
	"fmt"
	"time"
)

// Hash is sha256 of normalized req (prompt+model+temp+top_p)
type ExactCacheKey struct {
	UserID    string
	ModelID   string
	VersionID string
	Hash      string
}

// String converts the structured key into the final string used in Redis/map.
func (k ExactCacheKey) String() string {
	// exact:<USER_ID>:<MODEL_ID>:<VERSION_ID>:<HASH_HEX>
	return fmt.Sprintf("exact:%s:%s:%s:%s", k.UserID, k.ModelID, k.VersionID, k.Hash)
}

// ExactCache is the interface used by the handler.
// Implemented by memory cache (dev) and Redis cache (prod).
type ExactCache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}
