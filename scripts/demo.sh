#!/bin/bash
set -euo pipefail

echo "╔══════════════════════════════════════════════════════════╗"
echo "║       cdn-sim: QUIC vs TCP CDN Simulator Demo            ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

# Build
echo "▸ Building..."
make build 2>/dev/null
echo "  ✓ Built bin/cdnsim"
echo ""

# Run flagship experiment
echo "▸ Running flagship experiment (200 clients, 10 runs, ~12s)..."
bin/cdnsim run --config configs/reproduce_35pct.yaml --output-dir results/demo 2>&1 | grep -E "VERDICT|KEY FIND|p95 segment"
echo ""

# Run depth-1 comparison (shows honest negative result)
echo "▸ Running depth-1 comparison (no multiplexing — honest baseline)..."
bin/cdnsim run --config configs/depth1_startup.yaml --output-dir results/demo_depth1 2>&1 | grep "VERDICT"
echo ""

# Run parameter sweep
echo "▸ Running parameter sweep (20 combinations, ~10s)..."
bin/cdnsim sweep --config configs/sweep_loss_rtt.yaml --output-dir results/demo_sweep 2>&1 | tail -1
echo ""

# Generate charts (if matplotlib available)
if python3 -c "import matplotlib" 2>/dev/null; then
    echo "▸ Generating charts..."
    python3 scripts/analysis/compare.py results/demo 2>&1 | tail -5
    python3 scripts/analysis/sweep_heatmap.py results/demo_sweep 2>&1 | tail -1
    echo ""
fi

# Summary
echo "╔══════════════════════════════════════════════════════════╗"
echo "║  Results                                                  ║"
echo "╠══════════════════════════════════════════════════════════╣"
echo "║  Flagship:   results/demo/                                ║"
echo "║  Depth-1:    results/demo_depth1/                         ║"
echo "║  Sweep:      results/demo_sweep/                          ║"
echo "║  Dashboard:  open scripts/analysis/dashboard.html         ║"
echo "║              (drag summary.json onto the page)            ║"
echo "╚══════════════════════════════════════════════════════════╝"
