# cdn-sim

**Quantify QUIC vs TCP performance for video-streaming CDN deployment decisions.**

`cdn-sim` is a deterministic, reproducible CDN simulator with two coupled execution modes:

1. **Modeled mode** — closed-form TCP/H2 and QUIC/H3 transports with Gilbert-Elliott
   bursty loss, Reno congestion control, per-stream vs connection-level HOL blocking,
   ARC edge caches, Zipf popularity, anycast routing, BBA ABR, origin shield, and
   bandwidth variability. Pure Go, 120,000 segment simulations in under 15 seconds
   on a laptop.
2. **Emulated mode** — real `quic-go` HTTP/3 and `net/http` HTTP/2 servers running
   in Docker containers on bridges shaped by Linux `tc netem`, driven by the same
   experiment runner and producing the same result schema.

Both modes feed the same statistical pipeline: bootstrap 95% CIs, Cohen's d effect
sizes, Mann-Whitney U tests, publication-quality matplotlib charts, and a
drag-and-drop Chart.js dashboard.

```
┌───────────────────────────────────────────────────────────────────────┐
│                              cdn-sim                                  │
│                                                                       │
│   configs/*.yaml ──► experiment.Runner ──► metrics.Collector          │
│                          │                        │                  │
│         ┌────────────────┼──────────────┐         │                  │
│         ▼                ▼              ▼         │                  │
│   modeled.TCP      modeled.QUIC   emulated.TCP    │                  │
│   modeled.QUIC     (loss+Reno)    emulated.QUIC   │                  │
│         │                │              │        │                  │
│         │                │              ▼        │                  │
│         │                │        Docker stack   │                  │
│         │                │        + netem        │                  │
│         │                │                       │                  │
│         ▼                ▼                       ▼                  │
│                                                                       │
│   video.Player ──► edge cache ──► origin shield ──► origin            │
│                    (ARC/LRU)       (optional)                         │
│                                                                       │
│   metrics.EnhancedComparison ──► Bootstrap CI, Cohen's d, Mann-W      │
│                                    │                                  │
│                                    ▼                                  │
│                       summary.json, enhanced_comparison.json,         │
│                       comparison.json, raw.csv, summary.txt           │
│                                    │                                  │
│                                    ▼                                  │
│                       scripts/analysis/compare.py                     │
│                       scripts/analysis/sweep_heatmap.py               │
│                       scripts/analysis/dashboard.html                 │
└───────────────────────────────────────────────────────────────────────┘
```

## Quick start

```bash
make build                                            # compiles bin/cdnsim
make run-modeled                                      # runs configs/reproduce_35pct.yaml
python3 scripts/analysis/compare.py results/reproduce_35pct
```

You will see a summary like this on stdout:

```
════════════════════════════════════════════════════════════════════
CDN-SIM RESULTS: reproduce_35pct
════════════════════════════════════════════════════════════════════

Segment Latency (p95):
  TCP (H2):  9143.5ms
  QUIC (H3): 5407.6ms
  Improvement: 40.9% [95% CI: 36.9% — 44.5%]
  Effect size: 0.48 (small), p < 0.001 ***

Rebuffer Duration:
  TCP (H2):  19776.7ms
  QUIC (H3): 31.3ms
  Improvement: 99.8% [95% CI: 99.8% — 99.9%]
  Effect size: 1.36 (large), p < 0.001 ***

HOL Block Events:
  TCP (H2):  14.7 events/session
  QUIC (H3): 0 events/session (by design)

────────────────────────────────────────────────────────────────────
VERDICT: QUIC is DOMINANT under these network conditions

KEY FINDINGS:
• p95 segment latency improved by 40.9% [CI 36.9% — 44.5%], significant (p < 0.001)
• Rebuffer duration reduced by 99.8%
• Cache behavior is transport-independent
• HOL blocking eliminated by QUIC: TCP averaged 14.7 events/session
════════════════════════════════════════════════════════════════════
```

