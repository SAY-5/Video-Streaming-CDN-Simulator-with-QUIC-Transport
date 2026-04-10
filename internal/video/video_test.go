package video

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestGenerateManifestShape(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	m := GenerateManifest("c1", 60*time.Second, 4*time.Second, DefaultRepresentations(), rng)
	if m.TotalSegments != 15 {
		t.Fatalf("expected 15 segments, got %d", m.TotalSegments)
	}
	for _, r := range m.Representations {
		if len(r.SegmentSizes) != m.TotalSegments {
			t.Fatalf("rep %d has %d sizes", r.BitrateKbps, len(r.SegmentSizes))
		}
	}
}

func TestGenerateManifestLinearBitrate(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	m := GenerateManifest("c1", 40*time.Second, 4*time.Second, DefaultRepresentations(), rng)
	// Higher bitrates should produce roughly proportionally larger segments
	// on average (within the ±15% scene factor window).
	sum400 := int64(0)
	sum3000 := int64(0)
	for i := 0; i < m.TotalSegments; i++ {
		sum400 += m.Representations[0].SegmentSizes[i]
		sum3000 += m.Representations[3].SegmentSizes[i]
	}
	ratio := float64(sum3000) / float64(sum400)
	expected := 3000.0 / 400.0
	if math.Abs(ratio-expected) > 0.01 {
		t.Fatalf("bitrate ratio %.3f differs from %.3f", ratio, expected)
	}
}

func TestGenerateManifestDeterminism(t *testing.T) {
	a := GenerateManifest("c1", 40*time.Second, 4*time.Second, nil, rand.New(rand.NewSource(5)))
	b := GenerateManifest("c1", 40*time.Second, 4*time.Second, nil, rand.New(rand.NewSource(5)))
	for i := 0; i < a.TotalSegments; i++ {
		if a.Representations[0].SegmentSizes[i] != b.Representations[0].SegmentSizes[i] {
			t.Fatalf("determinism broken at %d", i)
		}
	}
}

func makeManifest() Manifest {
	return GenerateManifest("c1", 60*time.Second, 4*time.Second, DefaultRepresentations(), rand.New(rand.NewSource(1)))
}

func TestThroughputStartup(t *testing.T) {
	abr := NewThroughputBasedABR()
	m := makeManifest()
	state := PlayerState{SegmentIndex: 0, LastThroughputKbps: 9000, ThroughputHistory: []ThroughputSample{{Kbps: 9000}}}
	d := abr.SelectBitrate(state, m)
	if d.BitrateKbps != m.LowestBitrate() {
		t.Fatalf("startup should pick lowest bitrate, got %d", d.BitrateKbps)
	}
}

func TestThroughputSelectsUnderMargin(t *testing.T) {
	abr := NewThroughputBasedABR()
	m := makeManifest()
	state := PlayerState{
		SegmentIndex: 5,
		ThroughputHistory: []ThroughputSample{
			{Kbps: 5000}, {Kbps: 5000}, {Kbps: 5000},
		},
	}
	d := abr.SelectBitrate(state, m)
	// 5000 * 0.85 = 4250 → should pick 3000.
	if d.BitrateKbps != 3000 {
		t.Fatalf("expected 3000 under 5000 Kbps throughput, got %d", d.BitrateKbps)
	}
}

func TestBufferBasedCritical(t *testing.T) {
	abr := NewBufferBasedABR()
	m := makeManifest()
	d := abr.SelectBitrate(PlayerState{BufferLevel: 1 * time.Second, CurrentBitrateKbps: 3000}, m)
	if d.BitrateKbps != m.LowestBitrate() {
		t.Fatalf("critical should pick lowest, got %d", d.BitrateKbps)
	}
}

func TestBufferBasedSurplus(t *testing.T) {
	abr := NewBufferBasedABR()
	m := makeManifest()
	d := abr.SelectBitrate(PlayerState{BufferLevel: 20 * time.Second, CurrentBitrateKbps: 3000}, m)
	if d.BitrateKbps != m.HighestBitrate() {
		t.Fatalf("surplus should pick highest, got %d", d.BitrateKbps)
	}
}

func TestBufferBasedComfortLinear(t *testing.T) {
	abr := NewBufferBasedABR()
	m := makeManifest()
	d := abr.SelectBitrate(PlayerState{BufferLevel: 10 * time.Second, CurrentBitrateKbps: 400}, m)
	if d.BitrateKbps <= m.LowestBitrate() || d.BitrateKbps >= m.HighestBitrate() {
		t.Fatalf("comfort should pick middle bitrate, got %d", d.BitrateKbps)
	}
}

func TestBufferBasedHysteresis(t *testing.T) {
	abr := NewBufferBasedABR()
	m := makeManifest()
	// Buffer 7s, current 800 Kbps → target computed as ~1500 Kbps (one level
	// higher). Hysteresis should hold the player at 800.
	d := abr.SelectBitrate(PlayerState{BufferLevel: 7 * time.Second, CurrentBitrateKbps: 800}, m)
	if d.BitrateKbps != 800 {
		t.Fatalf("hysteresis should hold 800, got %d (reason=%s)", d.BitrateKbps, d.Reason)
	}
}

func TestBufferBasedBigJump(t *testing.T) {
	abr := NewBufferBasedABR()
	m := makeManifest()
	// Current 400 Kbps, surplus buffer, should jump straight to highest.
	d := abr.SelectBitrate(PlayerState{BufferLevel: 20 * time.Second, CurrentBitrateKbps: 400}, m)
	if d.BitrateKbps != m.HighestBitrate() {
		t.Fatalf("big jump failed, got %d", d.BitrateKbps)
	}
}
