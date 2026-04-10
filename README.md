# cdn-sim

A simulator for comparing HTTP/3 (QUIC) and HTTP/2 (TCP) performance in a video streaming CDN. Written in Go.

I built this to answer a specific question: under what network conditions does switching from TCP to QUIC actually help for video delivery, and by how much? The short answer is that QUIC wins when there's packet loss (above ~1%) because it eliminates head-of-line blocking across multiplexed streams. On clean networks it performs about the same or slightly worse due to userspace CPU overhead. The long answer is the rest of this repo.

## How it works

There are two modes. The modeled mode runs entirely in-process: it simulates TCP and QUIC transports using a Gilbert-Elliott loss model, Reno congestion control, and models the HOL blocking difference between HTTP/2 (connection-level blocking) and HTTP/3 (per-stream recovery). This is fast — 120k segment simulations in about 12 seconds — and fully deterministic. Same seed, same config, bit-identical output.

The emulated mode runs real `quic-go` HTTP/3 and `net/http` HTTP/2 servers inside Docker containers connected by `tc netem`-shaped bridges. Real packets, real loss, real TLS handshakes. Slower and noisier, but it validates that the modeled numbers aren't made up.

Both modes feed into the same metrics pipeline: bootstrap confidence intervals, Cohen's d effect sizes, Mann-Whitney U tests, and a set of matplotlib charts.

## Quick start

```bash
make build
make run-modeled
python3 scripts/analysis/compare.py results/reproduce_35pct
```

The flagship scenario (`configs/reproduce_35pct.yaml`) models 200 clients in Asia fetching video through Singapore and Mumbai edges to a US-East origin. The path has ~3.6% bursty loss at 200ms RTT. With prefetch depth 3, QUIC shows a ~54% p95 segment latency improvement and virtually eliminates rebuffering. The emulated mode cross-validates this: the modeled prediction falls inside the emulated confidence interval.

The output includes a human-readable summary with significance stars, per-segment CSV data, JSON aggregates, and (if matplotlib is installed) four chart PNGs.

## Sweep

The sweep mode runs a cross-product of parameters. `configs/sweep_loss_rtt.yaml` sweeps loss rate (0-7%) against RTT (20-200ms) and produces a heatmap:

```bash
bin/cdnsim sweep --config configs/sweep_loss_rtt.yaml
python3 scripts/analysis/sweep_heatmap.py results/sweep_loss_rtt
```

The heatmap tells you where to deploy HTTP/3 and where not to bother.

## Emulated mode

You need Docker (Colima works well on macOS):

```bash
brew install colima docker docker-compose
colima start --cpu 4 --memory 6 --disk 30
make docker-up
bash scripts/netem/apply_topology.sh harsh_asia
docker compose -f docker/docker-compose.yml exec -T client \
    /app/cdnsim run --config /configs/emulated_lossy.yaml --output-dir /results/emulated_lossy
make docker-down
```

The stack is 5 containers on 3 bridge networks: origin, regional shield, two edge PoPs (Singapore, Mumbai), and a client driver. Each network segment gets its own netem profile so you can shape the access link, regional hop, and intercontinental path independently.

Certs are self-signed ECDSA P-256, mounted at runtime (not baked into image layers). The edge server does real TLS verification against the CA cert when one is provided.

## What I learned building this

The HOL blocking model went through three iterations. The first version applied a flat `lossEvents * RTT` penalty to every stream in a batch, which overstated the effect. The second version made every stream wait for the full connection transfer time, which was correct under loss but wrong at 0% loss (no gap means no blocking). The final version is a binary model: at 0% loss, streams complete proportionally; under loss, all streams wait for the connection. It's a simplification — reality is somewhere between proportional and full-connection depending on where the loss occurs — but it captures the right physics.

I also found bugs in my own ARC cache implementation during code review. Case IV.B in the original Megiddo & Modha paper says you should move T1's LRU to B1 as a ghost entry when the cache is full. My code was deleting it entirely, which meant the ghost list never grew and the adaptive p parameter never learned. The test I wrote to catch this (`TestARCB1GhostHitGrowsP`) would have found the bug on day one if I'd written it first.

The reference data in `internal/experiment/reference.go` cites five sources I actually looked up: Langley et al. 2017 (SIGCOMM), Cloudflare 2020, Meta 2020, Kosek et al. 2021, and Akamai 2023. Kosek 2021 is interesting because it contradicts Langley: in their large-scale measurement, HTTP/3 and HTTP/2 performed about the same under high packet loss once you factor in real website infrastructure. The envelope in reference.go includes both views.

## Limitations

These are real and I don't want to pretend they aren't:

- The congestion model is Reno, not Cubic or BBR. Most production QUIC stacks use Cubic. Reno is simpler to implement and reason about but recovers differently after loss. I don't know whether switching to Cubic would widen or narrow the gap.
- The HOL blocking model is binary (proportional vs full connection). A per-packet model that tracks which bytes belong to which stream would be more accurate but significantly more complex.
- At prefetch depth 1 (single stream fetches), TCP and QUIC produce nearly identical segment latencies because they share the same congestion model. The QUIC advantage only shows up when multiple streams are multiplexed. This is correct behavior, not a bug, but it means the result depends on the prefetch depth knob.
- The loss model is stationary — the Gilbert-Elliott transition probabilities don't change over time. Real network loss is more variable.
- There's no connection migration, no UDP rate limiting by middleboxes, no NIC offload, no kernel bypass. The simulator answers "how much does the transport protocol matter?" not "what will my production improvement be?"
- QUIC's CPU cost is roughly 2x TCP (per Langley 2017, post-optimization). The latency numbers don't account for this.

## Extending

To add a new transport, implement `transport.Transport`. To add a new ABR algorithm, implement `video.ABRAlgorithm`. To add a new cache policy, implement `cache.Cache`. To add a new routing policy, implement `routing.RoutingPolicy`. Each has a registration point in `internal/experiment/runner.go` that you can grep for.

## CLI

```
cdnsim run      --config <yaml> [--output-dir <dir>] [--verbose] [--profile]
cdnsim validate --config <yaml>
cdnsim sweep    --config <yaml> [--output-dir <dir>] [--verbose]
cdnsim analyze  --results-dir <dir>
```

`make` wraps these: `build`, `test`, `run-modeled`, `sweep`, `docker-up`, `docker-down`, `run-emulated`, `analysis`, `full-suite`.

## Testing

```bash
make test  # go test ./... -race -count=1
```

12 packages with tests, all passing under the race detector. Coverage varies: analysis at 85%, cache at 67%, the transport models around 77%. The ARC cache has specific regression tests for the ghost-list bugs found during review. The statistics package has property-based tests for bootstrap coverage probability and Mann-Whitney type-I error rates.

## Docs

- `docs/adr/` has 7 architectural decision records covering the major design choices (why Gilbert-Elliott over uniform loss, why ARC over LRU, why Reno and not Cubic, etc.)
- `docs/CODEMAP.md` walks through the execution flow from CLI to transport model
- `docs/EXECUTIVE_SUMMARY.md` is a one-page summary you could hand to someone who doesn't want to read code

## What's next

I'd like to add Cubic congestion control alongside Reno to see how the comparison changes. A per-packet HOL model (tracking byte-to-stream assignment) would be more accurate than the binary approximation. And the emulated mode could use more sessions for tighter confidence intervals — 24 sessions works for detecting large effects but you'd want 200+ for anything subtle.
