package routing

import (
	"math/rand"
)

// RealisticBGP models imperfect BGP anycast: with probability MisroutePercent
// it sends the client to a random eligible non-optimal edge, otherwise it
// behaves like LatencyBased.
type RealisticBGP struct {
	MisrouteProb float64 // typical value ~0.15
}

// Name implements RoutingPolicy.
func (b RealisticBGP) Name() string { return "realistic_bgp" }

// Route implements RoutingPolicy.
func (b RealisticBGP) Route(client ClientInfo, edges []EdgePoP, rng *rand.Rand) (EdgePoP, error) {
	optimal, err := LatencyBased{}.Route(client, edges, rng)
	if err != nil {
		return EdgePoP{}, err
	}
	if rng.Float64() >= b.MisrouteProb {
		return optimal, nil
	}
	// Misroute: collect candidates that are not the optimal and have capacity.
	alts := make([]EdgePoP, 0, len(edges))
	for _, e := range edges {
		if e.ID == optimal.ID {
			continue
		}
		if e.Capacity <= 0 || e.CurrentLoad < e.Capacity {
			alts = append(alts, e)
		}
	}
	if len(alts) == 0 {
		return optimal, nil
	}
	return alts[rng.Intn(len(alts))], nil
}
