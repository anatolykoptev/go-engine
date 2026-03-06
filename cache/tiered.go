package cache

import (
	"time"

	"github.com/anatolykoptev/go-stealth/webcache"
)

// Tiered chains an L1 (fast, volatile) and L2 (persistent) cache.
// Get checks L1 first; on L2 hit the value is promoted to L1.
// Set writes to both tiers.
// Delegates to webcache.Tiered.
type Tiered = webcache.Tiered

// NewTiered creates a tiered cache. l2 may be nil (L1-only mode).
// defaultTTL is used when promoting L2 hits into L1.
func NewTiered(l1 Cache, l2 Cache, defaultTTL time.Duration) *Tiered {
	return webcache.NewTiered(l1, l2, defaultTTL)
}
