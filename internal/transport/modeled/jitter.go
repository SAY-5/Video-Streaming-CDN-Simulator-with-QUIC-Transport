package modeled

import (
	"math"
	"math/rand"
	"time"
)

// sampleJitter returns a jitter duration sampled from a zero-mean Normal
// distribution with the given standard deviation in milliseconds using the
// Box-Muller transform. Negative samples are clamped to zero (we never speed
// up below the base RTT).
func sampleJitter(stddevMs float64, rng *rand.Rand) time.Duration {
	if stddevMs <= 0 {
		return 0
	}
	u1 := rng.Float64()
	u2 := rng.Float64()
	if u1 < 1e-12 {
		u1 = 1e-12
	}
	z := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	ms := z * stddevMs
	if ms < 0 {
		// Use absolute value so the distribution is half-normal (always >= 0).
		ms = -ms
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// rttDuration converts a profile's BaseRTTMs to a time.Duration.
func rttDuration(baseRTTMs float64) time.Duration {
	return time.Duration(baseRTTMs * float64(time.Millisecond))
}
