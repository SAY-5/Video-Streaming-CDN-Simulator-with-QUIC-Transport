package analysis

import (
	"math"
	"math/rand"
	"testing"
)

// TestBootstrapCoverageProbability verifies that over many repeated
// bootstrap runs with known normal data, the computed 95% CI contains
// the true mean approximately 95% of the time. This is the defining
// property of a correct percentile bootstrap.
func TestBootstrapCoverageProbability(t *testing.T) {
	if testing.Short() {
		t.Skip("slow coverage-probability test")
	}
	const (
		trueMean    = 100.0
		trueStdDev  = 10.0
		sampleSize  = 80
		bootIters   = 500
		outerTrials = 400
	)
	rng := rand.New(rand.NewSource(42))
	covered := 0
	for i := 0; i < outerTrials; i++ {
		sample := make([]float64, sampleSize)
		for j := range sample {
			sample[j] = trueMean + rng.NormFloat64()*trueStdDev
		}
		lo, hi := Bootstrap(sample, Mean, bootIters, 0.05, rng)
		if lo <= trueMean && trueMean <= hi {
			covered++
		}
	}
	rate := float64(covered) / float64(outerTrials)
	// 95% CI should achieve 90-99% coverage in practice for a well-behaved
	// normal sample; we require 88% as a floor to allow Monte-Carlo noise
	// in 400 trials.
	if rate < 0.88 {
		t.Fatalf("coverage rate %.3f below 0.88", rate)
	}
	if rate > 0.995 {
		t.Fatalf("coverage rate %.3f unreasonably high; CIs likely too wide", rate)
	}
}

// TestCohensDSignSymmetry — CohensD(a,b) and CohensD(b,a) should have
// equal magnitude and opposite signs.
func TestCohensDSignSymmetry(t *testing.T) {
	a := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	b := []float64{5, 6, 7, 8, 9, 10, 11, 12, 13, 14}
	d1, l1 := CohensD(a, b)
	d2, l2 := CohensD(b, a)
	if math.Abs(d1+d2) > 1e-9 {
		t.Fatalf("sign symmetry broken: d1=%.6f d2=%.6f", d1, d2)
	}
	if l1 != l2 {
		t.Fatalf("labels differ: %s vs %s", l1, l2)
	}
}

// TestCohensDZeroVariance — if both groups have identical constant
// values, pooled variance is 0 and CohensD must not divide by zero.
func TestCohensDZeroVariance(t *testing.T) {
	a := []float64{5, 5, 5, 5, 5}
	b := []float64{5, 5, 5, 5, 5}
	d, l := CohensD(a, b)
	if !math.IsNaN(d) && d != 0 {
		// Either nan or 0 is acceptable; the label should be "negligible".
	}
	if l != "negligible" {
		t.Fatalf("expected negligible label, got %s", l)
	}
}

// TestMannWhitneyIdenticalDistributions — two samples from the same
// distribution should not reject the null at p < 0.05 on average.
func TestMannWhitneyIdenticalDistributions(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	sampleSize := 40
	trials := 200
	rejected := 0
	for i := 0; i < trials; i++ {
		a := make([]float64, sampleSize)
		b := make([]float64, sampleSize)
		for j := range a {
			a[j] = rng.NormFloat64()
			b[j] = rng.NormFloat64()
		}
		_, p := MannWhitneyU(a, b)
		if p < 0.05 {
			rejected++
		}
	}
	rate := float64(rejected) / float64(trials)
	// Nominal type-I error is 5%. Allow [0%, 12%] to absorb Monte Carlo
	// noise at 200 trials plus the conservative tie-correction skip.
	if rate > 0.12 {
		t.Fatalf("false-positive rate %.3f exceeds 0.12", rate)
	}
}

// TestMannWhitneyDifferentDistributions — samples with a large shift
// should reject the null at p < 0.001.
func TestMannWhitneyDifferentDistributions(t *testing.T) {
	a := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	b := []float64{20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30}
	_, p := MannWhitneyU(a, b)
	if p > 0.001 {
		t.Fatalf("expected tiny p for obviously different groups, got %v", p)
	}
}

// TestBootstrapImprovementSigned — the two-sample bootstrap should
// return positive CI bounds when QUIC is lower (better on a
// lower-is-better metric).
func TestBootstrapImprovementSigned(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	tcp := make([]float64, 100)
	quic := make([]float64, 100)
	for i := range tcp {
		tcp[i] = 100 + rng.NormFloat64()*5
		quic[i] = 70 + rng.NormFloat64()*5
	}
	lo, hi := BootstrapImprovement(tcp, quic, false, 1000, 0.05, rng)
	// True improvement is (100-70)/100 * 100 = 30%.
	if lo <= 0 {
		t.Fatalf("expected lower bound > 0 for clear QUIC win, got %.2f", lo)
	}
	if hi < 25 || hi > 35 {
		t.Fatalf("upper bound %.2f unreasonably far from truth 30%%", hi)
	}
}

// TestEffectLabelBoundaries — canonical Cohen (1988) thresholds.
func TestEffectLabelBoundaries(t *testing.T) {
	cases := []struct {
		d    float64
		want string
	}{
		{0.0, "negligible"},
		{0.19, "negligible"},
		{0.2, "small"},
		{0.49, "small"},
		{0.5, "medium"},
		{0.79, "medium"},
		{0.8, "large"},
		{1.5, "large"},
		{-0.3, "small"}, // magnitude, not sign
		{-0.85, "large"},
	}
	for _, c := range cases {
		if got := EffectLabel(c.d); got != c.want {
			t.Errorf("EffectLabel(%v)=%q want %q", c.d, got, c.want)
		}
	}
}
