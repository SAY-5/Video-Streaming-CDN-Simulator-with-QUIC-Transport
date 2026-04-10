package video

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/cdn-sim/cdn-sim/internal/cache"
	"github.com/cdn-sim/cdn-sim/internal/cdn"
	"github.com/cdn-sim/cdn-sim/internal/transport"
)

// PlaybackConfig holds tunables for a playback session.
type PlaybackConfig struct {
	MaxBuffer        time.Duration // default 30s
	StartupThreshold time.Duration // playback begins when buffer reaches this; default 2s
	// PrefetchDepth is the number of segments the player pipelines in one
	// HTTP/2 or QUIC connection. Depth=1 is strict serial fetch. Depth>=2
	// uses Transport.FetchConcurrent and is the regime where TCP's
	// head-of-line blocking (and QUIC's per-stream recovery) becomes
	// observable. Default 1.
	PrefetchDepth int
}

// DefaultPlaybackConfig returns the default PlaybackConfig.
func DefaultPlaybackConfig() PlaybackConfig {
	return PlaybackConfig{MaxBuffer: 30 * time.Second, StartupThreshold: 2 * time.Second, PrefetchDepth: 1}
}

// SegmentResult captures the outcome of one segment fetch during playback.
type SegmentResult struct {
	Index        int
	BitrateKbps  int
	SizeBytes    int64
	TTFB         time.Duration
	TotalLatency time.Duration
	GoodputMbps  float64
	CacheHit     bool
	ShieldHit    bool
	HOLBlocked   bool
	BufferBefore time.Duration
	BufferAfter  time.Duration
	Rebuffered   bool
}

// PlaybackResult aggregates metrics for a single viewing session.
type PlaybackResult struct {
	ContentID        string
	Protocol         string
	ABRName          string
	StartupLatency   time.Duration
	TotalPlayTime    time.Duration
	RebufferCount    int
	RebufferDuration time.Duration
	AvgBitrateKbps   float64
	BitrateChanges   int
	Segments         []SegmentResult
	CacheHitRate     float64
}

// PlaybackSession drives a single viewer through a complete video.
type PlaybackSession struct {
	Manifest  Manifest
	ABR       ABRAlgorithm
	Transport transport.Transport // client -> edge transport
	Cache     cache.Cache         // edge cache
	Shield    *cdn.OriginShield   // optional mid-tier cache; may be nil
	Profile   transport.NetworkProfile
	RNG       *rand.Rand
	Config    PlaybackConfig
	// ContentID is embedded in cache keys so different clients share hits.
	ContentID string
}

