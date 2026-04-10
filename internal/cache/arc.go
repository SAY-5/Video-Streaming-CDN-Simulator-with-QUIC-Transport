package cache

import (
	"container/list"
	"sync"
	"time"
)

// ARCCache implements IBM's Adaptive Replacement Cache algorithm with
// per-item size and TTL support. ARC maintains four lists:
//
//	T1: recent items seen once (LRU)
//	T2: items seen at least twice recently (LFU-ish)
//	B1: ghost list of items recently evicted from T1
//	B2: ghost list of items recently evicted from T2
//
// The target size of T1, p, adapts based on hits in B1 and B2. Scan-resistant:
// a sequential scan cycles through T1 without polluting T2.
//
// Because the classical algorithm is count-based, we approximate byte-bounded
// ARC by scaling sizes to the average item size: target p is in units of the
// current byte budget.
type ARCCache struct {
	maxSizeBytes int64
	p            int64 // target size of T1 in bytes
	mu           sync.Mutex

	t1 *list.List // recency list
	t2 *list.List // frequency list
	b1 *list.List // ghost for T1
	b2 *list.List // ghost for T2

	index   map[string]*list.Element
	ownerOf map[string]byte // 't','T','b','B' for t1/t2/b1/b2

	t1Bytes, t2Bytes, b1Bytes, b2Bytes int64

	stats CacheStats
}

type arcEntry struct {
	item Item
}

// NewARCCache constructs an ARC cache with the given byte budget.
func NewARCCache(maxSizeBytes int64) *ARCCache {
	return &ARCCache{
		maxSizeBytes: maxSizeBytes,
		t1:           list.New(),
		t2:           list.New(),
		b1:           list.New(),
		b2:           list.New(),
		index:        make(map[string]*list.Element),
		ownerOf:      make(map[string]byte),
		stats:        CacheStats{BytesMax: maxSizeBytes},
	}
}

// Name returns "arc".
func (c *ARCCache) Name() string { return "arc" }

// Stats returns a snapshot of current cache statistics.
func (c *ARCCache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.stats
	s.BytesUsed = c.t1Bytes + c.t2Bytes
	return s
}

// Clear empties the cache and its ghost lists.
func (c *ARCCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t1 = list.New()
	c.t2 = list.New()
	c.b1 = list.New()
	c.b2 = list.New()
	c.index = make(map[string]*list.Element)
	c.ownerOf = make(map[string]byte)
	c.t1Bytes, c.t2Bytes, c.b1Bytes, c.b2Bytes = 0, 0, 0, 0
	c.p = 0
	c.stats = CacheStats{BytesMax: c.maxSizeBytes}
}

// Get looks up an item by key.
func (c *ARCCache) Get(key string, now time.Time) (Item, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.index[key]
	if !ok {
		c.stats.Misses++
		return Item{}, false
	}
	owner := c.ownerOf[key]
	if owner != 't' && owner != 'T' {
		// Ghost list hit is not a real hit.
		c.stats.Misses++
		return Item{}, false
	}
	ent := el.Value.(*arcEntry)
	if !ent.item.Expiry.IsZero() && now.After(ent.item.Expiry) {
		// Expired: remove entirely.
		c.removeFromActive(key, el)
		c.stats.Misses++
		return Item{}, false
	}
	// Promote: T1 hit -> move to T2; T2 hit -> move to MRU of T2.
	if owner == 't' {
		c.t1.Remove(el)
		c.t1Bytes -= ent.item.SizeBytes
		newEl := c.t2.PushFront(ent)
		c.t2Bytes += ent.item.SizeBytes
		c.index[key] = newEl
		c.ownerOf[key] = 'T'
	} else {
		c.t2.MoveToFront(el)
	}
	c.stats.Hits++
	return ent.item, true
}

func (c *ARCCache) removeFromActive(key string, el *list.Element) {
	ent := el.Value.(*arcEntry)
	owner := c.ownerOf[key]
	switch owner {
	case 't':
		c.t1.Remove(el)
		c.t1Bytes -= ent.item.SizeBytes
	case 'T':
		c.t2.Remove(el)
		c.t2Bytes -= ent.item.SizeBytes
	}
	delete(c.index, key)
	delete(c.ownerOf, key)
}

