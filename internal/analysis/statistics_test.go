package analysis

import (
	"math"
	"math/rand"
	"testing"
)

const floatTol = 1e-9

func TestBootstrapMeanCoversTrue(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	data := make([]float64, 100)
	for i := range data {
		data[i] = rng.NormFloat64()*10 + 100
	}
	lower, upper := Bootstrap(data, Mean, 1000, 0.05, rng)
	const trueMean = 100.0
	if !(lower <= trueMean && trueMean <= upper) {
		t.Fatalf("true mean %.4f not inside CI [%.4f, %.4f]", trueMean, lower, upper)
	}
	if lower >= upper {
		t.Fatalf("lower (%.4f) should be < upper (%.4f)", lower, upper)
	}
}

func TestBootstrapEmpty(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	l, u := Bootstrap(nil, Mean, 100, 0.05, rng)
	if l != 0 || u != 0 {
		t.Fatalf("expected (0,0) for empty input, got (%v,%v)", l, u)
	}
}

func TestCohensDIdentical(t *testing.T) {
	a := []float64{1, 2, 3, 4, 5}
	b := []float64{1, 2, 3, 4, 5}
	d, label := CohensD(a, b)
	if math.Abs(d) > 1e-12 {
		t.Fatalf("expected d ~ 0, got %v", d)
	}
	if label != "negligible" {
		t.Fatalf("expected negligible, got %s", label)
	}
}

func TestCohensDLarge(t *testing.T) {
	g1 := []float64{1, 2, 3, 4, 5}
	g2 := []float64{10, 11, 12, 13, 14}
	d, label := CohensD(g1, g2)
	if math.Abs(d) < 0.8 {
		t.Fatalf("expected |d| >= 0.8, got %v", d)
	}
	if label != "large" {
		t.Fatalf("expected label 'large', got %s", label)
	}
}

func TestCohensDManual(t *testing.T) {
	// g1={1,2,3}, g2={4,5,6}: m1=2, m2=5, s1^2=s2^2=1, pooled sd = 1,
	// d = (2 - 5) / 1 = -3.
	g1 := []float64{1, 2, 3}
	g2 := []float64{4, 5, 6}
	d, label := CohensD(g1, g2)
	if math.Abs(d-(-3.0)) > 1e-6 {
		t.Fatalf("expected d = -3, got %v", d)
	}
	if label != "large" {
		t.Fatalf("expected large, got %s", label)
	}
}

func TestMannWhitneyUIdentical(t *testing.T) {
	a := []float64{1, 2, 3, 4, 5}
	b := []float64{1, 2, 3, 4, 5}
	_, p := MannWhitneyU(a, b)
	if p <= 0.5 {
		t.Fatalf("expected p > 0.5 for identical groups, got %v", p)
	}
}

func TestMannWhitneyUDifferent(t *testing.T) {
	g1 := make([]float64, 50)
	g2 := make([]float64, 50)
	for i := 0; i < 50; i++ {
		g1[i] = float64(i + 1)
		g2[i] = float64(i + 51)
	}
	_, p := MannWhitneyU(g1, g2)
	if p >= 0.001 {
		t.Fatalf("expected p < 0.001, got %v", p)
	}
}

func TestMannWhitneyUEmptyHandled(t *testing.T) {
	_, p := MannWhitneyU(nil, []float64{1, 2, 3})
	if p != 1.0 {
		t.Fatalf("expected p=1.0 for empty group, got %v", p)
	}
	_, p = MannWhitneyU([]float64{1, 2, 3}, nil)
	if p != 1.0 {
		t.Fatalf("expected p=1.0 for empty group, got %v", p)
	}
	_, p = MannWhitneyU(nil, nil)
	if p != 1.0 {
		t.Fatalf("expected p=1.0 for empty groups, got %v", p)
	}
}

func TestECDFShape(t *testing.T) {
	xs, ps := ECDF([]float64{3, 1, 2})
	wantXs := []float64{1, 2, 3}
	wantPs := []float64{1.0 / 3, 2.0 / 3, 1.0}
	if len(xs) != 3 || len(ps) != 3 {
		t.Fatalf("expected length 3, got xs=%d ps=%d", len(xs), len(ps))
	}
	for i := range wantXs {
		if math.Abs(xs[i]-wantXs[i]) > floatTol {
			t.Errorf("xs[%d]=%v want %v", i, xs[i], wantXs[i])
		}
		if math.Abs(ps[i]-wantPs[i]) > floatTol {
			t.Errorf("ps[%d]=%v want %v", i, ps[i], wantPs[i])
		}
	}
}

func TestPercentileBasic(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	// Nearest-rank: P50 => ceil(0.50*10)=5 => data[4]=5
	// P95 => ceil(0.95*10)=10 => data[9]=10
	// P99 => ceil(0.99*10)=10 => data[9]=10
	if got := Percentile(data, 50); math.Abs(got-5) > floatTol {
		t.Errorf("P50=%v want 5", got)
	}
	if got := Percentile(data, 95); math.Abs(got-10) > floatTol {
		t.Errorf("P95=%v want 10", got)
	}
	if got := Percentile(data, 99); math.Abs(got-10) > floatTol {
		t.Errorf("P99=%v want 10", got)
	}
	if got := Median(data); math.Abs(got-5) > floatTol {
		t.Errorf("Median=%v want 5", got)
	}
}

func TestStdDevKnown(t *testing.T) {
	data := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	if m := Mean(data); math.Abs(m-5) > 1e-9 {
		t.Errorf("Mean=%v want 5", m)
	}
	if sd := StdDev(data); math.Abs(sd-2) > 1e-9 {
		t.Errorf("StdDev=%v want 2", sd)
	}
}

func TestStatisticsDeterminism(t *testing.T) {
	data := []float64{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0, 10.0}
	rng1 := rand.New(rand.NewSource(12345))
	l1, u1 := Bootstrap(data, Mean, 500, 0.05, rng1)
	rng2 := rand.New(rand.NewSource(12345))
	l2, u2 := Bootstrap(data, Mean, 500, 0.05, rng2)
	if l1 != l2 || u1 != u2 {
		t.Fatalf("nondeterministic: (%v,%v) vs (%v,%v)", l1, u1, l2, u2)
	}
}
