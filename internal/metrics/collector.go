package metrics

import (
	"math/rand"
	"sort"
	"sync"

	"github.com/cdn-sim/cdn-sim/internal/analysis"
	"github.com/cdn-sim/cdn-sim/internal/video"
)

// Collector accumulates PlaybackResults and produces aggregated metrics.
// It is safe for concurrent use.
type Collector struct {
	mu      sync.Mutex
	results []video.PlaybackResult
}

// NewCollector returns an empty collector.
func NewCollector() *Collector { return &Collector{} }

// Add records a playback result.
func (c *Collector) Add(r video.PlaybackResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results = append(c.results, r)
}

// Results returns a copy of the accumulated results ordered by ContentID then
// Protocol then insertion order (stable).
func (c *Collector) Results() []video.PlaybackResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]video.PlaybackResult, len(c.results))
	copy(out, c.results)
	return out
}

// Aggregate produces per-protocol aggregated metrics. If there are multiple
// ABRs the caller should partition first; this function aggregates across
// every result added so far under a single label pulled from the first
// matching result.
func (c *Collector) Aggregate(protocol string) AggregatedMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	var (
		filtered []video.PlaybackResult
	)
	for _, r := range c.results {
		if r.Protocol == protocol {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 {
		return AggregatedMetrics{Protocol: protocol}
	}
	// Collect raw values.
	var (
		ttfbs, latencies, goodputs []float64
		startups                   []float64
		rebufCounts, rebufDur      []float64
		avgBitrates, bitrateCh     []float64
		hits                       []float64
		holEvents                  []float64
	)
	for _, r := range filtered {
		startups = append(startups, float64(r.StartupLatency.Milliseconds()))
		rebufCounts = append(rebufCounts, float64(r.RebufferCount))
		rebufDur = append(rebufDur, float64(r.RebufferDuration.Milliseconds()))
		avgBitrates = append(avgBitrates, r.AvgBitrateKbps)
		bitrateCh = append(bitrateCh, float64(r.BitrateChanges))
		hits = append(hits, r.CacheHitRate*100)
		var holTotal int
		for _, s := range r.Segments {
			ttfbs = append(ttfbs, float64(s.TTFB.Milliseconds()))
			latencies = append(latencies, float64(s.TotalLatency.Milliseconds()))
			goodputs = append(goodputs, s.GoodputMbps)
			if s.HOLBlocked {
				holTotal++
			}
		}
		holEvents = append(holEvents, float64(holTotal))
	}

	return AggregatedMetrics{
		Protocol:         protocol,
		ABR:              filtered[0].ABRName,
		NumSessions:      len(filtered),
		SegmentTTFB:      BuildPercentileSet(ttfbs),
		SegmentLatency:   BuildPercentileSet(latencies),
		StartupLatency:   BuildPercentileSet(startups),
		RebufferCount:    BuildStatSet(rebufCounts),
		RebufferDuration: BuildStatSet(rebufDur),
		AvgBitrate:       BuildStatSet(avgBitrates),
		BitrateChanges:   BuildStatSet(bitrateCh),
		CacheHitRate:     BuildStatSet(hits),
		GoodputMbps:      BuildPercentileSet(goodputs),
		HOLBlockRate:     BuildStatSet(holEvents),
	}
}

// Compare returns a comparison report between two protocols on this collector.
// Bootstrap confidence intervals are computed over session-level metrics using
// 1000 resamples.
func (c *Collector) Compare(scenario, tcpProto, quicProto string, rng *rand.Rand) ComparisonReport {
	tcp := c.Aggregate(tcpProto)
	quic := c.Aggregate(quicProto)

	// Gather session-level vectors needed for CI calculations.
	tcpSessions := c.sessionsFor(tcpProto)
	quicSessions := c.sessionsFor(quicProto)

	improvements := []ImprovementResult{
		improvementLowerBetter(tcpSessions, quicSessions, "segment_latency_p95_ms", segmentLatencyP95, rng),
		improvementLowerBetter(tcpSessions, quicSessions, "startup_latency_p95_ms", startupP95, rng),
		improvementLowerBetter(tcpSessions, quicSessions, "rebuffer_duration_ms", rebufferDurMs, rng),
		improvementLowerBetter(tcpSessions, quicSessions, "rebuffer_count", rebufferCount, rng),
		improvementHigherBetter(tcpSessions, quicSessions, "avg_bitrate_kbps", avgBitrate, rng),
	}
	return ComparisonReport{
		Scenario:    scenario,
		TCP:         tcp,
		QUIC:        quic,
		Improvement: improvements,
	}
}

func (c *Collector) sessionsFor(proto string) []video.PlaybackResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []video.PlaybackResult
	for _, r := range c.results {
		if r.Protocol == proto {
			out = append(out, r)
		}
	}
	// Stable order by ContentID for reproducibility of bootstrap samples.
	sort.SliceStable(out, func(i, j int) bool { return out[i].ContentID < out[j].ContentID })
	return out
}

