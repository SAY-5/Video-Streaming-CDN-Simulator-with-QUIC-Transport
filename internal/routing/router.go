// Package routing implements anycast routing policies that map a client to
// an edge PoP. Every policy is deterministic given a seeded *rand.Rand and
// returns a distinct error when no edge can accept the client.
package routing

import (
	"errors"
	"math/rand"

	"github.com/cdn-sim/cdn-sim/internal/transport"
)

// ErrNoEdgeAvailable is returned when all edges are at capacity.
var ErrNoEdgeAvailable = errors.New("no edge PoP available")

// ClientInfo describes a client's routable identity.
type ClientInfo struct {
	ID        string
	GeoTag    string
	Latitude  float64
	Longitude float64
}

// EdgePoP is a CDN point of presence.
type EdgePoP struct {
	ID              string                   `yaml:"id"`
	GeoTag          string                   `yaml:"geo_tag"`
	Latitude        float64                  `yaml:"latitude"`
	Longitude       float64                  `yaml:"longitude"`
	Capacity        int                      `yaml:"capacity"`
	CurrentLoad     int                      `yaml:"-"`
	NetworkToOrigin transport.NetworkProfile `yaml:"network_to_origin"`
	// ClientProfile defines the client->edge link characteristics at this PoP.
	ClientProfile transport.NetworkProfile `yaml:"client_profile"`
}

// RoutingPolicy decides which edge serves a given client.
type RoutingPolicy interface {
	Route(client ClientInfo, edges []EdgePoP, rng *rand.Rand) (EdgePoP, error)
	Name() string
}
