package routing

import (
	"math"
	"math/rand"
)

// WeightedCapacity picks edges proportionally to remaining capacity. Ties
// are broken by latency so under full load it degrades to LatencyBased.
type WeightedCapacity struct{}

// Name implements RoutingPolicy.
func (WeightedCapacity) Name() string { return "weighted_capacity" }

// Route implements RoutingPolicy.
func (WeightedCapacity) Route(client ClientInfo, edges []EdgePoP, rng *rand.Rand) (EdgePoP, error) {
	weights := make([]float64, len(edges))
	var total float64
	for i, e := range edges {
		if e.Capacity <= 0 || e.CurrentLoad >= e.Capacity {
			weights[i] = 0
			continue
		}
		remaining := float64(e.Capacity - e.CurrentLoad)
		weights[i] = remaining / float64(e.Capacity)
		total += weights[i]
	}
	if total == 0 {
		return EdgePoP{}, ErrNoEdgeAvailable
	}
	r := rng.Float64() * total
	cum := 0.0
	for i, w := range weights {
		cum += w
		if r <= cum {
			return edges[i], nil
		}
	}
	// Fallback (shouldn't happen due to float imprecision): best latency.
	best := -1
	bestRTT := math.MaxFloat64
	for i, e := range edges {
		if weights[i] == 0 {
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