Four charts (`latency_percentiles.png`, `latency_cdf.png`, `bitrate_timeline.png`,
`improvement_summary.png`) and a drag-and-drop dashboard (`scripts/analysis/dashboard.html`)
let you read the distribution shape, not just the percentiles.

## Reproducing the ~35–45% latency improvement

`configs/reproduce_35pct.yaml` models Asia clients served by Singapore/Mumbai edges
reaching a US-East origin. The path carries:

- 180–220 ms base RTT, 8 ms jitter
- Gilbert-Elliott bursty loss (~3.6% average, heavy-tailed bursts)
- 200 clients × 120 s content × 4 s segments, cold ARC cache, origin shield
- HTTP/2 pipelining at prefetch depth 3 (HOL blocking becomes observable)
- 10 runs for statistical power

Expected outcome with the shipped seed (`20260409`):

| metric                    | TCP       | QUIC      | Δ      | 95% CI          | d     | p        |
|---------------------------|-----------|-----------|--------|-----------------|-------|----------|
| p95 segment latency (ms)  | 9143.5    | 5407.6    | +40.9% | [36.9, 44.5]    | 0.48  | < 0.001  |
| startup latency p95 (ms)  | 571.0     | 359.9     | +37.0% | [33.0, 40.8]    | 0.44  | < 0.001  |
| rebuffer duration (ms)    | 19776.7   | 31.3      | +99.8% | [99.8, 99.9]    | 1.36  | < 0.001  |
| rebuffer count (mean)     | 4.36      | 0.04      | +99.1% | [98.8, 99.3]    | 1.37  | < 0.001  |
| HOL events (per session)  | 14.7      | 0         | n/a    | n/a             | n/a   | n/a      |
| avg bitrate (Kbps)        | 5440      | 5440      | 0%     | transport-neutral      |       |          |
| cache hit rate (%)        | 51.2      | 51.7      | +0.5%  | not significant (p=0.72) |     |          |

Runs in ~12 s for 120,000 segment simulations. Running the same YAML twice produces
bit-identical aggregates — every RNG is a seeded `*rand.Rand`, there is no global
`math/rand` state, and the collector orders results deterministically.

## CLI

```
cdnsim run      --config <yaml> [--output-dir <dir>] [--verbose]
cdnsim validate --config <yaml>
cdnsim sweep    --config <yaml> [--output-dir <dir>] [--verbose]
cdnsim analyze  --results-dir <dir>     # shells out to scripts/analysis/compare.py
```

`make` targets wrap these for common workflows: `build`, `test`, `run-modeled`,
`baseline`, `lossy`, `mobile`, `satellite`, `sweep`, `certs`, `docker-build`,
`docker-up`, `docker-down`, `run-emulated`, `validate`, `analysis`, `full-suite`.

## Running parameter sweeps

`configs/sweep_loss_rtt.yaml` sweeps loss rate × RTT and writes a 2D heatmap
showing exactly where the QUIC advantage lives:

```bash
bin/cdnsim sweep --config configs/sweep_loss_rtt.yaml
python3 scripts/analysis/sweep_heatmap.py results/loss-rtt-sweep
```

Output on the shipped 5 × 4 grid (20 combinations, 4800 sessions in ~5 s):

```
                 loss_pct
rtt_ms      0        1      3      5      7
  20    -159.1%  +45.2%  +25.4% +22.7% +24.3%
  50    -214.4%  +18.4%  +44.9% +40.6% +42.6%
 100    -173.9%  +22.5%  +22.0% +22.9% +37.9%
 200    -218.6%  +13.2%  +37.4% +38.1% +43.2%
```

