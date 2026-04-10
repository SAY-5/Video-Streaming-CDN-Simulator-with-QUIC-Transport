#!/usr/bin/env python3
"""sweep_heatmap.py — render a 2-parameter QUIC-vs-TCP improvement heatmap.

Reads sweep_index.json from a sweep results directory and for each result
entry loads the matching comparison.json (or enhanced_comparison.json) to
extract the segment_latency_p95_ms improvement. Writes sweep_heatmap.png
using a diverging RdYlGn colormap centered on zero.

Always prints a CSV table on stdout so the script works even without
matplotlib or numpy.

Usage:
    scripts/analysis/sweep_heatmap.py <sweep_results_directory>
"""
from __future__ import annotations

import json
import os
import sys

METRIC = "segment_latency_p95_ms"

BG = "#1a1a2e"
FG = "#e0e0e0"
GRID = "#3a3a52"


def _fail(msg: str, code: int = 1) -> int:
    print(f"sweep_heatmap.py: error: {msg}", file=sys.stderr)
    return code


def _load_json(path):
    if not os.path.isfile(path):
        return None
    try:
        with open(path, "r") as f:
            return json.load(f)
    except (OSError, json.JSONDecodeError) as e:
        print(f"sweep_heatmap.py: warn: cannot read {path}: {e}", file=sys.stderr)
        return None


def _extract_improvement(comparison, enhanced):
    """Return p95 improvement % from enhanced or legacy comparison."""
    if enhanced and isinstance(enhanced.get("improvements"), dict):
        row = enhanced["improvements"].get(METRIC)
        if isinstance(row, dict):
            try:
                return float(row.get("improvement_pct"))
            except (TypeError, ValueError):
                return None
    if comparison:
        for row in comparison.get("improvement", []) or []:
            if row.get("metric") == METRIC:
                try:
                    return float(row.get("improvement_pct"))
                except (TypeError, ValueError):
                    return None
    return None


def load_sweep(root):
    """Return (sweep_index, list of (params_dict, improvement_pct))."""
    index_path = os.path.join(root, "sweep_index.json")
    idx = _load_json(index_path)
    if not idx:
        return None, []
    points = []
    for entry in idx.get("results", []) or []:
        if not isinstance(entry, dict):
            continue
        params = entry.get("params") or {}
        subdir = entry.get("subdir") or ""
        if not subdir:
            continue
        full = os.path.join(root, subdir)
        comparison = _load_json(os.path.join(full, "comparison.json"))
        enhanced = _load_json(os.path.join(full, "enhanced_comparison.json"))
        imp = _extract_improvement(comparison, enhanced)
        if imp is None:
            continue
        points.append((params, imp))
    return idx, points


def print_csv(param_names, points):
    header = ",".join(list(param_names) + ["improvement_pct"])
    print(header)
    for params, imp in points:
        row = [str(params.get(n, "")) for n in param_names] + [f"{imp:.2f}"]
        print(",".join(row))


def render_heatmap(idx, points, out_dir):
    params_meta = idx.get("parameters") or []
    if len(params_meta) < 2:
        print("sweep_heatmap.py: heatmap requires exactly two sweep parameters; "
              f"found {len(params_meta)}. Printing CSV only.")
        param_names = [p.get("name") for p in params_meta] or (
            list(points[0][0].keys()) if points else []
        )
        print_csv(param_names, points)
        return False

    p1 = params_meta[0]
    p2 = params_meta[1]
    name1 = p1.get("name")
    name2 = p2.get("name")
    vals1 = list(p1.get("values") or sorted({pt[0].get(name1) for pt in points}))
    vals2 = list(p2.get("values") or sorted({pt[0].get(name2) for pt in points}))

    # Always CSV out.
    print_csv([name1, name2], points)

    try:
        import matplotlib
        matplotlib.use("Agg")
        import matplotlib.pyplot as plt
        import numpy as np
    except ImportError:
        print("matplotlib not installed; skipping PNG (CSV above).")
        return False

    matplotlib.rcParams["font.family"] = "serif"

    grid = np.full((len(vals2), len(vals1)), np.nan, dtype=float)
    for params, imp in points:
        try:
            j = vals1.index(params.get(name1))
            i = vals2.index(params.get(name2))
        except ValueError:
            continue
        grid[i, j] = imp

    if not np.isfinite(grid).any():
        print("sweep_heatmap.py: no finite improvement values; cannot render heatmap.")
        return False

    abs_max = float(np.nanmax(np.abs(grid)))
    if abs_max == 0:
        abs_max = 1.0

    fig, ax = plt.subplots(
        figsize=(1.2 * len(vals1) + 3.0, 0.8 * len(vals2) + 2.5)
    )
    fig.patch.set_facecolor(BG)
    ax.set_facecolor(BG)
    for spine in ax.spines.values():
        spine.set_color(GRID)
    ax.tick_params(colors=FG)
    ax.xaxis.label.set_color(FG)
    ax.yaxis.label.set_color(FG)
    ax.title.set_color(FG)

    x_edges = np.arange(len(vals1) + 1) - 0.5
    y_edges = np.arange(len(vals2) + 1) - 0.5
    mesh = ax.pcolormesh(
        x_edges, y_edges, grid,
        cmap="RdYlGn", vmin=-abs_max, vmax=abs_max, shading="flat",
    )

    ax.set_xticks(range(len(vals1)))
    ax.set_xticklabels([str(v) for v in vals1])
    ax.set_yticks(range(len(vals2)))
    ax.set_yticklabels([str(v) for v in vals2])
    ax.set_xlabel(name1)
    ax.set_ylabel(name2)
    ax.set_title("QUIC Improvement Surface", fontsize=14, pad=12)

    for i in range(len(vals2)):
        for j in range(len(vals1)):
            v = grid[i, j]
            if np.isnan(v):
                continue
            color = "black" if abs(v) > abs_max * 0.4 else FG
            ax.text(j, i, f"{v:+.1f}", ha="center", va="center",
                    color=color, fontsize=9)

    cbar = fig.colorbar(mesh, ax=ax)
    cbar.set_label("p95 segment-latency improvement (%)", color=FG)
    cbar.ax.yaxis.set_tick_params(color=FG)
    for t in cbar.ax.get_yticklabels():
        t.set_color(FG)

    fig.tight_layout()
    out = os.path.join(out_dir, "sweep_heatmap.png")
    fig.savefig(out, dpi=300, facecolor=BG)
    plt.close(fig)
    print(f"Heatmap saved to: {out}")
    return True


def main(argv):
    if len(argv) != 2:
        return _fail("usage: sweep_heatmap.py <sweep_results_directory>")
    root = argv[1]
    if not os.path.isdir(root):
        return _fail(f"not a directory: {root}")

    idx, points = load_sweep(root)
    if idx is None:
        return _fail(f"sweep_index.json not found in {root}")
    if not points:
        return _fail(f"no usable result subdirectories referenced from sweep_index.json in {root}")

    render_heatmap(idx, points, root)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