// Put inserts an item.
func (c *ARCCache) Put(item Item, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if item.SizeBytes > c.maxSizeBytes {
		return
	}
	key := item.Key
	if el, ok := c.index[key]; ok {
		owner := c.ownerOf[key]
		switch owner {
		case 't', 'T':
			// Update existing entry in place.
			ent := el.Value.(*arcEntry)
			if owner == 't' {
				c.t1Bytes -= ent.item.SizeBytes
				c.t1Bytes += item.SizeBytes
			} else {
				c.t2Bytes -= ent.item.SizeBytes
				c.t2Bytes += item.SizeBytes
			}
			ent.item = item
			return
		case 'b':
			// B1 hit: grow p (favor recency list).
			// CRIT-fix: use the ghost entry's recorded size for bookkeeping,
			// not the incoming item's size. The ghost was inserted with the
			// size of the ITEM THAT WAS EVICTED; re-inserting the key with a
			// different size (e.g. different bitrate representation) would
			// otherwise corrupt b1Bytes.
			ghostEnt := el.Value.(*arcEntry)
			ghostSize := ghostEnt.item.SizeBytes
			delta := int64(1)
			if c.b2Bytes > c.b1Bytes && c.b1Bytes > 0 {
				delta = c.b2Bytes / c.b1Bytes
			}
			step := delta * ghostSize
			c.p += step
			if c.p > c.maxSizeBytes {
				c.p = c.maxSizeBytes
			}
			// Remove from B1 and admit to T2.
			c.b1.Remove(el)
			c.b1Bytes -= ghostSize
			delete(c.index, key)
			delete(c.ownerOf, key)
			c.replace(item, false)
			newEl := c.t2.PushFront(&arcEntry{item: item})
			c.t2Bytes += item.SizeBytes
			c.index[key] = newEl
			c.ownerOf[key] = 'T'
			c.stats.BytesUsed = c.t1Bytes + c.t2Bytes
			return
		case 'B':
			// B2 hit: shrink p (favor frequency list).
			// CRIT-fix: same ghost-size correction as the B1 branch.
			ghostEnt := el.Value.(*arcEntry)
			ghostSize := ghostEnt.item.SizeBytes
			delta := int64(1)
			if c.b1Bytes > c.b2Bytes && c.b2Bytes > 0 {
				delta = c.b1Bytes / c.b2Bytes
			}
			step := delta * ghostSize
			c.p -= step
			if c.p < 0 {
				c.p = 0
			}
			c.b2.Remove(el)
			c.b2Bytes -= ghostSize
			delete(c.index, key)
			delete(c.ownerOf, key)
			c.replace(item, true)
			newEl := c.t2.PushFront(&arcEntry{item: item})
			c.t2Bytes += item.SizeBytes
			c.index[key] = newEl
			c.ownerOf[key] = 'T'
			c.stats.BytesUsed = c.t1Bytes + c.t2Bytes
			return
		}
	}
	// Fresh insertion into T1, following ARC Case IV from Megiddo &
	// Modha 2003. Two subcases:
	//
	//   Case IV.A: |L1| == c and |T1| < c
	//     Delete the LRU of B1; REPLACE(x).
	//   Case IV.B: |L1| == c and |T1| == c
	//     Move the LRU of T1 into the MRU of B1; REPLACE(x).
	//   Case IV.C: |L1| < c and |L1|+|L2| >= 2c
	//     Delete the LRU of B2; REPLACE(x).
	//   Case IV.D: otherwise
	//     REPLACE(x).
	//
	// The critical correctness point — and the bug that regression test
	// TestARCB1GhostHitGrowsP exposes — is that Case IV.B must MOVE T1's
	// LRU into B1 (creating a ghost entry), not silently delete it. The
	// earlier implementation used evictLRUList which dropped the entry
	// entirely, so the subsequent re-insertion of the same key was a
	// cold miss instead of a B1 hit, and ARC's adaptive p parameter
	// never grew.
	l1Bytes := c.t1Bytes + c.b1Bytes
	if l1Bytes+item.SizeBytes > c.maxSizeBytes {
		if c.t1Bytes < c.maxSizeBytes {
			// Case IV.A
			c.evictLRUList(c.b1, 'b')
			c.replace(item, false)
		} else {
			// Case IV.B: move T1's LRU to B1 as a ghost.
			c.moveT1LRUToB1()
			c.replace(item, false)
		}
	} else {
		l2Bytes := c.t1Bytes + c.t2Bytes + c.b1Bytes + c.b2Bytes
		if l2Bytes+item.SizeBytes >= 2*c.maxSizeBytes {
			// Case IV.C
			c.evictLRUList(c.b2, 'B')
		}
		// Case IV.D (and falls through from IV.C)
		c.replace(item, false)
	}
	newEl := c.t1.PushFront(&arcEntry{item: item})
	c.t1Bytes += item.SizeBytes
	c.index[key] = newEl
	c.ownerOf[key] = 't'
	c.stats.BytesUsed = c.t1Bytes + c.t2Bytes
}

