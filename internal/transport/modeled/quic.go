package modeled

import (
	"context"
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/cdn-sim/cdn-sim/internal/transport"
)

// ModeledQUICTransport is an HTTP/3-over-QUIC transport model. It uses the
// same congestion control and packet loss as the TCP model, but loss recovery
// is per-stream: a lost packet belonging to stream A does NOT stall stream B.
// This is the primary advantage of QUIC for multiplexed workloads under loss.
type ModeledQUICTransport struct {
	profile            transport.NetworkProfile
	rng                *rand.Rand
	loss               LossSimulator
	zeroRTTSuccessRate float64
	bandwidth          *BandwidthTrace
	wallClock          time.Duration
}

// NewModeledQUICTransport constructs a modeled QUIC/H3 transport.
// zeroRTTRate is the probability that a resumption handshake succeeds as 0-RTT.
func NewModeledQUICTransport(profile transport.NetworkProfile, rng *rand.Rand, zeroRTTRate float64) *ModeledQUICTransport {
	if zeroRTTRate < 0 {
		zeroRTTRate = 0
	}
	if zeroRTTRate > 1 {
		zeroRTTRate = 1
	}
	// Derive a child rng for loss (same design as TCP — see tcp.go comment).
	lossSeed := rng.Int63()
	return &ModeledQUICTransport{
		profile:            profile,
		rng:                rng,
		loss:               NewLossSimulator(profile.LossModel, rand.New(rand.NewSource(lossSeed))),
		zeroRTTSuccessRate: zeroRTTRate,
	}
}

// WithBandwidthTrace attaches a time-varying bandwidth trace.
func (m *ModeledQUICTransport) WithBandwidthTrace(bt *BandwidthTrace) *ModeledQUICTransport {
	m.bandwidth = bt
	return m
}

// Protocol returns "quic-h3".
func (m *ModeledQUICTransport) Protocol() string { return "quic-h3" }

// Handshake models QUIC establishment.
//
//	Full:        1 RTT (combined crypto+transport).
//	Resumption:  0 RTT with probability zeroRTTSuccessRate, else 1 RTT.
func (m *ModeledQUICTransport) Handshake(ctx context.Context, resumption bool) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	rtt := rttDuration(m.profile.BaseRTTMs)
	var total time.Duration
	if resumption && m.rng.Float64() < m.zeroRTTSuccessRate {
		// 0-RTT: effectively free (data flows with the first flight).
		total = 0
	} else {
		total = rtt
	}
	total += sampleJitter(m.profile.JitterMs, m.rng)
	return total, nil
}

func (m *ModeledQUICTransport) bwAt(t time.Duration) float64 {
	if m.bandwidth != nil {
		return m.bandwidth.BandwidthAt(t)
	}
	return m.profile.BandwidthMbps
}

// FetchSegment fetches a single segment on its own stream. Behaviour is
// equivalent to TCP's single-stream path: QUIC and TCP share the same
// congestion control, so a single stream under loss is similar.
func (m *ModeledQUICTransport) FetchSegment(ctx context.Context, req transport.SegmentRequest) (transport.SegmentResponse, error) {
	if err := ctx.Err(); err != nil {
		return transport.SegmentResponse{}, err
	}
	if req.SizeBytes <= 0 {
		return transport.SegmentResponse{SegmentID: req.SegmentID, Protocol: m.Protocol()}, nil
	}
	numPackets := int(math.Ceil(float64(req.SizeBytes) / float64(mtuPayload)))
	losses := m.loss.SimulatePackets(numPackets)
	lossOffsets := make([]int64, len(losses))
	for i, idx := range losses {
		lossOffsets[i] = int64(idx) * mtuPayload
	}
	sort.Slice(lossOffsets, func(i, j int) bool { return lossOffsets[i] < lossOffsets[j] })
	rtt := rttDuration(m.profile.BaseRTTMs)
	cc := NewCongestionController(rtt)
	cwndTime := cc.TransferTime(req.SizeBytes, lossOffsets)
	bwMbps := m.bwAt(m.wallClock)
	if bwMbps <= 0 {
		bwMbps = 1
	}
	bwTime := time.Duration(float64(req.SizeBytes*8) / (bwMbps * 1_000_000) * float64(time.Second))
	transferTime := cwndTime
	if bwTime > transferTime {
		transferTime = bwTime
	}
	ttfb := rtt/2 + sampleJitter(m.profile.JitterMs, m.rng)
	total := ttfb + transferTime + sampleJitter(m.profile.JitterMs, m.rng)
	m.wallClock += total
	goodput := 0.0
	if total > 0 {
		goodput = float64(req.SizeBytes*8) / total.Seconds() / 1_000_000
	}
	return transport.SegmentResponse{
		SegmentID:       req.SegmentID,
		BytesReceived:   req.SizeBytes,
		TTFB:            ttfb,
		TotalLatency:    total,
		GoodputMbps:     goodput,
		RetransmitCount: len(losses),
		Protocol:        m.Protocol(),
	}, nil
}

