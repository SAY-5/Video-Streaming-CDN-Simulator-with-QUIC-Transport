package experiment

import (
	"path/filepath"
	"testing"

	"github.com/cdn-sim/cdn-sim/internal/metrics"
)

// TestLoadEmulatedLossyConfig verifies that the configs/emulated_lossy.yaml
// scenario file loads and validates cleanly. This mirrors what the
// `cdnsim validate --config configs/emulated_lossy.yaml` CLI command does.
func TestLoadEmulatedLossyConfig(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "emulated_lossy.yaml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig(%s) failed: %v", path, err)
	}
	if cfg.Mode != "emulated" {
		t.Errorf("expected mode=emulated, got %q", cfg.Mode)
	}
	if cfg.Emulated == nil || len(cfg.Emulated.Edges) != 2 {
		t.Errorf("expected 2 emulated edges, got %+v", cfg.Emulated)
	}
	if cfg.Emulated.Edges[0].H2URL == "" || cfg.Emulated.Edges[0].H3URL == "" {
		t.Errorf("expected both h2_url and h3_url on edge 0")
	}
}

func buildAggregated(base float64) *metrics.AggregatedMetrics {
	return &metrics.AggregatedMetrics{
		Protocol:    "tcp-h2",
		NumSessions: 10,
		SegmentLatency: metrics.PercentileSet{
			P50: base, P95: base * 2, P99: base * 3,
		},
		StartupLatency: metrics.PercentileSet{
			P50: base * 5, P95: base * 8, P99: base * 10,
		},
		RebufferCount:    metrics.StatSet{Mean: 1.0},
		RebufferDuration: metrics.StatSet{Mean: 500},
		AvgBitrate:       metrics.StatSet{Mean: 4000},
		CacheHitRate:     metrics.StatSet{Mean: 75},
		GoodputMbps:      metrics.PercentileSet{P50: 20, P95: 40, P99: 60},
	}
}

func TestCrossValidateAllWithinTolerance(t *testing.T) {
	emulated := buildAggregated(100)
	// Scale modeled by 1.03 (within 5%).
	modeled := &metrics.AggregatedMetrics{
		Protocol:    "tcp-h2",
		NumSessions: 10,
		SegmentLatency: metrics.PercentileSet{
			P50: emulated.SegmentLatency.P50 * 1.03,
			P95: emulated.SegmentLatency.P95 * 1.03,
			P99: emulated.SegmentLatency.P99 * 1.03,
		},
		StartupLatency: metrics.PercentileSet{
			P50: emulated.StartupLatency.P50 * 0.97,
			P95: emulated.StartupLatency.P95 * 1.02,
			P99: emulated.StartupLatency.P99 * 1.04,
		},
		RebufferCount:    metrics.StatSet{Mean: emulated.RebufferCount.Mean * 1.02},
		RebufferDuration: metrics.StatSet{Mean: emulated.RebufferDuration.Mean * 0.98},
		AvgBitrate:       metrics.StatSet{Mean: emulated.AvgBitrate.Mean * 1.01},
		CacheHitRate:     metrics.StatSet{Mean: emulated.CacheHitRate.Mean * 1.03},
		GoodputMbps:      metrics.PercentileSet{P50: emulated.GoodputMbps.P50 * 0.97},
	}
	report := BuildCrossValidationReport("test", modeled, emulated)
	if !report.OverallOK {
		t.Fatalf("expected OverallOK=true, got false, pass_rate=%.1f", report.PassRate)
	}
	for _, r := range report.Results {
		if !r.WithinTolerance {
			t.Errorf("metric %s expected within tolerance, got dev=%.2f tol=%.2f", r.Metric, r.DeviationPct, r.Tolerance)
		}
	}
}

func TestCrossValidateLargeDeviation(t *testing.T) {
	emulated := buildAggregated(100)
	modeled := buildAggregated(100)
	// Blow out p95 segment latency by 50%.
	modeled.SegmentLatency.P95 = emulated.SegmentLatency.P95 * 1.5

	results := CrossValidate(modeled, emulated)
	var foundP95 bool
	for _, r := range results {
		if r.Metric == "segment_latency_p95_ms" {
			foundP95 = true
			if r.WithinTolerance {
				t.Errorf("expected p95 out of tolerance, got dev=%.2f tol=%.2f", r.DeviationPct, r.Tolerance)
			}
		} else if !r.WithinTolerance {
			t.Errorf("metric %s expected within tolerance, got dev=%.2f", r.Metric, r.DeviationPct)
		}
	}
	if !foundP95 {
		t.Fatalf("segment_latency_p95_ms not found in results")
	}
}

func makeComparisonWithImprovement(label string, pct float64) *metrics.ComparisonReport {
	return &metrics.ComparisonReport{
		Scenario: "test",
		Improvement: []metrics.ImprovementResult{
			{Metric: label, ImprovePct: pct},
		},
	}
}

func TestValidateAgainstReferencePass(t *testing.T) {
	// 30% improvement is inside the moderate_loss p95_segment_latency band [18,40].
	cmp := makeComparisonWithImprovement("segment_latency_p95_ms", 30)
	warnings := ValidateAgainstReference(cmp, "moderate_loss")
	for _, w := range warnings {
		if w.Metric == "p95_segment_latency" && w.Severity != "info" {
			t.Errorf("unexpected warning for in-range metric: %+v", w)
		}
	}
}

func TestValidateAgainstReferenceWarn(t *testing.T) {
	// 5% improvement is below the moderate_loss p95_segment_latency band [18,40].
	cmp := makeComparisonWithImprovement("segment_latency_p95_ms", 5)
	warnings := ValidateAgainstReference(cmp, "moderate_loss")
	found := false
	for _, w := range warnings {
		if w.Metric == "p95_segment_latency" && w.Severity == "warn" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected warn-severity warning for under-range p95 metric; got %+v", warnings)
	}
}
