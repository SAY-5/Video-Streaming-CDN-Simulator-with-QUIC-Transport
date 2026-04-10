package test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cdn-sim/cdn-sim/internal/experiment"
)

func loadAndRun(t *testing.T, path string) *experiment.ExperimentResults {
	t.Helper()
	cfg, err := experiment.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Output.Dir = t.TempDir()
	cfg.Runs = 1
	cfg.Clients.Count = 8
	cfg.Content.CatalogSize = 20
	cfg.Content.DurationSeconds = 16
	r := experiment.NewRunner(*cfg, nil)
	results, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return results
}

func TestIntegrationBaseline(t *testing.T) {
	path := filepath.Join("..", "configs", "baseline.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("baseline config missing")
	}
	results := loadAndRun(t, path)
	if _, ok := results.ByProtocol["tcp-h2"]; !ok {
		t.Fatal("missing tcp-h2 results")
	}
	if _, ok := results.ByProtocol["quic-h3"]; !ok {
		t.Fatal("missing quic-h3 results")
	}
	if results.Comparison == nil {
		t.Fatal("expected comparison report")
	}
}

func TestIntegrationOutputWrites(t *testing.T) {
	path := filepath.Join("..", "configs", "baseline.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("baseline config missing")
	}
	cfg, err := experiment.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Output.Dir = t.TempDir()
	cfg.Runs = 1
	cfg.Clients.Count = 4
	cfg.Content.CatalogSize = 10
	cfg.Content.DurationSeconds = 16
	r := experiment.NewRunner(*cfg, nil)
	results, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := experiment.WriteJSON(results, cfg.Output.Dir); err != nil {
		t.Fatal(err)
	}
	if err := experiment.WriteCSV(results, cfg.Output.Dir); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := experiment.WriteSummary(results, cfg.Output.Dir, &buf); err != nil {
		t.Fatal(err)
	}
	// Verify summary.json is valid JSON.
	data, err := os.ReadFile(filepath.Join(cfg.Output.Dir, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("summary.json invalid: %v", err)
	}
	// Verify raw.csv exists.
	if _, err := os.Stat(filepath.Join(cfg.Output.Dir, "raw.csv")); err != nil {
		t.Fatal(err)
	}
}