// FetchConcurrent implements per-stream loss recovery.
//
// QUIC multiplexes all streams over a single shared congestion window (one
// UDP path, one cwnd). Real quic-go does NOT give each stream its own
// InitialCWND; that would massively overstate QUIC's initial burst relative
// to TCP. Instead:
//
//   1. Compute a single connection-level transfer time over total bytes
//      using one shared CongestionController (identical to TCP), driven by
//      the union of all per-stream losses.
//   2. Each stream's transfer-time share is proportional to its byte share
//      of the connection.
//   3. Per-stream loss recovery adds a per-stream fast-retransmit penalty
//      ONLY to the affected stream — this is what distinguishes QUIC from
//      TCP's HOL-blocked connection: a loss on stream A does not stall
//      stream B.
//
// HOLBlockEvents is always zero.
func (m *ModeledQUICTransport) FetchConcurrent(ctx context.Context, reqs []transport.SegmentRequest) ([]transport.SegmentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(reqs) == 0 {
		return nil, nil
	}

	// Total wire bytes.
	var totalBytes int64
	for _, r := range reqs {
		totalBytes += r.SizeBytes
	}
	rtt := rttDuration(m.profile.BaseRTTMs)

	// --- Per-stream loss simulation (used only for per-stream recovery
	// penalty, not for per-stream cwnd). ---
	perStreamLosses := make([]int, len(reqs))
	var allLossOffsets []int64
	var byteOffset int64
	for i, r := range reqs {
		if r.SizeBytes <= 0 {
			continue
		}
		numPackets := int(math.Ceil(float64(r.SizeBytes) / float64(mtuPayload)))
		losses := m.loss.SimulatePackets(numPackets)
		perStreamLosses[i] = len(losses)
		for _, idx := range losses {
			allLossOffsets = append(allLossOffsets, byteOffset+int64(idx)*mtuPayload)
		}
		byteOffset += r.SizeBytes
	}
	sort.Slice(allLossOffsets, func(a, b int) bool { return allLossOffsets[a] < allLossOffsets[b] })

	// --- Shared congestion window transfer time over the whole
	// connection. Matches TCP's modeling except loss recovery is
	// per-stream instead of HOL-blocking every stream. ---
	cc := NewCongestionController(rtt)
	connTransferTime := cc.TransferTime(totalBytes, allLossOffsets)

	bwMbps := m.bwAt(m.wallClock)
	if bwMbps <= 0 {
		bwMbps = 1
	}
	bwTime := time.Duration(float64(totalBytes*8) / (bwMbps * 1_000_000) * float64(time.Second))
	if bwTime > connTransferTime {
		connTransferTime = bwTime
	}

	out := make([]transport.SegmentResponse, len(reqs))
	maxEnd := time.Duration(0)
	for i, r := range reqs {
		if r.SizeBytes <= 0 {
			out[i] = transport.SegmentResponse{SegmentID: r.SegmentID, Protocol: m.Protocol()}
			continue
		}
		share := float64(r.SizeBytes) / float64(totalBytes)
		streamTime := time.Duration(float64(connTransferTime) * share)
		// Per-stream loss recovery: one extra RTT per loss that affected
		// this specific stream. Critically, this is NOT multiplied across
		// streams — QUIC's advantage is precisely that stream B does not
		// wait for stream A's retransmit.
		streamLossPenalty := time.Duration(perStreamLosses[i]) * rtt
		transferTime := streamTime + streamLossPenalty
		ttfb := rtt/2 + sampleJitter(m.profile.JitterMs, m.rng)
		total := ttfb + transferTime + sampleJitter(m.profile.JitterMs, m.rng)
		if total > maxEnd {
			maxEnd = total
		}
		goodput := 0.0
		if total > 0 {
			goodput = float64(r.SizeBytes*8) / total.Seconds() / 1_000_000
		}
		out[i] = transport.SegmentResponse{
			SegmentID:       r.SegmentID,
			BytesReceived:   r.SizeBytes,
			TTFB:            ttfb,
			TotalLatency:    total,
			GoodputMbps:     goodput,
			RetransmitCount: perStreamLosses[i],
			HOLBlockEvents:  0, // per-stream recovery
			Protocol:        m.Protocol(),
		}
	}
	m.wallClock += maxEnd
	return out, nil
}
