package experiment

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"time"

	"github.com/cdn-sim/cdn-sim/internal/cache"
	"github.com/cdn-sim/cdn-sim/internal/cdn"
	"github.com/cdn-sim/cdn-sim/internal/metrics"
	"github.com/cdn-sim/cdn-sim/internal/routing"
	"github.com/cdn-sim/cdn-sim/internal/transport"
	"github.com/cdn-sim/cdn-sim/internal/transport/modeled"
	"github.com/cdn-sim/cdn-sim/internal/video"
)

// ExperimentResults bundles raw and aggregated outputs from a run.
type ExperimentResults struct {
	Config     ExperimentConfig
	ByProtocol map[string]*metrics.AggregatedMetrics
	Comparison         *metrics.ComparisonReport
	EnhancedComparison *metrics.EnhancedComparison
	RawResults         []video.PlaybackResult
	Duration           time.Duration
}

// Runner orchestrates an experiment.
type Runner struct {
	config ExperimentConfig
	logger *slog.Logger
}

// NewRunner constructs a Runner.
func NewRunner(config ExperimentConfig, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{config: config, logger: logger}
}

// Run executes the experiment for every configured protocol and run index.
func (r *Runner) Run(ctx context.Context) (*ExperimentResults, error) {
	if r.config.Mode == "emulated" {
		return r.runEmulated(ctx)
	}
	start := time.Now()
	collector := metrics.NewCollector()

	for _, proto := range r.config.Protocols {
		for runIdx := 0; runIdx < r.config.Runs; runIdx++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := r.runOne(ctx, proto, runIdx, collector); err != nil {
				return nil, fmt.Errorf("proto=%s run=%d: %w", proto, runIdx, err)
			}
		}
	}

	results := &ExperimentResults{
		Config:     r.config,
		ByProtocol: map[string]*metrics.AggregatedMetrics{},
		RawResults: collector.Results(),
		Duration:   time.Since(start),
	}
	for _, proto := range r.config.Protocols {
		m := collector.Aggregate(proto)
		results.ByProtocol[proto] = &m
	}
	if hasBoth(r.config.Protocols) {
		rng := rand.New(rand.NewSource(r.config.Seed))
		cmp := collector.Compare(r.config.Name, "tcp-h2", "quic-h3", rng)
		results.Comparison = &cmp
		enhRng := rand.New(rand.NewSource(r.config.Seed))
		results.EnhancedComparison = collector.CompareEnhanced(r.config.Name, "tcp-h2", "quic-h3", enhRng)
	}
	return results, nil
}

func hasBoth(ps []string) bool {
	tcp, quic := false, false
	for _, p := range ps {
		if p == "tcp-h2" {
			tcp = true
		}
		if p == "quic-h3" {
			quic = true
		}
	}
	return tcp && quic
}

