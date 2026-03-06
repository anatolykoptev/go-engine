package cache

import "github.com/anatolykoptev/go-stealth/webcache"

// Redis is a cache backed by Redis.
// Delegates to webcache.Redis.
type Redis = webcache.Redis

// NewRedis creates a Redis cache from a connection URL (e.g. "redis://localhost:6379/0").
// Returns nil if the URL is invalid or Redis is unreachable.
func NewRedis(redisURL string) *Redis {
	return webcache.NewRedis(redisURL)
}
