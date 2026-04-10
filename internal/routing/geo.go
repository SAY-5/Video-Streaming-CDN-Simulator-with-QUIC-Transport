package routing

import "math/rand"

// GeoAffinity routes clients to edges that share their geo tag. If the
// matching edge is at capacity or absent, it falls back to LatencyBased.
type GeoAffinity struct{}

// Name implements RoutingPolicy.
func (GeoAffinity) Name() string { return "geo_affinity" }

// Route implements RoutingPolicy.
func (g GeoAffinity) Route(client ClientInfo, edges []EdgePoP, rng *rand.Rand) (EdgePoP, error) {
	for _, e := range edges {
		if e.GeoTag == client.GeoTag && (e.Capacity <= 0 || e.CurrentLoad < e.Capacity) {
			return e, nil
		}
	}
	return LatencyBased{}.Route(client, edges, rng)
}
