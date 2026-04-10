# cdn-sim Code Map

Understand the full system in 15 minutes by following the execution flow.

## Entry point: `cmd/cdnsim/main.go`

Parses CLI subcommands (`run`, `validate`, `sweep`, `analyze`), loads YAML config via `experiment.LoadConfig`, dispatches to the appropriate runner. Supports `--profile` for CPU/memory profiling.

## The experiment loop: `internal/experiment/runner.go`

This is the orchestrator. For each (protocol x run):

1. **Seeds RNG** deterministically from `config.Seed + runIdx` (no protocol salt — both protocols see the same loss patterns for fair comparison)
2. **Builds topology**: edges with per-edge caches, optional origin shield
3. **Generates content catalog**: Zipf-distributed popularity via `internal/cache/popularity.go`
4. **Creates client population**: geo-distributed, sorted by ID for deterministic iteration
5. **Routes each client** to an edge via `internal/routing/` (anycast)
6. **Runs playback sessions**: manifest generation, transport construction, ABR-driven fetch loop
7. **Collects metrics** into `internal/metrics/collector.go`

Two modes: `runOne` (modeled — pure Go transport models) and `runOneEmulated` (real HTTP/2+HTTP/3 via Docker).

## The playback loop: `internal/video/player.go`

The heart of the simulator. For each batch of `PrefetchDepth` segments:

1. **ABR selects bitrate** → `abr_buffer.go` (BBA with danger zones) or `abr_throughput.go` (EWMA)
2. **Check edge cache** → `internal/cache/arc.go` or `lru.go`
3. **On miss**: check shield → `internal/cdn/shield.go`
4. **On shield miss**: fetch from origin via transport
5. **Transport layer** → `internal/transport/modeled/tcp.go` or `quic.go` (or emulated equivalents)
6. **Update buffer**, detect rebuffer, record per-segment metrics
7. **Compute throughput** (excluding cache-hit bytes) for ABR feedback

Key design: `fetchTime = max(batch latencies)`. Buffer drains by `fetchTime` during playback. Batch enters buffer as `batchSize * segmentDuration`.

## The transport model: `internal/transport/modeled/`

- **`loss.go`**: Gilbert-Elliott two-state Markov chain (GOOD/BAD). NoLoss and UniformLoss alternatives. Loss simulator has its own child RNG isolated from jitter draws.
- **`congestion.go`**: Reno slow-start (cwnd doubles per RTT) + congestion avoidance (cwnd += MSS per RTT). Loss triggers ssthresh = cwnd/2.
- **`tcp.go`**: **THE key differentiator**. `FetchConcurrent` at 0% loss: each stream completes proportionally (share x connTransferTime). Under loss: ALL streams wait for the full connTransferTime because TCP delivers bytes in-order — a gap anywhere blocks everything after it. This binary model captures the essential HOL-blocking physics.
- **`quic.go`**: `FetchConcurrent` uses one SHARED congestion controller (not per-stream). Each stream gets share x connTransferTime + per-stream loss recovery (only affected stream pays retransmit RTT). HOLBlockEvents is always 0.
- **`bandwidth.go`**: 3-state Markov bandwidth trace (LOW/MED/HIGH) for mobile variability.
- **`jitter.go`**: Half-normal jitter via Box-Muller transform.

## The statistical pipeline: `internal/analysis/` + `internal/metrics/`

- **`analysis/statistics.go`**: Bootstrap CI, Cohen's d (pooled sample variance), Mann-Whitney U (normal approximation), ECDF, BootstrapImprovement (two-sample CI for improvement %)
- **`metrics/collector.go`**: Accumulates PlaybackResults, produces AggregatedMetrics per protocol. Delegates all stat helpers to `internal/analysis` (single source of truth).
- **`metrics/comparison.go`**: EnhancedComparison with 9 metrics x {improvement%, 95% CI, effect size, p-value, significance}. Verdict logic: negligible/moderate/large/dominant.
- **`experiment/summary_pretty.go`**: Box-drawing UTF-8 summary printer with p-value stars.

## The sweep engine: `internal/experiment/sweep.go`

Cross-product of parameter values (e.g. loss_pct x rtt_ms) → `applyOverride` patches config → runs full experiment for each combination → writes `sweep_index.json` + `heatmap.json`.

## Key interfaces (extension points):

| Interface | Package | Add new... |
|---|---|---|
| `transport.Transport` | `internal/transport/` | TCP/QUIC variants, SRT, WebRTC |
| `routing.RoutingPolicy` | `internal/routing/` | Anycast policies |
| `video.ABRAlgorithm` | `internal/video/` | Bitrate selection algorithms |
| `cache.Cache` | `internal/cache/` | Eviction policies |
| `modeled.LossSimulator` | `internal/transport/modeled/` | Loss models |

## File tree (abridged)

```
cmd/cdnsim/              CLI entry point
cmd/origin-server/       Real HTTP/2 + HTTP/3 origin
cmd/edge-server/         Real HTTP/2 + HTTP/3 caching edge
internal/
  analysis/              Bootstrap, Cohen's d, Mann-Whitney, ECDF
  cache/                 LRU, ARC, Zipf popularity
  cdn/                   Origin shield
  experiment/            Config, runner, sweep, reporter, validation, reference
  metrics/               Aggregation, enhanced comparison
  routing/               Latency/weighted/geo/BGP policies, haversine
  serverapi/             Shared HTTP API contract
  servertls/             Self-signed cert generation
  transport/             Transport interface
  transport/modeled/     Gilbert-Elliott, Reno, TCP/QUIC models
  transport/emulated/    Real HTTP/2+HTTP/3 clients, CPU tracker, TCP_INFO
  video/                 Manifest, ABR (BBA + throughput), playback session
configs/                 YAML scenarios
docker/                  Dockerfiles, compose, certs
scripts/                 netem, analysis (matplotlib + Chart.js)
docs/                    ADRs, codemap, executive summary
test/                    Integration + determinism
```
