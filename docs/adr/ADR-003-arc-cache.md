# ADR-003: ARC (Adaptive Replacement Cache) for Edge Caching

## Context

CDN edge caches serve video segments to nearby clients. The eviction policy directly affects hit rates, which determine how many segment fetches actually traverse the lossy client-to-edge transport where TCP/QUIC differences show up. Higher hit rate = fewer transport-level fetches = less signal to measure. Lower hit rate = more fetches = more signal but also more noise. The cache policy needs to be realistic so hit rates in simulation match what production CDNs observe.

Video streaming has a specific pathology: scan pollution. A viewer watching a 30-segment video fetches segments 0, 1, 2, ... 29 in strict sequential order, never revisiting a segment. Under pure LRU, these sequential accesses evict previously-cached popular content, replacing it with segments that will never be accessed again. The next viewer of a different video causes the same effect -- the cache continuously cycles through one-hit wonders.

Production CDNs (Akamai, Cloudflare, Fastly) use scan-resistant caching policies precisely because of this. The most well-known is IBM's Adaptive Replacement Cache (ARC, Megiddo & Modha 2003), which balances recency and frequency adaptively.

## Decision

We implement ARC as the primary edge cache (`internal/cache/arc.go`) with a byte-bounded adaptation. Classical ARC uses count-based sizing (each item = 1 unit); our implementation tracks byte sizes through all four lists and the target parameter `p`.

ARC maintains four lists:

- T1 (recency): items seen exactly once recently. New items enter here.
- T2 (frequency): items seen at least twice recently. A T1 hit promotes to T2.
- B1 (ghost of T1): metadata-only records of items recently evicted from T1.
- B2 (ghost of T2): metadata-only records of items recently evicted from T2.

The adaptive parameter `p` (target size of T1 in bytes) shifts based on ghost-list hits: a B1 hit increases `p` (favor recency -- the workload is scanning), a B2 hit decreases `p` (favor frequency -- stable popularity distribution).

The `replace` subroutine evicts from T1 or T2 into the ghost lists based on whether `t1Bytes` exceeds `p`.

### The Case IV.B Bug

This one was annoying to find. During code review we discovered a correctness bug in ARC's Case IV.B handling -- the path taken when a fresh key is inserted and `|L1| == c` (combined size of T1 + B1 equals cache capacity) and `|T1| == c` (T1 alone is full).

The original ARC paper specifies: "Delete the LRU page of T1." Our initial implementation called `evictLRUList(c.t1, 't')`, which removed the entry entirely -- deleting it from the index. The correct behavior is to *move* T1's LRU entry into B1 as a ghost, preserving the key so a future re-access triggers a B1 hit and grows `p`.

The bug meant that under scan-heavy workloads, the adaptive parameter `p` never grew because the ghost entries that would trigger B1 hits were being silently discarded. ARC effectively degraded to static LRU.

The fix: `moveT1LRUToB1()` (line 305 of `arc.go`) explicitly moves the entry to B1's MRU position, preserving its key and original size in the ghost record. The regression test `TestARCB1GhostHitGrowsP` (in `arc_ghost_test.go`) verifies that `p` increases after a B1 ghost hit.

A related fix in the B1/B2 hit branches (the `CRIT-fix` comments at lines 165-169 and 193-196): when a ghost hit re-inserts a key, the `p` adjustment and `b1Bytes`/`b2Bytes` bookkeeping must use the ghost entry's recorded size, not the incoming item's size. The incoming item may have a different size (e.g., a different bitrate representation of the same segment). Using the wrong size corrupts the byte counters.

## Consequences

Scan resistance works: ARC correctly isolates sequential one-hit-wonder accesses in T1, preventing them from evicting frequently-accessed content in T2. In our simulations, ARC achieves 15-25% higher hit rates than LRU on Zipf-distributed video catalogs with sequential segment access.

Cache behavior is transport-independent -- it sees only keys and sizes, not protocols. This is verified statistically: `cache_hit_rate_pct` shows no significant difference between protocols (p > 0.1) in the enhanced comparison report.

The byte-bounded adaptation is approximate. Classical ARC is count-based, so `p` shifting in byte units can behave differently when item sizes vary widely. For video segments (which vary by bitrate but stay within a 2-4x range), this approximation is adequate.

## Alternatives Considered

*LRU:* Simplest and most common. Vulnerable to scan pollution, which is the dominant access pattern here. We provide LRU as a fallback (`cache.NewLRUCache`) but don't use it as the default. LRU results understate the hit rates a production CDN would achieve.

*LFU (Least Frequently Used):* Resists scans by favoring frequently-accessed items, but pure LFU has no recency signal -- a segment popular last hour but now cold stays cached indefinitely. Also requires per-item frequency counters that grow without bound unless decayed. ARC's T2 list provides frequency-like behavior without explicit counters.

*LIRS (Low Inter-reference Recency Set):* An academic algorithm (Jiang & Zhang 2002) that uses inter-reference recency to detect scan patterns. Strong theoretical properties but significantly harder to implement correctly (the stack pruning logic is subtle), and its practical advantage over ARC for video workloads is marginal. We went with ARC for its simpler implementation and extensive production deployment history.
