package cache

import (
	"container/list"
	"sync"
	"time"
)

// LRUCache is a byte-bounded LRU cache with per-item TTL support. It is
// safe for concurrent use.
type LRUCache struct {
	maxSizeBytes int64
	currentSize  int64
	items        map[string]*list.Element
	evictList    *list.List // front = most recent
	stats        CacheStats
	mu           sync.RWMutex
}

type lruEntry struct {
	item Item
}

// NewLRUCache returns an LRU cache that will evict entries when its total
// byte usage exceeds maxSizeBytes.
func NewLRUCache(maxSizeBytes int64) *LRUCache {
	return &LRUCache{
		maxSizeBytes: maxSizeBytes,
		items:        make(map[string]*list.Element),
		evictList:    list.New(),
		stats:        CacheStats{BytesMax: maxSizeBytes},
	}
}

// Name returns "lru".
func (c *LRUCache) Name() string { return "lru" }

// Get returns the item at key if present and not expired.
func (c *LRUCache) Get(key string, now time.Time) (Item, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		c.stats.Misses++
		return Item{}, false
	}
	ent := el.Value.(*lruEntry)
	if !ent.item.Expiry.IsZero() && now.After(ent.item.Expiry) {
		// Expired: remove and count as miss.
		c.removeElement(el)
		c.stats.Misses++
		return Item{}, false
	}
	c.evictList.MoveToFront(el)
	c.stats.Hits++
	return ent.item, true
}

// Put inserts or updates an item.
func (c *LRUCache) Put(item Item, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if item.SizeBytes > c.maxSizeBytes {
		return // too big to cache
	}
	if el, ok := c.items[item.Key]; ok {
		ent := el.Value.(*lruEntry)
		c.currentSize -= ent.item.SizeBytes
		ent.item = item
		c.currentSize += item.SizeBytes
		c.evictList.MoveToFront(el)
	} else {
		el := c.evictList.PushFront(&lruEntry{item: item})
		c.items[item.Key] = el
		c.currentSize += item.SizeBytes
	}
	for c.currentSize > c.maxSizeBytes {
		back := c.evictList.Back()
		if back == nil {
			break
		}
		c.removeElement(back)
		c.stats.Evictions++
	}
	c.stats.BytesUsed = c.currentSize
}

func (c *LRUCache) removeElement(el *list.Element) {
	ent := el.Value.(*lruEntry)
	c.currentSize -= ent.item.SizeBytes
	delete(c.items, ent.item.Key)
	c.evictList.Remove(el)
}

// Stats returns a snapshot of cache statistics.
func (c *LRUCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.stats
	s.BytesUsed = c.currentSize
	return s
}

// Clear empties the cache.
func (c *LRUCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*list.Element)
	c.evictList = list.New()
	c.currentSize = 0
	c.stats = CacheStats{BytesMax: c.maxSizeBytes}
}
