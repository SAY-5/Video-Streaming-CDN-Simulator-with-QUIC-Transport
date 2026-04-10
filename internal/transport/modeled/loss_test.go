package modeled

import (
	"math"
	"math/rand"
	"testing"

	"github.com/cdn-sim/cdn-sim/internal/transport"
)

func TestGilbertElliottZeroTransition(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	g := NewGilbertElliott(transport.LossModel{
		Type:           "gilbert_elliott",
		PGoodToBad:     0,
		PBadToGood:     1,
		LossInBadState: 1.0,
	}, rng)
	for i := 0; i < 1000; i++ {
		if g.IsLost() {
			t.Fatalf("expected zero loss when PGoodToBad=0, got loss at %d", i)
		}
	}
}

func TestGilbertElliottAlwaysBad(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	g := NewGilbertElliott(transport.LossModel{
		Type:           "gilbert_elliott",
		PGoodToBad:     1,
		PBadToGood:     0,
		LossInBadState: 1.0,
	}, rng)
	// First IsLost: start in GOOD, not lost, transition to BAD.
	first := g.IsLost()
	if first {
		t.Fatalf("first packet should be in GOOD state (not lost)")
	}
	// Subsequent packets: in BAD, always lost.
	for i := 0; i < 500; i++ {
		if !g.IsLost() {
			t.Fatalf("expected loss at packet %d (stuck in BAD state)", i)
		}
	}
}

func TestGilbertElliottSteadyState(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	pGB, pBG, lossBad := 0.03, 0.3, 0.4
	g := NewGilbertElliott(transport.LossModel{
		Type:           "gilbert_elliott",
		PGoodToBad:     pGB,
		PBadToGood:     pBG,
		LossInBadState: lossBad,
	}, rng)
	n := 200000
	lost := 0
	for i := 0; i < n; i++ {
		if g.IsLost() {
			lost++
		}
	}
	actual := float64(lost) / float64(n)
	theoretical := pGB / (pGB + pBG) * lossBad
	if math.Abs(actual-theoretical) > 0.01 {
		t.Fatalf("loss rate %.4f differs from theoretical %.4f by more than 1%%", actual, theoretical)
	}
}

func TestGilbertElliottBurstiness(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	g := NewGilbertElliott(transport.LossModel{
		Type:           "gilbert_elliott",
		PGoodToBad:     0.05,
		PBadToGood:     0.2,
		LossInBadState: 1.0, // every BAD packet is lost
	}, rng)
	n := 200000
	losses := g.SimulatePackets(n)
	// Compute average burst length: consecutive lost packets.
	if len(losses) == 0 {
		t.Fatal("expected some losses")
	}
	bursts := 0
	total := 0
	inBurst := false
	burstLen := 0
	prev := -2
	for _, idx := range losses {
		if idx == prev+1 {
			burstLen++
		} else {
			if inBurst {
				total += burstLen
				bursts++
			}
			inBurst = true
			burstLen = 1
		}
		prev = idx
	}
	if inBurst {
		total += burstLen
		bursts++
	}
	avgBurst := float64(total) / float64(bursts)
	// Expected mean burst length is 1/PBadToGood = 5. Allow generous tolerance.
	if avgBurst < 3.0 || avgBurst > 8.0 {
		t.Fatalf("average burst length %.2f out of expected band [3,8]", avgBurst)
	}
}

func TestGilbertElliottDeterminism(t *testing.T) {
	cfg := transport.LossModel{
		Type: "gilbert_elliott", PGoodToBad: 0.04, PBadToGood: 0.25, LossInBadState: 0.5,
	}
	a := NewGilbertElliott(cfg, rand.New(rand.NewSource(7)))
	b := NewGilbertElliott(cfg, rand.New(rand.NewSource(7)))
	for i := 0; i < 5000; i++ {
		if a.IsLost() != b.IsLost() {
			t.Fatalf("determinism broken at %d", i)
		}
	}
}

func TestUniformLoss(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	u := NewLossSimulator(transport.LossModel{Type: "uniform", UniformPercent: 5}, rng)
	n := 100000
	lost := len(u.SimulatePackets(n))
	rate := float64(lost) / float64(n)
	if math.Abs(rate-0.05) > 0.005 {
		t.Fatalf("uniform loss rate %.4f not near 0.05", rate)
	}
}

func TestNoLoss(t *testing.T) {
	s := NewLossSimulator(transport.LossModel{Type: "none"}, rand.New(rand.NewSource(1)))
	if len(s.SimulatePackets(10000)) != 0 {
		t.Fatal("expected zero loss for Type=none")
	}
}
