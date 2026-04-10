# ADR-005: Reno Congestion Control Model

## Context

The congestion controller determines how quickly a transport pushes data onto the network and how it responds to packet loss. It's the core mechanism translating loss events into delivery-time penalties, and the choice of algorithm affects the magnitude of the TCP-vs-QUIC gap in our results.

Real transport stacks use a variety of algorithms:

- TCP Reno / NewReno: classical AIMD (Additive Increase, Multiplicative Decrease). Slow start doubles cwnd each RTT until loss; congestion avoidance adds one MSS per RTT. On loss: `ssthresh = cwnd/2`, `cwnd = ssthresh`.
- TCP Cubic: default on Linux since 2.6.19. Uses a cubic function of time since last loss to compute cwnd, recovering more aggressively than Reno. Most production TCP stacks use this.
- BBR: Google's model-based algorithm. Probes bandwidth and RTT independently, doesn't react to individual loss events, maintains higher throughput on lossy links. Used by Google's QUIC deployment.
- quic-go uses NewReno by default (as of v0.42+), though Cubic and BBR are available as options.

## Decision

We model Reno congestion control (`internal/transport/modeled/congestion.go`, `CongestionController`) for both TCP and QUIC transports. The model operates at RTT granularity:

1. Initial state: `cwnd = 10 * MSS` (14,600 bytes, per RFC 6928), `ssthresh = infinity`, slow-start active.
2. Each RTT: send `min(cwnd, remaining_bytes)` bytes.
3. Slow start (cwnd < ssthresh): double cwnd each RTT.
4. Congestion avoidance (cwnd >= ssthresh): add one MSS per RTT.
5. On loss within an RTT's window: `ssthresh = cwnd/2` (floor: `2 * MSS`), `cwnd = ssthresh`. Add one extra RTT for fast retransmit recovery.
6. Total time = RTT_count * RTT.

The same `CongestionController` is used by both `ModeledTCPTransport` and `ModeledQUICTransport`. The difference between protocols isn't in congestion control -- it's in how loss affects multiplexed streams. TCP's `FetchConcurrent` models HOL blocking (all streams wait for the full connection when any loss occurs), while QUIC's `FetchConcurrent` applies per-stream loss recovery penalties (only the affected stream waits an extra RTT).

Why Reno specifically:

It's the simplest valid model of AIMD congestion control. Fewer parameters means fewer calibration decisions and fewer confounds. quic-go's default is NewReno, which is functionally identical to Reno at our RTT-level simulation granularity (the NewReno difference -- not resetting cwnd on each loss within a single window -- is captured by our "one extra RTT per loss window" model). And using the same controller for both protocols ensures the only variable between TCP and QUIC is the multiplexing behavior, not the congestion response.

## Consequences

Our results are conservative. Cubic recovers faster than Reno after loss (the cubic function's aggressive ramp). If we modeled Cubic, TCP would recover more quickly from HOL-blocking stalls, and the TCP-vs-QUIC gap would be smaller at moderate loss rates (1-3%). At high loss rates (5%+), the gap is dominated by the number of stall events rather than recovery speed, so the algorithm matters less.

This means we slightly overstate QUIC's advantage at moderate loss and are approximately correct at high loss. The bias direction is actually favorable for decision-making: if the simulator says "deploy QUIC above 1% loss," the real threshold might be slightly higher (say 1.5%), but the recommendation is conservative -- you won't deploy QUIC where it doesn't help.

RTT-level granularity means we don't simulate ACK clocking, pacing, or sub-RTT cwnd evolution. For video segments of 50-500KB (35-350 packets at 1460-byte MSS), a transfer takes 3-15 RTTs under Reno, so the RTT-level model captures the essential dynamics. For very small segments (< 1 RTT of data), the model is less accurate because it doesn't capture initial burst behavior within a single RTT.

No AIMD interaction between streams: in real TCP, multiple HTTP/2 streams share one cwnd and one loss recovery state machine. Our model correctly captures this by computing a single connection-level transfer time over total bytes. In real QUIC, streams share one cwnd but have independent loss recovery. Our model captures this by computing a shared connection-level transfer time plus per-stream loss penalties.

## Alternatives Considered

*Cubic:* Would produce tighter results (smaller gap) because of its aggressive recovery. We didn't implement it because the additional complexity (cubic function with K and C parameters, HyStart for slow-start exit) adds calibration decisions without changing the qualitative conclusion. Cubic would be the right choice if we were producing precise quantitative predictions rather than directional deployment guidance.

*BBR:* BBR v1/v2 estimates bottleneck bandwidth and min RTT, then paces at `BtlBw * (1/min_RTT)`. It doesn't halve its rate on loss, so TCP-with-BBR would perform much better under loss than TCP-with-Reno, potentially narrowing or eliminating the QUIC advantage. But BBR isn't the default on most TCP stacks (requires explicit `setsockopt`), and modeling it correctly requires simulating the probe phases (STARTUP, DRAIN, PROBE_BW, PROBE_RTT) -- substantially more complex. Appropriate for a study about Google's infrastructure specifically, not a general CDN recommendation.

*Per-protocol congestion control (Reno for TCP, Cubic for QUIC):* Would match the defaults of Linux TCP (Cubic) and quic-go (NewReno). But it confounds the comparison -- delivery-time differences would reflect both multiplexing behavior and the congestion control algorithm. Using the same algorithm for both isolates the multiplexing effect, which is what we're trying to measure.
