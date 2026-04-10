package routing

import (
	"math/rand"
	"testing"
)

func sampleEdges() []EdgePoP {
	return []EdgePoP{
		{ID: "sg", GeoTag: "asia", Latitude: 1.35, Longitude: 103.82, Capacity: 100},
		{ID: "frankfurt", GeoTag: "europe", Latitude: 50.11, Longitude: 8.68, Capacity: 100},
		{ID: "sfo", GeoTag: "us-west", Latitude: 37.77, Longitude: -122.42, Capacity: 100},
	}
}

func TestLatencyBasedPicksNearest(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	client := ClientInfo{ID: "c1", GeoTag: "asia", Latitude: 1.29, Longitude: 103.85}
	p := LatencyBased{}
	edge, err := p.Route(client, sampleEdges(), rng)
	if err != nil {
		t.Fatal(err)
	}
	if edge.ID != "sg" {
		t.Fatalf("expected sg, got %s", edge.ID)
	}
}

func TestLatencyBasedSkipsFull(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	edges := sampleEdges()
	edges[0].CurrentLoad = 100 // sg full
	client := ClientInfo{ID: "c1", Latitude: 1.29, Longitude: 103.85}
	edge, err := LatencyBased{}.Route(client, edges, rng)
	if err != nil {
		t.Fatal(err)
	}
	if edge.ID == "sg" {
		t.Fatal("full edge should be skipped")
	}
}

func TestAllFullReturnsError(t *testing.T) {
	edges := sampleEdges()
	for i := range edges {
		edges[i].CurrentLoad = edges[i].Capacity
	}
	client := ClientInfo{ID: "c1"}
	if _, err := (LatencyBased{}).Route(client, edges, rand.New(rand.NewSource(1))); err != ErrNoEdgeAvailable {
		t.Fatalf("expected ErrNoEdgeAvailable, got %v", err)
	}
}

func TestWeightedCapacityPrefersEmptier(t *testing.T) {
	edges := sampleEdges()
	edges[0].CurrentLoad = 95 // nearly full
	edges[1].CurrentLoad = 10 // mostly empty
	edges[2].CurrentLoad = 10
	client := ClientInfo{ID: "c1"}
	counts := map[string]int{}
	rng := rand.New(rand.NewSource(1))
	p := WeightedCapacity{}
	for i := 0; i < 2000; i++ {
		e, err := p.Route(client, edges, rng)
		if err != nil {
			t.Fatal(err)
		}
		counts[e.ID]++
	}
	if counts["sg"] > counts["frankfurt"] {
		t.Fatalf("sg (nearly full) should be picked less often: %+v", counts)
	}
}

func TestGeoAffinityMatches(t *testing.T) {
	edges := sampleEdges()
	client := ClientInfo{ID: "c1", GeoTag: "europe", Latitude: 50, Longitude: 8}
	edge, err := (GeoAffinity{}).Route(client, edges, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatal(err)
	}
	if edge.ID != "frankfurt" {
		t.Fatalf("expected frankfurt, got %s", edge.ID)
	}
}

func TestRealisticBGPMisrouteRate(t *testing.T) {
	edges := sampleEdges()
	client := ClientInfo{ID: "c1", Latitude: 1.29, Longitude: 103.85} // near sg
	p := RealisticBGP{MisrouteProb: 0.15}
	rng := rand.New(rand.NewSource(7))
	misroutes := 0
	total := 20000
	for i := 0; i < total; i++ {
		e, err := p.Route(client, edges, rng)
		if err != nil {
			t.Fatal(err)
		}
		if e.ID != "sg" {
			misroutes++
		}
	}
	rate := float64(misroutes) / float64(total)
	if rate < 0.12 || rate > 0.18 {
		t.Fatalf("misroute rate %.3f not near 0.15", rate)
	}
}

func TestRoutingDeterminism(t *testing.T) {
	edges := sampleEdges()
	client := ClientInfo{ID: "c1", Latitude: 1.29, Longitude: 103.85}
	p := RealisticBGP{MisrouteProb: 0.15}
	a := rand.New(rand.NewSource(42))
	b := rand.New(rand.NewSource(42))
	for i := 0; i < 100; i++ {
		ra, _ := p.Route(client, edges, a)
		rb, _ := p.Route(client, edges, b)
		if ra.ID != rb.ID {
			t.Fatalf("determinism broken at %d", i)
		}
	}
}
