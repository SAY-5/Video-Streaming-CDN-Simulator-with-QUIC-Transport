package metrics

import (
	"math"
	"sort"
)

// Percentile computes the nearest-rank percentile of an already-unsorted
// slice. p must be in [0, 100].
func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	rank := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// BuildPercentileSet returns P50/P95/P99/min/max.
func BuildPercentileSet(values []float64) PercentileSet {
	if len(values) == 0 {
		return PercentileSet{}
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	return PercentileSet{
		P50: percentileSorted(sorted, 50),
		P95: percentileSorted(sorted, 95),
		P99: percentileSorted(sorted, 99),
		Min: sorted[0],
		Max: sorted[len(sorted)-1],
	}
}

func percentileSorted(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// BuildStatSet returns mean, stddev, min, max.
func BuildStatSet(values []float64) StatSet {
	if len(values) == 0 {
		return StatSet{}
	}
	var sum, min, max float64
	min = values[0]
	max = values[0]
	for _, v := range values {
		sum += v
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	mean := sum / float64(len(values))
	var variance float64
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	variance /= float64(len(values))
	return StatSet{Mean: mean, StdDev: math.Sqrt(variance), Min: min, Max: max}
}
