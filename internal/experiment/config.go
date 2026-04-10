// Package experiment wires together topology, content, routing, caches, and
// transports for a parameterised CDN simulation run. It loads YAML scenario
// files, executes deterministic runs, and emits aggregated metrics.
package experiment

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/cdn-sim/cdn-sim/internal/routing"
	"github.com/cdn-sim/cdn-sim/internal/transport"
)

// TopologyConfig describes the PoPs and origin network.
type TopologyConfig struct {
	Edges          []routing.EdgePoP        `yaml:"edges"`
	OriginLocation LatLon                   `yaml:"origin_location"`
	OriginNetwork  transport.NetworkProfile `yaml:"origin_network"`
}

// LatLon is a simple geographic coordinate.
type LatLon struct {
	Latitude  float64 `yaml:"latitude"`
	Longitude float64 `yaml:"longitude"`
}

// ContentConfig defines the content catalog.
type ContentConfig struct {
	CatalogSize     int     `yaml:"catalog_size"`
	ZipfAlpha       float64 `yaml:"zipf_alpha"`
	DurationSeconds int     `yaml:"duration_seconds"`
	SegmentSeconds  int     `yaml:"segment_seconds"`
}

// ClientConfig describes the client population and routing policy.
type ClientConfig struct {
	Count          int      `yaml:"count"`
	Policy         string   `yaml:"policy"` // latency_based, weighted_capacity, geo_affinity, realistic_bgp
	MisrouteProb   float64  `yaml:"misroute_prob,omitempty"`
	Geos           []string `yaml:"geos,omitempty"`
	GeoWeighted    bool     `yaml:"geo_weighted,omitempty"`
	ClientLocations []LatLon `yaml:"client_locations,omitempty"`
}

// CacheExperConfig describes the edge cache configuration.
type CacheExperConfig struct {
	Type         string `yaml:"type"` // lru, arc
	SizeBytes    int64  `yaml:"size_bytes"`
	WarmUp       bool   `yaml:"warm_up"`
	TTLSeconds   int    `yaml:"ttl_seconds"`
}

// ShieldConfig optionally enables a regional origin shield.
type ShieldConfig struct {
	SizeBytes  int64 `yaml:"size_bytes"`
	TTLSeconds int   `yaml:"ttl_seconds"`
}

// QUICConfig tunes the QUIC transport model.
type QUICConfig struct {
	ZeroRTTRate float64 `yaml:"zero_rtt_rate"`
}

// BandwidthConfig enables synthetic bandwidth traces.
type BandwidthConfig struct {
	Enabled     bool    `yaml:"enabled"`
	Variability float64 `yaml:"variability"`
}

// OutputConfig controls result writing.
type OutputConfig struct {
	Dir       string `yaml:"dir"`
	EmitCSV   bool   `yaml:"emit_csv"`
	EmitJSON  bool   `yaml:"emit_json"`
}

// PlaybackYAMLConfig mirrors video.PlaybackConfig for YAML loading.
type PlaybackYAMLConfig struct {
	MaxBufferSeconds     int `yaml:"max_buffer_seconds"`
	StartupThresholdSecs int `yaml:"startup_threshold_seconds"`
	PrefetchDepth        int `yaml:"prefetch_depth"`
}

// EmulatedEdgeConfig describes one real edge server endpoint that the
// emulated runner should target.
type EmulatedEdgeConfig struct {
	ID    string `yaml:"id"`
	H2URL string `yaml:"h2_url"`
	H3URL string `yaml:"h3_url"`
}

// EmulatedConfig groups options used only in emulated mode, where real
// HTTP/2 and HTTP/3 requests are sent to Dockerised edge servers.
type EmulatedConfig struct {
	Edges        []EmulatedEdgeConfig `yaml:"edges"`
	CACertPath   string               `yaml:"ca_cert_path,omitempty"`
	NetemProfile string               `yaml:"netem_profile,omitempty"` // informational only
	InsecureTLS  bool                 `yaml:"insecure_tls,omitempty"`  // default true for self-signed
}

// ExperimentConfig is the top-level YAML schema for a scenario.
type ExperimentConfig struct {
	Name        string             `yaml:"name"`
	Description string             `yaml:"description"`
	Mode        string             `yaml:"mode"` // "modeled" (default) or "emulated"
	Seed        int64              `yaml:"seed"`
	Topology    TopologyConfig     `yaml:"topology"`
	Content     ContentConfig      `yaml:"content"`
	Clients     ClientConfig       `yaml:"clients"`
	Protocols   []string           `yaml:"protocols"`
	ABR         string             `yaml:"abr"`
	CacheConfig CacheExperConfig   `yaml:"cache"`
	Shield      *ShieldConfig      `yaml:"shield,omitempty"`
	QUIC        QUICConfig         `yaml:"quic_settings"`
	Bandwidth   *BandwidthConfig   `yaml:"bandwidth,omitempty"`
	Playback    PlaybackYAMLConfig `yaml:"playback"`
	Runs        int                `yaml:"runs"`
	Output      OutputConfig       `yaml:"output"`
	Emulated    *EmulatedConfig    `yaml:"emulated,omitempty"`
}

// LoadConfig reads an ExperimentConfig from a YAML file.
func LoadConfig(path string) (*ExperimentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg ExperimentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate enforces schema invariants.
func (c *ExperimentConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("experiment name is required")
	}
	if len(c.Protocols) == 0 {
		return fmt.Errorf("at least one protocol required")
	}
	if c.Runs <= 0 {
		c.Runs = 1
	}
	if c.Clients.Count <= 0 {
		return fmt.Errorf("clients.count must be > 0")
	}
	if len(c.Topology.Edges) == 0 {
		return fmt.Errorf("topology.edges must not be empty")
	}
	if c.Content.CatalogSize <= 0 {
		c.Content.CatalogSize = 100
	}
	if c.Content.ZipfAlpha <= 1 {
		c.Content.ZipfAlpha = 1.1
	}
	if c.Content.DurationSeconds <= 0 {
		c.Content.DurationSeconds = 60
	}
	if c.Content.SegmentSeconds <= 0 {
		c.Content.SegmentSeconds = 4
	}
	if c.CacheConfig.Type == "" {
		c.CacheConfig.Type = "lru"
	}
	if c.CacheConfig.SizeBytes <= 0 {
		c.CacheConfig.SizeBytes = 1 << 30
	}
	if c.ABR == "" {
		c.ABR = "buffer_based"
	}
	if c.Clients.Policy == "" {
		c.Clients.Policy = "latency_based"
	}
	if c.Mode == "" {
		c.Mode = "modeled"
	}
	if c.Mode != "modeled" && c.Mode != "emulated" {
		return fmt.Errorf("mode must be 'modeled' or 'emulated', got %q", c.Mode)
	}
	if c.Mode == "emulated" {
		if c.Emulated == nil || len(c.Emulated.Edges) == 0 {
			return fmt.Errorf("emulated mode requires emulated.edges")
		}
		for i, e := range c.Emulated.Edges {
			if e.ID == "" {
				return fmt.Errorf("emulated.edges[%d].id is required", i)
			}
			if e.H2URL == "" || e.H3URL == "" {
				return fmt.Errorf("emulated.edges[%d] requires both h2_url and h3_url", i)
			}
		}
	}
	return nil
}
