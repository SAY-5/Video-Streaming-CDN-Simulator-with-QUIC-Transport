# ADR-002: Gilbert-Elliott Bursty Loss Model

## Context

Packet loss is the variable that matters most for whether QUIC outperforms TCP in multiplexed video streaming. The choice of loss model directly controls the shape of the results, and an unrealistic model produces misleading conclusions.

Real Internet loss is bursty, not uniform. Empirical measurements (Bolot 1993, Yajnik et al. 1999, Jiang & Schulzrinne 2002) consistently show that losses cluster in bursts: once a loss occurs, the probability of subsequent losses jumps way above the unconditional rate. This burstiness hits TCP disproportionately hard -- a burst of consecutive losses triggers an RTO rather than a fast retransmit, and the resulting head-of-line blocking stalls *all* multiplexed HTTP/2 streams simultaneously. Isolated single-packet losses, by contrast, get handled by fast retransmit within one RTT and cause minimal disruption.

A uniform random loss model (each packet dropped independently with probability p) would systematically understate the TCP-vs-QUIC difference because it produces the kind of isolated losses that TCP handles relatively gracefully. We need a model that captures the burst structure that makes TCP's HOL blocking pathological.

## Decision

We implement the Gilbert-Elliott two-state Markov chain as the primary loss model (`internal/transport/modeled/loss.go`, `GilbertElliott` struct). Two states:

- GOOD state: no loss. Transitions to BAD with probability `PGoodToBad` per packet.
- BAD state: each packet lost with probability `LossInBadState`. Transitions back to GOOD with probability `PBadToGood` per packet.

Steady-state probability of being in BAD:

```
pi_bad = PGoodToBad / (PGoodToBad + PBadToGood)
```

Steady-state average loss rate:

```
avg_loss = pi_bad * LossInBadState
         = PGoodToBad / (PGoodToBad + PBadToGood) * LossInBadState
```

For our flagship scenario (`configs/reproduce_35pct.yaml`): `PGoodToBad = 0.03`, `PBadToGood = 0.3`, `LossInBadState = 0.4`. That gives:

```
pi_bad   = 0.03 / (0.03 + 0.3) = 0.0909
avg_loss = 0.0909 * 0.4 = 3.6%
```

Average burst length (mean sojourn time in BAD) is `1 / PBadToGood = 1 / 0.3 = 3.33 packets`. So losses come in bursts of ~3 consecutive packets, which is realistic for congested Internet paths.

The `LossSimulator` interface also supports `"uniform"` and `"none"` types. Sweep configs (`configs/sweep_loss_rtt.yaml`) use uniform loss for cleaner parameter-space exploration where we want to vary loss rate on a single axis without the confound of burst length.

Each loss simulator instance gets its own child `*rand.Rand` derived from the parent transport RNG's first draw. This isolation ensures the loss sequence is identical for TCP and QUIC transports constructed from the same seed, even though their handshake and jitter code consume different numbers of RNG draws from the parent. See ADR-007 for the full RNG isolation design.

## Consequences

Why this matters for HOL blocking: a burst of 3 lost packets in a TCP bytestream creates a 3-packet gap. HTTP/2 streams whose data falls after that gap are blocked until all 3 retransmissions complete -- in our congestion model, that costs one extra RTT for fast-retransmit recovery. With QUIC, only the streams whose packets were actually lost get delayed; other streams proceed normally. The burst structure of Gilbert-Elliott losses amplifies this difference compared to uniform loss, where individual drops are spread out and less likely to create multi-packet gaps.

Calibration: the parameters `(0.03, 0.3, 0.4)` were chosen to produce ~3.6% average loss with burst lengths typical of trans-Pacific paths (Singapore/Mumbai to US-East). These are conservative; mobile and satellite paths often have longer bursts (`PBadToGood` < 0.1).

Limitation: the model is memoryless within each state. Real network loss often has longer-range correlations (congestion episodes lasting hundreds of milliseconds). Gilbert-Elliott captures packet-level burst structure but not these macro-scale patterns.

## Alternatives Considered

*Uniform random loss (Bernoulli):* Each packet dropped independently with probability p. Simplest model, and we do use it in sweep configs for parameter exploration. But it underestimates TCP's vulnerability to HOL blocking because isolated losses are handled efficiently by fast retransmit. Using it as the primary model would produce a smaller TCP-vs-QUIC gap, understating QUIC's real-world benefit on lossy paths.

*Markov-Modulated Poisson Process (MMPP):* More expressive model with multiple states and Poisson-distributed events within each. Can capture richer temporal structure but needs more parameters to calibrate, and the extra complexity doesn't materially change the qualitative conclusion (bursty loss hurts TCP more than QUIC). We went with Gilbert-Elliott for parsimony: two parameters beyond the loss rate, well-understood steady-state math, wide use in the networking literature.

*Trace-driven replay:* Record real packet traces (e.g., from `tcpdump`) and replay the exact loss pattern. Gold standard for realism but breaks deterministic reproducibility (traces are host-specific) and makes parameter sweeps impossible -- you can't just "set loss to 5%" on a trace. We plan to support trace-driven loss in a future phase for validation against specific network paths.
