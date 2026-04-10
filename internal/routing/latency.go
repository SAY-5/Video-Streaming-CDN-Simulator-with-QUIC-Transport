package routing

import (
	"math"
	"math/rand"
)

// LatencyBased picks the edge with the lowest estimated RTT that still has
// capacity.
type LatencyBased struct{}

// Name implements RoutingPolicy.
func (LatencyBased) Name() string { return "latency_based" }

// Route implements RoutingPolicy.
func (LatencyBased) Route(client ClientInfo, edges []EdgePoP, rng *rand.Rand) (EdgePoP, error) {
	best := -1
	bestRTT := math.MaxFloat64
	for i, e := range edges {
		if e.Capacity > 0 && e.CurrentLoad >= e.Capacity {
			continue
		}
		d := HaversineKm(client.Latitude, client.Longitude, e.Latitude, e.Longitude)
		rtt := EstimateRTTMs(d)
		if rtt < bestRTT {
			bestRTT = rtt
			best = i
		}
	}
	if best < 0 {
		return EdgePoP{}, ErrNoEdgeAvailable
	}
	return edges[best], nil
}
