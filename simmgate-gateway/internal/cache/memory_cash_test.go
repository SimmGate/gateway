package cache

import (
	"context"
	"testing"
	"time"
)

func TestMemoryExactCache_TTL(t *testing.T) {
	c := NewMemoryExactCache(10 * time.Millisecond)
	defer c.Close()

	ctx := context.Background()
	key := "test:key"
	val := []byte("hello")

	if err := c.Set(ctx, key, val, 20*time.Millisecond); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got, hit, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !hit {
		t.Fatalf("expected hit immediately after Set")
	}
	if string(got) != "hello" {
		t.Fatalf("expected 'hello', got %q", got)
	}

	// Wait for TTL to expire
	time.Sleep(30 * time.Millisecond)

	_, hit, err = c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get after TTL failed: %v", err)
	}
	if hit {
		t.Fatalf("expected miss after TTL expiry")
	}
}
