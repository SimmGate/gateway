package cache

import (
	"context"
	"sync"
	"time"
)

type memoryEntry struct {
	value     []byte
	expiresAt time.Time
}

type MemoryExactCache struct {
	mu              sync.RWMutex
	items           map[string]memoryEntry
	stopCleanup     chan struct{}
	cleanupOnce     sync.Once
	cleanupInterval time.Duration
}

//create new in cache memory
//if time duration is less than 0 def time of 5 mins is used

func NewMemoryExactCache(cleanupInterval time.Duration) *MemoryExactCache {
	if cleanupInterval <= 0 {
		cleanupInterval = 5 * time.Minute
	}

	c := &MemoryExactCache{
		items:           make(map[string]memoryEntry),
		stopCleanup:     make(chan struct{}),
		cleanupInterval: cleanupInterval,
	}

	//background cleanup routine
	go c.cleanupExpired()

	return c
}

//get retrieves value from cache

func (c *MemoryExactCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	c.mu.RLock()
	entry, ok := c.items[key]
	c.mu.RUnlock()

	if !ok {
		return nil, false, nil
	}

	now := time.Now()
	if now.After(entry.expiresAt) {
		c.mu.Lock()
		if e, exists := c.items[key]; exists && now.After(e.expiresAt) {
			delete(c.items, key)
		}
		c.mu.Unlock()
		return nil, false, nil
	}

	return entry.value, true, nil
}

// sets value in cache with ttl
func (c *MemoryExactCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return nil
	}

	// Copy to decouple from caller's buffer
	valueCopy := make([]byte, len(value))
	copy(valueCopy, value)

	expiresAt := time.Now().Add(ttl)

	c.mu.Lock()
	c.items[key] = memoryEntry{
		value:     valueCopy,
		expiresAt: expiresAt,
	}
	c.mu.Unlock()

	return nil
}

// cleanupExpired runs periodically to remove expired entries.
func (c *MemoryExactCache) cleanupExpired() {
	ticker := time.NewTicker(c.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			c.mu.Lock()
			for k, v := range c.items {
				if now.After(v.expiresAt) {
					delete(c.items, k)
				}
			}
			c.mu.Unlock()
		case <-c.stopCleanup:
			return
		}
	}
}

// Close stops the cleanup goroutine. Call this on shutdown or in tests.
func (c *MemoryExactCache) Close() error {
	c.cleanupOnce.Do(func() {
		close(c.stopCleanup)
	})
	return nil
}

// Len returns the number of items currently in the cache.
func (c *MemoryExactCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Clear removes all items from cache. Useful for tests or manual resets.
func (c *MemoryExactCache) Clear() {
	c.mu.Lock()
	c.items = make(map[string]memoryEntry)
	c.mu.Unlock()
}
