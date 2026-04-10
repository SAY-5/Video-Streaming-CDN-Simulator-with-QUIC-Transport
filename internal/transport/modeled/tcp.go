package modeled

import (
	"context"
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/cdn-sim/cdn-sim/internal/transport"
)

// mtuPayload is the MTU (1500) minus IP+TCP headers (40 bytes).
const mtuPayload int64 = 1460

// ModeledTCPTransport is an HTTP/2-over-TCP transport model. It captures the
// head-of-line (HOL) blocking behavior that characterises TCP under loss:
// a single lost packet stalls ALL concurrent HTTP/2 streams sharing that
// connection for a retransmit round trip.
type ModeledTCPTransport struct {
	profile   transport.NetworkProfile
	rng       *rand.Rand
	loss      LossSimulator
	bandwidth *BandwidthTrace // optional time-varying bandwidth
	wallClock time.Duration   // accumulated time for bandwidth trace lookup
}

// NewModeledTCPTransport constructs a modeled TCP/H2 transport. rng must be
// non-nil; the caller owns the seed for reproducibility.
//
// Internally, the loss simulator is given its own child rng (derived from
// the first draw of the parent) so that loss draws are isolated from
// jitter/handshake draws. This ensures that two transports (TCP and QUIC)
// constructed from the same seed see the EXACT same loss sequence even
// though their handshake code consumes a different number of jitter draws.
// This is critical for a fair modeled comparison: the only variable
// between protocols should be the transport's response to loss (HOL
// blocking vs per-stream recovery), not the loss pattern itself.
func NewModeledTCPTransport(profile transport.NetworkProfile, rng *rand.Rand) *ModeledTCPTransport {
	lossSeed := rng.Int63()
	return &ModeledTCPTransport{
		profile: profile,
		rng:     rng,
		loss:    NewLossSimulator(profile.LossModel, rand.New(rand.NewSource(lossSeed))),
	}
}

// WithBandwidthTrace attaches a time-varying bandwidth trace.
func (m *ModeledTCPTransport) WithBandwidthTrace(bt *BandwidthTrace) *ModeledTCPTransport {
	m.bandwidth = bt
	return m
}

// Protocol returns "tcp-h2".
func (m *ModeledTCPTransport) Protocol() string { return "tcp-h2" }

// Handshake models TCP + TLS 1.3 establishment.
//
//	Full:        TCP SYN-ACK (1 RTT) + TLS 1.3 (1 RTT)   = 2 RTT
//	Resumption:  TCP SYN-ACK (1 RTT) + TLS 1.3 0-RTT     = ~1.5 RTT
func (m *ModeledTCPTransport) Handshake(ctx context.Context, resumption bool) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	rtt := rttDuration(m.profile.BaseRTTMs)
	var total time.Duration
	if resumption {
		total = rtt + rtt/2
	} else {
		total = 2 * rtt
	}
	total += sampleJitter(m.profile.JitterMs, m.rng)
	return total, nil
}

// bwAt returns the bandwidth in Mbps at the given wall-clock offset.
func (m *ModeledTCPTransport) bwAt(t time.Duration) float64 {
	if m.bandwidth != nil {
		return m.bandwidth.BandwidthAt(t)
	}
	return m.profile.BandwidthMbps
}

// computeDelivery models a single-stream fetch and returns (total latency,
// TTFB, retransmit count). This is the core delivery model shared by both
// FetchSegment and QUIC's per-stream logic.
func (m *ModeledTCPTransport) computeDelivery(sizeBytes int64) (time.Duration, time.Duration, int) {
	if sizeBytes <= 0 {
		return 0, 0, 0
	}
	numPackets := int(math.Ceil(float64(sizeBytes) / float64(mtuPayload)))
	losses := m.loss.SimulatePackets(numPackets)
	// Convert lost packet indices to byte offsets.
	lossOffsets := make([]int64, len(losses))
	for i, idx := range losses {
		lossOffsets[i] = int64(idx) * mtuPayload
	}
	sort.Slice(lossOffsets, func(i, j int) bool { return lossOffsets[i] < lossOffsets[j] })

	rtt := rttDuration(m.profile.BaseRTTMs)
	cc := NewCongestionController(rtt)
	cwndTime := cc.TransferTime(sizeBytes, lossOffsets)

	// Also bound by bandwidth: bandwidth * time >= bytes*8.
	bwMbps := m.bwAt(m.wallClock)
	if bwMbps <= 0 {
		bwMbps = 1 // guard
	}
	bwTime := time.Duration(float64(sizeBytes*8) / (bwMbps * 1_000_000) * float64(time.Second))

	// The fetch is bounded by whichever is slower.
	transferTime := cwndTime
	if bwTime > transferTime {
		transferTime = bwTime
	}

	// TTFB: half RTT (request) + small server processing + jitter.
	ttfb := rtt/2 + sampleJitter(m.profile.JitterMs, m.rng)

	// Total latency: request up, processing, transfer time, plus jitter across the transfer.
	jitterTotal := sampleJitter(m.profile.JitterMs, m.rng)
	total := ttfb + transferTime + jitterTotal
	return total, ttfb, len(losses)
}

