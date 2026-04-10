package cdn

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/cdn-sim/cdn-sim/internal/cache"
	"github.com/cdn-sim/cdn-sim/internal/transport"
	"github.com/cdn-sim/cdn-sim/internal/transport/modeled"
)

func TestShieldMissThenHit(t *testing.T) {
	profile := transport.NetworkProfile{BaseRTTMs: 30, BandwidthMbps: 100, LossModel: transport.LossModel{Type: "none"}}
	tr := modeled.NewModeledTCPTransport(profile, rand.New(rand.NewSource(1)))
	sh := NewOriginShield(cache.NewLRUCache(10_000_000), tr, 3600)
	req := transport.SegmentRequest{SegmentID: "s1", SizeBytes: 100_000}
	_, hit, err := sh.Fetch(context.Background(), req, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("first fetch should miss")
	}
	_, hit2, err := sh.Fetch(context.Background(), req, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !hit2 {
		t.Fatal("second fetch should hit")
	}
}

func TestShieldHitLowerLatency(t *testing.T) {
	profile := transport.NetworkProfile{BaseRTTMs: 200, BandwidthMbps: 50, LossModel: transport.LossModel{Type: "none"}}
	tr := modeled.NewModeledTCPTransport(profile, rand.New(rand.NewSource(1)))
	sh := NewOriginShield(cache.NewLRUCache(10_000_000), tr, 3600)
	req := transport.SegmentRequest{SegmentID: "s1", SizeBytes: 500_000}
	r1, _, _ := sh.Fetch(context.Background(), req, time.Time{})
	r2, hit, _ := sh.Fetch(context.Background(), req, time.Time{})
	if !hit {
		t.Fatal("should hit on second fetch")
	}
	if r2.TotalLatency >= r1.TotalLatency {
		t.Fatalf("hit (%v) should be faster than miss (%v)", r2.TotalLatency, r1.TotalLatency)
	}
}

func TestShieldWithoutCache(t *testing.T) {
	profile := transport.NetworkProfile{BaseRTTMs: 20, BandwidthMbps: 100, LossModel: transport.LossModel{Type: "none"}}
	tr := modeled.NewModeledTCPTransport(profile, rand.New(rand.NewSource(1)))
	sh := NewOriginShield(nil, tr, 0)
	_, hit, err := sh.Fetch(context.Background(), transport.SegmentRequest{SegmentID: "x", SizeBytes: 10_000}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("no cache should never hit")
	}
	// Ensure time.Now() is still used (avoid unused-import warning risk).
	_ = time.Now()
}
