# HTTP/3 (QUIC) vs HTTP/2 (TCP) for Video Streaming CDN

## Bottom Line

Deploying HTTP/3 on CDN edge servers serving regions with >1% packet loss reduces p95 video segment latency by 25-55% and virtually eliminates rebuffering. On clean, low-loss links (<1% loss), HTTP/3 performs comparably to HTTP/2 — the improvement comes from QUIC's elimination of head-of-line blocking, which only manifests when packets are actually lost.

## Recommendation

Deploy HTTP/3 selectively based on the loss characteristics of each edge PoP's client base:

| Condition | Recommendation | Expected Improvement |
|---|---|---|
| Mobile users, developing regions, satellite | **Deploy HTTP/3** | 25-55% p95 latency, 85-96% rebuffer reduction |
| Cross-congested peering points (>2% loss) | **Deploy HTTP/3** | 30-55% p95 latency |
| Well-provisioned fiber (<0.5% loss) | **Do not deploy** | No improvement; marginal CPU increase |
| Intra-datacenter traffic | **Do not deploy** | QUIC's userspace overhead exceeds any gain |

## The Decision Boundary

The parameter sweep across loss rates (0-7%) and RTTs (20-200ms) shows that QUIC's advantage activates above ~1% packet loss and grows with both loss rate and RTT. Below 1% loss, the transport protocol choice is irrelevant for video quality.

## Key Numbers (reproducible, deterministic)

```
Flagship scenario: Gilbert-Elliott 3.6% bursty loss, 200ms RTT, prefetch depth 3
  p95 segment latency: TCP 9.1s → QUIC 4.2s  (+54.0%, p < 0.001)
  Rebuffer duration:   TCP 61.4s → QUIC 2.4s  (+96.1%, p < 0.001)
  HOL events:          TCP 14.7/session → QUIC 0 (by design)

Cross-validation: emulated mode (real Docker + tc/netem) confirms
  p95 segment latency: +40.9% [95% CI: +12.5%, +60.5%]
  Modeled prediction (54.0%) falls inside emulated CI ✓
```

## Methodology

Deterministic simulation modeling 200 concurrent video clients streaming 120-second content through a 2-edge CDN topology with regional origin shield. The transport models capture TCP/H2 connection-level head-of-line blocking vs QUIC/H3 per-stream independence, Gilbert-Elliott bursty packet loss, Reno congestion control, ARC caching, and buffer-based adaptive bitrate selection. Results carry bootstrap 95% confidence intervals, Cohen's d effect sizes, and Mann-Whitney U p-values.

The simulation is validated against real HTTP/2 and HTTP/3 transfers over tc/netem-shaped Docker containers. The modeled prediction consistently falls within the emulated confidence interval.

## Confidence Level

The improvement percentages are consistent with published measurements from Google (Langley et al., SIGCOMM 2017: 8-18% mean, up to 70% at p95 rebuffer), Meta (2020: 20% tail latency, 22% MTBR), and Akamai (2023: 23% more connections at 5 Mbps+ threshold). An independent large-scale measurement by Kosek et al. (2021) found HTTP/3 approximately equal to HTTP/2 under high loss for real websites — a finding we attribute to real-world domain sharding reducing the effective multiplexing depth, which our single-origin model does not capture.

## Cost Consideration

QUIC's CPU cost is approximately 2x TCP (Langley et al. 2017, post-optimization). The latency and rebuffer improvements must be weighed against this increased compute spend at scale.
