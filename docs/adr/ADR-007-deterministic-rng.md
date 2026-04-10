# ADR-007: Deterministic RNG Design with Loss Isolation

## Status
Accepted

## Context

Reproducibility is a non-negotiable requirement for a simulation study. Two runs with the same seed, same config, and same binary must produce bit-identical results. This rules out any use of global random state (`math/rand` default source), time-based seeds, or goroutine-non-deterministic ordering.

The challenge is that the simulator has multiple independent sources of randomness:

- **Client generation:** geographic perturbation of client positions.
- **Content popularity:** Zipf-distributed content selection.
- **Manifest generation:** segment sizes within a content item.
- **Loss simulation:** Gilbert-Elliott or uniform packet loss per transport.
- **Jitter:** Half-normal jitter on handshake and segment delivery.
- **Handshake:** QUIC 0-RTT success probability.
- **Routing:** BGP misroute probability, weighted capacity tie-breaking.
- **ABR:** (currently deterministic given inputs, but future ABR algorithms may add exploration noise).
- **Bootstrap resampling:** Statistical analysis.
- **Bandwidth trace generation:** 3-state Markov chain for synthetic traces.

If all of these share a single RNG, then changing the number of clients (which changes the number of geographic perturbation draws) would shift the loss sequence for all subsequent transports, making parameter sweeps unreliable. Worse, TCP and QUIC transports consume different numbers of RNG draws (QUIC's `Handshake` may draw for 0-RTT probability while TCP does not), so a shared RNG would produce different loss sequences for the two protocols.

## Decision

### No Global RNG

The codebase never calls `math/rand.Float64()`, `math/rand.Intn()`, or any top-level `math/rand` function. Every function that needs randomness takes an explicit `*rand.Rand` parameter. This is enforced by code review (and could be enforced by a linter in CI).

### Seed Derivation

The master seed comes from the YAML config (`seed: 20260409`). From this, deterministic child seeds are derived:

```
baseSeed = config.Seed + runIdx * 1_000_003
```

Each client gets its own RNG via FNV-1a hashing of the client ID:

```
hash("client-0042") -> stable uint32 -> baseSeed + int64(hash)
```

This means adding or removing clients does not shift other clients' random sequences.

### Loss-RNG Isolation

The most critical isolation is between the **loss simulator** and the **jitter/handshake** draws within a transport. Both TCP and QUIC transports derive a child RNG for the loss simulator from the parent's first draw:

```go
// In NewModeledTCPTransport and NewModeledQUICTransport:
lossSeed := rng.Int63()  // first draw from parent
loss := NewLossSimulator(profile.LossModel, rand.New(rand.NewSource(lossSeed)))
```

After this, the parent RNG is used only for jitter and handshake draws. The loss simulator has its own isolated stream. This ensures that:

1. TCP and QUIC transports constructed from the same seed see **exactly the same loss sequence**.
2. The fact that QUIC's `Handshake` draws one extra float (for 0-RTT probability) does not shift the loss sequence relative to TCP.

Without this isolation, the loss patterns would diverge between protocols, and the comparison would be confounded: we would not know whether differences in delivery time came from the transport's response to loss or from different loss patterns.

### protoSalt Was REMOVED from Modeled Mode

An earlier implementation included a protocol-specific salt in the seed:

```go
// REMOVED from modeled mode:
if proto == "quic-h3" { protoSalt = 1 << 32 }
baseSeed = config.Seed + int64(runIdx)*1_000_003 + protoSalt
```

This was **removed** because it defeated fair comparison. With a protocol salt, TCP and QUIC runs would see different client populations, different content assignments, and different loss patterns. The only way to isolate the transport variable is to give both protocols the same seed, same clients, same content, and same loss -- then measure how each protocol responds to those identical conditions.

The protocol salt is **retained** in emulated mode (`internal/experiment/emulated.go` line 76) because emulated transports have opaque internal state (kernel TCP buffers, quic-go's internal RNG) that we cannot control. The salt ensures that emulated runs at least have distinct but reproducible populations.

### Per-Component Seed Offsets

Different simulation components use different offsets from `baseSeed` to ensure independence:

- Transport RNG: `baseSeed + int64(hash(clientID + "-tr"))`
- ABR RNG: `baseSeed + int64(hash(clientID + "-abr"))`
- Popularity RNG: `baseSeed + 31`
- Shield transport RNG: `baseSeed + 17`
- Routing RNG: `baseSeed + int64(hash(clientID))`

These offsets are arbitrary constants chosen to be distinct. The FNV-1a hash of the client ID string provides per-client isolation.

## Consequences

**Bit-identical results:** The determinism test (`test/determinism_test.go`) runs the same config twice and asserts identical output. This passes on Linux, macOS, and in CI.

**Fair protocol comparison:** TCP and QUIC see identical loss patterns, identical client populations, and identical content. The only variable is the transport's behavior: HOL blocking, handshake latency, per-stream recovery.

**Thread safety:** Each `*rand.Rand` instance is used by exactly one goroutine (the current implementation is single-threaded per run). If we parallelize client sessions in a future phase, each goroutine will need its own `*rand.Rand` derived from a per-client seed.

**Cost of RNG threading:** Every function signature that needs randomness has an extra `*rand.Rand` parameter. This is verbose but explicit. There are approximately 15 call sites that pass RNG parameters. The alternative (a context-carried RNG) would be cleaner but less obvious at each call site.

## Alternatives Considered

**Global `math/rand` with `rand.Seed()`:** The simplest approach, but non-reproducible in the presence of goroutines (the global source is mutex-protected and draw order depends on scheduling). Also, `rand.Seed()` was deprecated in Go 1.20. Rejected.

**Context-carried RNG (`context.WithValue`):** Cleaner API (no explicit `*rand.Rand` parameters) but hides the randomness dependency, making it harder to reason about which components share random state. Also adds a type assertion at each use site. Rejected in favor of explicit parameters.

**Deterministic seeded goroutines (one RNG per goroutine, derived from a master):** This is what we would use for parallel execution. The current single-threaded design avoids the complexity of goroutine-deterministic scheduling. When we parallelize, we will derive per-client seeds (already done) and run each client session in its own goroutine with its own `*rand.Rand`.
