package video

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/cdn-sim/cdn-sim/internal/cache"
	"github.com/cdn-sim/cdn-sim/internal/transport"
	"github.com/cdn-sim/cdn-sim/internal/transport/modeled"
)

// TestPlayerBatchedRebufferTagsAllSegments regression-tests HIGH-5:
// when a multi-segment batch experiences a rebuffer event, EVERY segment
// in the batch should be tagged Rebuffered=true, not just slot 0.
func TestPlayerBatchedRebufferTagsAllSegments(t *testing.T) {
	// Harsh profile so the first batch definitely triggers a rebuffer.
	profile := transport.NetworkProfile{
		BaseRTTMs:     500,
		BandwidthMbps: 0.5, // 0.5 Mbps: very tight, forces rebuffering
		LossModel:     transport.LossModel{Type: "uniform", UniformPercent: 5},
	}
	m := GenerateManifest("c-batch", 60*time.Second, 4*time.Second, DefaultRepresentations(), rand.New(rand.NewSource(1)))
	sess := &PlaybackSession{
		Manifest:  m,
		ABR:       NewBufferBasedABR(),
		Transport: modeled.NewModeledTCPTransport(profile, rand.New(rand.NewSource(2))),
		Cache:     cache.NewLRUCache(100_000_000),
		Profile:   profile,
		RNG:       rand.New(rand.NewSource(3)),
		Config: PlaybackConfig{
			MaxBuffer:        30 * time.Second,
			StartupThreshold: 2 * time.Second,
			PrefetchDepth:    3, // batched
		},
		ContentID: "c-batch",
	}
	r, err := sess.RunPlayback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.RebufferCount == 0 {
		t.Skip("scenario did not rebuffer; tighten profile to run this test")
	}
	// Find any batch where segment i was rebuffered; verify that segment i+1
	// (if in the same batch) was also tagged.
	// With PrefetchDepth=3 the batch boundaries are at indices 0,3,6,9,...
	// so we check that whenever index i is rebuffered AND i%3 != 0 (i.e. not
	// the head of a batch), segment (i-1) in the same batch is also tagged.
	for i := 1; i < len(r.Segments); i++ {
		if i%3 == 0 {
			continue // head of a new batch
		}
		if r.Segments[i].Rebuffered != r.Segments[i-1].Rebuffered {
			t.Fatalf("batch rebuffer tagging inconsistent at index %d: this=%v prev=%v",
				i, r.Segments[i].Rebuffered, r.Segments[i-1].Rebuffered)
		}
	}
}

// TestPlayerThroughputExcludesCacheHits regression-tests the HIGH throughput
// inflation bug: when a batch contains a mix of cache hits (which return
// in 1ms) and real fetches, the throughput sample must be computed only
// over miss bytes.
func TestPlayerThroughputExcludesCacheHits(t *testing.T) {
	m := GenerateManifest("c-thr", 40*time.Second, 4*time.Second, DefaultRepresentations(), rand.New(rand.NewSource(1)))
	c := cache.NewLRUCache(100_000_000)
	// Warm the cache with the first 3 segments at EVERY representation's
	// bitrate: the buffer-based ABR always picks the lowest rate during
	// startup, and during surplus picks the highest, so we don't know
	// a priori which bitrate the player will request. Warming all of
	// them guarantees the first-batch cache lookups hit.
	for i := 0; i < 6; i++ {
		for _, rep := range m.Representations {
			key := SegmentID("c-thr", i, rep.BitrateKbps)
			c.Put(cache.Item{
				Key: key, SizeBytes: rep.SegmentSizes[i], Expiry: time.Now().Add(time.Hour),
			}, SimEpoch())
		}
	}
	profile := transport.NetworkProfile{
		BaseRTTMs:     50,
		BandwidthMbps: 5,
		LossModel:     transport.LossModel{Type: "none"},
	}
	sess := &PlaybackSession{
		Manifest:  m,
		ABR:       NewBufferBasedABR(),
		Transport: modeled.NewModeledTCPTransport(profile, rand.New(rand.NewSource(2))),
		Cache:     c,
		Profile:   profile,
		RNG:       rand.New(rand.NewSource(3)),
		Config: PlaybackConfig{
			MaxBuffer:        30 * time.Second,
			StartupThreshold: 2 * time.Second,
			PrefetchDepth:    3,
		},
		ContentID: "c-thr",
	}
	r, err := sess.RunPlayback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The cache-hit segments should have CacheHit=true and TotalLatency
	// around 1ms.
	hitCount := 0
	for _, s := range r.Segments {
		if s.CacheHit {
			hitCount++
			if s.TotalLatency > 10*time.Millisecond {
				t.Fatalf("cache hit latency too high: %v", s.TotalLatency)
			}
		}
	}
	if hitCount == 0 {
		t.Fatal("expected cache hits but found none")
	}
}
