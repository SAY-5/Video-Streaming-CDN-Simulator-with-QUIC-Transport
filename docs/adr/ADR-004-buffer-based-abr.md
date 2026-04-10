# ADR-004: Buffer-Based ABR (BBA) for Bitrate Adaptation

## Status
Accepted

## Context

Adaptive Bitrate (ABR) algorithms determine which quality level the video player requests for each segment. The ABR choice affects both the QoE metrics we measure (average bitrate, rebuffer rate, bitrate stability) and the transport-level behavior (higher bitrates produce larger segments, which take longer to transfer and expose more packets to loss).

For a transport comparison study, the ABR algorithm must be:

1. **Deterministic** given the same inputs -- no neural network inference or online learning that varies across runs.
2. **Responsive to transport differences** -- if QUIC delivers segments faster than TCP, the ABR should capitalize by selecting higher bitrates.
3. **Realistic** -- the algorithm should resemble what production players actually use, so the QoE improvements we measure are credible.

The key signal that distinguishes TCP from QUIC in our simulation is **segment delivery time**. Under loss, TCP's HOL blocking inflates delivery times for all concurrent segments, while QUIC's per-stream recovery keeps fast streams fast. The ABR algorithm must translate these delivery-time differences into observable QoE differences.

## Decision

We implement Buffer-Based Adaptation (BBA, Huang et al. 2014) as the default ABR algorithm (`internal/video/abr_buffer.go`, `BufferBasedABR`). BBA uses the client's playback buffer level as the primary signal, with four zones:

- **Critical** (buffer < 2s): Select the lowest bitrate immediately. The player is about to rebuffer.
- **Danger** (buffer < 6s): Hold or decrease bitrate. Do not increase. If the last measured throughput is below the current bitrate, step down one level.
- **Comfort** (6s - 15s): Linear interpolation between the lowest and highest available bitrate. Higher buffer = higher bitrate.
- **Surplus** (buffer > 15s): Select the highest available bitrate. The buffer is healthy.

**Hysteresis** prevents oscillation: if the target bitrate computed by the comfort-zone interpolation is within one representation level of the current bitrate, the player holds steady. This avoids rapid up-down-up-down switches that degrade visual quality.

BBA is the right choice for a transport comparison because:

- **Buffer level naturally integrates transport performance.** If QUIC delivers segments 30% faster, the buffer grows faster, BBA selects higher bitrates, and we observe higher average bitrate in the metrics. If TCP's HOL blocking stalls a batch, the buffer drains, BBA drops to a lower bitrate, and we observe more bitrate switches and potentially rebuffers.
- **No throughput estimation bias.** Throughput-based ABR (also implemented as `abr_throughput.go`) estimates available bandwidth from recent segment fetches. Cache hits return in ~1ms and inflate the estimate, causing the player to overshoot. We fixed this in the player (the `HIGH fix from review R1` at line 268 of `player.go` excludes cache-hit bytes from the throughput sample), but buffer-based ABR sidesteps the issue entirely.
- **Deterministic.** Given the same buffer level and manifest, BBA always produces the same decision. There is no learned state or random exploration.

## Consequences

**Transport sensitivity:** BBA translates delivery-time differences into bitrate differences with a lag of 1-2 segments (the time for the buffer level to change). This is realistic -- production players exhibit similar inertia. The danger zone and hysteresis prevent the ABR from overreacting to single-segment anomalies.

**Conservative in the comfort zone:** The linear interpolation in the comfort zone means BBA does not aggressively pursue the highest bitrate when the buffer is moderately healthy. This is deliberate: BBA prioritizes rebuffer avoidance over bitrate maximization, matching the preference of most streaming services (Netflix, YouTube).

**Throughput-based ABR as a second option:** We also implement `ThroughputBasedABR` for comparison. It selects the highest bitrate that does not exceed the harmonic mean of recent throughput samples. This is available via `abr: throughput_based` in the YAML config but is not the default because it is more sensitive to cache-hit throughput inflation.

## Alternatives Considered

**Throughput-only ABR:** Estimates available bandwidth and selects the highest bitrate below that estimate. This is what early DASH players (dash.js < v3) used. The problem is that throughput estimation is noisy: a single cache hit or a single HOL-blocked segment can swing the estimate dramatically, causing oscillation. We provide this as an option but default to BBA.

**Pensieve (neural ABR, Mao et al. 2017):** Uses a neural network trained via reinforcement learning to map (buffer, throughput, segment sizes) to a bitrate decision. Pensieve achieves state-of-the-art QoE in controlled evaluations, but it introduces non-determinism (floating-point order-of-operations in the NN), requires a trained model checkpoint, and adds a Python/ONNX dependency. These are disqualifying for a deterministic simulation study. We would need to retrain the model for each network condition, which defeats the purpose of a parameter sweep.

**MPC (Model Predictive Control, Yin et al. 2015):** Formulates bitrate selection as an optimization problem over the next K segments, maximizing a QoE objective (bitrate - rebuffer penalty - switch penalty). MPC requires a throughput prediction model and solves a combinatorial optimization at each step. The computational cost is acceptable, but the throughput prediction model introduces the same cache-hit inflation problem as throughput-based ABR. MPC also has tunable weights for the QoE objective, and different weight settings would change the TCP-vs-QUIC comparison, adding a confound we want to avoid.
