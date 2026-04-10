package modeled

import (
	"context"
	"math/rand"
	"testing"

	"github.com/cdn-sim/cdn-sim/internal/transport"
)

func TestQUICProtocol(t *testing.T) {
	q := NewModeledQUICTransport(baselineProfile(), rand.New(rand.NewSource(1)), 0.85)
	if q.Protocol() != "quic-h3" {
		t.Fatalf("got %s", q.Protocol())
	}
}

func TestQUICZeroRTTRate(t *testing.T) {
	n := 5000
	success := 0
	q := NewModeledQUICTransport(baselineProfile(), rand.New(rand.NewSource(1)), 0.85)
	for i := 0; i < n; i++ {
		d, _ := q.Handshake(context.Background(), true)
		if d == 0 {
			success++
		}
	}
	rate := float64(success) / float64(n)
	if rate < 0.82 || rate > 0.88 {
		t.Fatalf("0-RTT rate %.3f not within [0.82,0.88]", rate)
	}
}

func TestQUICConcurrentNoHOL(t *testing.T) {
	profile := transport.NetworkProfile{
		BaseRTTMs:     50,
		BandwidthMbps: 10,
		LossModel:     transport.LossModel{Type: "uniform", UniformPercent: 5},
	}
	q := NewModeledQUICTransport(profile, rand.New(rand.NewSource(1)), 0.85)
	reqs := []transport.SegmentRequest{
		{SegmentID: "a", SizeBytes: 200_000},
		{SegmentID: "b", SizeBytes: 200_000},
		{SegmentID: "c", SizeBytes: 200_000},
	}
	resp, err := q.FetchConcurrent(context.Background(), reqs)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range resp {
		if r.HOLBlockEvents != 0 {
			t.Fatalf("QUIC must never report HOLBlockEvents, got %d", r.HOLBlockEvents)
		}
	}
}

func TestQUICFasterThanTCPUnderLoss(t *testing.T) {
	profile := transport.NetworkProfile{
		BaseRTTMs:     100,
		BandwidthMbps: 10,
		LossModel: transport.LossModel{
			Type: "gilbert_elliott", PGoodToBad: 0.05, PBadToGood: 0.25, LossInBadState: 0.5,
		},
		JitterMs: 0,
	}
	reqs := []transport.SegmentRequest{
		{SegmentID: "a", SizeBytes: 200_000},
		{SegmentID: "b", SizeBytes: 200_000},
		{SegmentID: "c", SizeBytes: 200_000},
		{SegmentID: "d", SizeBytes: 200_000},
		{SegmentID: "e", SizeBytes: 200_000},
	}

	var tcpTotal, quicTotal int64
	runs := 20
	for i := 0; i < runs; i++ {
		seed := int64(i + 1)
		tcp := NewModeledTCPTransport(profile, rand.New(rand.NewSource(seed)))
		quic := NewModeledQUICTransport(profile, rand.New(rand.NewSource(seed)), 0.85)
		tResp, _ := tcp.FetchConcurrent(context.Background(), reqs)
		qResp, _ := quic.FetchConcurrent(context.Background(), reqs)
		for _, r := range tResp {
			tcpTotal += r.TotalLatency.Nanoseconds()
		}
		for _, r := range qResp {
			quicTotal += r.TotalLatency.Nanoseconds()
		}
	}
	if quicTotal >= tcpTotal {
		t.Fatalf("expected QUIC advantage under loss: tcp=%d ns, quic=%d ns", tcpTotal, quicTotal)
	}
}
