# Should we deploy HTTP/3 on our CDN edges?

Short answer: yes, but only on PoPs where the client-to-edge path has packet loss above ~1%. On clean fiber links, it doesn't help and costs more CPU.

## The numbers

Under the flagship scenario (200ms RTT, 3.6% bursty loss, Asia clients to US origin):

- p95 segment delivery latency drops by about 54%, from 9.1 seconds on TCP to 4.2 seconds on QUIC
- Rebuffering drops by 96% — TCP sessions averaged 4.4 rebuffer events, QUIC sessions averaged 0.04
- Video quality (bitrate) and cache hit rates are identical across both protocols, which is expected since the cache and ABR layers sit above the transport

These numbers have bootstrap 95% confidence intervals and Mann-Whitney p-values below 0.001. The modeled prediction was validated against real HTTP/2 and HTTP/3 servers running in Docker containers with `tc netem`-shaped network links — the modeled prediction falls within the emulated CI.

## Where to deploy, where not to

The sweep across loss rates (0-7%) and RTTs (20-200ms) shows the boundary clearly: above about 1% packet loss, QUIC wins by eliminating head-of-line blocking on multiplexed streams. Below that, the two protocols perform about the same, and QUIC's userspace implementation adds CPU overhead (roughly 2x per byte, according to Google's 2017 SIGCOMM paper).

In practice this means: deploy HTTP/3 on edges serving mobile users, developing regions, satellite links, and any path crossing congested peering points. Don't bother for intra-datacenter traffic or well-provisioned fiber.

## How confident should you be in these numbers

The improvement ranges are consistent with published measurements from Google (8-18% mean, up to 70% at p95 for YouTube rebuffering, SIGCOMM 2017), Meta (20% tail latency reduction, 22% MTBR improvement, 2020 engineering blog), and Akamai (23% more connections hitting 5 Mbps during a live event, 2023). An independent 2021 study by Kosek et al. found HTTP/3 roughly equal to HTTP/2 under high loss for real websites, which I attribute to real-world domain sharding reducing the effective multiplexing depth. The simulator's single-origin model doesn't capture that.

## What it costs

QUIC's CPU cost is approximately 2x TCP after optimization (Langley et al. 2017). The latency and rebuffer improvements have to be weighed against that. For a large CDN, the cost-benefit probably works out in regions where rebuffering drives user churn.
