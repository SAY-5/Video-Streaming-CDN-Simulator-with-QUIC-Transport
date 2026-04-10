// Package cdn contains CDN composition primitives: origin shield, edge
// assembly, and other layers above the transport abstraction.
package cdn

import (
	"context"
	"fmt"
	"time"

	"github.com/cdn-sim/cdn-sim/internal/cache"
	"github.com/cdn-sim/cdn-sim/internal/transport"
)

// OriginShield is a regional mid-tier cache between edge and origin. Its job
// is to soak edge cache misses so the origin sees far less traffic. For
// latency purposes, a shield hit still pays the edge->shield RTT, which
// is a key reason QUIC's RTT savings compound across CDN tiers.
type OriginShield struct {
	Cache             cache.Cache
	Transport         transport.Transport // transport used to fetch from origin
	DefaultTTLSeconds int
}

// NewOriginShield constructs an origin shield with the given cache and
// transport-to-origin.
func NewOriginShield(c cache.Cache, t transport.Transport, ttlSec int) *OriginShield {
	return &OriginShield{Cache: c, Transport: t, DefaultTTLSeconds: ttlSec}
}

// Fetch resolves a segment request through the shield. It returns:
//
//	response: the segment delivery measurement (latency, goodput, etc.)
//	hit:      whether the shield cache served this request
//	error:    propagated from the origin transport on miss
//
// Note that the caller (edge) is responsible for modelling the client->edge
// leg. Fetch only covers the shield->origin behaviour plus shield lookup.
// Fetch takes the caller's simulated "now" so shield TTL bookkeeping uses
// the same clock as the player's edge cache (video.SimEpoch + wallClock).
// Passing a zero time defaults to time.Now() for backward compatibility
// with any future test-only caller.
func (s *OriginShield) Fetch(ctx context.Context, req transport.SegmentRequest, now time.Time) (transport.SegmentResponse, bool, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if s.Cache != nil {
		if _, ok := s.Cache.Get(req.SegmentID, now); ok {
			// Shield hit: no origin transport needed. Approximate a small
			// local fetch latency of 2 ms.
			return transport.SegmentResponse{
				SegmentID:     req.SegmentID,
				BytesReceived: req.SizeBytes,
				TTFB:          2 * time.Millisecond,
				TotalLatency:  2 * time.Millisecond,
				GoodputMbps:   0,
				Protocol:      "shield-hit",
			}, true, nil
		}
	}
	resp, err := s.Transport.FetchSegment(ctx, req)
	if err != nil {
		return resp, false, fmt.Errorf("shield origin fetch: %w", err)
	}
	if s.Cache != nil {
		ttl := time.Duration(s.DefaultTTLSeconds) * time.Second
		expiry := time.Time{}
		if ttl > 0 {
			expiry = now.Add(ttl)
		}
		s.Cache.Put(cache.Item{
			Key:       req.SegmentID,
			SizeBytes: req.SizeBytes,
			Expiry:    expiry,
		}, now)
	}
	return resp, false, nil
}
