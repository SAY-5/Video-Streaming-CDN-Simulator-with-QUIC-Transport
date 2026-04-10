# ADR-001: Dual-Mode Architecture (Modeled + Emulated)

## Status
Accepted

## Context

The CDN simulator needs to answer a quantitative question: "Under what network conditions does HTTP/3 (QUIC) deliver measurably better video streaming QoE than HTTP/2 (TCP)?" There are two fundamentally different ways to answer this:

1. **Modeled simulation** -- build deterministic mathematical models of congestion control, packet loss, HOL blocking, and handshake latency, then run thousands of sessions in seconds. This gives tight control over every variable and perfect reproducibility, but the models are simplifications of reality.

2. **Emulated validation** -- run real HTTP/2 and HTTP/3 servers in Docker containers, shape traffic with `tc netem`, and measure actual protocol behavior on the wire. This captures the full complexity of real protocol stacks (quic-go, Go's net/http2) but is slow, non-deterministic, and sensitive to host CPU scheduling.

Neither mode alone is sufficient. Modeled-only results can be dismissed as "just a model." Emulated-only results are hard to reproduce and expensive to sweep across parameter spaces. We need both, and they need to share as much infrastructure as possible so we can cross-validate.

The core design constraint is that all CDN logic -- the video player, ABR algorithm, cache, origin shield, routing, and metrics collection -- must be agnostic to which mode is active. Only the transport layer changes between modes.

## Decision

We define a single `transport.Transport` interface (in `internal/transport/transport.go`) with three methods: `FetchSegment`, `FetchConcurrent`, and `Handshake`. All CDN simulation logic talks exclusively through this interface.

Two implementations exist behind this boundary:

- **`internal/transport/modeled/`**: `ModeledTCPTransport` and `ModeledQUICTransport` use deterministic RNG-driven models of congestion control (Reno), packet loss (Gilbert-Elliott or uniform), and jitter (half-normal). They require no network, no Docker, no sockets.

- **`internal/transport/emulated/`**: `EmulatedTCPTransport` and `EmulatedQUICTransport` make real HTTP requests to Dockerized edge servers over `tc netem`-shaped links. They measure actual TTFB, goodput, and CPU time from the kernel/quic-go.

The experiment runner (`internal/experiment/runner.go`) checks `cfg.Mode` and dispatches to either `runOne` (modeled) or `runOneEmulated` (emulated). Both paths share the same `PlaybackSession`, `Collector`, `ComparisonReport`, and output reporters. The emulated path intentionally disables the simulator-local cache (`Cache: nil`) because the real edge server handles caching transparently.

**Modeled mode is the primary result.** It produces the sweep heatmaps, the statistical comparisons, and the deployment recommendation. Emulated mode is validation: it confirms that the modeled transport's predictions are directionally correct and within a reasonable error band of real protocol behavior.

## Consequences

**What we gained:**
- Modeled experiments complete in seconds, enabling 20-point parameter sweeps (5 loss rates x 4 RTTs = 20 combinations) that would take hours in emulated mode.
- Emulated mode catches modeling errors. If the modeled transport predicts 35% p95 improvement but emulated shows 5%, we know the model is wrong.
- All metrics, reporting, and statistical analysis code is written once and shared.

**What we gave up:**
- The emulated path uses a `protoSalt` in its seed derivation (`protoSalt = 1 << 32` for QUIC) that the modeled path deliberately omits. This means emulated TCP and QUIC runs see different client populations and content assignments, while modeled runs see identical ones. The asymmetry exists because emulated transport state is opaque (kernel TCP buffers, quic-go internal state), so isolating the transport variable perfectly is not possible.
- The `Transport` interface is deliberately minimal (no connection pooling, no stream priorities, no flow control backpressure). This simplifies the abstraction but means the modeled transports cannot capture second-order effects like HTTP/2 priority inversion.

**What we would do differently:**
- Add a `TransportStats() TransportStats` method to the interface so emulated transports can surface kernel-level metrics (RTT estimates, cwnd snapshots) without breaking the abstraction.
- Build a cross-validation reporter that automatically compares modeled vs. emulated results for the same scenario and flags divergences above a configurable threshold.

## Alternatives Considered

**Single-mode (modeled only):** Simpler to build, but the results would lack empirical grounding. Reviewers and decision-makers would rightfully question whether the models are accurate. Rejected because validation is essential for credibility.

**Single-mode (emulated only):** Captures real protocol behavior, but a 5x4 sweep with 10 runs each would require 200 Docker-based experiment runs, each taking 30-60 seconds. Total wall time: 1-3 hours. Sweeps become impractical during iterative development. Rejected because rapid iteration on parameters matters.

**Separate codebases for each mode:** Would avoid the shared-interface complexity, but would double the maintenance burden for the player, ABR, cache, and metrics code. Bug fixes would need to be applied in two places. Rejected because DRY matters more than mode-specific optimization.
