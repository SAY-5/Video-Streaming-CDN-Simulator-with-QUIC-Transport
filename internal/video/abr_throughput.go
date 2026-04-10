package video

// ThroughputBasedABR selects bitrate using a throughput estimate with a
// safety margin of 0.85. It implements an EWMA over past fetches, a
// conservative startup ramp (first N segments always lowest), and periodic
// bandwidth probing (every probeInterval segments it tries one level up).
type ThroughputBasedABR struct {
	Alpha          float64 // EWMA weight on latest sample, default 0.3
	SafetyMargin   float64 // default 0.85
	StartupLength  int     // segments forced to lowest bitrate, default 3
	ProbeInterval  int     // segments between bandwidth probes, default 10
	MinSamplesEWMA int     // require this many samples before EWMA kicks in
}

// NewThroughputBasedABR returns an ABR algorithm with sensible defaults.
func NewThroughputBasedABR() *ThroughputBasedABR {
	return &ThroughputBasedABR{
		Alpha:          0.3,
		SafetyMargin:   0.85,
		StartupLength:  3,
		ProbeInterval:  10,
		MinSamplesEWMA: 3,
	}
}

// Name implements ABRAlgorithm.
func (t *ThroughputBasedABR) Name() string { return "throughput_based" }

// SelectBitrate implements ABRAlgorithm.
func (t *ThroughputBasedABR) SelectBitrate(state PlayerState, manifest Manifest) ABRDecision {
	// Startup: conservative ramp.
	if state.SegmentIndex < t.StartupLength {
		return ABRDecision{BitrateKbps: manifest.LowestBitrate(), Reason: "startup"}
	}

	// Compute throughput estimate.
	var estimate float64
	if len(state.ThroughputHistory) < t.MinSamplesEWMA {
		// Simple average.
		sum := 0.0
		for _, s := range state.ThroughputHistory {
			sum += s.Kbps
		}
		if len(state.ThroughputHistory) > 0 {
			estimate = sum / float64(len(state.ThroughputHistory))
		} else {
			estimate = float64(manifest.LowestBitrate())
		}
	} else {
		// EWMA: newer samples weighted by Alpha.
		estimate = state.ThroughputHistory[0].Kbps
		for _, s := range state.ThroughputHistory[1:] {
			estimate = t.Alpha*s.Kbps + (1-t.Alpha)*estimate
		}
	}

	target := int(estimate * t.SafetyMargin)
	chosen := manifest.ClosestBitrate(target)

	// Probe: every ProbeInterval segments, try one level above current.
	if t.ProbeInterval > 0 && state.SegmentIndex%t.ProbeInterval == 0 && state.SegmentIndex > t.StartupLength {
		probe := oneLevelAbove(manifest, state.CurrentBitrateKbps)
		if probe > chosen {
			return ABRDecision{BitrateKbps: probe, Reason: "probe"}
		}
	}

	return ABRDecision{BitrateKbps: chosen, Reason: "ewma"}
}

func oneLevelAbove(m Manifest, current int) int {
	best := current
	for _, r := range m.Representations {
		if r.BitrateKbps > current {
			if best == current || r.BitrateKbps < best {
				best = r.BitrateKbps
			}
		}
	}
	return best
}

func oneLevelBelow(m Manifest, current int) int {
	best := current
	for _, r := range m.Representations {
		if r.BitrateKbps < current {
			if best == current || r.BitrateKbps > best {
				best = r.BitrateKbps
			}
		}
	}
	return best
}
