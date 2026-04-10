package metrics

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/cdn-sim/cdn-sim/internal/video"
)

func TestPercentileExact(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if p := Percentile(vals, 50); p != 5 {
		t.Fatalf("p50=%v", p)
	}
	if p := Percentile(vals, 95); p != 10 {
		t.Fatalf("p95=%v", p)
	}
	if p := Percentile(vals, 99); p != 10 {
		t.Fatalf("p99=%v", p)
	}
}

func TestStatSetMean(t *testing.T) {
	vals := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	s := BuildStatSet(vals)
	if math.Abs(s.Mean-5) > 1e-9 {
		t.Fatalf("mean=%v", s.Mean)
	}
	if math.Abs(s.StdDev-2) > 0.1 {
		t.Fatalf("stddev=%v", s.StdDev)
	}
}

func synth(proto string, startupMs int, latencyMs int, n int) video.PlaybackResult {
	r := video.PlaybackResult{Protocol: proto, StartupLatency: time.Duration(startupMs) * time.Millisecond}
	for i := 0; i < n; i++ {
		r.Segments = append(r.Segments, video.SegmentResult{
			TotalLatency: time.Duration(latencyMs) * time.Millisecond,
			BitrateKbps:  1500,
		})
	}
	r.AvgBitrateKbps = 1500
	return r
}

func TestCompareImprovement(t *testing.T) {
	c := NewCollector()
	for i := 0; i < 20; i++ {
		c.Add(synth("tcp-h2", 340, 340, 30))
		c.Add(synth("quic-h3", 220, 220, 30))
	}
	rng := rand.New(rand.NewSource(1))
	rep := c.Compare("test", "tcp-h2", "quic-h3", rng)
	found := false
	for _, imp := range rep.Improvement {
		if imp.Metric == "segment_latency_p95_ms" {
			found = true
			expected := (340.0 - 220.0) / 340.0 * 100
			if math.Abs(imp.ImprovePct-expected) > 0.5 {
				t.Fatalf("improvement %.2f not close to %.2f", imp.ImprovePct, expected)
			}
			if imp.CILowerPct > imp.CIUpperPct {
				t.Fatalf("CI inverted")
			}
			if imp.EffectLabel == "" {
				t.Fatal("effect label missing")
			}
		}
	}
	if !found {
		t.Fatal("segment_latency improvement missing")
	}
}

func TestEmptyCollector(t *testing.T) {
	c := NewCollector()
	m := c.Aggregate("tcp-h2")
	if m.NumSessions != 0 {
		t.Fatal("expected empty")
	}
}
