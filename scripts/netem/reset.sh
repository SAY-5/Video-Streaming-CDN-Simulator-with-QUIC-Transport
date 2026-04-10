#!/bin/bash
# scripts/netem/reset.sh — clear netem/qdisc config from an interface.
#
# Usage: reset.sh <iface>
set -euo pipefail

if [[ $# -lt 1 ]]; then
    echo "usage: $0 <iface>" >&2
    exit 2
fi

iface="$1"
tc qdisc del dev "$iface" root 2>/dev/null || true
echo "cleared qdisc on $iface"
tc qdisc show dev "$iface"
