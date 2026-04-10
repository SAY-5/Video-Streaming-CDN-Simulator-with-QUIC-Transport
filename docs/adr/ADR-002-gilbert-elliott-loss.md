# ADR-002: Gilbert-Elliott Bursty Loss Model

## Status
Accepted

## Context

Packet loss is the single most important variable in determining whether QUIC outperforms TCP for multiplexed video streaming. The choice of loss model directly controls the shape of the results -- and an unrealistic model produces misleading conclusions.

Real Internet loss is **bursty**, not uniform. Empirical measurements (Bolot 1993, Yajnik et al. 1999, Jiang & Schulzrinne 2002) consistently show that packet losses cluster in bursts: once a loss occurs, the probability of subsequent losses is much higher than the unconditional loss rate. This burstiness has a disproportionate impact on TCP because a burst of consecutive losses triggers a retransmission timeout (RTO) rather than a fast retransmit, and the resulting head-of-line blocking stalls ALL multiplexed HTTP/2 streams simultaneously. Isolated single-packet losses, by contrast, are typically handled by fast retransmit within one RTT and cause minimal disruption.

A uniform random loss model (each packet dropped independently with probability p) would systematically understate the difference between TCP and QUIC because it produces isolated losses that TCP handles relatively gracefully. The Gilbert-Elliott model captures the burst structure that makes TCP's HOL blocking pathological.

## Decision

We implement the Gilbert-Elliott two-state Markov chain as the primary loss model (`internal/transport/modeled/loss.go`, `GilbertElliott` struct). The model has two states:

- **GOOD state**: no loss. Transitions to BAD with probability `PGoodToBad` per packet.
- **BAD state**: each packet lost with probability `LossInBadState`. Transitions back to GOOD with probability `PBadToGood` per packet.

The steady-state probability of being in the BAD state is:

```
pi_bad = PGoodToBad / (PGoodToBad + PBadToGood)
```

The steady-state average loss rate is:

```
avg_loss = pi_bad * LossInBadState
         = PGoodToBad / (PGoodToBad + PBadToGood) * LossInBadState
```

For our flagship scenario (`configs/reproduce_35pct.yaml`): `PGoodToBad = 0.03`, `PBadToGood = 0.3`, `LossInBadState = 0.4`. This gives:

```
pi_bad   = 0.03 / (0.03 + 0.3) = 0.0909
avg_loss = 0.0909 * 0.4 = 3.6%
```

The average burst length (mean sojourn time in BAD) is `1 / PBadToGood = 1 / 0.3 = 3.33 packets`. So losses come in bursts of ~3 consecutive packets, which is realistic for congested Internet paths.

The `LossSimulator` interface also supports `"uniform"` and `"none"` types. The sweep configs (`configs/sweep_loss_rtt.yaml`) use uniform loss for cleaner parameter-space exploration where we want to vary the loss rate on a single axis without the confound of burst length.

Each loss simulator instance receives its own child `*rand.Rand` derived from the parent transport RNG's first draw. This isolation ensures that the loss sequence is identical for TCP and QUIC transports constructed from the same seed, even though their handshake and jitter code consume different numbers of RNG draws from the parent. See ADR-007 for the full RNG isolation design.

## Consequences

**Why it matters for HOL blocking:** A burst of 3 lost packets in a TCP bytestream creates a 3-packet gap. HTTP/2 streams whose data falls after that gap are blocked until all 3 retransmissions complete. In our congestion model, this costs one extra RTT for the fast-retransmit recovery. With QUIC, only the stream(s) whose packets were actually lost are delayed; other streams proceed normally. The burst structure of Gilbert-Elliott losses amplifies this difference compared to uniform loss, where individual drops are spread out and less likely to create multi-packet gaps.

**Calibration:** The parameters `(0.03, 0.3, 0.4)` were chosen to produce a ~3.6% average loss rate with burst lengths typical of trans-Pacific paths (Singapore/Mumbai to US-East). These are conservative; mobile and satellite paths often exhibit longer bursts (`PBadToGood` < 0.1).

**Limitation:** The model is memoryless within each state. Real network loss often has longer-range correlations (e.g., congestion episodes lasting hundreds of milliseconds). The Gilbert-Elliott model captures the packet-level burst structure but not these macro-scale patterns.

## Alternatives Considered

**Uniform random loss (Bernoulli):** Each packet dropped independently with probability p. This is the simplest model and is used in the sweep configs for parameter exploration. However, it underestimates TCP's vulnerability to HOL blocking because isolated losses are handled efficiently by fast retransmit. Using uniform loss as the primary model would produce a smaller TCP-vs-QUIC gap, understating QUIC's real-world benefit on lossy paths.

**Markov-Modulated Poisson Process (MMPP):** A more expressive model with multiple states and Poisson-distributed events within each state. MMPP can capture richer temporal structure but requires more parameters to calibrate, and the additional complexity does not materially change the qualitative conclusion (bursty loss hurts TCP more than QUIC). We chose Gilbert-Elliott for its parsimony: two parameters beyond the loss rate, well-understood steady-state math, and wide use in the networking literature.

**Trace-driven replay:** Record real packet traces (e.g., from `tcpdump`) and replay the exact loss pattern. This is the gold standard for realism but breaks deterministic reproducibility (traces are host-specific) and makes parameter sweeps impossible (you cannot "set loss to 5%" on a trace). We plan to support trace-driven loss in a future phase for validation against specific network paths.
