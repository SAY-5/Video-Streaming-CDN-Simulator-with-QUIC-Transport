package analysis

import (
	"math"
	"math/rand"
	"sort"
)

// Bootstrap computes a (1-alpha) confidence interval for an arbitrary
// statistic using bootstrap resampling.
//
// statFn computes the statistic of interest from a sample (e.g. mean,
// median, percentile). data is the observed sample. iterations is the
// number of bootstrap resamples (1000 is conventional). alpha is the
// total tail probability (0.05 for a 95% CI).
//
// Algorithm:
//  1. Resample data with replacement, iterations times.
//  2. Compute statFn on each resample.
//  3. Sort the bootstrapped statistics.
//  4. CI lower = bootstrapped[alpha/2 * iterations]
//  5. CI upper = bootstrapped[(1-alpha/2) * iterations]
//
// If data is empty or iterations <= 0, Bootstrap returns (0, 0).
func Bootstrap(data []float64, statFn func([]float64) float64, iterations int, alpha float64, rng *rand.Rand) (lower, upper float64) {
	if len(data) == 0 || iterations <= 0 {
		return 0, 0
	}
	n := len(data)
	resampled := make([]float64, n)
	stats := make([]float64, iterations)
	for i := 0; i < iterations; i++ {
		for j := 0; j < n; j++ {
			resampled[j] = data[rng.Intn(n)]
		}
		stats[i] = statFn(resampled)
	}
	sort.Float64s(stats)

	loIdx := int(alpha / 2 * float64(iterations))
	hiIdx := int((1 - alpha/2) * float64(iterations))
	if loIdx < 0 {
		loIdx = 0
	}
	if loIdx > iterations-1 {
		loIdx = iterations - 1
	}
	if hiIdx < 0 {
		hiIdx = 0
	}
	if hiIdx > iterations-1 {
		hiIdx = iterations - 1
	}
	return stats[loIdx], stats[hiIdx]
}

// CohensD computes Cohen's d effect size between two groups using the
// pooled standard deviation. interpretation is one of "negligible",
// "small", "medium", "large" per the conventional thresholds:
//
//	|d| < 0.2  : negligible
//	|d| < 0.5  : small
//	|d| < 0.8  : medium
//	|d| >= 0.8 : large
//
// Pooled stddev: sqrt(((n1-1)*s1^2 + (n2-1)*s2^2) / (n1+n2-2)).
// If either group is empty or the pooled stddev is zero, CohensD
// returns (0, "negligible").
func CohensD(group1, group2 []float64) (d float64, interpretation string) {
	n1 := len(group1)
	n2 := len(group2)
	if n1 == 0 || n2 == 0 || n1+n2 <= 2 {
		return 0, "negligible"
	}
	m1 := Mean(group1)
	m2 := Mean(group2)
	s1 := sampleVariance(group1, m1)
	s2 := sampleVariance(group2, m2)
	pooledVar := (float64(n1-1)*s1 + float64(n2-1)*s2) / float64(n1+n2-2)
	if pooledVar <= 0 {
		return 0, "negligible"
	}
	pooledSD := math.Sqrt(pooledVar)
	d = (m1 - m2) / pooledSD
	ad := math.Abs(d)
	switch {
	case ad < 0.2:
		interpretation = "negligible"
	case ad < 0.5:
		interpretation = "small"
	case ad < 0.8:
		interpretation = "medium"
	default:
		interpretation = "large"
	}
	return d, interpretation
}

// sampleVariance returns the unbiased (n-1) sample variance given a
// precomputed mean.
func sampleVariance(data []float64, mean float64) float64 {
	if len(data) < 2 {
		return 0
	}
	var ss float64
	for _, v := range data {
		diff := v - mean
		ss += diff * diff
	}
	return ss / float64(len(data)-1)
}

// MannWhitneyU performs a Mann-Whitney U test (two-sided, non-parametric
// rank-sum test for whether two samples come from the same distribution).
// Returns the U statistic and an approximate two-sided p-value computed
// from the normal approximation with continuity correction.
//
// Tie correction to the variance is omitted; in the presence of many
// ties the reported p-value will be slightly conservative.
//
// Use this instead of a t-test because latency distributions are NOT
// normal — they are right-skewed with heavy tails.
func MannWhitneyU(group1, group2 []float64) (u float64, pValue float64) {
	n1 := len(group1)
	n2 := len(group2)
	if n1 == 0 || n2 == 0 {
		return 0, 1.0
	}

	type entry struct {
		value float64
		group int // 1 or 2
	}
	combined := make([]entry, 0, n1+n2)
	for _, v := range group1 {
		combined = append(combined, entry{v, 1})
	}
	for _, v := range group2 {
		combined = append(combined, entry{v, 2})
	}
	sort.Slice(combined, func(i, j int) bool {
		return combined[i].value < combined[j].value
	})

	// Assign average ranks for ties.
	ranks := make([]float64, len(combined))
	i := 0
	for i < len(combined) {
		j := i
		for j+1 < len(combined) && combined[j+1].value == combined[i].value {
			j++
		}
		// tied positions i..j, ranks (i+1)..(j+1) -> average
		avg := float64((i+1)+(j+1)) / 2.0
		for k := i; k <= j; k++ {
			ranks[k] = avg
		}
		i = j + 1
	}

	var r1 float64
	for k, e := range combined {
		if e.group == 1 {
			r1 += ranks[k]
		}
	}

	n1f := float64(n1)
	n2f := float64(n2)
	u1 := r1 - n1f*(n1f+1)/2.0
	u2 := n1f*n2f - u1
	u = math.Min(u1, u2)

	meanU := n1f * n2f / 2.0
	varU := n1f * n2f * (n1f + n2f + 1) / 12.0
	if varU <= 0 {
		return u, 1.0
	}
	// Continuity correction: shrink |U - meanU| by 0.5.
	diff := math.Abs(u-meanU) - 0.5
	if diff < 0 {
		diff = 0
	}
	z := diff / math.Sqrt(varU)
	pValue = 2 * (1 - phi(z))
	if pValue > 1 {
		pValue = 1
	}
	if pValue < 0 {
		pValue = 0
	}
	return u, pValue
}