// RunPlayback executes the session and returns aggregated metrics.
func (s *PlaybackSession) RunPlayback(ctx context.Context) (PlaybackResult, error) {
	if s.Config.MaxBuffer == 0 && s.Config.StartupThreshold == 0 {
		s.Config = DefaultPlaybackConfig()
	}
	if s.Config.PrefetchDepth <= 0 {
		s.Config.PrefetchDepth = 1
	}
	result := PlaybackResult{
		ContentID: s.ContentID,
		Protocol:  s.Transport.Protocol(),
		ABRName:   s.ABR.Name(),
	}

	hs, err := s.Transport.Handshake(ctx, false)
	if err != nil {
		return result, fmt.Errorf("handshake: %w", err)
	}
	wallClock := hs
	buffer := time.Duration(0)
	playbackStarted := false
	cacheHits := 0
	var history []ThroughputSample
	currentBitrate := 0

	i := 0
	for i < s.Manifest.TotalSegments {
		state := PlayerState{
			BufferLevel:        buffer,
			MaxBuffer:          s.Config.MaxBuffer,
			LastThroughputKbps: lastKbps(history),
			ThroughputHistory:  history,
			CurrentBitrateKbps: currentBitrate,
			SegmentIndex:       i,
			StartupComplete:    playbackStarted,
			RebufferCount:      result.RebufferCount,
		}
		dec := s.ABR.SelectBitrate(state, s.Manifest)
		rep := s.Manifest.RepresentationForBitrate(dec.BitrateKbps)

		// Determine the batch: [i, i+batchSize).
		batchSize := s.Config.PrefetchDepth
		if batchSize < 1 {
			batchSize = 1
		}
		if i+batchSize > s.Manifest.TotalSegments {
			batchSize = s.Manifest.TotalSegments - i
		}

		bufBefore := buffer
		now := wallClockToTime(wallClock)

		// Build request list and check cache.
		batchReqs := make([]transport.SegmentRequest, 0, batchSize)
		batchKeys := make([]string, batchSize)
		batchSizes := make([]int64, batchSize)
		batchHits := make([]bool, batchSize)
		batchShieldHits := make([]bool, batchSize)
		// missIndices[k] → the slot in batchReqs that corresponds to batch
		// slot k (only meaningful when batchHits[k] is false).
		missIndices := make([]int, batchSize)
		for k := 0; k < batchSize; k++ {
			idx := i + k
			size := rep.SegmentSizes[idx]
			key := SegmentID(s.ContentID, idx, rep.BitrateKbps)
			batchKeys[k] = key
			batchSizes[k] = size
			if s.Cache != nil {
				if _, ok := s.Cache.Get(key, now); ok {
					batchHits[k] = true
					cacheHits++
					continue
				}
			}
			missIndices[k] = len(batchReqs)
			batchReqs = append(batchReqs, transport.SegmentRequest{
				SegmentID:   key,
				BitrateKbps: rep.BitrateKbps,
				SizeBytes:   size,
			})
		}

		// Fetch any remaining (missed) segments concurrently.
		var missResponses []transport.SegmentResponse
		shieldHitsByReq := make([]bool, len(batchReqs))
		if len(batchReqs) > 0 {
			if s.Shield != nil {
				// Shield pipeline: every miss requests from shield, then edge.
				for j, req := range batchReqs {
					sResp, shHit, err := s.Shield.Fetch(ctx, req, now)
					if err != nil {
						return result, fmt.Errorf("shield fetch: %w", err)
					}
					shieldHitsByReq[j] = shHit
					_ = sResp
				}
				edgeResps, err := s.Transport.FetchConcurrent(ctx, batchReqs)
				if err != nil {
					return result, fmt.Errorf("edge batch: %w", err)
				}
				missResponses = edgeResps
			} else {
				resps, err := s.Transport.FetchConcurrent(ctx, batchReqs)
				if err != nil {
					return result, fmt.Errorf("transport batch: %w", err)
				}
				missResponses = resps
			}
			// Cache inserts.
			if s.Cache != nil {
				for _, req := range batchReqs {
					s.Cache.Put(cache.Item{
						Key:       req.SegmentID,
						SizeBytes: req.SizeBytes,
						Expiry:    now.Add(1 * time.Hour),
					}, now)
				}
			}
		}
		// Propagate shield hit/miss info back to batch slot indices.
		for k := 0; k < batchSize; k++ {
			if !batchHits[k] {
				batchShieldHits[k] = shieldHitsByReq[missIndices[k]]
			}
		}

		// Reassemble per-slot responses.
		missCursor := 0
		batchResps := make([]transport.SegmentResponse, batchSize)
		for k := 0; k < batchSize; k++ {
			if batchHits[k] {
				batchResps[k] = transport.SegmentResponse{
					SegmentID:     batchKeys[k],
					BytesReceived: batchSizes[k],
					TTFB:          time.Millisecond,
					TotalLatency:  time.Millisecond,
					GoodputMbps:   float64(batchSizes[k]*8) / 0.001 / 1_000_000,
					Protocol:      s.Transport.Protocol(),
				}
			} else {
				batchResps[k] = missResponses[missCursor]
				missCursor++
			}
		}

		// Batch latency: the full batch completes together when pipelined,
		// so wall-clock advances by the max response time.
		var maxLatency time.Duration
		for _, r := range batchResps {
			if r.TotalLatency > maxLatency {
				maxLatency = r.TotalLatency
			}
		}
		fetchTime := maxLatency
		wallClock += fetchTime

		rebuffered := false
		if playbackStarted {
			if fetchTime >= buffer {
				stall := fetchTime - buffer
				buffer = 0
				result.RebufferDuration += stall
				result.RebufferCount++
				wallClock += stall
				rebuffered = true
			} else {
				buffer -= fetchTime
			}
		}

		// The entire batch enters the buffer.
		batchDur := time.Duration(batchSize) * s.Manifest.SegmentDuration
		buffer += batchDur
		if buffer > s.Config.MaxBuffer {
			excess := buffer - s.Config.MaxBuffer
			buffer = s.Config.MaxBuffer
			wallClock += excess
		}

		if !playbackStarted && buffer >= s.Config.StartupThreshold {
			playbackStarted = true
			result.StartupLatency = wallClock
		}

		if currentBitrate != 0 && currentBitrate != rep.BitrateKbps {
			result.BitrateChanges++
		}
		currentBitrate = rep.BitrateKbps

		// Throughput sample: ONLY count bytes that actually crossed the
		// transport — cache hits return in ~1ms and including their bytes
		// inflates the estimate so the ABR algorithm overshoots.
		// (HIGH fix from review R1.)
		var missBytes int64
		for k, sz := range batchSizes {
			if !batchHits[k] {
				missBytes += sz
			}
		}
		if fetchTime > 0 && missBytes > 0 {
			kbps := float64(missBytes*8) / fetchTime.Seconds() / 1000
			history = append(history, ThroughputSample{Kbps: kbps, FetchTime: fetchTime})
			if len(history) > 20 {
				history = history[len(history)-20:]
			}
		}

		for k := 0; k < batchSize; k++ {
			resp := batchResps[k]
			result.Segments = append(result.Segments, SegmentResult{
				Index:        i + k,
				BitrateKbps:  rep.BitrateKbps,
				SizeBytes:    batchSizes[k],
				TTFB:         resp.TTFB,
				TotalLatency: resp.TotalLatency,
				GoodputMbps:  resp.GoodputMbps,
				CacheHit:     batchHits[k],
				ShieldHit:    batchShieldHits[k],
				HOLBlocked:   resp.HOLBlockEvents > 0,
				BufferBefore: bufBefore,
				BufferAfter:  buffer,
				// HIGH-5 fix: all segments in a rebuffered batch are
				// rebuffered, not just k==0. When the batch stalls,
				// every segment in that batch arrived under stall.
				Rebuffered: rebuffered,
			})
		}
		i += batchSize
	}

	if !playbackStarted && result.StartupLatency == 0 {
		result.StartupLatency = wallClock
	}
	result.TotalPlayTime = wallClock
	if len(result.Segments) > 0 {
		var sumBitrate float64
		for _, seg := range result.Segments {
			sumBitrate += float64(seg.BitrateKbps)
		}
		result.AvgBitrateKbps = sumBitrate / float64(len(result.Segments))
		result.CacheHitRate = float64(cacheHits) / float64(len(result.Segments))
	}
	return result, nil
}

func lastKbps(history []ThroughputSample) float64 {
	if len(history) == 0 {
		return 0
	}
	return history[len(history)-1].Kbps
}

// wallClockToTime converts a simulator-relative duration into a time.Time.
// We use a stable epoch so cache TTL comparisons work correctly regardless
// of wall-clock drift during testing.
var simEpoch = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

func wallClockToTime(wc time.Duration) time.Time { return simEpoch.Add(wc) }

// SimEpoch returns the deterministic simulator epoch. Use this from other
// packages (e.g. experiment warm-up, cdn/shield) to feed cache.Put / Get
// calls so that TTL semantics are reproducible and don't drift across
// machines or clocks.
func SimEpoch() time.Time { return simEpoch }
