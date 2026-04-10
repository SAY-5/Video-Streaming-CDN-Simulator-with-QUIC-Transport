// Package video models video manifests, adaptive bitrate (ABR) algorithms,
// and playback sessions driven by the transport layer.
package video

import "time"

// Representation is one quality level of a piece of content.
type Representation struct {
	BitrateKbps  int
	Width        int
	Height       int
	SegmentSizes []int64 // byte size per segment (varies with scene complexity)
}

// Manifest describes a piece of content available in multiple bitrates.
type Manifest struct {
	ContentID       string
	SegmentDuration time.Duration
	TotalSegments   int
	Representations []Representation
}

// DefaultRepresentations returns the library's default bitrate ladder.
func DefaultRepresentations() []Representation {
	return []Representation{
		{BitrateKbps: 400, Width: 640, Height: 360},
		{BitrateKbps: 800, Width: 854, Height: 480},
		{BitrateKbps: 1500, Width: 1280, Height: 720},
		{BitrateKbps: 3000, Width: 1920, Height: 1080},
		{BitrateKbps: 6000, Width: 2560, Height: 1440},
		{BitrateKbps: 12000, Width: 3840, Height: 2160},
	}
}

// LowestBitrate returns the minimum bitrate across all representations.
func (m Manifest) LowestBitrate() int {
	if len(m.Representations) == 0 {
		return 0
	}
	min := m.Representations[0].BitrateKbps
	for _, r := range m.Representations[1:] {
		if r.BitrateKbps < min {
			min = r.BitrateKbps
		}
	}
	return min
}

// HighestBitrate returns the maximum bitrate across all representations.
func (m Manifest) HighestBitrate() int {
	if len(m.Representations) == 0 {
		return 0
	}
	max := m.Representations[0].BitrateKbps
	for _, r := range m.Representations[1:] {
		if r.BitrateKbps > max {
			max = r.BitrateKbps
		}
	}
	return max
}

// ClosestBitrate returns the representation whose bitrate is the largest
// value <= target, falling back to the lowest.
func (m Manifest) ClosestBitrate(target int) int {
	best := m.LowestBitrate()
	for _, r := range m.Representations {
		if r.BitrateKbps <= target && r.BitrateKbps > best {
			best = r.BitrateKbps
		}
	}
	return best
}

// RepresentationForBitrate returns the Representation with the exact bitrate,
// snapping to the nearest lower option if not found.
func (m Manifest) RepresentationForBitrate(bitrateKbps int) Representation {
	var chosen Representation
	chosen = m.Representations[0]
	for _, r := range m.Representations {
		if r.BitrateKbps <= bitrateKbps && r.BitrateKbps >= chosen.BitrateKbps {
			chosen = r
		}
	}
	return chosen
}