// runOne executes one (protocol, run_index) slice of the experiment. Results
// are collected in deterministic (client_id) order so concurrency does not
// affect reproducibility.
func (r *Runner) runOne(ctx context.Context, proto string, runIdx int, collector *metrics.Collector) error {
	// Seed RNGs deterministically from (seed, runIdx) WITHOUT a protocol
	// salt. Both TCP and QUIC runs for the same (run, client) pair must
	// see the same client population, same content assignments, and — most
	// critically — the same packet-loss patterns. The ONLY variable between
	// protocols should be the transport's response to that loss (HOL
	// blocking vs per-stream recovery, 2-RTT handshake vs 0-RTT, etc.).
	//
	// The modeled transport constructors derive a child rng for the loss
	// simulator from the first draw of the parent rng, so loss sequences
	// are identical across protocols even though handshake jitter draws
	// consume different numbers of parent-rng draws. See tcp.go and
	// quic.go NewModeled*Transport for the isolation design.
	baseSeed := r.config.Seed + int64(runIdx)*1_000_003
	rngMaster := rand.New(rand.NewSource(baseSeed))

	// Build clients with deterministic locations + geo tags.
	clients := buildClients(r.config.Clients, rngMaster)
	// Build edges (copy so we can mutate CurrentLoad locally).
	edges := append([]routing.EdgePoP(nil), r.config.Topology.Edges...)
	// Initialize edge caches, keyed per edge.
	edgeCaches := make(map[string]cache.Cache, len(edges))
	for _, e := range edges {
		edgeCaches[e.ID] = newCache(r.config.CacheConfig)
	}
	// Shield (one per experiment — in the model it is regional and shared).
	var shield *cdn.OriginShield
	if r.config.Shield != nil {
		originTransport := buildTransport(proto, r.config.Topology.OriginNetwork, rand.New(rand.NewSource(baseSeed+17)), r.config)
		sCache := cache.NewLRUCache(r.config.Shield.SizeBytes)
		shield = cdn.NewOriginShield(sCache, originTransport, r.config.Shield.TTLSeconds)
	}

	// Routing policy.
	policy := buildPolicy(r.config.Clients)

	// Zipf popularity generator.
	popularity := cache.NewZipfPopularity(r.config.Content.CatalogSize, r.config.Content.ZipfAlpha, rand.New(rand.NewSource(baseSeed+31)))

	// ABR algorithm.
	abr := buildABR(r.config.ABR)

	// Optionally warm up caches: put the top-K content into every edge cache.
	if r.config.CacheConfig.WarmUp {
		warmUpCaches(edges, edgeCaches, r.config)
	}

	// Sort clients by ID for deterministic iteration.
	sort.SliceStable(clients, func(i, j int) bool { return clients[i].ID < clients[j].ID })

	for _, client := range clients {
		edge, err := policy.Route(client, edges, rand.New(rand.NewSource(baseSeed+int64(hash(client.ID)))))
		if err != nil {
			return err
		}
		for i := range edges {
			if edges[i].ID == edge.ID {
				edges[i].CurrentLoad++
				break
			}
		}

		contentIdx := popularity.NextIndex()
		contentID := fmt.Sprintf("content-%d", contentIdx)

		manifestRNG := rand.New(rand.NewSource(int64(contentIdx+1) * 9973))
		manifest := video.GenerateManifest(
			contentID,
			time.Duration(r.config.Content.DurationSeconds)*time.Second,
			time.Duration(r.config.Content.SegmentSeconds)*time.Second,
			nil,
			manifestRNG,
		)

		clientProfile := edge.ClientProfile
		if clientProfile.BandwidthMbps == 0 {
			clientProfile = r.config.Topology.OriginNetwork
		}
		sessTransport := buildTransport(proto, clientProfile, rand.New(rand.NewSource(baseSeed+int64(hash(client.ID+"-tr")))), r.config)

		pbCfg := video.DefaultPlaybackConfig()
		if r.config.Playback.MaxBufferSeconds > 0 {
			pbCfg.MaxBuffer = time.Duration(r.config.Playback.MaxBufferSeconds) * time.Second
		}
		if r.config.Playback.StartupThresholdSecs > 0 {
			pbCfg.StartupThreshold = time.Duration(r.config.Playback.StartupThresholdSecs) * time.Second
		}
		if r.config.Playback.PrefetchDepth > 0 {
			pbCfg.PrefetchDepth = r.config.Playback.PrefetchDepth
		}
		sess := &video.PlaybackSession{
			Manifest:  manifest,
			ABR:       abr,
			Transport: sessTransport,
			Cache:     edgeCaches[edge.ID],
			Shield:    shield,
			Profile:   clientProfile,
			RNG:       rand.New(rand.NewSource(baseSeed + int64(hash(client.ID+"-abr")))),
			Config:    pbCfg,
			ContentID: contentID,
		}
		pr, err := sess.RunPlayback(ctx)
		if err != nil {
			return err
		}
		collector.Add(pr)
	}
	return nil
}

// buildTransport instantiates a modeled transport for proto with the given profile.
func buildTransport(proto string, profile transport.NetworkProfile, rng *rand.Rand, cfg ExperimentConfig) transport.Transport {
	switch proto {
	case "quic-h3":
		q := modeled.NewModeledQUICTransport(profile, rng, cfg.QUIC.ZeroRTTRate)
		if cfg.Bandwidth != nil && cfg.Bandwidth.Enabled {
			bt := modeled.NewSyntheticTrace(cfg.Content.DurationSeconds*2, profile.BandwidthMbps, cfg.Bandwidth.Variability, rng)
			q = q.WithBandwidthTrace(bt)
		}
		return q
	default:
		t := modeled.NewModeledTCPTransport(profile, rng)
		if cfg.Bandwidth != nil && cfg.Bandwidth.Enabled {
			bt := modeled.NewSyntheticTrace(cfg.Content.DurationSeconds*2, profile.BandwidthMbps, cfg.Bandwidth.Variability, rng)
			t = t.WithBandwidthTrace(bt)
		}
		return t
	}
}

