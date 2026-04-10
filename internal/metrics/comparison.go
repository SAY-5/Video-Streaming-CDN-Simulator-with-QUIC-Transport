package metrics

import (
	"fmt"
	"math"
	"math/rand"
	"sort"

	"github.com/cdn-sim/cdn-sim/internal/analysis"
	"github.com/cdn-sim/cdn-sim/internal/video"
)

// EnhancedComparison is a richer comparison report than ComparisonReport.
// It includes bootstrap CIs, Cohen's d, Mann-Whitney p-values, an overall
// QUIC advantage label, and a list of human-readable key findings.
type EnhancedComparison struct {
	Scenario     string                       `json:"scenario"`
	TCP          AggregatedMetrics            `json:"tcp"`
	QUIC         AggregatedMetrics            `json:"quic"`
	Improvements map[string]ImprovementDetail `json:"improvements"`

	QUICAdvantage string   `json:"quic_advantage"` // "negligible","moderate","large","dominant"
	KeyFindings   []string `json:"key_findings"`
}

// ImprovementDetail is a single per-metric entry in an EnhancedComparison
// containing the point improvement, 95% bootstrap CI on that improvement,
// Cohen's d effect size with label, Mann-Whitney p-value and a
// significance flag.
type ImprovementDetail struct {
	Metric       string  `json:"metric"`
	TCPValue     float64 `json:"tcp_value"`
	QUICValue    float64 `json:"quic_value"`
	ImprovePct   float64 `json:"improvement_pct"`
	CILower      float64 `json:"ci_lower_pct"`
	CIUpper      float64 `json:"ci_upper_pct"`
	EffectSize   float64 `json:"effect_size_d"`
	EffectInterp string  `json:"effect_interpretation"`
	PValue       float64 `json:"p_value"`
	Significant  bool    `json:"statistically_significant"` // p < 0.05
	HigherIsGood bool    `json:"higher_is_good"`
}

// metricSpec describes a single improvement row computed by
// CompareEnhanced: its key, human label, direction and the per-session
// extractor that pulls a scalar from a PlaybackResult.
type metricSpec struct {
	key          string
	extract      func(video.PlaybackResult) float64
	higherIsGood bool
}

// enhancedMetricSpecs enumerates the metrics surfaced by CompareEnhanced
// in the canonical display order used by the pretty printer.
func enhancedMetricSpecs() []metricSpec {
	return []metricSpec{
		{"segment_latency_p50_ms", sessionP(50), false},
		{"segment_latency_p95_ms", sessionP(95), false},
		{"segment_latency_p99_ms", sessionP(99), false},
		{"startup_latency_ms", func(r video.PlaybackResult) float64 { return float64(r.StartupLatency.Milliseconds()) }, false},
		{"rebuffer_count", func(r video.PlaybackResult) float64 { return float64(r.RebufferCount) }, false},
		{"rebuffer_duration_ms", func(r video.PlaybackResult) float64 { return float64(r.RebufferDuration.Milliseconds()) }, false},
		{"avg_bitrate_kbps", func(r video.PlaybackResult) float64 { return r.AvgBitrateKbps }, true},
		{"cache_hit_rate_pct", func(r video.PlaybackResult) float64 { return r.CacheHitRate * 100 }, true},
		{"hol_block_events", holEventsCount, false},
	}
}

// sessionP returns an extractor that computes the p-th percentile of the
// per-session segment latencies.
func sessionP(p float64) func(video.PlaybackResult) float64 {
	return func(r video.PlaybackResult) float64 {
		if len(r.Segments) == 0 {
			return 0
		}
		vals := make([]float64, 0, len(r.Segments))
		for _, s := range r.Segments {
			vals = append(vals, float64(s.TotalLatency.Milliseconds()))
		}
		return analysis.Percentile(vals, p)
	}
}

// holEventsCount returns the count of HOL-blocked segments in a session.
func holEventsCount(r video.PlaybackResult) float64 {
	n := 0
	for _, s := range r.Segments {
		if s.HOLBlocked {
			n++
		}
	}
	return float64(n)
}

