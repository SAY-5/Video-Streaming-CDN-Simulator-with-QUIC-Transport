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

func goodProfile() transport.NetworkProfile {
	return transport.NetworkProfile{
		BaseRTTMs:     30,
		BandwidthMbps: 50,
		LossModel:     transport.LossModel{Type: "none"},
	}
}

func TestPlaybackNoLoss(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	m := GenerateManifest("c1", 20*time.Second, 4*time.Second, DefaultRepresentations(), rng)
	sess := &PlaybackSession{
		Manifest:  m,
		ABR:       NewBufferBasedABR(),
		Transport: modeled.NewModeledTCPTransport(goodProfile(), rand.New(rand.NewSource(2))),
		Cache:     cache.NewLRUCache(100_000_000),
		Profile:   goodProfile(),
		RNG:       rand.New(rand.NewSource(3)),
		Config:    DefaultPlaybackConfig(),
		ContentID: "c1",
	}
	r, err := sess.RunPlayback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Segments) != m.TotalSegments {
		t.Fatalf("expected %d segments, got %d", m.TotalSegments, len(r.Segments))
	}
	if r.RebufferCount != 0 {
		t.Fatalf("no rebuffering expected on clean network, got %d", r.RebufferCount)
	}
	if r.StartupLatency == 0 {
		t.Fatal("startup latency should be >0")
	}
	if r.AvgBitrateKbps == 0 {
		t.Fatal("avg bitrate should be >0")
	}
}

func TestPlaybackDeterminism(t *testing.T) {
	build := func() *PlaybackSession {
		rng := rand.New(rand.NewSource(1))
		m := GenerateManifest("c1", 20*time.Second, 4*time.Second, DefaultRepresentations(), rng)
		return &PlaybackSession{
			Manifest:  m,
			ABR:       NewThroughputBasedABR(),
			Transport: modeled.NewModeledTCPTransport(goodProfile(), rand.New(rand.NewSource(2))),
			Cache:     cache.NewLRUCache(100_000_000),
			Profile:   goodProfile(),
			RNG:       rand.New(rand.NewSource(3)),
			Config:    DefaultPlaybackConfig(),
			ContentID: "c1",
		}
	}
	a, _ := build().RunPlayback(context.Background())
	b, _ := build().RunPlayback(context.Background())
	if a.StartupLatency != b.StartupLatency || a.AvgBitrateKbps != b.AvgBitrateKbps {
		t.Fatalf("non-deterministic playback: %+v vs %+v", a, b)
	}
}

func TestPlaybackUnderLoss(t *testing.T) {
	profile := transport.NetworkProfile{
		BaseRTTMs:     150,
		BandwidthMbps: 3,
		LossModel: transport.LossModel{
			Type: "gilbert_elliott", PGoodToBad: 0.05, PBadToGood: 0.2, LossInBadState: 0.6,
		},
	}
	rng := rand.New(rand.NewSource(1))
	m := GenerateManifest("c1", 20*time.Second, 4*time.Second, DefaultRepresentations(), rng)
	sess := &PlaybackSession{
		Manifest:  m,
		ABR:       NewBufferBasedABR(),
		Transport: modeled.NewModeledTCPTransport(profile, rand.New(rand.NewSource(2))),
		Cache:     cache.NewLRUCache(100_000_000),
		Profile:   profile,
		RNG:       rand.New(rand.NewSource(3)),
		Config:    DefaultPlaybackConfig(),
		ContentID: "c1",
	}
	r, err := sess.RunPlayback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.AvgBitrateKbps >= float64(m.HighestBitrate()) {
		t.Fatalf("under loss, avg bitrate should drop below highest: %v", r.AvgBitrateKbps)
	}
}
