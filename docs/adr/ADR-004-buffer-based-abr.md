# ADR-004: Buffer-Based ABR (BBA) for Bitrate Adaptation

## Context

Adaptive Bitrate (ABR) algorithms determine which quality level the video player requests for each segment. The ABR choice affects both the QoE metrics we measure (average bitrate, rebuffer rate, bitrate stability) and transport-level behavior -- higher bitrates mean larger segments, longer transfers, and more packets exposed to loss.

For a transport comparison study, the ABR algorithm needs to be deterministic given the same inputs (no neural net inference or online learning that varies across runs), responsive to transport differences (if QUIC delivers faster, the ABR should capitalize by selecting higher bitrates), and realistic enough that the QoE improvements we measure are credible.

The key signal distinguishing TCP from QUIC in our simulation is segment delivery time. Under loss, TCP's HOL blocking inflates delivery times for all concurrent segments, while QUIC's per-stream recovery keeps fast streams fast. The ABR needs to translate those delivery-time differences into observable QoE differences.

## Decision

We implement Buffer-Based Adaptation (BBA, Huang et al. 2014) as the default ABR algorithm (`internal/video/abr_buffer.go`, `BufferBasedABR`). BBA uses the client's playback buffer level as the primary signal, with four zones:

- *Critical* (buffer < 2s): select the lowest bitrate immediately. The player is about to rebuffer.
- *Danger* (buffer < 6s): hold or decrease. Don't increase. If last measured throughput is below current bitrate, step down one level.
- *Comfort* (6s - 15s): linear interpolation between lowest and highest available bitrate. Higher buffer = higher bitrate.
- *Surplus* (buffer > 15s): select the highest available bitrate.

Hysteresis prevents oscillation: if the target bitrate from comfort-zone interpolation is within one representation level of the current bitrate, the player holds steady. This avoids rapid up-down-up-down switches that degrade visual quality.

BBA is the right fit for a transport comparison because:

Buffer level naturally integrates transport performance. If QUIC delivers segments 30% faster, the buffer grows faster, BBA selects higher bitrates, and we see higher average bitrate in the metrics. If TCP's HOL blocking stalls a batch, the buffer drains, BBA drops quality, and we see more bitrate switches and potentially rebuffers.

It also sidesteps a throughput estimation problem. Throughput-based ABR (also implemented as `abr_throughput.go`) estimates bandwidth from recent segment fetches, but cache hits return in ~1ms and inflate the estimate, causing the player to overshoot. We did fix this in the player (the `HIGH fix from review R1` at line 268 of `player.go` excludes cache-hit bytes from the throughput sample), but buffer-based ABR avoids the issue entirely.

And it's deterministic -- given the same buffer level and manifest, BBA always produces the same decision. No learned state, no random exploration.

## Consequences

BBA translates delivery-time differences into bitrate differences with a lag of 1-2 segments (the time for the buffer level to change). This is realistic -- production players exhibit similar inertia. The danger zone and hysteresis prevent overreacting to single-segment anomalies.

The linear interpolation in the comfort zone means BBA doesn't aggressively pursue the highest bitrate when the buffer is moderately healthy. This is deliberate -- BBA prioritizes rebuffer avoidance over bitrate maximization, matching the preference of most streaming services (Netflix, YouTube).

We also implement `ThroughputBasedABR` for comparison. It selects the highest bitrate not exceeding the harmonic mean of recent throughput samples. Available via `abr: throughput_based` in YAML config but isn't the default because it's more sensitive to cache-hit throughput inflation.

## Alternatives Considered

*Throughput-only ABR:* Estimates available bandwidth and picks the highest bitrate below that. This is what early DASH players (dash.js < v3) used. Throughput estimation is noisy though -- a single cache hit or a single HOL-blocked segment can swing the estimate dramatically and cause oscillation. We provide it as an option but default to BBA.

*Pensieve (neural ABR, Mao et al. 2017):* Uses a neural network trained via RL to map (buffer, throughput, segment sizes) to a bitrate decision. State-of-the-art QoE in controlled evaluations, but introduces non-determinism (floating-point order-of-operations in the NN), requires a trained model checkpoint, and adds a Python/ONNX dependency. Disqualifying for a deterministic simulation study. We'd also need to retrain for each network condition, which defeats the purpose of a parameter sweep.

*MPC (Model Predictive Control, Yin et al. 2015):* Formulates bitrate selection as optimization over the next K segments, maximizing a QoE objective (bitrate - rebuffer penalty - switch penalty). Requires a throughput prediction model, which has the same cache-hit inflation problem. MPC also has tunable weights for the QoE objective, and different weight settings would change the TCP-vs-QUIC comparison -- adding a confound we want to avoid.
