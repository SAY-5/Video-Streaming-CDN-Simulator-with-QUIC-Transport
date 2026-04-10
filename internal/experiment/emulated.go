package experiment

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"time"

	"github.com/cdn-sim/cdn-sim/internal/cache"
	"github.com/cdn-sim/cdn-sim/internal/metrics"
	"github.com/cdn-sim/cdn-sim/internal/routing"
	"github.com/cdn-sim/cdn-sim/internal/transport"
	emtransport "github.com/cdn-sim/cdn-sim/internal/transport/emulated"
	"github.com/cdn-sim/cdn-sim/internal/video"
)

// runEmulated executes the scenario against the real HTTP/2 and HTTP/3
// edge servers described by cfg.Emulated. It reuses the modeled-mode
// infrastructure for client generation, popularity sampling, manifest
// generation, ABR, and PlaybackSession — only the transport changes and
// the local cache is disabled so every fetch crosses the wire.
func (r *Runner) runEmulated(ctx context.Context) (*ExperimentResults, error) {
	start := time.Now()
	collector := metrics.NewCollector()

	if r.config.Emulated == nil || len(r.config.Emulated.Edges) == 0 {
		return nil, fmt.Errorf("emulated mode requires emulated.edges")
	}

	tlsCfg, err := buildEmulatedTLSConfig(r.config.Emulated)
	if err != nil {
		return nil, fmt.Errorf("build emulated tls config: %w", err)
	}

	for _, proto := range r.config.Protocols {
		for runIdx := 0; runIdx < r.config.Runs; runIdx++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := r.runOneEmulated(ctx, proto, runIdx, collector, tlsCfg); err != nil {
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
	}
	return results, nil
}

// runOneEmulated is the emulated-mode analogue of runOne. It mirrors the
// modeled structure as closely as possible so result shapes line up for
// cross-validation.
func (r *Runner) runOneEmulated(ctx context.Context, proto string, runIdx int, collector *metrics.Collector, tlsCfg *tls.Config) error {
	protoSalt := int64(0)
	if proto == "quic-h3" {
		protoSalt = 1 << 32
	}
	baseSeed := r.config.Seed + int64(runIdx)*1_000_003 + protoSalt
	rngMaster := rand.New(rand.NewSource(baseSeed))

	clients := buildClients(r.config.Clients, rngMaster)
	edges := append([]routing.EdgePoP(nil), r.config.Topology.Edges...)

	policy := buildPolicy(r.config.Clients)
	popularity := cache.NewZipfPopularity(r.config.Content.CatalogSize, r.config.Content.ZipfAlpha, rand.New(rand.NewSource(baseSeed+31)))
	abr := buildABR(r.config.ABR)

	sort.SliceStable(clients, func(i, j int) bool { return clients[i].ID < clients[j].ID })

	emuEdges := r.config.Emulated.Edges

	// Track transports to close QUIC ones at the end of the run.
	var quicCloses []func() error
	defer func() {
		for _, c := range quicCloses {
			_ = c()
		}
	}()

	for i, client := range clients {
		edge, err := policy.Route(client, edges, rand.New(rand.NewSource(baseSeed+int64(hash(client.ID)))))
		if err != nil {
			return err
		}
		for j := range edges {
			if edges[j].ID == edge.ID {
				edges[j].CurrentLoad++
				break
			}
		}

		// Round-robin pick an emulated edge by client index.
		emuEdge := emuEdges[i%len(emuEdges)]

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

		sessTransport, closer, err := buildEmulatedTransport(proto, emuEdge, tlsCfg)
		if err != nil {
			return err
		}
		if closer != nil {
			quicCloses = append(quicCloses, closer)
		}

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
			// Cache is intentionally nil in emulated mode: the real edge
			// server's in-memory cache handles hits transparently, so the
			// simulator-local cache must be disabled to avoid short-
			// circuiting the transport.
			Cache:     nil,
			Shield:    nil, // emulated mode: real edge handles its own upstream chain
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

// buildEmulatedTLSConfig constructs a *tls.Config for the emulated edges.
//
// Precedence:
//  1. If CACertPath is non-empty, load it into RootCAs and verify. InsecureTLS
//     is ignored in this path — supplying a CA cert AND requesting insecure
//     is contradictory and almost certainly unintended, so we return an error
//     instead of silently discarding the pool (this was the bug fixed in
//     review round R1, CRIT-2).
//  2. If CACertPath is empty, require InsecureTLS: true. Without it, we fail
//     loudly so the operator sees what's happening and cannot accidentally
//     ship a lax TLS posture under the illusion that the default is safe.
func buildEmulatedTLSConfig(emu *EmulatedConfig) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13}
	if emu.CACertPath != "" {
		pem, err := os.ReadFile(emu.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("read ca cert %s: %w", emu.CACertPath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("parse ca cert %s", emu.CACertPath)
		}
		cfg.RootCAs = pool
		if emu.InsecureTLS {
			return nil, fmt.Errorf("ca_cert_path and insecure_tls are mutually exclusive; pick one")
		}
		return cfg, nil
	}
	if !emu.InsecureTLS {
		return nil, fmt.Errorf("emulated TLS requires either ca_cert_path or an explicit insecure_tls: true")
	}
	cfg.InsecureSkipVerify = true
	return cfg, nil
}

// buildEmulatedTransport returns a real transport targeting the given
// emulated edge. For quic-h3 it also returns a closer function so the
// caller can release UDP sockets at the end of the run.
func buildEmulatedTransport(proto string, edge EmulatedEdgeConfig, tlsCfg *tls.Config) (transport.Transport, func() error, error) {
	switch proto {
	case "quic-h3":
		t := emtransport.NewEmulatedQUICTransport(edge.H3URL, tlsCfg)
		return t, t.Close, nil
	case "tcp-h2":
		t := emtransport.NewEmulatedTCPTransport(edge.H2URL, tlsCfg)
		return t, nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported protocol %q in emulated mode", proto)
	}
}

