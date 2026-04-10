package video

import (
	"fmt"
	"math"
	"math/rand"
	"time"
)

// GenerateManifest builds a Manifest for the given content. Scene-complexity
// variation is modeled as a per-segment multiplier sampled from a clamped
// normal distribution N(1.0, 0.15), with the same factor applied across all
// bitrate levels for a given segment index (complexity is content-dependent,
// not bitrate-dependent).
func GenerateManifest(contentID string, duration, segmentDuration time.Duration, reps []Representation, rng *rand.Rand) Manifest {
	if len(reps) == 0 {
		reps = DefaultRepresentations()
	}
	numSegments := int(math.Ceil(duration.Seconds() / segmentDuration.Seconds()))
	if numSegments <= 0 {
		numSegments = 1
	}

	// Per-segment complexity factors shared across bitrates.
	factors := make([]float64, numSegments)
	for i := 0; i < numSegments; i++ {
		factors[i] = clamp(normalSample(rng, 1.0, 0.15), 0.5, 1.8)
	}

	out := make([]Representation, len(reps))
	for i, r := range reps {
		sizes := make([]int64, numSegments)
		meanBytes := float64(r.BitrateKbps*1000) / 8 * segmentDuration.Seconds()
		for s := 0; s < numSegments; s++ {
			sizes[s] = int64(meanBytes * factors[s])
		}
		r.SegmentSizes = sizes
		out[i] = r
	}

	return Manifest{
		ContentID:       contentID,
		SegmentDuration: segmentDuration,
		TotalSegments:   numSegments,
		Representations: out,
	}
}

// SegmentID returns a stable identifier for a segment index at a bitrate.
func SegmentID(contentID string, segIdx, bitrateKbps int) string {
	return fmt.Sprintf("%s/%d/seg-%d", contentID, bitrateKbps, segIdx)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// normalSample uses Box-Muller to return a sample from N(mean, stddev).
func normalSample(rng *rand.Rand, mean, stddev float64) float64 {
	u1 := rng.Float64()
	u2 := rng.Float64()
	if u1 < 1e-12 {
		u1 = 1e-12
	}
	z := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	return mean + z*stddev
}
