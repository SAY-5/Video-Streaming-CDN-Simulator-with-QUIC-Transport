package experiment

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cdn-sim/cdn-sim/internal/routing"
	"github.com/cdn-sim/cdn-sim/internal/transport"
)

func tinyBaseConfig() ExperimentConfig {
	return ExperimentConfig{
		Name:        "sweep-unit",
		Description: "tiny base for sweep unit tests",
		Seed:        1234,
		Topology: TopologyConfig{
			OriginLocation: LatLon{Latitude: 38.9, Longitude: -77.0},
			OriginNetwork: transport.NetworkProfile{
				BaseRTTMs:     50,
				BandwidthMbps: 1000,
				LossModel:     transport.LossModel{Type: "none"},
			},
			Edges: []routing.EdgePoP{
				{
					ID:        "edge-a",
					GeoTag:    "asia",
					Latitude:  1.35,
					Longitude: 103.82,
					Capacity:  100,
					ClientProfile: transport.NetworkProfile{
						BaseRTTMs:     50,
						BandwidthMbps: 25,
						LossModel:     transport.LossModel{Type: "none"},
					},
					NetworkToOrigin: transport.NetworkProfile{
						BaseRTTMs:     50,
						BandwidthMbps: 1000,
						LossModel:     transport.LossModel{Type: "none"},
					},
				},
				{
					ID:        "edge-b",
					GeoTag:    "asia",
					Latitude:  1.4,
					Longitude: 103.9,
					Capacity:  100,
					ClientProfile: transport.NetworkProfile{
						BaseRTTMs:     60,
						BandwidthMbps: 25,
						LossModel:     transport.LossModel{Type: "none"},
					},
					NetworkToOrigin: transport.NetworkProfile{
						BaseRTTMs:     60,
						BandwidthMbps: 1000,
						LossModel:     transport.LossModel{Type: "none"},
					},
				},
			},
		},
		Content: ContentConfig{
			CatalogSize:     10,
			ZipfAlpha:       1.1,
			DurationSeconds: 10,
			SegmentSeconds:  4,
		},
		Clients: ClientConfig{
			Count:  4,
			Policy: "latency_based",
			Geos:   []string{"asia"},
		},
		Protocols: []string{"tcp-h2", "quic-h3"},
		ABR:       "buffer_based",
		CacheConfig: CacheExperConfig{
			Type:      "arc",
			SizeBytes: 64 << 20,
		},
		QUIC: QUICConfig{ZeroRTTRate: 0.85},
		Playback: PlaybackYAMLConfig{
			PrefetchDepth: 2,
		},
		Runs: 1,
		Output: OutputConfig{
			EmitJSON: true,
			EmitCSV:  false,
		},
	}
}

func TestSweepCrossProduct(t *testing.T) {
	dir := t.TempDir()
	cfg := SweepConfig{
		Name:        "cross-product-test",
		Description: "2x2 cross product",
		BaseConfig:  tinyBaseConfig(),
		Parameters: []SweepParameter{
			{
				Name:   "loss_pct",
				Path:   "topology.edges[*].client_profile.loss_model.uniform_percent",
				Values: []float64{0, 3},
			},
			{
				Name:   "rtt_ms",
				Path:   "topology.edges[*].client_profile.base_rtt_ms",
				Values: []float64{20, 100},
			},
		},
		OutputDir: dir,
	}

	res, err := RunSweep(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("RunSweep: %v", err)
	}
	if res.Index == nil || len(res.Index.Results) != 4 {
		t.Fatalf("expected 4 entries, got %+v", res.Index)
	}
	for _, entry := range res.Index.Results {
		cmp := filepath.Join(dir, entry.Subdir, "comparison.json")
		if _, err := os.Stat(cmp); err != nil {
			t.Errorf("missing comparison.json for %s: %v", entry.Subdir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "sweep_index.json")); err != nil {
		t.Errorf("sweep_index.json not written: %v", err)
	}
	heatPath := filepath.Join(dir, "heatmap.json")
	if _, err := os.Stat(heatPath); err != nil {
		t.Fatalf("heatmap.json not written: %v", err)
	}
	data, err := os.ReadFile(heatPath)
	if err != nil {
		t.Fatal(err)
	}
	var hm HeatmapData
	if err := json.Unmarshal(data, &hm); err != nil {
		t.Fatalf("unmarshal heatmap: %v", err)
	}
	if len(hm.Values) != 2 || len(hm.Values[0]) != 2 {
		t.Fatalf("expected 2x2 Values, got %dx%d", len(hm.Values), len(hm.Values[0]))
	}
	if hm.XLabel != "loss_pct" || hm.YLabel != "rtt_ms" {
		t.Errorf("unexpected labels %s/%s", hm.XLabel, hm.YLabel)
	}
}

func TestSweepDeterministic(t *testing.T) {
	makeCfg := func(dir string) SweepConfig {
		return SweepConfig{
			Name:       "determinism",
			BaseConfig: tinyBaseConfig(),
			Parameters: []SweepParameter{
				{
					Name:   "loss_pct",
					Path:   "topology.edges[*].client_profile.loss_model.uniform_percent",
					Values: []float64{0, 2},
				},
				{
					Name:   "rtt_ms",
					Path:   "topology.edges[*].client_profile.base_rtt_ms",
					Values: []float64{20, 80},
				},
			},
			OutputDir: dir,
		}
	}
	r1, err := RunSweep(context.Background(), makeCfg(t.TempDir()), nil)
	if err != nil {
		t.Fatalf("RunSweep 1: %v", err)
	}
	r2, err := RunSweep(context.Background(), makeCfg(t.TempDir()), nil)
	if err != nil {
		t.Fatalf("RunSweep 2: %v", err)
	}
	if r1.Heatmap == nil || r2.Heatmap == nil {
		t.Fatal("heatmap missing")
	}
	if !reflect.DeepEqual(r1.Heatmap.Values, r2.Heatmap.Values) {
		t.Fatalf("heatmap values differ:\n%v\n%v", r1.Heatmap.Values, r2.Heatmap.Values)
	}
}

func TestApplyOverrideUniformLoss(t *testing.T) {
	cfg := tinyBaseConfig()
	if err := applyOverride(&cfg, "topology.edges[*].client_profile.loss_model.uniform_percent", 5); err != nil {
		t.Fatal(err)
	}
	for i, e := range cfg.Topology.Edges {
		if e.ClientProfile.LossModel.Type != "uniform" {
			t.Errorf("edge %d: loss_model.type = %q, want uniform", i, e.ClientProfile.LossModel.Type)
		}
		if e.ClientProfile.LossModel.UniformPercent != 5 {
			t.Errorf("edge %d: uniform_percent = %v", i, e.ClientProfile.LossModel.UniformPercent)
		}
	}
}

func TestApplyOverrideRTT(t *testing.T) {
	cfg := tinyBaseConfig()
	if err := applyOverride(&cfg, "topology.edges[*].client_profile.base_rtt_ms", 200); err != nil {
		t.Fatal(err)
	}
	for i, e := range cfg.Topology.Edges {
		if e.ClientProfile.BaseRTTMs != 200 {
			t.Errorf("edge %d: base_rtt_ms = %v", i, e.ClientProfile.BaseRTTMs)
		}
	}
}

func TestApplyOverrideUnknownPath(t *testing.T) {
	cfg := tinyBaseConfig()
	err := applyOverride(&cfg, "topology.edges[*].does_not_exist", 1)
	if err == nil {
		t.Fatal("expected error for unknown path")
	}
}
