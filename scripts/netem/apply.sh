#!/bin/bash
# scripts/netem/apply.sh — apply a named netem profile to a network
# interface. Must be run with NET_ADMIN (inside a container with
# cap_add: [NET_ADMIN], or via sudo on the host).
#
# Usage:
#   apply.sh <profile> <iface>
#
# Profiles:
#   baseline    low delay, negligible loss, high bandwidth
#   lossy       3% uniform loss, moderate RTT, moderate bw
#   high_loss   8% uniform loss, moderate RTT
#   mobile_3g   100ms RTT, 5% Gilbert-Elliott loss, 2 Mbps
#   satellite   600ms RTT, 1% loss, 20 Mbps, 40ms jitter
set -euo pipefail

if [[ $# -lt 2 ]]; then
    echo "usage: $0 <baseline|lossy|high_loss|mobile_3g|satellite> <iface>" >&2
    exit 2
fi

profile="$1"
iface="$2"

# Clear any existing qdisc first (idempotent).
tc qdisc del dev "$iface" root 2>/dev/null || true

case "$profile" in
    baseline)
        tc qdisc add dev "$iface" root netem \
            delay 10ms 1ms distribution normal \
            loss 0.01% \
            rate 1000mbit
        ;;
    lossy)
        tc qdisc add dev "$iface" root netem \
            delay 50ms 5ms distribution normal \
            loss 3% \
            rate 100mbit
        ;;
    high_loss)
        tc qdisc add dev "$iface" root netem \
            delay 60ms 8ms distribution normal \
            loss 8% \
            rate 100mbit
        ;;
    mobile_3g)
        # Gilbert-Elliott loss: p=5% transition into bad state, 20% recover.
        tc qdisc add dev "$iface" root netem \
            delay 100ms 20ms distribution normal \
            loss gemodel 5% 20% 80% 5% \
            rate 2mbit
        ;;
    satellite)
        tc qdisc add dev "$iface" root netem \
            delay 600ms 40ms distribution normal \
            loss 1% \
            rate 20mbit
        ;;
    *)
        echo "unknown profile: $profile" >&2
        exit 2
        ;;
esac

echo "applied profile=$profile iface=$iface"
tc qdisc show dev "$iface"
