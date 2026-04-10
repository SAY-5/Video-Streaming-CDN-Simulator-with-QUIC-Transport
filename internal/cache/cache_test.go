package cache

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func TestLRUBasicEviction(t *testing.T) {
	c := NewLRUCache(2000)
	now := time.Now()
	c.Put(Item{Key: "a", SizeBytes: 800}, now)
	c.Put(Item{Key: "b", SizeBytes: 800}, now)
	c.Put(Item{Key: "c", SizeBytes: 800}, now) // evicts a
	if _, ok := c.Get("a", now); ok {
		t.Fatal("a should have been evicted")
	}
	if _, ok := c.Get("b", now); !ok {
		t.Fatal("b should be present")
	}
	if _, ok := c.Get("c", now); !ok {
		t.Fatal("c should be present")
	}
}

func TestLRUMoveToFront(t *testing.T) {
	c := NewLRUCache(2000)
	now := time.Now()
	c.Put(Item{Key: "a", SizeBytes: 800}, now)
	c.Put(Item{Key: "b", SizeBytes: 800}, now)
	// Touch a so it becomes most-recent.
	if _, ok := c.Get("a", now); !ok {
		t.Fatal("a should be present")
	}
	// Add c: b should be evicted.
	c.Put(Item{Key: "c", SizeBytes: 800}, now)
	if _, ok := c.Get("b", now); ok {
		t.Fatal("b should have been evicted")
	}
	if _, ok := c.Get("a", now); !ok {
		t.Fatal("a should survive")
	}
}

func TestLRUTTL(t *testing.T) {
	c := NewLRUCache(2000)
	now := time.Now()
	c.Put(Item{Key: "a", SizeBytes: 100, Expiry: now.Add(time.Second)}, now)
	if _, ok := c.Get("a", now); !ok {
		t.Fatal("expected hit before expiry")
	}
	if _, ok := c.Get("a", now.Add(2*time.Second)); ok {
		t.Fatal("expected miss after expiry")
	}
}

func TestLRUStats(t *testing.T) {
	c := NewLRUCache(1000)
	now := time.Now()
	c.Put(Item{Key: "a", SizeBytes: 500}, now)
	c.Get("a", now) // hit
	c.Get("b", now) // miss
	s := c.Stats()
	if s.Hits != 1 || s.Misses != 1 {
		t.Fatalf("bad stats: %+v", s)
	}
	if hr := s.HitRate(); hr != 0.5 {
		t.Fatalf("hit rate=%v", hr)
	}
}

func TestARCBasic(t *testing.T) {
	c := NewARCCache(3000)
	now := time.Now()
	c.Put(Item{Key: "a", SizeBytes: 1000}, now)
	c.Put(Item{Key: "b", SizeBytes: 1000}, now)
	c.Put(Item{Key: "c", SizeBytes: 1000}, now)
	if _, ok := c.Get("a", now); !ok {
		t.Fatal("a should be present")
	}
}

func TestARCScanResistance(t *testing.T) {
	c := NewARCCache(10_000)
	now := time.Now()
	// Populate with X and access it several times so it lives in T2.
	c.Put(Item{Key: "hot", SizeBytes: 1000}, now)
	for i := 0; i < 5; i++ {
		c.Get("hot", now)
	}
	// Now scan: 20 distinct items of 1000 bytes each. An LRU would evict hot;
	// ARC should not, because sequential one-shot items go through T1.
	for i := 0; i < 20; i++ {
		c.Put(Item{Key: fmt.Sprintf("s%d", i), SizeBytes: 1000}, now)
	}
	if _, ok := c.Get("hot", now); !ok {
		t.Fatal("hot item should survive a scan in ARC (scan resistance)")
	}
}

func TestARCTTL(t *testing.T) {
	c := NewARCCache(3000)
	now := time.Now()
	c.Put(Item{Key: "a", SizeBytes: 100, Expiry: now.Add(time.Second)}, now)
	if _, ok := c.Get("a", now.Add(2*time.Second)); ok {
		t.Fatal("expired item should miss")
	}
}

func TestZipfDistributionSkew(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	z := NewZipfPopularity(1000, 1.2, rng)
	counts := make(map[int]int)
	n := 100_000
	for i := 0; i < n; i++ {
		counts[z.NextIndex()]++
	}
	// Top 10% of IDs (0..99) should account for a large share of requests.
	topShare := 0
	for i := 0; i < 100; i++ {
		topShare += counts[i]
	}
	share := float64(topShare) / float64(n)
	if share < 0.7 {
		t.Fatalf("expected top 10%% to receive >=70%% of requests, got %.2f", share)
	}
}
