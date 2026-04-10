# Analysis scripts

Post-processing utilities for `cdnsim` result directories. Both scripts are
pure Python 3 with an optional `matplotlib` dependency — the ASCII / CSV
output modes work without any extra packages.

## Dependencies

```bash
python3 --version   # 3.8+
pip install matplotlib   # optional; enables PNG charts
```

Scripts use `matplotlib.use("Agg")` before importing `pyplot`, so they
work on headless machines and inside containers.

## `compare.py`

Renders a side-by-side TCP-vs-QUIC comparison for a single scenario.

Inputs (all inside the results directory):

- `summary.json` — per-protocol aggregated metrics
- `comparison.json` — improvement %, bootstrap CIs, Cohen's d (optional)
- `raw.csv` — per-segment rows (optional; only needed for CDF / timeline)

Output:

- ASCII comparison table on stdout (always produced)
- `latency_percentiles.png` — grouped bar chart of p50/p95/p99 segment
  latency for TCP vs QUIC
- `latency_cdf.png` — overlaid CDFs of segment latency
- `bitrate_timeline.png` — mean bitrate vs segment index, one line per
  protocol
- `improvement_summary.png` — horizontal bars of improvement % with 95% CI
  error bars

### Example

```bash
scripts/analysis/compare.py results/reproduce_35pct
```

## `sweep_heatmap.py`

Visualises parameter sweeps across `(loss_rate, base_rtt_ms)`. Expects
the input directory to contain one subdirectory per scenario, each with
its own `summary.json` and `comparison.json`.

Output:

- `sweep_heatmap.png` — diverging (`RdYlGn`) heatmap of p95 segment
  latency improvement %, centred on zero. Each cell is annotated with
  the exact value.
- When matplotlib is missing, or when the sweep degenerates to a single
  axis, the script prints a `loss_pct,rtt_ms,improvement_pct` CSV table
  to stdout instead.

### Example

```bash
scripts/analysis/sweep_heatmap.py results/sweep_loss_rtt
```

## Interpreting the charts

- **Green cells / positive bars** — QUIC wins. The higher the value,
  the more QUIC's per-stream recovery beats TCP head-of-line blocking.
- **Red cells / negative bars** — TCP is ahead. Expected in clean,
  low-RTT scenarios where the QUIC user-space CPU cost dominates.
- **CI error bars that cross zero** indicate the improvement is not
  statistically distinguishable from noise at that sample size — run
  more `runs:` or more `clients:` to tighten them.
- **Latency CDFs** — the right tail is what matters for QoE. A QUIC
  curve that rises steeply and plateaus well before the TCP curve is
  the structural HOL-blocking win you are looking for.
