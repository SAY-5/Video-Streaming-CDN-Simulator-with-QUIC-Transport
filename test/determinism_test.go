package test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cdn-sim/cdn-sim/internal/experiment"
)

func TestDeterminism(t *testing.T) {
	path := filepath.Join("..", "configs", "baseline.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("baseline config missing")
	}
	build := func() *experiment.ExperimentResults {
		cfg, err := experiment.LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Output.Dir = t.TempDir()
		cfg.Runs = 1
		cfg.Clients.Count = 8
		cfg.Content.CatalogSize = 20
		cfg.Content.DurationSeconds = 16
		r := experiment.NewRunner(*cfg, nil)
		results, err := r.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return results
	}
	a := build()
	b := build()
	// Compare per-protocol aggregates.
	for proto, am := range a.ByProtocol {
		bm, ok := b.ByProtocol[proto]
		if !ok {
			t.Fatalf("missing proto %s in second run", proto)
		}
		if !reflect.DeepEqual(am, bm) {
			t.Fatalf("non-deterministic aggregates for %s: %+v vs %+v", proto, am, bm)
		}
	}
}

func TestDifferentSeedDiffers(t *testing.T) {
	path := filepath.Join("..", "configs", "baseline.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("baseline config missing")
	}
	build := func(seed int64) *experiment.ExperimentResults {
		cfg, err := experiment.LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Seed = seed
		cfg.Output.Dir = t.TempDir()
		cfg.Runs = 1
		cfg.Clients.Count = 8
		cfg.Content.CatalogSize = 20
		cfg.Content.DurationSeconds = 16
		r := experiment.NewRunner(*cfg, nil)
		results, err := r.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return results
	}
	a := build(1)
	b := build(2)
	if reflect.DeepEqual(a.ByProtocol["tcp-h2"], b.ByProtocol["tcp-h2"]) {
		t.Fatal("different seeds should produce different results")
	}
}
