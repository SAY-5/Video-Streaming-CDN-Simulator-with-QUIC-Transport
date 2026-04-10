# ADR-001: Dual-Mode Architecture (Modeled + Emulated)

## Context and Decision

We need to answer a quantitative question: under what network conditions does HTTP/3 (QUIC) deliver measurably better video streaming QoE than HTTP/2 (TCP)? There are two ways to get at this, and neither one is sufficient alone.

Modeled simulation builds deterministic math models of congestion control, packet loss, HOL blocking, and handshake latency, then cranks through thousands of sessions in seconds. You get tight variable control and perfect reproducibility, but the models are simplifications. Emulated validation runs real HTTP/2 and HTTP/3 servers in Docker, shapes traffic with `tc netem`, and measures actual protocol behavior on the wire. This captures the full complexity of real stacks (quic-go, Go's net/http2) but it's slow, non-deterministic, and sensitive to host CPU scheduling.

Modeled-only results can be dismissed as "just a model." Emulated-only results are hard to reproduce and too expensive to sweep across parameter spaces. We need both, and they need to share as much infrastructure as possible so we can cross-validate.

The core constraint: all CDN logic -- video player, ABR algorithm, cache, origin shield, routing, metrics collection -- must be agnostic to which mode is active. Only the transport layer changes.

So we define a single `transport.Transport` interface (in `internal/transport/transport.go`) with three methods: `FetchSegment`, `FetchConcurrent`, and `Handshake`. All simulation logic talks exclusively through this interface.

Two implementations sit behind it:

- `internal/transport/modeled/`: `ModeledTCPTransport` and `ModeledQUICTransport` use deterministic RNG-driven models of congestion control (Reno), packet loss (Gilbert-Elliott or uniform), and jitter (half-normal). No network, no Docker, no sockets.

- `internal/transport/emulated/`: `EmulatedTCPTransport` and `EmulatedQUICTransport` make real HTTP requests to Dockerized edge servers over `tc netem`-shaped links. They measure actual TTFB, goodput, and CPU time from the kernel/quic-go.

The experiment runner (`internal/experiment/runner.go`) checks `cfg.Mode` and dispatches to either `runOne` (modeled) or `runOneEmulated` (emulated). Both paths share the same `PlaybackSession`, `Collector`, `ComparisonReport`, and output reporters. The emulated path intentionally disables the simulator-local cache (`Cache: nil`) because the real edge server handles caching transparently.

Modeled mode is the primary result -- it produces the sweep heatmaps, the statistical comparisons, and the deployment recommendation. Emulated mode is validation: it confirms the modeled transport's predictions are directionally correct and within a reasonable error band.

## Consequences

What we gained:
- Modeled experiments complete in seconds, enabling 20-point parameter sweeps (5 loss rates x 4 RTTs = 20 combinations) that would take hours in emulated mode.
- Emulated mode catches modeling errors. If modeled predicts 35% p95 improvement but emulated shows 5%, we know the model is wrong.
- All metrics, reporting, and statistical analysis code is written once and shared.

What we gave up:
- The emulated path uses a `protoSalt` in its seed derivation (`protoSalt = 1 << 32` for QUIC) that the modeled path deliberately omits. This means emulated TCP and QUIC runs see different client populations and content assignments, while modeled runs see identical ones. The asymmetry exists because emulated transport state is opaque (kernel TCP buffers, quic-go internal state), so we can't perfectly isolate the transport variable there.
- The `Transport` interface is deliberately minimal -- no connection pooling, no stream priorities, no flow control backpressure. Simpler abstraction, but it means the modeled transports can't capture second-order effects like HTTP/2 priority inversion.

Things we'd do differently next time:
- Add a `TransportStats() TransportStats` method to the interface so emulated transports can surface kernel-level metrics (RTT estimates, cwnd snapshots) without breaking the abstraction.
- Build a cross-validation reporter that automatically compares modeled vs. emulated results for the same scenario and flags divergences above a configurable threshold.

## Alternatives Considered

*Modeled only:* Simpler to build, but results lack empirical grounding. Reviewers would rightfully question whether the models are accurate. Validation is essential for credibility.

*Emulated only:* Captures real protocol behavior, but a 5x4 sweep with 10 runs each means 200 Docker-based experiment runs at 30-60 seconds each. Total wall time: 1-3 hours. That's impractical during iterative development.

*Separate codebases for each mode:* Avoids the shared-interface complexity but doubles maintenance for the player, ABR, cache, and metrics code. Bug fixes need applying in two places. DRY matters more here than mode-specific optimization.
