// Package cache provides cache implementations (LRU, ARC) and a Zipf-distributed
// content popularity generator for CDN edge simulation.
package cache

import "time"

// Item is a cached object.
type Item struct {
	Key       string
	SizeBytes int64
	Expiry    time.Time // zero means no expiry
	Value     any       // opaque payload; usually unused in the simulator
}

// CacheStats captures cache operational counters.
type CacheStats struct {
	Hits      int64
	Misses    int64
	Evictions int64
	BytesUsed int64
	BytesMax  int64
}

// HitRate returns the hit ratio in [0, 1].
func (s CacheStats) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}
	return float64(s.Hits) / float64(total)
}

// Cache is the interface implemented by all cache backends.
type Cache interface {
	// Get returns the item and true on hit (non-expired), or false on miss.
	Get(key string, now time.Time) (Item, bool)
	// Put inserts an item, evicting as needed. Items larger than the
	// configured max size are silently dropped.
	Put(item Item, now time.Time)
	// Stats returns a snapshot of cache statistics.
	Stats() CacheStats
	// Clear empties the cache and resets stats.
	Clear()
	// Name returns a short identifier, e.g. "lru" or "arc".
	Name() string
}
