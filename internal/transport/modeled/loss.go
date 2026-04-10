// Package modeled provides modeled (non-socket) implementations of the
// transport.Transport interface. The models are deterministic given a seeded
// *rand.Rand and are the foundation for reproducible experiments.
package modeled

import (
	"math/rand"

	"github.com/cdn-sim/cdn-sim/internal/transport"
)

// LossSimulator is the common interface for packet loss models.
type LossSimulator interface {
	// IsLost advances the simulator by one packet and reports whether that
	// packet was lost.
	IsLost() bool
	// SimulatePackets runs n packets through the model and returns the
	// indices of lost packets in [0, n).
	SimulatePackets(n int) []int
}

// NewLossSimulator constructs a LossSimulator matching cfg.Type.
func NewLossSimulator(cfg transport.LossModel, rng *rand.Rand) LossSimulator {
	switch cfg.Type {
	case "gilbert_elliott":
		return NewGilbertElliott(cfg, rng)
	case "uniform":
		return &UniformLoss{rate: cfg.UniformPercent / 100.0, rng: rng}
	default:
		return &NoLoss{}
	}
}

// NoLoss is a trivial lossless simulator.
type NoLoss struct{}

// IsLost always returns false.
func (NoLoss) IsLost() bool { return false }

// SimulatePackets returns an empty slice.
func (NoLoss) SimulatePackets(n int) []int { return nil }

// UniformLoss drops each packet independently with a fixed probability.
type UniformLoss struct {
	rate float64
	rng  *rand.Rand
}

// IsLost reports whether the next packet is lost under the uniform model.
func (u *UniformLoss) IsLost() bool {
	if u.rate <= 0 {
		return false
	}
	return u.rng.Float64() < u.rate
}

// SimulatePackets runs n packets and returns loss indices.
func (u *UniformLoss) SimulatePackets(n int) []int {
	out := make([]int, 0, int(float64(n)*u.rate)+1)
	for i := 0; i < n; i++ {
		if u.IsLost() {
			out = append(out, i)
		}
	}
	return out
}

// GilbertElliott models bursty packet loss via a two-state Markov chain.
// In the GOOD state, packets are never lost. In the BAD state, packets are
// lost with probability LossInBadState. Each packet transmission triggers a
// state transition check:
//
//	GOOD -> BAD with probability PGoodToBad
//	BAD  -> GOOD with probability PBadToGood
//
// The steady-state average loss rate is:
//
//	PGoodToBad / (PGoodToBad + PBadToGood) * LossInBadState
type GilbertElliott struct {
	pGoodToBad     float64
	pBadToGood     float64
	lossInBadState float64
	inBadState     bool
	rng            *rand.Rand
}

// NewGilbertElliott creates a new bursty loss model.
func NewGilbertElliott(cfg transport.LossModel, rng *rand.Rand) *GilbertElliott {
	return &GilbertElliott{
		pGoodToBad:     cfg.PGoodToBad,
		pBadToGood:     cfg.PBadToGood,
		lossInBadState: cfg.LossInBadState,
		rng:            rng,
	}
}

// IsLost simulates one packet transmission and advances the state machine.
// It returns true if the packet is lost.
func (g *GilbertElliott) IsLost() bool {
	// Determine loss in current state.
	var lost bool
	if g.inBadState {
		lost = g.rng.Float64() < g.lossInBadState
	}
	// State transition.
	if g.inBadState {
		if g.rng.Float64() < g.pBadToGood {
			g.inBadState = false
		}
	} else {
		if g.rng.Float64() < g.pGoodToBad {
			g.inBadState = true
		}
	}
	return lost
}

// SimulatePackets runs n packets through the model and returns loss indices.
func (g *GilbertElliott) SimulatePackets(n int) []int {
	out := make([]int, 0, n/10)
	for i := 0; i < n; i++ {
		if g.IsLost() {
			out = append(out, i)
		}
	}
	return out
}
