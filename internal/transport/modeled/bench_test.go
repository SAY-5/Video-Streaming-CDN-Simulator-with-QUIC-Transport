package modeled

import (
	"context"
	"math/rand"
	"testing"

	"github.com/cdn-sim/cdn-sim/internal/transport"
)

func benchProfile() transport.NetworkProfile {
	return transport.NetworkProfile{
		BaseRTTMs:     50,
		BandwidthMbps: 100,
		LossModel: transport.LossModel{
			Type: "gilbert_elliott", PGoodToBad: 0.03, PBadToGood: 0.3, LossInBadState: 0.4,
		},
		JitterMs: 5,
	}
}

func BenchmarkTCPFetchSegment(b *testing.B) {
	t := NewModeledTCPTransport(benchProfile(), rand.New(rand.NewSource(1)))
	req := transport.SegmentRequest{SegmentID: "s1", BitrateKbps: 3000, SizeBytes: 1_500_000}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t.FetchSegment(ctx, req)
	}
}

func BenchmarkQUICFetchSegment(b *testing.B) {
	q := NewModeledQUICTransport(benchProfile(), rand.New(rand.NewSource(1)), 0.85)
	req := transport.SegmentRequest{SegmentID: "s1", BitrateKbps: 3000, SizeBytes: 1_500_000}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.FetchSegment(ctx, req)
	}
}

func BenchmarkTCPFetchConcurrent3(b *testing.B) {
	t := NewModeledTCPTransport(benchProfile(), rand.New(rand.NewSource(1)))
	reqs := []transport.SegmentRequest{
		{SegmentID: "a", SizeBytes: 500_000},
		{SegmentID: "b", SizeBytes: 500_000},
		{SegmentID: "c", SizeBytes: 500_000},
	}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t.FetchConcurrent(ctx, reqs)
	}
}

func BenchmarkGilbertElliott100Packets(b *testing.B) {
	cfg := transport.LossModel{
		Type: "gilbert_elliott", PGoodToBad: 0.03, PBadToGood: 0.3, LossInBadState: 0.4,
	}
	ge := NewGilbertElliott(cfg, rand.New(rand.NewSource(1)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ge.SimulatePackets(100)
	}
}
