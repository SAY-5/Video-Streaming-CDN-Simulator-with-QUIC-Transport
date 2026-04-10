#!/bin/bash
# scripts/netem/apply_topology.sh — apply a named end-to-end network
# scenario across the cdn-sim docker-compose topology. Runs tc inside
# each relevant container via `docker exec`.
#
# Interface mapping inside containers (Docker assigns eth<N> in
# alphabetical order of the compose network name):
#
#   origin-net   172.20.0.0/24   (alphabetically first)
#   shield-net   172.21.0.0/24
#   client-net   172.22.0.0/24
#
# Services and their interfaces:
#   origin        eth0=origin-net
#   shield        eth0=origin-net, eth1=shield-net
#   edge-sg       eth0=client-net, eth1=shield-net     (client-net < shield-net alphabetically)
#   edge-mumbai   eth0=client-net, eth1=shield-net
#   client        eth0=client-net
#
# Usage:
#   apply_topology.sh <scenario>
#
# Scenarios:
#   asia_deployment   Asia edges (SG, Mumbai) reach US-East origin via
#                     shield. Edge <-> client is a lossy last mile, and
#                     shield <-> origin is a long-RTT cross-Pacific path.
set -euo pipefail

SCENARIO="${1:-asia_deployment}"
COMPOSE_FILE="$(cd "$(dirname "$0")/../.." && pwd)/docker/docker-compose.yml"

resolve() {
    local svc="$1"
    local cid
    cid=$(docker compose -f "$COMPOSE_FILE" ps -q "$svc" 2>/dev/null || true)
    if [[ -z "$cid" ]]; then
        echo "service $svc is not running" >&2
        exit 1
    fi
    echo "$cid"
}

tc_exec() {
    local cid="$1"; shift
    docker exec "$cid" tc "$@"
}

apply_netem() {
    local cid="$1"
    local iface="$2"
    shift 2
    tc_exec "$cid" qdisc del dev "$iface" root 2>/dev/null || true
    tc_exec "$cid" qdisc add dev "$iface" root netem "$@"
}

case "$SCENARIO" in
    asia_deployment)
        echo "==> scenario: asia_deployment"

        origin=$(resolve origin)
        shield=$(resolve shield)
        edge_sg=$(resolve edge-sg)
        edge_mumbai=$(resolve edge-mumbai)
        # The client container does not need to be running for netem
        # application; rules live on shield and edge interfaces only.

        # Shield <-> Origin: long-haul, low loss, high bandwidth.
        # Shape on shield's origin-net interface (eth0).
        echo "shield eth0 (origin-net): RTT 180ms, 0.1% loss, 1Gbps"
        apply_netem "$shield" eth0 \
            delay 90ms 5ms distribution normal \
            loss 0.1% \
            rate 1000mbit

        # Edge-SG <-> Client: SG last mile, moderate.
        # edge-sg eth0 = client-net
        echo "edge-sg eth0 (client-net): RTT 25ms, 0.5% loss, 200Mbps"
        apply_netem "$edge_sg" eth0 \
            delay 12ms 2ms distribution normal \
            loss 0.5% \
            rate 200mbit

        # Edge-SG <-> Shield: regional hop.
        # edge-sg eth1 = shield-net
        echo "edge-sg eth1 (shield-net): RTT 60ms, 0.5% loss, 500Mbps"
        apply_netem "$edge_sg" eth1 \
            delay 30ms 3ms distribution normal \
            loss 0.5% \
            rate 500mbit

        # Edge-Mumbai <-> Client: Mumbai last mile, lossier.
        # edge-mumbai eth0 = client-net
        echo "edge-mumbai eth0 (client-net): RTT 40ms, 2% loss, 100Mbps"
        apply_netem "$edge_mumbai" eth0 \
            delay 20ms 4ms distribution normal \
            loss 2% \
            rate 100mbit

        # Edge-Mumbai <-> Shield: regional hop, slightly higher RTT.
        echo "edge-mumbai eth1 (shield-net): RTT 80ms, 0.5% loss, 500Mbps"
        apply_netem "$edge_mumbai" eth1 \
            delay 40ms 4ms distribution normal \
            loss 0.5% \
            rate 500mbit

        echo "==> asia_deployment applied"
        ;;
    harsh_asia)
        # Harsher profile than asia_deployment: higher loss and RTT on
        # every hop so the QUIC HOL-blocking advantage dominates the
        # userspace CPU tax on loopback docker bridges. This is the
        # profile the emulated-mode validation should use when it wants
        # to reproduce the modeled result on a single host.
        echo "==> scenario: harsh_asia"

        shield=$(resolve shield)
        edge_sg=$(resolve edge-sg)
        edge_mumbai=$(resolve edge-mumbai)

        # Shield <-> Origin: long-haul cross-Pacific, 5% loss.
        echo "shield eth0 (origin-net): RTT 220ms, 5% loss, 500Mbps"
        apply_netem "$shield" eth0 \
            delay 110ms 10ms distribution normal \
            loss 5% 25% \
            rate 500mbit

        # Edge-SG <-> Client: lossy last mile, 8% loss.
        echo "edge-sg eth0 (client-net): RTT 80ms, 8% loss, 50Mbps"
        apply_netem "$edge_sg" eth0 \
            delay 40ms 8ms distribution normal \
            loss 8% 30% \
            rate 50mbit

        # Edge-SG <-> Shield: regional hop, 3% loss.
        echo "edge-sg eth1 (shield-net): RTT 100ms, 3% loss, 200Mbps"
        apply_netem "$edge_sg" eth1 \
            delay 50ms 6ms distribution normal \
            loss 3% 20% \
            rate 200mbit

        # Edge-Mumbai <-> Client: even lossier last mile, 10% loss.
        echo "edge-mumbai eth0 (client-net): RTT 100ms, 10% loss, 30Mbps"
        apply_netem "$edge_mumbai" eth0 \
            delay 50ms 10ms distribution normal \
            loss 10% 30% \
            rate 30mbit

        # Edge-Mumbai <-> Shield.
        echo "edge-mumbai eth1 (shield-net): RTT 140ms, 3% loss, 200Mbps"
        apply_netem "$edge_mumbai" eth1 \
            delay 70ms 8ms distribution normal \
            loss 3% 20% \
            rate 200mbit

        echo "==> harsh_asia applied"
        ;;
    *)
        echo "unknown scenario: $SCENARIO" >&2
        echo "available: asia_deployment, harsh_asia" >&2
        exit 2
        ;;
esac