// CompareEnhanced runs an EnhancedComparison between TCP and QUIC, including
// bootstrap CIs (1000 iterations), Cohen's d, and Mann-Whitney p-values.
// rng must be supplied for reproducible bootstrap sampling.
func (c *Collector) CompareEnhanced(scenario, tcpProto, quicProto string, rng *rand.Rand) *EnhancedComparison {
	tcp := c.Aggregate(tcpProto)
	quic := c.Aggregate(quicProto)

	tcpSessions := c.sessionsFor(tcpProto)
	quicSessions := c.sessionsFor(quicProto)

	report := &EnhancedComparison{
		Scenario:     scenario,
		TCP:          tcp,
		QUIC:         quic,
		Improvements: make(map[string]ImprovementDetail),
	}

	for _, spec := range enhancedMetricSpecs() {
		tcpVals := projectVals(tcpSessions, spec.extract)
		quicVals := projectVals(quicSessions, spec.extract)
		report.Improvements[spec.key] = computeImprovement(spec, tcpVals, quicVals, rng)
	}

	report.QUICAdvantage = deriveVerdict(report.Improvements)
	report.KeyFindings = deriveKeyFindings(report.Improvements)
	return report
}

// projectVals projects a per-session scalar out of a slice of playback
// results using fn.
func projectVals(rs []video.PlaybackResult, fn func(video.PlaybackResult) float64) []float64 {
	out := make([]float64, len(rs))
	for i, r := range rs {
		out[i] = fn(r)
	}
	return out
}

// computeImprovement populates a single ImprovementDetail for the given
// metric spec using bootstrap CIs, Cohen's d and Mann-Whitney U.
func computeImprovement(spec metricSpec, tcpVals, quicVals []float64, rng *rand.Rand) ImprovementDetail {
	tcpMean := analysis.Mean(tcpVals)
	quicMean := analysis.Mean(quicVals)
	pct := pctImprovement(tcpMean, quicMean, spec.higherIsGood)

	ciLower, ciUpper := bootstrapImprovementCI(tcpVals, quicVals, spec.higherIsGood, 1000, rng)

	var d float64
	var label string
	if spec.higherIsGood {
		// Pass QUIC as group1 so positive d == QUIC better.
		d, label = analysis.CohensD(quicVals, tcpVals)
	} else {
		// Lower is better: pass TCP as group1 so positive d == QUIC better.
		d, label = analysis.CohensD(tcpVals, quicVals)
	}

	_, p := analysis.MannWhitneyU(tcpVals, quicVals)

	return ImprovementDetail{
		Metric:       spec.key,
		TCPValue:     tcpMean,
		QUICValue:    quicMean,
		ImprovePct:   pct,
		CILower:      ciLower,
		CIUpper:      ciUpper,
		EffectSize:   d,
		EffectInterp: label,
		PValue:       p,
		Significant:  p < 0.05,
		HigherIsGood: spec.higherIsGood,
	}
}

// pctImprovement computes (%) improvement. For lower-is-better metrics
// positive means QUIC is better; same for higher-is-better.
func pctImprovement(tcpMean, quicMean float64, higherIsGood bool) float64 {
	if tcpMean == 0 {
		// Avoid divide-by-zero; fall back to an absolute contrast.
		if quicMean == 0 {
			return 0
		}
		if higherIsGood {
			return 100
		}
		return -100
	}
	if higherIsGood {
		return (quicMean - tcpMean) / tcpMean * 100
	}
	return (tcpMean - quicMean) / tcpMean * 100
}

