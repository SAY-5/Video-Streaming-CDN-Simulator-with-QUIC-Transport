package metrics

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/cdn-sim/cdn-sim/internal/video"
)

// synthSession constructs a single-session PlaybackResult with a fixed
// per-segment latency (in ms). Seg count defaults to 20.
func synthSession(id int, proto string, latencyMs int, opts ...func(*video.PlaybackResult)) video.PlaybackResult {
	segs := make([]video.SegmentResult, 20)
	for i := range segs {
		segs[i] = video.SegmentResult{
			Index:        i,
			BitrateKbps:  3000,
			SizeBytes:    750_000,
			TTFB:         time.Duration(latencyMs/4) * time.Millisecond,
			TotalLatency: time.Duration(latencyMs) * time.Millisecond,
			GoodputMbps:  10,
		}
	}
	r := video.PlaybackResult{
		ContentID:        fmt.Sprintf("content-%04d", id),
		Protocol:         proto,
		ABRName:          "buffer_based",
		StartupLatency:   500 * time.Millisecond,
		RebufferCount:    0,
		RebufferDuration: 0,
		AvgBitrateKbps:   3000,
		BitrateChanges:   0,
		Segments:         segs,
		CacheHitRate:     0.5,
	}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}

// jitter perturbs every segment's TotalLatency by +/- jitterMs around
// the nominal latency so that test distributions have positive variance.
func jitter(jitterMs int, seed int64) func(*video.PlaybackResult) {
	return func(r *video.PlaybackResult) {
		rng := rand.New(rand.NewSource(seed))
		for i := range r.Segments {
			delta := time.Duration(rng.Intn(2*jitterMs+1)-jitterMs) * time.Millisecond
			r.Segments[i].TotalLatency += delta
			if r.Segments[i].TotalLatency < 0 {
				r.Segments[i].TotalLatency = 0
			}
		}
	}
}

func buildCollector(tcpMean, quicMean int, n int) *Collector {
	c := NewCollector()
	for i := 0; i < n; i++ {
		c.Add(synthSession(i, "tcp-h2", tcpMean, jitter(40, int64(i*7+1))))
	}
	for i := 0; i < n; i++ {
		c.Add(synthSession(i, "quic-h3", quicMean, jitter(40, int64(i*11+1))))
	}
	return c
}

func TestCompareEnhancedBasic(t *testing.T) {
	c := buildCollector(340, 220, 30)
	rng := rand.New(rand.NewSource(42))
	report := c.CompareEnhanced("test-basic", "tcp-h2", "quic-h3", rng)

	imp, ok := report.Improvements["segment_latency_p95_ms"]
	if !ok {
		t.Fatalf("missing p95 improvement")
	}
	// True improvement (340-220)/340 ≈ 35.3%.
	if imp.ImprovePct < 30 || imp.ImprovePct > 40 {
		t.Errorf("expected p95 improvement ~35%%, got %.2f", imp.ImprovePct)
	}
	if !(imp.CILower <= imp.ImprovePct && imp.ImprovePct <= imp.CIUpper) {
		t.Errorf("CI [%f,%f] does not contain point %f", imp.CILower, imp.CIUpper, imp.ImprovePct)
	}
	if imp.PValue >= 0.05 {
		t.Errorf("expected p<0.05, got %f", imp.PValue)
	}
	if !imp.Significant {
		t.Errorf("expected Significant=true")
	}
	if imp.EffectInterp == "negligible" {
		t.Errorf("expected non-negligible effect size, got %q (d=%f)", imp.EffectInterp, imp.EffectSize)
	}
}

func TestCompareEnhancedNoDifference(t *testing.T) {
	c := NewCollector()
	for i := 0; i < 30; i++ {
		c.Add(synthSession(i, "tcp-h2", 250, jitter(30, int64(i*3+1))))
		c.Add(synthSession(i, "quic-h3", 250, jitter(30, int64(i*3+1))))
	}
	rng := rand.New(rand.NewSource(7))
	report := c.CompareEnhanced("test-nodiff", "tcp-h2", "quic-h3", rng)

	imp := report.Improvements["segment_latency_p95_ms"]
	if math.Abs(imp.ImprovePct) > 5 {
		t.Errorf("expected near-zero improvement, got %.2f", imp.ImprovePct)
	}
	if imp.PValue < 0.05 {
		t.Errorf("expected p>0.05 for identical distributions, got %f", imp.PValue)
	}
	if imp.Significant {
		t.Errorf("expected Significant=false")
	}
	if report.QUICAdvantage != "negligible" {
		t.Errorf("expected QUICAdvantage=negligible, got %q", report.QUICAdvantage)
	}
}

func TestCompareEnhancedDeterministic(t *testing.T) {
	c := buildCollector(340, 220, 30)
	r1 := c.CompareEnhanced("d", "tcp-h2", "quic-h3", rand.New(rand.NewSource(1234)))
	r2 := c.CompareEnhanced("d", "tcp-h2", "quic-h3", rand.New(rand.NewSource(1234)))

	a := r1.Improvements["segment_latency_p95_ms"]
	b := r2.Improvements["segment_latency_p95_ms"]
	if a.CILower != b.CILower {
		t.Errorf("non-deterministic CI lower: %v vs %v", a.CILower, b.CILower)
	}
	if a.CIUpper != b.CIUpper {
		t.Errorf("non-deterministic CI upper: %v vs %v", a.CIUpper, b.CIUpper)
	}
}

func TestKeyFindingsTransportIndependentCache(t *testing.T) {
	c := NewCollector()
	for i := 0; i < 30; i++ {
		t1 := synthSession(i, "tcp-h2", 250, jitter(20, int64(i+1)))
		t1.CacheHitRate = 0.62 + float64(i%5)*0.001
		q1 := synthSession(i, "quic-h3", 250, jitter(20, int64(i+1)))
		q1.CacheHitRate = 0.62 + float64(i%5)*0.001
		c.Add(t1)
		c.Add(q1)
	}
	rng := rand.New(rand.NewSource(99))
	report := c.CompareEnhanced("cache-indep", "tcp-h2", "quic-h3", rng)

	found := false
	for _, f := range report.KeyFindings {
		if f == "Cache behavior is transport-independent" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected cache-indep finding; got %v", report.KeyFindings)
	}
}

func TestKeyFindingsHOLBlockingMessage(t *testing.T) {
	c := NewCollector()
	for i := 0; i < 25; i++ {
		// TCP: 3 HOL-blocked segments per session.
		tcp := synthSession(i, "tcp-h2", 340, jitter(30, int64(i+1)))
		for j := 0; j < 3; j++ {
			tcp.Segments[j].HOLBlocked = true
		}
		c.Add(tcp)
		// QUIC: zero HOL events.
		c.Add(synthSession(i, "quic-h3", 220, jitter(30, int64(i+1))))
	}
	rng := rand.New(rand.NewSource(17))
	report := c.CompareEnhanced("hol", "tcp-h2", "quic-h3", rng)

	found := false
	for _, f := range report.KeyFindings {
		if containsSubstr(f, "HOL blocking eliminated by QUIC") && containsSubstr(f, "3.0 events/session") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected HOL finding with per-session count; got %v", report.KeyFindings)
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