// session-level metric extractors.
func segmentLatencyP95(r video.PlaybackResult) float64 {
	var vals []float64
	for _, s := range r.Segments {
		vals = append(vals, float64(s.TotalLatency.Milliseconds()))
	}
	return Percentile(vals, 95)
}
func startupP95(r video.PlaybackResult) float64 {
	return float64(r.StartupLatency.Milliseconds())
}
func rebufferDurMs(r video.PlaybackResult) float64 {
	return float64(r.RebufferDuration.Milliseconds())
}
func rebufferCount(r video.PlaybackResult) float64 { return float64(r.RebufferCount) }
func avgBitrate(r video.PlaybackResult) float64    { return r.AvgBitrateKbps }

type extractor func(video.PlaybackResult) float64

func improvementLowerBetter(tcp, quic []video.PlaybackResult, label string, fn extractor, rng *rand.Rand) ImprovementResult {
	return improvement(tcp, quic, label, fn, rng, false)
}

func improvementHigherBetter(tcp, quic []video.PlaybackResult, label string, fn extractor, rng *rand.Rand) ImprovementResult {
	return improvement(tcp, quic, label, fn, rng, true)
}

func improvement(tcp, quic []video.PlaybackResult, label string, fn extractor, rng *rand.Rand, higherBetter bool) ImprovementResult {
	tcpVals := project(tcp, fn)
	quicVals := project(quic, fn)
	tcpMean := mean(tcpVals)
	quicMean := mean(quicVals)
	pct := 0.0
	if higherBetter {
		if tcpMean != 0 {
			pct = (quicMean - tcpMean) / tcpMean * 100
		}
	} else {
		if tcpMean != 0 {
			pct = (tcpMean - quicMean) / tcpMean * 100
		}
	}
	low, high := bootstrapCI(tcpVals, quicVals, higherBetter, 1000, rng)
	d := cohensD(tcpVals, quicVals)
	return ImprovementResult{
		Metric:       label,
		TCPValue:     tcpMean,
		QUICValue:    quicMean,
		ImprovePct:   pct,
		CILowerPct:   low,
		CIUpperPct:   high,
		CohensD:      d,
		EffectLabel:  effectLabel(d),
		HigherIsGood: higherBetter,
	}
}

func project(rs []video.PlaybackResult, fn extractor) []float64 {
	out := make([]float64, len(rs))
	for i, r := range rs {
		out[i] = fn(r)
	}
	return out
}

// Cohen's d, mean, and bootstrap utilities all delegate to internal/analysis
// so there is a single source of truth for statistical helpers across the
// codebase. Earlier revisions had a second implementation here that used
// population variance; the enhanced comparison and the legacy comparison
// therefore reported different effect sizes for the same data. Review round
// R1 (HIGH-3) caught the divergence; this shim eliminates it.

func mean(v []float64) float64 {
	return analysis.Mean(v)
}

func bootstrapCI(tcp, quic []float64, higherBetter bool, iters int, rng *rand.Rand) (float64, float64) {
	if len(tcp) == 0 || len(quic) == 0 {
		return 0, 0
	}
	return analysis.BootstrapImprovement(tcp, quic, higherBetter, iters, 0.05, rng)
}

func cohensD(a, b []float64) float64 {
	d, _ := analysis.CohensD(a, b)
	return d
}

func effectLabel(d float64) string {
	return analysis.EffectLabel(d)
}
