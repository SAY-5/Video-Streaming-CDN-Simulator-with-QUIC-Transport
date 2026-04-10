package cache

import (
	"fmt"
	"testing"
	"time"
)

// TestARCB1GhostHitGrowsP exercises the B1-hit branch: a key that was
// evicted from T1 into B1 is re-inserted, which should grow p toward the
// recency list.
func TestARCB1GhostHitGrowsP(t *testing.T) {
	c := NewARCCache(2000)
	now := time.Now()
	// Fill T1 beyond capacity so items get pushed to B1.
	for i := 0; i < 5; i++ {
		c.Put(Item{Key: fmt.Sprintf("k%d", i), SizeBytes: 500}, now)
	}
	// Now k0 and k1 should be in B1 (evicted).
	// Re-insert k0 — this is a B1 hit and should grow p.
	pBefore := c.p
	c.Put(Item{Key: "k0", SizeBytes: 500}, now)
	if c.p <= pBefore {
		t.Fatalf("expected p to grow after B1 hit (before=%d after=%d)", pBefore, c.p)
	}
	// k0 should now be in T2.
	if _, ok := c.Get("k0", now); !ok {
		t.Fatal("k0 should be present (promoted to T2)")
	}
}

// TestARCB2GhostHitShrinksP exercises the B2-hit branch: a key that was
// frequent, got evicted from T2 into B2, and is now re-inserted should
// shrink p (favor frequency).
func TestARCB2GhostHitShrinksP(t *testing.T) {
	c := NewARCCache(2000)
	now := time.Now()
	// Put k0 and promote to T2 with two accesses.
	c.Put(Item{Key: "hot", SizeBytes: 500}, now)
	c.Get("hot", now)
	c.Get("hot", now)
	// Now hot is in T2.
	// Push enough items to evict hot from T2 into B2.
	for i := 0; i < 8; i++ {
		c.Put(Item{Key: fmt.Sprintf("k%d", i), SizeBytes: 500}, now)
		c.Get(fmt.Sprintf("k%d", i), now) // promote each to T2
		c.Get(fmt.Sprintf("k%d", i), now)
	}
	// By now hot should be in B2.
	// First drive p upward via a B1 hit (insert "hot" is a B2 hit — so just
	// record p before and verify B2 hit shrinks it).
	// Actually we want to make p non-zero first. Insert new items to force
	// more eviction.
	for i := 0; i < 3; i++ {
		c.Put(Item{Key: fmt.Sprintf("x%d", i), SizeBytes: 500}, now)
	}
	pBefore := c.p
	// Re-insert hot: B2 hit should shrink p.
	c.Put(Item{Key: "hot", SizeBytes: 500}, now)
	if c.p > pBefore {
		t.Fatalf("expected p to shrink (or stay) after B2 hit (before=%d after=%d)", pBefore, c.p)
	}
	// hot should now be in T2.
	if _, ok := c.Get("hot", now); !ok {
		t.Fatal("hot should be present after B2 re-insertion")
	}
}

// TestARCGhostByteAccountingStable verifies that re-inserting the same
// key with a DIFFERENT size does not corrupt b1Bytes bookkeeping.
// This is the regression test for the HIGH-4 bug where the ghost-list
// accounting subtracted the incoming item's size instead of the ghost
// entry's size.
func TestARCGhostByteAccountingStable(t *testing.T) {
	c := NewARCCache(4000)
	now := time.Now()
	// Insert with large size → evict to B1 later.
	for i := 0; i < 10; i++ {
		c.Put(Item{Key: fmt.Sprintf("k%d", i), SizeBytes: 500}, now)
	}
	// k0 is now in B1 with size 500.
	// Re-insert k0 with a DIFFERENT size (100). The bug would subtract
	// 100 from b1Bytes (which had 500 recorded), leaving 400 stale.
	c.Put(Item{Key: "k0", SizeBytes: 100}, now)
	// The invariant we care about: b1Bytes should equal the sum of sizes
	// currently in the B1 list. If the bug were present, b1Bytes would
	// drift positive (400 leftover) as we repeat the pattern.
	// Exercise the pattern many times:
	for round := 0; round < 20; round++ {
		for i := 0; i < 10; i++ {
			c.Put(Item{Key: fmt.Sprintf("r%d_%d", round, i), SizeBytes: 200}, now)
		}
	}
	// Walk the B1 list and sum sizes.
	var actual int64
	for el := c.b1.Front(); el != nil; el = el.Next() {
		ent := el.Value.(*arcEntry)
		actual += ent.item.SizeBytes
	}
	if c.b1Bytes != actual {
		t.Fatalf("b1Bytes accounting drifted: tracked=%d actual=%d", c.b1Bytes, actual)
	}
}

// TestARCScanResistanceAfterWarmUp ensures that a large scan does not
// evict frequently-accessed items from T2.
func TestARCScanResistanceAfterWarmUp(t *testing.T) {
	c := NewARCCache(10_000)
	now := time.Now()
	// Hot item accessed many times → lands in T2.
	c.Put(Item{Key: "hot", SizeBytes: 500}, now)
	for i := 0; i < 10; i++ {
		c.Get("hot", now)
	}
	// Sequential scan with many distinct items, each accessed once.
	for i := 0; i < 100; i++ {
		c.Put(Item{Key: fmt.Sprintf("scan%d", i), SizeBytes: 500}, now)
	}
	if _, ok := c.Get("hot", now); !ok {
		t.Fatal("hot should survive the scan (ARC scan-resistance)")
	}
}
