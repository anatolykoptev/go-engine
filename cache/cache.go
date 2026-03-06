// Package cache provides a two-tier caching layer (L1 memory + L2 Redis)
// with automatic promotion on L2 hits.
//
// The [Cache] interface is implemented by [Memory], [Redis], and [Tiered].
// [Tiered] chains L1 and L2 with automatic promotion on L2 hits.
//
// All implementations delegate to go-stealth/webcache for the canonical logic.
package cache

import "github.com/anatolykoptev/go-stealth/webcache"

// Cache is the interface for key-value storage with TTL.
// Identical to webcache.Cache — kept for backward compatibility.
type Cache = webcache.Cache

// Key builds a deterministic cache key from parts using SHA256.
// Returns a 24-char hex string with the given prefix.
func Key(prefix string, parts ...string) string {
	return webcache.Key(prefix, parts...)
}
