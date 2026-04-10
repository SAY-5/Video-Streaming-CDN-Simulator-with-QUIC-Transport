package experiment

import (
	"math"

	"github.com/cdn-sim/cdn-sim/internal/metrics"
)

// ValidationResult is one row of the modeled-vs-emulated cross-validation
// report. DeviationPct is an absolute percentage of the emulated baseline,
// and WithinTolerance reflects whether the modeled value falls within the
// metric-specific tolerance band.
type ValidationResult struct {
	Metric          string  `json:"metric"`
	ModeledValue    float64 `json:"modeled"`
	EmulatedValue   float64 `json:"emulated"`
	DeviationPct    float64 `json:"deviation_pct"`
	Tolerance       float64 `json:"tolerance_pct"`
	WithinTolerance bool    `json:"within_tolerance"`
}

// CrossValidationReport summarises a full cross-validation run comparing
// modeled and emulated outputs for a single scenario. OverallOK is true
// if at least 80% of the compared metrics fall within their tolerances.
type CrossValidationReport struct {
	Scenario  string             `json:"scenario"`
	Results   []ValidationResult `json:"results"`
	PassRate  float64            `json:"pass_rate_pct"`
	OverallOK bool               `json:"overall_ok"`
}

// Tolerance bands (in percent) applied by CrossValidate. Tail latencies
// are noisier than medians, and session-level descriptive stats get an
// intermediate band.
const (
	tolP50Latency      = 20.0
	tolTailLatency     = 30.0
	tolSessionDescript = 25.0
)

// CrossValidate compares modeled vs emulated AggregatedMetrics for the same
// scenario. The tolerance policy is:
//
//   - p50 latency metrics: 20%
//   - p95/p99 latency metrics: 30% (tail latencies are noisier)
//   - Bitrate, rebuffer, cache-hit, goodput: 25%
//
// DeviationPct is computed as abs(modeled-emulated)/emulated*100, guarding
// against zero emulated values.
func CrossValidate(modeled, emulated *metrics.AggregatedMetrics) []ValidationResult {
	if modeled == nil || emulated == nil {
		return nil
	}
	type cmp struct {
		name     string
		modeled  float64
		emulated float64
		tol      float64
	}
	items := []cmp{
		{"segment_latency_p50_ms", modeled.SegmentLatency.P50, emulated.SegmentLatency.P50, tolP50Latency},
		{"segment_latency_p95_ms", modeled.SegmentLatency.P95, emulated.SegmentLatency.P95, tolTailLatency},
		{"segment_latency_p99_ms", modeled.SegmentLatency.P99, emulated.SegmentLatency.P99, tolTailLatency},
		{"startup_latency_p50_ms", modeled.StartupLatency.P50, emulated.StartupLatency.P50, tolP50Latency},
		{"startup_latency_p95_ms", modeled.StartupLatency.P95, emulated.StartupLatency.P95, tolTailLatency},
		{"startup_latency_p99_ms", modeled.StartupLatency.P99, emulated.StartupLatency.P99, tolTailLatency},
		{"rebuffer_count_mean", modeled.RebufferCount.Mean, emulated.RebufferCount.Mean, tolSessionDescript},
		{"rebuffer_duration_mean_ms", modeled.RebufferDuration.Mean, emulated.RebufferDuration.Mean, tolSessionDescript},
		{"avg_bitrate_mean_kbps", modeled.AvgBitrate.Mean, emulated.AvgBitrate.Mean, tolSessionDescript},
		{"cache_hit_rate_mean_pct", modeled.CacheHitRate.Mean, emulated.CacheHitRate.Mean, tolSessionDescript},
		{"goodput_mbps_p50", modeled.GoodputMbps.P50, emulated.GoodputMbps.P50, tolSessionDescript},
	}
	out := make([]ValidationResult, 0, len(items))
	for _, it := range items {
		dev := deviationPct(it.modeled, it.emulated)
		out = append(out, ValidationResult{
			Metric:          it.name,
			ModeledValue:    it.modeled,
			EmulatedValue:   it.emulated,
			DeviationPct:    dev,
			Tolerance:       it.tol,
			WithinTolerance: dev <= it.tol,
		})
	}
	return out
}

// deviationPct returns |m-e|/|e|*100, or 0 when both sides are zero, or
// 100 when emulated is zero but modeled is not (treated as maximally
// deviant so the caller flags the mismatch).
func deviationPct(modeled, emulated float64) float64 {
	if emulated == 0 {
		if modeled == 0 {
			return 0
		}
		return 100
	}
	return math.Abs(modeled-emulated) / math.Abs(emulated) * 100
}

// BuildCrossValidationReport runs CrossValidate and aggregates pass/fail
// statistics. OverallOK is true when at least 80% of metrics fall within
// their per-metric tolerance bands.
func BuildCrossValidationReport(scenario string, modeled, emulated *metrics.AggregatedMetrics) *CrossValidationReport {
	results := CrossValidate(modeled, emulated)
	passed := 0
	for _, r := range results {
		if r.WithinTolerance {
			passed++
		}
	}
	var passRate float64
	if len(results) > 0 {
		passRate = float64(passed) / float64(len(results)) * 100
	}
	return &CrossValidationReport{
		Scenario:  scenario,
		Results:   results,
		PassRate:  passRate,
		OverallOK: passRate >= 80.0,
	}
}
