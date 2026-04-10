package video

import "time"

// BufferBasedABR implements BBA with danger zones, using buffer level as the
// primary signal. Hysteresis prevents oscillation by requiring the target
// bitrate to be more than one representation level away before switching.
type BufferBasedABR struct {
	CriticalBufferS float64 // below this: lowest bitrate
	DangerBufferS   float64 // below this: do not increase
	ComfortBufferS  float64 // linear interpolation starts here
	SurplusBufferS  float64 // above this: highest bitrate
}

// NewBufferBasedABR returns an ABR algorithm with the default danger zones.
func NewBufferBasedABR() *BufferBasedABR {
	return &BufferBasedABR{
		CriticalBufferS: 2,
		DangerBufferS:   6,
		ComfortBufferS:  6,
		SurplusBufferS:  15,
	}
}

// Name implements ABRAlgorithm.
func (b *BufferBasedABR) Name() string { return "buffer_based" }

// SelectBitrate implements ABRAlgorithm.
func (b *BufferBasedABR) SelectBitrate(state PlayerState, manifest Manifest) ABRDecision {
	bufSec := state.BufferLevel.Seconds()
	low := manifest.LowestBitrate()
	high := manifest.HighestBitrate()
	current := state.CurrentBitrateKbps
	if current == 0 {
		current = low
	}

	var target int
	var reason string
	switch {
	case bufSec < b.CriticalBufferS:
		return ABRDecision{BitrateKbps: low, Reason: "buffer_critical"}
	case bufSec < b.DangerBufferS:
		// Don't increase; may decrease.
		target = current
		if len(state.ThroughputHistory) > 0 {
			last := state.ThroughputHistory[len(state.ThroughputHistory)-1].Kbps
			if last < float64(current) {
				target = oneLevelBelow(manifest, current)
			}
		}
		reason = "buffer_danger"
	case bufSec >= b.SurplusBufferS:
		target = high
		reason = "buffer_surplus"
	default:
		// Comfort: linear interpolation between low and high over the window.
		frac := (bufSec - b.ComfortBufferS) / (b.SurplusBufferS - b.ComfortBufferS)
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		goal := float64(low) + float64(high-low)*frac
		target = manifest.ClosestBitrate(int(goal))
		reason = "buffer_comfort"
	}

	// Hysteresis: if target is within one representation level of current,
	// stay put to avoid oscillation.
	if levelsApart(manifest, current, target) <= 1 && reason != "buffer_critical" {
		return ABRDecision{BitrateKbps: current, Reason: reason + "_hysteresis"}
	}

	return ABRDecision{BitrateKbps: target, Reason: reason}
}

// levelsApart returns the number of representation levels between a and b.
func levelsApart(m Manifest, a, b int) int {
	indexA := -1
	indexB := -1
	// Sort by bitrate to get consistent ordering.
	ordered := make([]int, 0, len(m.Representations))
	for _, r := range m.Representations {
		ordered = append(ordered, r.BitrateKbps)
	}
	// Simple insertion sort — small slice.
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && ordered[j] < ordered[j-1]; j-- {
			ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
		}
	}
	for i, v := range ordered {
		if v == a {
			indexA = i
		}
		if v == b {
			indexB = i
		}
	}
	if indexA == -1 || indexB == -1 {
		return 2 // treat unknowns as "far"
	}
	if indexA > indexB {
		return indexA - indexB
	}
	return indexB - indexA
}

// msToDuration is a small helper used by tests to construct buffer levels.
func msToDuration(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}