// FetchSegment implements transport.Transport.
func (m *ModeledTCPTransport) FetchSegment(ctx context.Context, req transport.SegmentRequest) (transport.SegmentResponse, error) {
	if err := ctx.Err(); err != nil {
		return transport.SegmentResponse{}, err
	}
	total, ttfb, retrans := m.computeDelivery(req.SizeBytes)
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
		RetransmitCount: retrans,
		Protocol:        m.Protocol(),
	}, nil
}

// FetchConcurrent models HTTP/2 over TCP: all streams share one TCP
// connection. TCP delivers bytes in strict sequence-number order across
// one bytestream; a gap (lost packet) anywhere blocks delivery to the
// application for EVERYTHING after it, including bytes from unrelated
// HTTP/2 streams. This is the head-of-line blocking phenomenon.
//
// Correctness model (following review R1):
//
//	TCP: every stream waits for the ENTIRE connection to complete.
//	  Bytes are multiplexed by HTTP/2 framing but delivered in TCP order.
//	  A loss at any point stalls all streams until the gap fills.
//	  → stream[i].TotalLatency ≈ connTransferTime (same for all streams)
//
//	Contrast with QUIC (see quic.go FetchConcurrent):
//	  QUIC: each stream finishes at share × connTransferTime + per-stream
//	  loss penalty, because loss on stream A does NOT block stream B.
//
// This difference is what makes QUIC faster under loss for multiplexed
// workloads — not a per-event × RTT penalty formula, but the simple fact
// that TCP forces ALL streams to wait for the connection's slowest byte,
// while QUIC lets fast streams finish early.
func (m *ModeledTCPTransport) FetchConcurrent(ctx context.Context, reqs []transport.SegmentRequest) ([]transport.SegmentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(reqs) == 0 {
		return nil, nil
	}

	// Total wire bytes across all streams.
	var totalBytes int64
	for _, r := range reqs {
		totalBytes += r.SizeBytes
	}
	numPackets := int(math.Ceil(float64(totalBytes) / float64(mtuPayload)))
	losses := m.loss.SimulatePackets(numPackets)
	lossOffsets := make([]int64, len(losses))
	for i, idx := range losses {
		lossOffsets[i] = int64(idx) * mtuPayload
	}
	sort.Slice(lossOffsets, func(i, j int) bool { return lossOffsets[i] < lossOffsets[j] })

	rtt := rttDuration(m.profile.BaseRTTMs)
	cc := NewCongestionController(rtt)
	connTransferTime := cc.TransferTime(totalBytes, lossOffsets)

	bwMbps := m.bwAt(m.wallClock)
	if bwMbps <= 0 {
		bwMbps = 1
	}
	bwTime := time.Duration(float64(totalBytes*8) / (bwMbps * 1_000_000) * float64(time.Second))
	if bwTime > connTransferTime {
		connTransferTime = bwTime
	}

	// HOL block events: each loss event on the TCP connection blocks every
	// stream simultaneously.
	holEvents := len(lossOffsets)

	// TCP HOL blocking model:
	//
	//   NO LOSS: bytes are delivered in-order with no gaps. Streams are
	//   interleaved by HTTP/2 framing, so each stream's last byte arrives
	//   at approximately share × connTransferTime. No HOL blocking because
	//   there's nothing to block on. This matches QUIC's behavior.
	//
	//   ANY LOSS: a gap at byte offset X blocks delivery of everything
	//   after X to the application layer until the retransmit fills the
	//   gap (1 RTT). Since streams are interleaved, a gap anywhere in the
	//   connection affects bytes from ALL streams that fall after X. For
	//   randomly-positioned losses with interleaved streams, the expected
	//   completion time for every stream converges to connTransferTime
	//   (the full connection time including recovery RTTs).
	//
	// This binary model (proportional vs full-connection) captures the
	// essential physics. It is a simplification: in reality a loss very
	// late in the transfer only blocks a small fraction of remaining bytes,
	// so the true penalty is somewhere between proportional and full. The
	// binary model is conservative (slightly pessimistic for TCP under
	// sparse loss) which is acceptable for a "how much does QUIC help?"
	// simulator — it overstates QUIC's advantage at 1-2% loss by a small
	// amount but gets the direction and magnitude right at 3%+.
	hasLoss := len(lossOffsets) > 0
	out := make([]transport.SegmentResponse, len(reqs))
	for i, r := range reqs {
		ttfb := rtt/2 + sampleJitter(m.profile.JitterMs, m.rng)
		var streamTime time.Duration
		if hasLoss {
			// HOL blocking: all streams wait for the full connection.
			streamTime = connTransferTime
		} else {
			// No loss: streams complete proportionally (no blocking).
			share := float64(r.SizeBytes) / float64(totalBytes)
			streamTime = time.Duration(float64(connTransferTime) * share)
		}
		total := ttfb + streamTime + sampleJitter(m.profile.JitterMs, m.rng)
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
			RetransmitCount: holEvents,
			HOLBlockEvents:  holEvents,
			Protocol:        m.Protocol(),
		}
	}
	m.wallClock += connTransferTime
	return out, nil
}
