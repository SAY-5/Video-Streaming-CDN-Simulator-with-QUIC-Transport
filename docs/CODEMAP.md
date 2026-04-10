# How the code is organized

If you want to understand how this works, start at `cmd/cdnsim/main.go` and follow the calls down. Here's the path.

## CLI → runner

`cmd/cdnsim/main.go` parses flags, loads a YAML config, and hands it to `experiment.Runner`. The runner loops over (protocol, run index) pairs. For each pair it builds a topology (edges with caches, an optional shield, a popularity generator), creates clients, routes them to edges, and runs playback sessions. Modeled mode (`runOne`) uses in-process transport models; emulated mode (`runOneEmulated`) makes real HTTP requests to Docker containers.

## The playback session

`internal/video/player.go` is where most of the interesting behavior lives. It fetches segments in batches of `PrefetchDepth`:

1. The ABR algorithm picks a bitrate based on current buffer level and throughput history. `abr_buffer.go` (BBA with danger zones) is the default; `abr_throughput.go` (EWMA with safety margin) is the alternative.
2. The player checks the edge cache for each segment in the batch. Hits return in ~1ms.
3. Misses go through the shield (if present) and then the transport.
4. The transport returns per-segment latencies. The batch's wall-clock cost is the max of these latencies — all segments arrive together since they're fetched concurrently.
5. The player drains the buffer by the fetch time (playback continued during the fetch), adds the batch's worth of video duration, and checks for rebuffering.
6. Throughput is computed from miss-only bytes to avoid inflating the estimate with cache hits.

## Transport models

These live in `internal/transport/modeled/` and this is where the TCP-vs-QUIC difference actually happens.

`loss.go` implements a Gilbert-Elliott two-state Markov chain. The loss simulator has its own child RNG isolated from the jitter RNG so that both protocols see identical loss sequences for the same client and run.

`congestion.go` models Reno: slow-start doubling per RTT, congestion avoidance adding 1 MSS per RTT, halving on loss. It returns the number of RTTs needed to transfer N bytes given a list of loss offsets.

`tcp.go` `FetchConcurrent` is the core of the HOL blocking model. At 0% loss, each stream completes at its proportional share of the connection time — same as QUIC. Under loss, all streams wait for the full connection time because TCP delivers bytes in order and a gap blocks everything after it. This binary model is a simplification (reality depends on where the loss falls relative to each stream's bytes) but gets the direction right.

`quic.go` `FetchConcurrent` uses a shared congestion controller across all streams (matching real quic-go behavior) but per-stream loss recovery. A loss on stream A adds one RTT to stream A without affecting stream B. This is the HOL blocking difference.

## Statistics

`internal/analysis/statistics.go` has the math: bootstrap resampling, Cohen's d with pooled sample variance, Mann-Whitney U with normal approximation, ECDF. `internal/metrics/collector.go` accumulates results and delegates to these functions. `internal/metrics/comparison.go` produces the enhanced comparison with CIs, effect sizes, p-values, and a verdict label.

## Caches

`internal/cache/arc.go` implements the full ARC algorithm from Megiddo & Modha 2003, including ghost lists (B1/B2) for adaptive partition sizing. There's a bug story here: the original implementation had a broken Case IV.B where evicted items weren't moved to the ghost list, so the adaptive parameter never learned. Regression tests for the ghost-list behavior are in `arc_ghost_test.go`.

## Sweep

`internal/experiment/sweep.go` takes a base config and a list of parameters to vary, generates the cross-product, runs each combination, and writes a `heatmap.json` for the Python visualization script to consume.

## Extension points

If you want to add something, these are the interfaces to implement:

- `transport.Transport` — new transport protocol
- `routing.RoutingPolicy` — new anycast policy
- `video.ABRAlgorithm` — new bitrate selection
- `cache.Cache` — new eviction policy
- `modeled.LossSimulator` — new loss model

Each has a builder function in `runner.go` where you register the new implementation.
