# cdn-sim

Ever wondered why some YouTube videos buffer like crazy while others play smooth? A lot of it comes down to the internet protocol carrying the video data. There are two main ones right now: the old reliable **TCP** (used by HTTP/2) and the newer **QUIC** (used by HTTP/3). Big companies like Google, Cloudflare, and Netflix have been switching to QUIC, claiming it's faster — but *when* is it actually faster, and by *how much*?

That's what this project figures out.

I built a simulator in Go that pretends to be a mini CDN (Content Delivery Network — basically the system of servers around the world that delivers video to your device). It creates fake viewers, fake video catalogs, fake network conditions with packet loss and latency, and then measures how long each video segment takes to arrive under both protocols. Then it runs statistics on the results so we're not just guessing — we actually know if the difference is real.

## The big picture, explained simply

When you watch a video online, your device doesn't download the whole thing at once. It grabs it in small pieces called **segments** (usually 2-4 seconds each). Your video player is always fetching the next few segments ahead of where you're watching — this is called **prefetching**.

Here's where the protocols differ:

With **HTTP/2 over TCP**, all those prefetched segments travel through one shared pipe. If one packet gets lost (which happens all the time on the internet — WiFi drops, congested links, undersea cables), *everything* in that pipe has to wait while that one lost packet gets resent. This is called **head-of-line blocking**. It's like a single-lane road where one stalled car blocks everyone behind it.

With **HTTP/3 over QUIC**, each segment gets its own independent lane. If a packet is lost for segment #5, only segment #5 waits for the resend. Segments #6 and #7 keep flowing. No shared traffic jam.

This project simulates both scenarios thousands of times and measures the difference.

## What did I find?

Under the worst-case scenario I tested (200ms round-trip time, 3.6% packet loss — think someone in India streaming from a server in the US), QUIC cut the worst-case segment delivery time roughly in half and almost completely eliminated rebuffering (that spinning wheel you see when the video pauses to load).

But here's the thing nobody tells you: on a clean network with barely any packet loss, **QUIC doesn't help at all**. It actually performs slightly worse because QUIC runs in software (userspace) rather than in the operating system kernel like TCP does, which costs more CPU. So the answer isn't "QUIC is always better" — it's "QUIC is better when the network is bad."

The parameter sweep (testing every combination of loss rate from 0% to 7% and latency from 20ms to 200ms) shows the crossover point is around 1% packet loss. Below that, don't bother switching. Above that, QUIC wins and the advantage grows the worse the network gets.

## How the simulator works

There are two modes:

**Modeled mode** does everything with math. It simulates packet loss using a Gilbert-Elliott model (a two-state random process that creates realistic *bursty* loss — packets tend to get lost in clumps, not one at a time), models congestion control (how fast the sender ramps up its sending speed), and calculates how head-of-line blocking affects each segment. This mode is fast — 120,000 segment simulations in about 12 seconds on a laptop — and completely deterministic. Run it twice with the same config, get bit-identical output.

**Emulated mode** runs actual HTTP/2 and HTTP/3 servers in Docker containers with Linux `tc netem` shaping the network between them (adding artificial delay, loss, and jitter). Real packets on real sockets with real TLS handshakes. It's slower and noisier, but it validates that the modeled numbers are in the right ballpark and not just artifacts of my math being wrong.

Both modes feed into the same statistics pipeline that computes confidence intervals (so you know the range of plausible results, not just a single number), effect sizes (how big the difference actually is in practical terms), and significance tests (whether the difference is real or just random noise).

## Quick start

```bash
git clone https://github.com/SAY-5/Video-Streaming-CDN-Simulator-with-QUIC-Transport.git
cd Video-Streaming-CDN-Simulator-with-QUIC-Transport
make build
make run-modeled
```

That's it. It builds the simulator, runs the flagship scenario (200 clients in Asia, Singapore and Mumbai edge servers, US-East origin, 3.6% bursty loss, 200ms RTT), and prints a summary showing how much better QUIC performed.

If you have Python 3 with matplotlib installed, you can also generate charts:

```bash
python3 scripts/analysis/compare.py results/reproduce_35pct
```

This gives you four PNGs: a latency percentile comparison, a CDF (cumulative distribution), a bitrate timeline, and an improvement summary bar chart.

## Running a parameter sweep

Want to see *exactly* where QUIC starts winning? The sweep mode tests every combination of parameters:

```bash
bin/cdnsim sweep --config configs/sweep_loss_rtt.yaml
python3 scripts/analysis/sweep_heatmap.py results/sweep_loss_rtt
```

