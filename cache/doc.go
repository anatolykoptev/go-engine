// Package cache provides a two-tier caching layer (L1 memory + L2 Redis)
// with singleflight deduplication.
//
// The [Cache] interface is implemented by [Memory], [Redis], and [Tiered].
// [Tiered] chains L1 and L2 with automatic promotion on L2 hits.
package cache
