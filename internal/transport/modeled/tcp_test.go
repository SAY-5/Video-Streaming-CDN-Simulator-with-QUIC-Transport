package modeled

import (
	"context"
	"math/rand"
	"testing"

	"github.com/cdn-sim/cdn-sim/internal/transport"
)

func baselineProfile() transport.NetworkProfile {
	return transport.NetworkProfile{
		BaseRTTMs:     50,
		BandwidthMbps: 10,
		LossModel:     transport.LossModel{Type: "none"},
		JitterMs:      0,
	}
}

func TestTCPFetchSegmentZeroLoss(t *testing.T) {
	m := NewModeledTCPTransport(baselineProfile(), rand.New(rand.NewSource(1)))
	resp, err := m.FetchSegment(context.Background(), transport.SegmentRequest{
		SegmentID: "s1", BitrateKbps: 1000, SizeBytes: 125_000, // 1 Mbit
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TTFB >= resp.TotalLatency {
		t.Fatal("TTFB must be < TotalLatency")
	}
	if resp.GoodputMbps <= 0 {
		t.Fatal("goodput should be positive")
	}
	if resp.Protocol != "tcp-h2" {
		t.Fatalf("protocol=%s", resp.Protocol)
	}
}

func TestTCPHandshakeFullVsResumption(t *testing.T) {
	m := NewModeledTCPTransport(baselineProfile(), rand.New(rand.NewSource(1)))
	full, _ := m.Handshake(context.Background(), false)
	resume, _ := m.Handshake(context.Background(), true)
	if resume >= full {
		t.Fatalf("resumption %v should be faster than full %v", resume, full)
	}
}

func TestTCPFetchConcurrentHOLBlocking(t *testing.T) {
	// With bursty loss we expect HOL block events on concurrent fetch.
	profile := transport.NetworkProfile{
		BaseRTTMs:     50,
		BandwidthMbps: 10,
		LossModel: transport.LossModel{
			Type: "uniform", UniformPercent: 5,
		},
	}
	m := NewModeledTCPTransport(profile, rand.New(rand.NewSource(1)))
	reqs := []transport.SegmentRequest{
		{SegmentID: "a", SizeBytes: 200_000},
		{SegmentID: "b", SizeBytes: 200_000},
		{SegmentID: "c", SizeBytes: 200_000},
	}
	resp, err := m.FetchConcurrent(context.Background(), reqs)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range resp {
		if r.HOLBlockEvents == 0 {
			t.Fatalf("expected HOL block events on lossy TCP concurrent fetch: %+v", r)
		}
	}
}

func TestTCPFetchConcurrentNoLossNoHOL(t *testing.T) {
	m := NewModeledTCPTransport(baselineProfile(), rand.New(rand.NewSource(1)))
	reqs := []transport.SegmentRequest{
		{SegmentID: "a", SizeBytes: 100_000},
		{SegmentID: "b", SizeBytes: 100_000},
	}
	resp, _ := m.FetchConcurrent(context.Background(), reqs)
	for _, r := range resp {
		if r.HOLBlockEvents != 0 {
			t.Fatalf("unexpected HOL events without loss: %d", r.HOLBlockEvents)
		}
	}
}