This produces a heatmap with loss rate on one axis and latency on the other. Green cells = QUIC wins. Red cells = TCP wins (or they're about the same). You can visually see the boundary where deploying HTTP/3 starts making sense.

## Emulated mode (real servers in Docker)

If you want to validate the modeled results with real network traffic:

```bash
# macOS — Colima gives you a Linux VM with tc/netem support
brew install colima docker docker-compose
colima start --cpu 4 --memory 6 --disk 30

make docker-up                                  # builds containers, generates TLS certs
bash scripts/netem/apply_topology.sh harsh_asia  # shapes the network links
docker compose -f docker/docker-compose.yml exec -T client \
    /app/cdnsim run --config /configs/emulated_lossy.yaml --output-dir /results/emulated_lossy
make docker-down                                # tears everything down
```

The Docker stack has 5 containers connected by 3 networks: an origin server, a regional shield (mid-tier cache), two edge servers (Singapore and Mumbai), and a client that drives the test. Each network link gets its own loss/delay/jitter profile, so you can shape the "last mile" (client to edge) differently from the "backbone" (edge to origin).

## Project structure

If you want to poke around the code:

```
cmd/cdnsim/              the CLI — parses flags, loads config, calls the runner
cmd/origin-server/       real HTTP/2 + HTTP/3 origin for emulated mode
cmd/edge-server/         real HTTP/2 + HTTP/3 caching edge for emulated mode

internal/
  experiment/            the runner that orchestrates everything
  transport/modeled/     the math — loss model, congestion control, TCP vs QUIC
  transport/emulated/    real HTTP clients for Docker mode
  video/                 the video player, ABR algorithms, manifest generation
  cache/                 LRU and ARC (adaptive replacement cache) implementations
  cdn/                   origin shield logic
  routing/               how clients get assigned to edge servers
  analysis/              bootstrap CIs, Cohen's d, Mann-Whitney U
  metrics/               collects and aggregates per-segment results

configs/                 YAML files defining different scenarios
docker/                  Dockerfiles and compose config
scripts/                 network shaping scripts and Python analysis/charting
docs/                    architecture decision records and walkthroughs
```

## Key concepts if you're new to this

**CDN (Content Delivery Network):** Instead of everyone fetching video from one server far away, copies are cached on servers closer to the viewer. Netflix has edge servers in most major cities. This project simulates that — the "edge" servers cache popular video segments so they don't have to be fetched from the "origin" every time.

**Packet loss:** The internet isn't perfect. Packets (small chunks of data) sometimes get dropped — maybe a router's buffer overflowed, or there was interference on a WiFi link. Loss rates of 1-5% are common on mobile networks and international links. The simulator models this with a Gilbert-Elliott process, which is a fancy way of saying "losses come in bursts" rather than being evenly spread out. This matters because bursty loss is much worse for TCP than scattered loss.

**Round-trip time (RTT):** How long it takes a message to go from your device to the server and back. Talking to a server in your city might be 5-20ms. Talking to a server across the ocean is 100-200ms. Higher RTT means every retransmission costs more time.

**ABR (Adaptive Bitrate):** The video player constantly adjusts the quality based on how fast segments are arriving. If the network is fast, you get 4K. If it slows down, you get 720p. The simulator uses Buffer-Based Adaptation (BBA) — it watches how full the playback buffer is and adjusts quality accordingly, similar to what Netflix uses.

**ARC (Adaptive Replacement Cache):** A smart caching algorithm that keeps track of both recently-used and frequently-used items, and adapts the balance between the two based on what the workload actually looks like. It's better than simple LRU (least recently used) for video because it resists "scan pollution" — when someone watches a video straight through, each segment is only accessed once, and under LRU those one-time segments would push out popular content that other viewers need.

## What I learned building this

The HOL blocking model went through three tries before I got it right. The first version applied a flat penalty to every stream when loss occurred, which overstated the effect. The second made every stream wait for the entire connection, which was right under loss but wrong when there was no loss. The final version switches between two behaviors: proportional completion when there's no loss, full-connection waiting when there is. It's not perfect — in reality the impact depends on exactly *where* in the byte stream the loss happens — but it captures the right physics.

I also found a bug in my ARC cache during code review. The original paper (Megiddo & Modha 2003) says that when the cache is full and you need to evict something, you should move it to a "ghost list" — a record of recently evicted items that helps the cache learn. My code was just deleting items outright, which meant the ghost list stayed empty and the cache never learned to adapt. The test I wrote after finding this (`TestARCB1GhostHitGrowsP`) would've caught it immediately if I'd written tests first. Lesson learned.

## Limitations (being honest)

- The congestion control model is Reno (the simplest textbook algorithm). Most real systems use Cubic, which recovers faster after loss. I don't know if switching would make the QUIC advantage bigger or smaller.
- The HOL blocking model is binary — either loss blocks everything or nothing. A packet-level model would be more accurate but way more complex.
- At prefetch depth 1 (fetching one segment at a time), TCP and QUIC perform identically because there's nothing to head-of-line block. The QUIC advantage only appears when you're fetching multiple segments simultaneously. This is correct behavior, but it means the "prefetch depth" setting matters a lot.
- The loss model is stationary — the probabilities don't change over time. Real network loss waxes and wanes.
- No connection migration, no UDP throttling by middleboxes, no hardware offload. The simulator answers "how much does the *protocol* matter?" not "what will my exact production numbers be?"

## Testing

```bash
make test  # runs go test ./... -race -count=1
```

12 packages with tests, all passing under Go's race detector. Coverage varies by package — the analysis code is at 85%, the cache at 67%, transport models around 77%. The ARC cache has regression tests specifically for the ghost-list bugs I found during development. The statistics package has property-based tests that verify the bootstrap produces the right coverage probability and the Mann-Whitney test has correct type-I error rates.

## Docs

- `docs/adr/` — 7 architecture decision records explaining *why* I made the choices I did (why Gilbert-Elliott over uniform loss, why ARC over LRU, why Reno and not Cubic, etc.)
- `docs/CODEMAP.md` — a walkthrough of how the code is organized, following the execution path from CLI to transport model
- `docs/EXECUTIVE_SUMMARY.md` — a one-pager for people who don't want to read code

## What's next

I want to add Cubic congestion control alongside Reno to see how the comparison changes. A per-packet HOL model that tracks which bytes belong to which stream would be more realistic. And the emulated mode could use way more test sessions — 24 is enough to detect big effects but you'd want 200+ for subtle ones.

## CLI reference

```
cdnsim run      --config <yaml> [--output-dir <dir>] [--verbose] [--profile]
cdnsim validate --config <yaml>
cdnsim sweep    --config <yaml> [--output-dir <dir>] [--verbose]
cdnsim analyze  --results-dir <dir>
```

`make` wraps these for common workflows: `build`, `test`, `run-modeled`, `sweep`, `docker-up`, `docker-down`, `run-emulated`, `analysis`, `full-suite`.