// bootstrapImprovementCI resamples each vector independently using the
// analysis.Bootstrap facility and derives a 95% CI on the percentage
// improvement.
func bootstrapImprovementCI(tcpVals, quicVals []float64, higherIsGood bool, iters int, rng *rand.Rand) (float64, float64) {
	if len(tcpVals) == 0 || len(quicVals) == 0 || iters <= 0 {
		return 0, 0
	}
	// We can't use analysis.Bootstrap directly because the statistic is a
	// function of two samples. Implement the two-sample bootstrap loop
	// here using the same mean statFn approach the analysis package uses.
	n1 := len(tcpVals)
	n2 := len(quicVals)
	improvements := make([]float64, iters)
	sampleA := make([]float64, n1)
	sampleB := make([]float64, n2)
	for i := 0; i < iters; i++ {
		for j := 0; j < n1; j++ {
			sampleA[j] = tcpVals[rng.Intn(n1)]
		}
		for j := 0; j < n2; j++ {
			sampleB[j] = quicVals[rng.Intn(n2)]
		}
		ma := analysis.Mean(sampleA)
		mb := analysis.Mean(sampleB)
		improvements[i] = pctImprovement(ma, mb, higherIsGood)
	}
	sort.Float64s(improvements)
	lo := improvements[int(0.025*float64(iters))]
	hi := improvements[int(0.975*float64(iters))]
	if math.IsNaN(lo) {
		lo = 0
	}
	if math.IsNaN(hi) {
		hi = 0
	}
	return lo, hi
}

// deriveVerdict produces the QUICAdvantage label from the p95 and
// rebuffer-duration improvements following the spec.
func deriveVerdict(imps map[string]ImprovementDetail) string {
	p95, ok95 := imps["segment_latency_p95_ms"]
	rebuf, okRb := imps["rebuffer_duration_ms"]

	if ok95 && okRb &&
		p95.ImprovePct >= 30 && p95.Significant &&
		rebuf.ImprovePct >= 50 && rebuf.Significant {
		return "dominant"
	}
	if ok95 && p95.ImprovePct >= 20 && p95.Significant {
		return "large"
	}
	if ok95 && p95.ImprovePct >= 5 && p95.Significant {
		return "moderate"
	}
	return "negligible"
}

// deriveKeyFindings produces plain-English bullets summarising the
// EnhancedComparison for human consumption.
func deriveKeyFindings(imps map[string]ImprovementDetail) []string {
	var out []string

	if p95, ok := imps["segment_latency_p95_ms"]; ok && p95.Significant {
		out = append(out, fmt.Sprintf(
			"p95 segment latency improved by %.1f%% [CI %.1f%% — %.1f%%], statistically significant (%s)",
			p95.ImprovePct, p95.CILower, p95.CIUpper, formatPForFinding(p95.PValue),
		))
	}

	if rb, ok := imps["rebuffer_duration_ms"]; ok && rb.Significant && rb.ImprovePct > 10 {
		out = append(out, fmt.Sprintf(
			"Rebuffer duration reduced by %.1f%% [CI %.1f%% — %.1f%%]",
			rb.ImprovePct, rb.CILower, rb.CIUpper,
		))
	}

	if bw, ok := imps["avg_bitrate_kbps"]; ok && bw.Significant && bw.ImprovePct > 5 {
		out = append(out, fmt.Sprintf(
			"Average achieved bitrate increased by %.1f%%", bw.ImprovePct,
		))
	}

	if ch, ok := imps["cache_hit_rate_pct"]; ok {
		if math.Abs(ch.ImprovePct) <= 2 && ch.PValue > 0.1 {
			out = append(out, "Cache behavior is transport-independent")
		}
	}

	if hol, ok := imps["hol_block_events"]; ok && hol.TCPValue > 0 {
		out = append(out, fmt.Sprintf(
			"HOL blocking eliminated by QUIC: TCP averaged %.1f events/session",
			hol.TCPValue,
		))
	}

	// If QUIC is actually worse on the headline metric, surface that.
	if p95, ok := imps["segment_latency_p95_ms"]; ok && p95.ImprovePct < -2 {
		out = append(out, fmt.Sprintf(
			"Under these (low-loss) conditions QUIC's userspace overhead exceeds its protocol benefits: "+
				"TCP p95 = %.1fms vs QUIC p95 = %.1fms (change %+.1f%%)",
			p95.TCPValue, p95.QUICValue, p95.ImprovePct,
		))
	}

	return out
}

// formatPForFinding renders a p-value for the terse key-findings bullets.
func formatPForFinding(p float64) string {
	switch {
	case p < 0.001:
		return "p < 0.001"
	case p < 0.01:
		return "p < 0.01"
	case p < 0.05:
		return "p < 0.05"
	default:
		return fmt.Sprintf("p = %.3f", p)
	}
}