This is the deployment-decision chart. At 0% loss the userspace QUIC stack
pays a clear CPU tax; once loss crosses ~1% the HOL-blocking elimination
dominates and QUIC wins 13–45% at p95. The column at loss=0 is not a bug —
it is the answer to "should we deploy HTTP/3 on a clean fiber link in our
home datacenter?" (No.) The columns at loss ≥ 1% are the answer to "should
we deploy HTTP/3 for Asia users crossing the Pacific to our origin?" (Yes.)

## Emulated mode (real Docker + netem)

Phase 2 ships a real-socket validation path: a containerised origin, a
regional shield, two edge PoPs, and a client driver wired together with Linux
`netem` on loopback bridges. The same `cdnsim` binary drives both modes —
the YAML `mode:` field picks modeled or emulated.

### Prerequisites

- **Docker** with Compose v2. On macOS, Colima is recommended over Docker
  Desktop because it exposes the host kernel to `tc qdisc`. Install:
  ```bash
  brew install colima docker docker-compose
  colima start --cpu 4 --memory 6 --disk 30
  ```
- **openssl** on the host (only for `make certs`, which generates the
  self-signed CA and leaf certs consumed by the servers).
- A few GB of RAM. Emulated mode is resource-intensive; keep `clients:`
  small (≤20 for first runs) until you have validated that your host can
  sustain netem-induced packet delay without dropping.

### Quick start

```bash
make docker-up         # certs → build → up → healthcheck
bash scripts/netem/apply_topology.sh asia_deployment
docker compose -f docker/docker-compose.yml run --rm \
    -v "$(pwd)/configs:/configs:ro" -v "$(pwd)/results:/results" \
    client run --config /configs/emulated_lossy.yaml \
               --output-dir /results/emulated_lossy
make docker-down
```

### Network topology

Three user-defined bridge networks isolate the client↔edge, edge↔shield,
and shield↔origin legs so each can carry its own netem profile:

```
                                                     origin-net 172.20.0.0/24
                                                     ┌────────────────────┐
                                                     │  origin   .10      │
                                                     │  (HTTP/2 + HTTP/3) │
                                                     └─────────┬──────────┘
                                                               │ netem
                                  shield-net 172.21.0.0/24     │ shield↔origin
                                  ┌────────────────────────────┴─────┐
                                  │  shield    .20                    │
                                  │  (regional cache, H2/H3 server)   │
                                  └───┬────────────────────────────┬──┘
                                      │ netem: edge↔shield         │
          client-net 172.22.0.0/24    │                            │
          ┌──────────────┐            │                            │
          │  edge-sg    .30 ──────────┘                            │
          │  edge-mumbai.40 ───────────────────────────────────────┘
          │  client    .100 ── netem: client↔edge (access link)
          └──────────────┘
```

`scripts/netem/apply_topology.sh` wraps `tc qdisc` to install an
`asia_deployment` profile (high-RTT intercontinental origin link plus
lossy last mile) on the appropriate interfaces inside the containers.
Other profiles live in `scripts/netem/apply.sh`: `baseline`, `lossy`,
`high_loss`, `mobile_3g`, `satellite`.

### Cross-validation

`internal/experiment/validate.go` compares modeled aggregates against
emulated aggregates for the same scenario. Target tolerance is **p50
within 20%** and **p95/p99 within 30%**; anything wider indicates a
mismatch between the modeled loss/RTT knobs and what netem actually
delivered on the wire, and should be investigated before any numbers
ship. Reference ranges from published deployments (Google HTTP/3 at
scale, Cloudflare, Facebook, Fastly) live in
`internal/experiment/reference.go`; a helper emits warnings when the
observed improvement falls outside the published envelope for a
comparable loss/RTT condition.

### CPU overhead analysis

Segment results carry a `CPUTime` field. In modeled mode it is left at
zero; in emulated mode it is read from `getrusage(RUSAGE_SELF)`. The
value matters at scale because QUIC's latency win over TCP can be
partially consumed by its 2–4× higher CPU-per-byte cost on commodity
cores — **the improvement % you report for p95 latency is only
meaningful alongside a CPU budget**. The emulated runs already show
this: on a clean network with no loss (the default state inside a
lossless Docker bridge before netem is applied), QUIC is 2–4× slower
than TCP at p95. The whole point of running emulated mode under netem
is to prove that the HOL-blocking elimination more than compensates
for the CPU tax once the network degrades to realistic conditions.