func newCache(cfg CacheExperConfig) cache.Cache {
	switch cfg.Type {
	case "arc":
		return cache.NewARCCache(cfg.SizeBytes)
	default:
		return cache.NewLRUCache(cfg.SizeBytes)
	}
}

func buildPolicy(cc ClientConfig) routing.RoutingPolicy {
	switch cc.Policy {
	case "weighted_capacity":
		return routing.WeightedCapacity{}
	case "geo_affinity":
		return routing.GeoAffinity{}
	case "realistic_bgp":
		prob := cc.MisrouteProb
		if prob <= 0 {
			prob = 0.15
		}
		return routing.RealisticBGP{MisrouteProb: prob}
	default:
		return routing.LatencyBased{}
	}
}

func buildABR(name string) video.ABRAlgorithm {
	switch name {
	case "throughput_based":
		return video.NewThroughputBasedABR()
	default:
		return video.NewBufferBasedABR()
	}
}

func buildClients(cc ClientConfig, rng *rand.Rand) []routing.ClientInfo {
	out := make([]routing.ClientInfo, cc.Count)
	geos := cc.Geos
	if len(geos) == 0 {
		geos = []string{"asia", "europe", "us-east", "us-west"}
	}
	for i := 0; i < cc.Count; i++ {
		geo := geos[i%len(geos)]
		lat, lon := geoCenter(geo)
		// Slightly perturb each client's position so edges near the mean
		// don't get identical RTTs.
		lat += (rng.Float64() - 0.5) * 4
		lon += (rng.Float64() - 0.5) * 4
		out[i] = routing.ClientInfo{
			ID:        fmt.Sprintf("client-%04d", i),
			GeoTag:    geo,
			Latitude:  lat,
			Longitude: lon,
		}
	}
	return out
}

func geoCenter(geo string) (float64, float64) {
	switch geo {
	case "asia":
		return 1.35, 103.82 // Singapore
	case "europe":
		return 50.11, 8.68 // Frankfurt
	case "us-east":
		return 40.71, -74.00 // NYC
	case "us-west":
		return 37.77, -122.42 // SF
	case "south-america":
		return -23.55, -46.63 // São Paulo
	case "africa":
		return -26.20, 28.04 // Johannesburg
	default:
		return 0, 0
	}
}

// manifestSeedMultiplier is the per-content-index seed formula shared
// between runOne's session-level manifest generation and warmUpCaches.
// Keeping one constant guarantees warmed entries match the manifest a
// session will request for the same content.
const manifestSeedMultiplier = 9973

// warmUpCaches pre-populates every edge cache with the top-K items from
// the catalog, keyed deterministically. It uses the simulated epoch
// (simEpoch) rather than time.Now() so that TTL-based correctness is
// reproducible across hosts.
func warmUpCaches(edges []routing.EdgePoP, caches map[string]cache.Cache, cfg ExperimentConfig) {
	topN := cfg.Content.CatalogSize / 10
	if topN < 1 {
		topN = 1
	}
	for _, e := range edges {
		c := caches[e.ID]
		for idx := 0; idx < topN; idx++ {
			contentID := fmt.Sprintf("content-%d", idx)
			segDur := time.Duration(cfg.Content.SegmentSeconds) * time.Second
			dur := time.Duration(cfg.Content.DurationSeconds) * time.Second
			manifest := video.GenerateManifest(contentID, dur, segDur, nil, rand.New(rand.NewSource(int64(idx+1)*manifestSeedMultiplier)))
			for seg := 0; seg < manifest.TotalSegments; seg++ {
				rep := manifest.Representations[len(manifest.Representations)/2]
				key := video.SegmentID(contentID, seg, rep.BitrateKbps)
				c.Put(cache.Item{Key: key, SizeBytes: rep.SegmentSizes[seg], Expiry: time.Time{}}, video.SimEpoch())
			}
		}
	}
}

// hash is a small FNV-1a for stable client seed derivation.
func hash(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
