// Package transport defines the core transport abstraction used by the CDN
// simulator. All CDN logic (cache, shield, player, routing) talks to transports
// exclusively through the Transport interface, so swapping modeled transports
// for emulated or real ones in later phases does not require touching callers.
package transport

import (
	"context"
	"time"
)

// SegmentRequest represents a video segment fetch.
type SegmentRequest struct {
	SegmentID   string
	BitrateKbps int
	SizeBytes   int64
}

// SegmentResponse contains delivery measurement for one segment.
type SegmentResponse struct {
	SegmentID       string
	BytesReceived   int64
	TTFB            time.Duration // time to first byte
	TotalLatency    time.Duration // complete transfer time
	GoodputMbps     float64       // effective throughput
	Used0RTT        bool
	RetransmitCount int
	HOLBlockEvents  int
	Protocol        string // "tcp-h2", "quic-h3"
	// CPUTime is the (approximate, process-wide) delta in user+system CPU
	// time consumed during the fetch. Populated by the emulated transports
	// in Phase 2; modeled transports leave it zero. It enables cost-benefit
	// analysis comparing latency wins against CPU spend (QUIC userspace vs
	// kernel TCP).
	CPUTime time.Duration
}

// NetworkProfile defines link characteristics between two endpoints.
type NetworkProfile struct {
	BaseRTTMs     float64   `yaml:"base_rtt_ms"`
	BandwidthMbps float64   `yaml:"bandwidth_mbps"`
	LossModel     LossModel `yaml:"loss_model"`
	JitterMs      float64   `yaml:"jitter_ms"`
}

// LossModel supports none, uniform, and Gilbert-Elliott bursty loss.
type LossModel struct {
	Type           string  `yaml:"type"` // "none", "uniform", "gilbert_elliott"
	UniformPercent float64 `yaml:"uniform_percent,omitempty"`
	// Gilbert-Elliott parameters
	PGoodToBad     float64 `yaml:"p_good_to_bad,omitempty"`
	PBadToGood     float64 `yaml:"p_bad_to_good,omitempty"`
	LossInBadState float64 `yaml:"loss_in_bad_state,omitempty"`
}

// Transport is the core abstraction. CDN logic ONLY talks through this.
type Transport interface {
	// FetchSegment fetches one video segment, measuring delivery.
	FetchSegment(ctx context.Context, req SegmentRequest) (SegmentResponse, error)

	// FetchConcurrent fetches multiple segments with multiplexing behavior.
	// TCP/H2: multiplexed but HOL-blocked at TCP layer.
	// QUIC/H3: independent streams, no cross-stream HOL blocking.
	FetchConcurrent(ctx context.Context, reqs []SegmentRequest) ([]SegmentResponse, error)

	// Handshake models connection establishment.
	// TCP+TLS1.3: 2 RTT (full) or ~1.5 RTT (resumption + TLS 0-RTT data, but TCP SYN still needed).
	// QUIC: 1 RTT (full) or 0 RTT (resumption).
	Handshake(ctx context.Context, resumption bool) (time.Duration, error)

	// Protocol returns identifier: "tcp-h2" or "quic-h3".
	Protocol() string
}