### Known limitations

- `GetTCPInfo` is a stub on every platform — Linux-only TCP_INFO
  extraction via `golang.org/x/sys/unix` is a deferred follow-up. Kernel
  retransmit and cwnd counters therefore show zero in the emulated
  results; the latency measurements themselves are unaffected.
- 0-RTT in emulated mode is **implicit** in `quic-go`: there is no
  toggle, and whether a given connection actually sends early data
  depends on whether a session ticket from a prior connection is still
  valid. `quic_settings.zero_rtt_rate` remains a modeled-only knob.
- Emulated mode is resource-intensive. Start with `clients: 10` and
  `runs: 3` and scale up gradually; netem packet queues grow quickly on
  overloaded hosts and can themselves distort the results you are
  trying to measure.

## Configuration reference

Top-level YAML keys (full schema in `internal/experiment/config.go`):

| key             | type     | notes |
|-----------------|----------|-------|
| `name`          | string   | required; used as output subdirectory |
| `description`   | string   | free-form |
| `seed`          | int64    | seeds every RNG; same seed → same results |
| `mode`          | string   | `modeled` (default) or `emulated` |
| `topology`      | object   | edge PoPs and origin network profile |
| `content`       | object   | catalog size, Zipf alpha, duration, segment length |
| `clients`       | object   | count, routing policy, geo tags |
| `protocols`     | [string] | `tcp-h2`, `quic-h3`, or both |
| `abr`           | string   | `buffer_based` or `throughput_based` |
| `cache`         | object   | `lru` or `arc`, byte size, TTL, warm-up flag |
| `shield`        | object?  | optional regional shield config |
| `quic_settings` | object   | `zero_rtt_rate` success probability |
| `bandwidth`     | object?  | optional synthetic variability trace |
| `playback`      | object   | `prefetch_depth`, buffer thresholds |
| `emulated`      | object?  | real edge URLs (emulated mode only) |
| `runs`          | int      | repetitions for statistical power |
| `output`        | object   | directory, CSV/JSON toggles |

Network profile:

```yaml
base_rtt_ms: 200
bandwidth_mbps: 1000
jitter_ms: 8
loss_model:
  type: gilbert_elliott     # or "uniform", "none"
  p_good_to_bad: 0.03
  p_bad_to_good: 0.30
  loss_in_bad_state: 0.40
```

## Design decisions

**Gilbert-Elliott loss.** Uniform-loss models understate the damage TCP
suffers because they don't generate the clustered drops that fast
retransmit and congestion avoidance handle poorly. Gilbert-Elliott
reproduces the real-world observation that packet drops come in bursts
(BAD-state residency) separated by quiet periods (GOOD-state residency).
This is what makes QUIC's per-stream recovery look like a structural
win rather than a rounding error.

**ARC cache.** LRU pollutes under sequential scans — any one-shot
content cycles through and evicts frequently-watched items. ARC's
four-list design keeps recency (T1) and frequency (T2) separate and
uses ghost-list hits to adapt the partitioning. For Zipf-distributed
video popularity this gives 10–30% better hit rates than LRU at the
same byte budget, and the scan-resistance is visible in the tests.

**Buffer-based ABR with danger zones.** Pure throughput-based ABR
oscillates and starves during bandwidth dips. BBA treats the current
buffer level as a proxy for future bandwidth and steps the bitrate up
and down with hysteresis. The CRITICAL/DANGER/COMFORT/SURPLUS zones
match how real players (Netflix BBA, Twitch) behave.