// phi is the standard normal CDF.
func phi(x float64) float64 {
	return 0.5 * (1 + math.Erf(x/math.Sqrt2))
}

// ECDF returns the empirical cumulative distribution function. xs is a
// sorted copy of data; ps[i] = (i+1)/len(data). Both slices have length
// len(data). For empty input both return values are empty slices.
func ECDF(data []float64) (xs, ps []float64) {
	n := len(data)
	if n == 0 {
		return []float64{}, []float64{}
	}
	xs = make([]float64, n)
	copy(xs, data)
	sort.Float64s(xs)
	ps = make([]float64, n)
	for i := 0; i < n; i++ {
		ps[i] = float64(i+1) / float64(n)
	}
	return xs, ps
}

// Mean returns the arithmetic mean of data. Returns 0 for empty input.
func Mean(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}
	var sum float64
	for _, v := range data {
		sum += v
	}
	return sum / float64(len(data))
}

// Median returns the 50th percentile of data.
func Median(data []float64) float64 {
	return Percentile(data, 50)
}

// Percentile returns the p-th percentile (p in [0, 100]) using the
// nearest-rank method on a sorted copy of data. Returns 0 for empty
// input. The caller's slice is never mutated.
func Percentile(data []float64, p float64) float64 {
	n := len(data)
	if n == 0 {
		return 0
	}
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	sorted := make([]float64, n)
	copy(sorted, data)
	sort.Float64s(sorted)
	// Nearest-rank: rank = ceil(p/100 * n), 1-based.
	rank := int(math.Ceil(p / 100.0 * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}

// StdDev returns the population standard deviation of data. Returns 0
// for empty input or single-element input.
func StdDev(data []float64) float64 {
	n := len(data)
	if n == 0 {
		return 0
	}
	m := Mean(data)
	var ss float64
	for _, v := range data {
		diff := v - m
		ss += diff * diff
	}
	return math.Sqrt(ss / float64(n))
}

// BootstrapImprovement computes a (1-alpha) bootstrap confidence interval
// for the QUIC-over-TCP improvement percentage. a and b are the two
// independent session-level samples (TCP and QUIC respectively). When
// higherBetter is false, the improvement is (meanA - meanB)/meanA * 100
// (so a positive number means QUIC is better on a lower-is-better metric
// like latency). When higherBetter is true, the improvement is
// (meanB - meanA)/meanA * 100 (positive = QUIC is better on a
// higher-is-better metric like bitrate).
//
// Iterations are typically 1000. This is a two-sample analogue of
// Bootstrap and eliminates duplicate implementations in callers.
func BootstrapImprovement(a, b []float64, higherBetter bool, iterations int, alpha float64, rng *rand.Rand) (lower, upper float64) {
	if len(a) == 0 || len(b) == 0 || iterations <= 0 {
		return 0, 0
	}
	na := len(a)
	nb := len(b)
	resampleA := make([]float64, na)
	resampleB := make([]float64, nb)
	improvements := make([]float64, iterations)
	for i := 0; i < iterations; i++ {
		for j := 0; j < na; j++ {
			resampleA[j] = a[rng.Intn(na)]
		}
		for j := 0; j < nb; j++ {
			resampleB[j] = b[rng.Intn(nb)]
		}
		ma := Mean(resampleA)
		mb := Mean(resampleB)
		var imp float64
		if ma != 0 {
			if higherBetter {
				imp = (mb - ma) / ma * 100
			} else {
				imp = (ma - mb) / ma * 100
			}
		}
		improvements[i] = imp
	}
	sort.Float64s(improvements)
	loIdx := int(alpha / 2 * float64(iterations))
	hiIdx := int((1 - alpha/2) * float64(iterations))
	if loIdx < 0 {
		loIdx = 0
	}
	if loIdx > iterations-1 {
		loIdx = iterations - 1
	}
	if hiIdx < 0 {
		hiIdx = 0
	}
	if hiIdx > iterations-1 {
		hiIdx = iterations - 1
	}
	return improvements[loIdx], improvements[hiIdx]
}

// EffectLabel returns the standard Cohen (1988) interpretation of a raw
// d value: negligible (|d|<0.2), small (<0.5), medium (<0.8), large (>=0.8).
// This is the single source of truth for effect-size labels across the
// codebase.
func EffectLabel(d float64) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < 0.2:
		return "negligible"
	case d < 0.5:
		return "small"
	case d < 0.8:
		return "medium"
	default:
		return "large"
	}
}
