# ADR-005: Reno Congestion Control Model

## Status
Accepted

## Context

The congestion controller determines how quickly a transport can push data onto the network and how it responds to packet loss. This is the core mechanism that translates packet loss events into delivery-time penalties. The choice of congestion control algorithm affects the magnitude of the TCP-vs-QUIC gap in our results.

Real transport stacks use a variety of congestion control algorithms:

- **TCP Reno / NewReno:** The classical AIMD (Additive Increase, Multiplicative Decrease) algorithm. Slow start doubles cwnd each RTT until loss; congestion avoidance adds one MSS per RTT. On loss: `ssthresh = cwnd/2`, `cwnd = ssthresh`.
- **TCP Cubic:** The default on Linux since 2.6.19. Uses a cubic function of time since last loss event to compute cwnd, recovering more aggressively than Reno after loss. Most production TCP stacks use Cubic.
- **BBR (Bottleneck Bandwidth and Round-trip propagation time):** Google's model-based algorithm. BBR probes bandwidth and RTT independently, does not react to individual loss events, and maintains higher throughput on lossy links. Used by Google's QUIC deployment.
- **QUIC stacks:** quic-go uses NewReno by default (as of v0.42+), though Cubic and BBR are available as options.

## Decision

We model **Reno** congestion control (`internal/transport/modeled/congestion.go`, `CongestionController`) for both TCP and QUIC transports. The model operates at RTT granularity:

1. **Initial state:** `cwnd = 10 * MSS` (14,600 bytes, per RFC 6928), `ssthresh = infinity`, slow-start active.
2. **Each RTT:** Send `min(cwnd, remaining_bytes)` bytes.
3. **Slow start (cwnd < ssthresh):** Double cwnd each RTT.
4. **Congestion avoidance (cwnd >= ssthresh):** Add one MSS per RTT.
5. **On loss within an RTT's window:** `ssthresh = cwnd/2` (floor: `2 * MSS`), `cwnd = ssthresh`. Add one extra RTT for fast retransmit recovery.
6. **Total time = RTT_count * RTT.**

The same `CongestionController` is used by both `ModeledTCPTransport` and `ModeledQUICTransport`. The difference between protocols is not in congestion control but in **how loss affects multiplexed streams**: TCP's `FetchConcurrent` models HOL blocking (all streams wait for the full connection when any loss occurs), while QUIC's `FetchConcurrent` applies per-stream loss recovery penalties (only the affected stream waits an extra RTT).

Reno was chosen because:

- It is the **simplest valid model** of AIMD congestion control. Fewer parameters means fewer calibration decisions and fewer confounds.
- quic-go's default is NewReno, which is functionally identical to Reno for our RTT-level simulation (the difference -- NewReno avoids resetting cwnd on each loss within a single window -- is captured by our "one extra RTT per loss window" model).
- Using the same congestion controller for both protocols ensures that the only variable between TCP and QUIC is the multiplexing behavior, not the congestion response.

## Consequences

**Results are conservative.** Cubic recovers faster than Reno after a loss event (the cubic function's aggressive ramp). If we modeled Cubic, TCP would recover more quickly from HOL-blocking stalls, and the TCP-vs-QUIC gap would be **smaller** at moderate loss rates (1-3%). At high loss rates (5%+), the gap is dominated by the number of stall events rather than recovery speed, so the algorithm matters less.

This means our results overstate QUIC's advantage slightly at moderate loss and are approximately correct at high loss. The direction of the bias is favorable for decision-making: if the simulator says "deploy QUIC above 1% loss," the real threshold may be slightly higher (e.g., 1.5%), but the recommendation is conservative -- you won't deploy QUIC where it does not help.

**RTT-level granularity.** The model does not simulate ACK clocking, pacing, or sub-RTT cwnd evolution. For video segments of 50-500KB (35-350 packets at 1460-byte MSS), a transfer takes 3-15 RTTs under Reno, so the RTT-level model captures the essential dynamics. For very small segments (< 1 RTT of data), the model is less accurate because it does not capture the initial burst behavior within a single RTT.

**No AIMD interaction between streams.** In real TCP, multiple HTTP/2 streams share one cwnd and one loss recovery state machine. Our model correctly captures this by computing a single connection-level transfer time over total bytes. In real QUIC, streams share one cwnd but have independent loss recovery. Our model captures this by computing a shared connection-level transfer time plus per-stream loss penalties.

## Alternatives Considered

**Cubic:** Would produce tighter results (smaller TCP-vs-QUIC gap) because Cubic recovers aggressively after loss. We did not implement Cubic because the additional complexity (cubic function with K and C parameters, HyStart for slow-start exit) adds calibration decisions without changing the qualitative conclusion. Cubic would be the right choice if we were trying to produce precise quantitative predictions rather than directional deployment guidance.

**BBR:** BBR v1/v2 is model-based: it estimates bottleneck bandwidth and min RTT, then paces at `BtlBw * (1/min_RTT)`. BBR does not halve its rate on loss, which means TCP-with-BBR would perform much better under loss than TCP-with-Reno, potentially narrowing or eliminating the QUIC advantage. However, BBR is not the default on most TCP stacks (it requires explicit `setsockopt`), and modeling it correctly requires simulating the probe phases (STARTUP, DRAIN, PROBE_BW, PROBE_RTT), which is substantially more complex. BBR would be appropriate for a study specifically about Google's infrastructure (where BBR is deployed) but not for a general CDN recommendation.

**Per-protocol congestion control (Reno for TCP, Cubic for QUIC):** This would match the defaults of Linux TCP (Cubic) and quic-go (NewReno). However, it would confound the comparison: differences in delivery time would reflect both the multiplexing behavior AND the congestion control algorithm. Using the same algorithm for both isolates the multiplexing effect, which is what we are trying to measure.