**Pipelined prefetch.** Real video players fetch multiple segments
ahead of the playhead over a shared HTTP/2 or QUIC connection. Without
pipelining, TCP and QUIC look nearly identical — the QUIC advantage
only appears when the head-of-line blocking regime is actually
exercised. `playback.prefetch_depth` controls the pipeline width and
is the single most important knob for reproducing the QUIC improvement.

**Bootstrap CIs and effect sizes.** Latency distributions are not
normal. Bootstrap resampling (1000 iterations) produces honest 95%
confidence intervals without assuming anything about the shape; Cohen's
d puts a number on "how different are these two distributions,
standardised by their own spread"; Mann-Whitney U is a non-parametric
rank test that doesn't care about tails. Every headline number in the
comparison report carries all three.

**Determinism.** Every RNG is a seeded `*rand.Rand` — there is no
global `math/rand` state. Client iteration is sorted by ID, collector
results are stable, and bootstrap resampling seeds from `config.seed`.
Two runs of the same YAML produce bit-identical aggregates.

## Extending the simulator

- **New routing policy** — implement `routing.RoutingPolicy` and
  register it in `experiment.buildPolicy`.
- **New ABR** — implement `video.ABRAlgorithm` and register it in
  `experiment.buildABR`.
- **New transport** — implement `transport.Transport`. Modeled
  transports live in `internal/transport/modeled/`; real-socket
  transports live in `internal/transport/emulated/`.
- **New loss model** — implement `modeled.LossSimulator` and register
  it in `modeled.NewLossSimulator`.
- **New sweep parameter** — add a supported path in
  `experiment.applyOverride` in `internal/experiment/sweep.go`.

## Package layout

```
cmd/cdnsim/              CLI: run, validate, sweep, analyze
cmd/origin-server/       Real HTTP/2 + HTTP/3 origin (quic-go + net/http)
cmd/edge-server/         Real HTTP/2 + HTTP/3 caching edge/shield
internal/analysis/       Bootstrap CI, Cohen's d, Mann-Whitney U, ECDF
internal/cache/          LRU, ARC, Zipf popularity
internal/cdn/            Origin shield composition
internal/experiment/     Config, runner, sweep, reporter, validation
internal/metrics/        Aggregation, legacy + enhanced comparison
internal/routing/        Latency / weighted / geo / BGP anycast policies
internal/serverapi/      Shared HTTP API contract between servers + client
internal/servertls/      Self-signed cert generation for emulated mode
internal/transport/      Transport interface
internal/transport/modeled/   Gilbert-Elliott, Reno, modeled TCP & QUIC
internal/transport/emulated/  Real HTTP/2 + HTTP/3 client transports
internal/video/          Manifest, ABR (throughput + BBA), playback session
test/                    Integration + determinism
configs/                 Scenario YAMLs (modeled + emulated + sweep)
docker/                  Dockerfiles, compose, cert generation
scripts/netem/           netem profile application scripts
scripts/analysis/        matplotlib + Chart.js visualisation
```

## Testing

```bash
make test        # full race-detection suite
make test-short  # shorter run for CI
```

Every package passes `go test ./... -race -count=1`. The integration
suite runs a baseline scenario end-to-end; the determinism suite
asserts that two back-to-back runs of the same config produce identical
per-protocol aggregates.

## Phase 4 roadmap

- Linux-only `GetTCPInfo` via `golang.org/x/sys/unix` for real kernel
  retransmit counters in emulated mode.
- Explicit `quic-go` session-ticket priming to measure 0-RTT empirically.
- eBPF-based per-flow diagnostics inside the emulated containers.
- Concurrent sweep execution with a worker pool.
- Grafana + Prometheus exporter for live dashboards when running large
  cross-region experiments.

## Citation

If you use cdn-sim in a paper or internal memo, please cite it as:

```
cdn-sim: a deterministic modeled + emulated CDN simulator for
quantifying QUIC vs TCP tradeoffs in video streaming.
https://github.com/cdn-sim/cdn-sim
```