// replace is the ARC REPLACE subroutine: move the LRU page of T1 (or T2) to
// the ghost list to make space for new content.
func (c *ARCCache) replace(incoming Item, inB2 bool) {
	for c.t1Bytes+c.t2Bytes+incoming.SizeBytes > c.maxSizeBytes {
		if c.t1Bytes > 0 && (c.t1Bytes > c.p || (inB2 && c.t1Bytes == c.p)) {
			// Evict LRU of T1 into B1.
			back := c.t1.Back()
			if back == nil {
				break
			}
			ent := back.Value.(*arcEntry)
			c.t1.Remove(back)
			c.t1Bytes -= ent.item.SizeBytes
			// Move to B1.
			bEl := c.b1.PushFront(&arcEntry{item: Item{Key: ent.item.Key, SizeBytes: ent.item.SizeBytes}})
			c.b1Bytes += ent.item.SizeBytes
			c.index[ent.item.Key] = bEl
			c.ownerOf[ent.item.Key] = 'b'
			c.stats.Evictions++
		} else if c.t2Bytes > 0 {
			back := c.t2.Back()
			if back == nil {
				break
			}
			ent := back.Value.(*arcEntry)
			c.t2.Remove(back)
			c.t2Bytes -= ent.item.SizeBytes
			bEl := c.b2.PushFront(&arcEntry{item: Item{Key: ent.item.Key, SizeBytes: ent.item.SizeBytes}})
			c.b2Bytes += ent.item.SizeBytes
			c.index[ent.item.Key] = bEl
			c.ownerOf[ent.item.Key] = 'B'
			c.stats.Evictions++
		} else {
			break
		}
	}
}

// moveT1LRUToB1 moves the least-recently-used entry of T1 into the MRU
// position of B1, preserving the ghost entry's original size. Used by
// ARC Case IV.B and by any other path that needs to ghost-evict from T1.
func (c *ARCCache) moveT1LRUToB1() {
	back := c.t1.Back()
	if back == nil {
		return
	}
	ent := back.Value.(*arcEntry)
	c.t1.Remove(back)
	c.t1Bytes -= ent.item.SizeBytes
	bEl := c.b1.PushFront(&arcEntry{item: Item{Key: ent.item.Key, SizeBytes: ent.item.SizeBytes}})
	c.b1Bytes += ent.item.SizeBytes
	c.index[ent.item.Key] = bEl
	c.ownerOf[ent.item.Key] = 'b'
	c.stats.Evictions++
}

// evictLRUList evicts the LRU entry of the given list without moving it to
// a ghost list — used when the ghost lists themselves overflow.
func (c *ARCCache) evictLRUList(l *list.List, owner byte) {
	back := l.Back()
	if back == nil {
		return
	}
	ent := back.Value.(*arcEntry)
	l.Remove(back)
	switch owner {
	case 't':
		c.t1Bytes -= ent.item.SizeBytes
	case 'T':
		c.t2Bytes -= ent.item.SizeBytes
	case 'b':
		c.b1Bytes -= ent.item.SizeBytes
	case 'B':
		c.b2Bytes -= ent.item.SizeBytes
	}
	delete(c.index, ent.item.Key)
	delete(c.ownerOf, ent.item.Key)
}
