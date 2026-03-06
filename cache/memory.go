package cache

import (
	"context"
	"time"

	"github.com/anatolykoptev/go-stealth/webcache"
)

// Memory is an in-memory cache backed by sync.Map with TTL and eviction.
// Delegates to webcache.Memory.
type Memory = webcache.Memory

// MemoryOption configures a Memory cache.
type MemoryOption = webcache.MemoryOption

// WithMaxEntries sets the maximum number of entries before eviction triggers.
// Zero or negative disables eviction.
func WithMaxEntries(n int) MemoryOption {
	return webcache.WithMaxEntries(n)
}

// WithCleanupInterval sets how often expired entries are removed.
// Defaults to 5 minutes.
func WithCleanupInterval(d time.Duration) MemoryOption {
	return webcache.WithCleanupInterval(d)
}

// NewMemory creates an in-memory cache.
// The cleanup goroutine runs until ctx is cancelled.
func NewMemory(ctx context.Context, opts ...MemoryOption) *Memory {
	return webcache.NewMemory(ctx, opts...)
}
