#!/usr/bin/env python3
"""compare.py — publication-quality TCP-vs-QUIC comparison charts + ASCII table.

Reads summary.json, comparison.json, enhanced_comparison.json (optional),
and raw.csv from a results directory produced by `cdnsim run`, prints an
ASCII comparison table, and — if matplotlib is available — writes four
300-DPI charts into the same directory:

    latency_percentiles.png
    latency_cdf.png
    bitrate_timeline.png
    improvement_summary.png

Usage:
    scripts/analysis/compare.py <results_directory> [--no-charts]
"""
from __future__ import annotations

import csv
import json
import math
import os
import sys
from collections import defaultdict

TCP = "tcp-h2"
QUIC = "quic-h3"

# cdn-sim color scheme
BG = "#1a1a2e"
FG = "#e0e0e0"
GRID = "#3a3a52"
QUIC_COLOR = "#4cc9f0"
TCP_COLOR = "#f72585"
POS_COLOR = "#43aa8b"
NEG_COLOR = "#f72585"
NEUTRAL_COLOR = "#6c757d"


# ---------------------------------------------------------------------------
# I/O helpers
# ---------------------------------------------------------------------------

def _fail(msg: str) -> None:
    print(f"compare.py: error: {msg}", file=sys.stderr)
    sys.exit(1)


def _load_json(path: str):
    if not os.path.isfile(path):
        return None
    try:
        with open(path, "r") as f:
            return json.load(f)
    except (OSError, json.JSONDecodeError) as e:
        print(f"compare.py: warn: cannot read {path}: {e}", file=sys.stderr)
        return None


def _load_raw(path: str):
    rows_by_proto: dict[str, list[dict]] = defaultdict(list)
    if not os.path.isfile(path):
        return rows_by_proto
    try:
        with open(path, "r", newline="") as f:
            reader = csv.DictReader(f)
            for row in reader:
                proto = row.get("protocol", "")
                rows_by_proto[proto].append(row)
    except OSError as e:
        print(f"compare.py: warn: cannot read {path}: {e}", file=sys.stderr)
    return rows_by_proto


def _safe_get(d, *keys, default=None):
    cur = d
    for k in keys:
        if not isinstance(cur, dict) or k not in cur:
            return default
        cur = cur[k]
    return cur


def _scenario_name(summary, comparison, enhanced) -> str:
    if enhanced and enhanced.get("scenario"):
        return enhanced["scenario"]
    if comparison and comparison.get("scenario"):
        return comparison["scenario"]
    cfg = (summary or {}).get("config", {}) or {}
    for key in ("name", "Name"):
        if isinstance(cfg, dict) and cfg.get(key):
            return str(cfg[key])
    return "unknown"


# ---------------------------------------------------------------------------
# Percentile helpers (pure python)
# ---------------------------------------------------------------------------

def _percentile(sorted_vals, pct):
    if not sorted_vals:
        return None
    if len(sorted_vals) == 1:
        return float(sorted_vals[0])
    k = (len(sorted_vals) - 1) * (pct / 100.0)
    lo = int(math.floor(k))
    hi = int(math.ceil(k))
    if lo == hi:
        return float(sorted_vals[lo])
    return float(sorted_vals[lo] + (sorted_vals[hi] - sorted_vals[lo]) * (k - lo))


def _raw_latencies(raw, proto):
    vals = []
    for r in raw.get(proto, []):
        try:
            vals.append(float(r.get("latency_ms", "")))
        except (TypeError, ValueError):
            continue
    vals.sort()
    return vals


def _raw_percentiles(raw, proto, pcts=(50, 75, 90, 95, 99)):
    vals = _raw_latencies(raw, proto)
    return {f"p{p}": _percentile(vals, p) for p in pcts}


# ---------------------------------------------------------------------------
# Improvement lookup — prefer enhanced_comparison.json
# ---------------------------------------------------------------------------

def _improvement_map(comparison, enhanced):
    """Return dict[metric_key] -> dict with normalized improvement fields."""
    out = {}
    if enhanced and isinstance(enhanced.get("improvements"), dict):
        for k, v in enhanced["improvements"].items():
            if not isinstance(v, dict):
                continue
            out[k] = {
                "metric": v.get("metric", k),
                "tcp_value": v.get("tcp_value"),
                "quic_value": v.get("quic_value"),
                "improvement_pct": v.get("improvement_pct"),
                "ci_lower_pct": v.get("ci_lower_pct"),
                "ci_upper_pct": v.get("ci_upper_pct"),
                "effect_size_d": v.get("effect_size_d"),
                "effect_interpretation": v.get("effect_interpretation"),
                "p_value": v.get("p_value"),
                "statistically_significant": v.get("statistically_significant"),
                "higher_is_good": v.get("higher_is_good"),
            }
        return out
    if comparison:
        for row in comparison.get("improvement", []) or []:
            metric = row.get("metric")
            if metric:
                out[metric] = dict(row)
    return out


def _compute_pct(tcp_val, quic_val, higher_is_good):
    try:
        t = float(tcp_val); q = float(quic_val)
    except (TypeError, ValueError):
        return None
    if t == 0:
        return None
    return (q - t) / t * 100.0 if higher_is_good else (t - q) / t * 100.0


# ---------------------------------------------------------------------------
# ASCII table
# ---------------------------------------------------------------------------

def _fmt_num(v, width=10, prec=1):
    if v is None:
        return f"{'n/a':>{width}}"
    try:
        return f"{float(v):{width}.{prec}f}"
    except (TypeError, ValueError):
        return f"{str(v):>{width}}"


def print_ascii_table(summary, comparison, enhanced):
    by_proto = (summary or {}).get("by_protocol", {}) or {}
    tcp = by_proto.get(TCP, {}) or {}
    quic = by_proto.get(QUIC, {}) or {}
    scenario = _scenario_name(summary, comparison, enhanced)
    imp = _improvement_map(comparison, enhanced)

    def rowline(label, tcp_val, quic_val, metric_key=None, higher_is_good=False):
        pct = None
        if metric_key and metric_key in imp:
            pct = imp[metric_key].get("improvement_pct")
            if imp[metric_key].get("higher_is_good") is not None:
                higher_is_good = bool(imp[metric_key]["higher_is_good"])
        if pct is None:
            pct = _compute_pct(tcp_val, quic_val, higher_is_good)
        pct_str = f"{pct:+8.2f}%" if pct is not None else f"{'n/a':>9}"
        sig_mark = ""
        if metric_key and metric_key in imp:
            if imp[metric_key].get("statistically_significant") is True:
                sig_mark = " *"
            elif imp[metric_key].get("statistically_significant") is False:
                sig_mark = "  "
        print(f"{label:<32}{_fmt_num(tcp_val, 12, 1)}  "
              f"{_fmt_num(quic_val, 10, 1)}  {pct_str}{sig_mark}")

    bar = "=" * 70
    sep = "-" * 70
    print(bar)
    print(f"CDN-SIM RESULTS: {scenario}")
    print(bar)
    print(f"{'Metric':<32}{'TCP':>12}  {'QUIC':>10}  {'Δ%':>9}")
    print(sep)

    rowline("Segment Latency p50 (ms)",
            _safe_get(tcp, "segment_latency_ms", "p50"),
            _safe_get(quic, "segment_latency_ms", "p50"),
            "segment_latency_p50_ms")
    rowline("Segment Latency p75 (ms)",
            _safe_get(tcp, "segment_latency_ms", "p75"),
            _safe_get(quic, "segment_latency_ms", "p75"),
            "segment_latency_p75_ms")
    rowline("Segment Latency p90 (ms)",
            _safe_get(tcp, "segment_latency_ms", "p90"),
            _safe_get(quic, "segment_latency_ms", "p90"),
            "segment_latency_p90_ms")
    rowline("Segment Latency p95 (ms)",
            _safe_get(tcp, "segment_latency_ms", "p95"),
            _safe_get(quic, "segment_latency_ms", "p95"),
            "segment_latency_p95_ms")
    rowline("Segment Latency p99 (ms)",
            _safe_get(tcp, "segment_latency_ms", "p99"),
            _safe_get(quic, "segment_latency_ms", "p99"),
            "segment_latency_p99_ms")
    rowline("Startup Latency p50 (ms)",
            _safe_get(tcp, "startup_latency_ms", "p50"),
            _safe_get(quic, "startup_latency_ms", "p50"),
            "startup_latency_p50_ms")
    rowline("Startup Latency p95 (ms)",
            _safe_get(tcp, "startup_latency_ms", "p95"),
            _safe_get(quic, "startup_latency_ms", "p95"),
            "startup_latency_p95_ms")
    rowline("Rebuffer Count (mean)",
            _safe_get(tcp, "rebuffer_count", "mean"),
            _safe_get(quic, "rebuffer_count", "mean"),
            "rebuffer_count_mean")
    rowline("Rebuffer Duration (ms,mean)",
            _safe_get(tcp, "rebuffer_duration_ms", "mean"),
            _safe_get(quic, "rebuffer_duration_ms", "mean"),
            "rebuffer_duration_ms_mean")
    rowline("Avg Bitrate (Kbps)",
            _safe_get(tcp, "avg_bitrate_kbps", "mean"),
            _safe_get(quic, "avg_bitrate_kbps", "mean"),
            "avg_bitrate_kbps", higher_is_good=True)
    rowline("Cache Hit Rate (%)",
            _safe_get(tcp, "cache_hit_rate_pct", "mean"),
            _safe_get(quic, "cache_hit_rate_pct", "mean"),
            "cache_hit_rate_pct", higher_is_good=True)
    rowline("Goodput p50 (Mbps)",
            _safe_get(tcp, "goodput_mbps", "p50"),
            _safe_get(quic, "goodput_mbps", "p50"),
            "goodput_p50_mbps", higher_is_good=True)
    print(bar)

    if enhanced:
        advantage = enhanced.get("quic_advantage")
        if advantage is not None:
            try:
                print(f"VERDICT: QUIC advantage = {float(advantage):+.2f}%")
            except (TypeError, ValueError):
                print(f"VERDICT: {advantage}")
        findings = enhanced.get("key_findings") or []
        if findings:
            print("Key findings:")
            for f in findings:
                print(f"  - {f}")
        print(bar)


# ---------------------------------------------------------------------------
# Chart rendering
# ---------------------------------------------------------------------------

def _style_axes(ax):
    ax.set_facecolor(BG)
    for spine in ax.spines.values():
        spine.set_color(GRID)
    ax.tick_params(colors=FG, which="both")
    ax.yaxis.label.set_color(FG)
    ax.xaxis.label.set_color(FG)
    ax.title.set_color(FG)
    ax.grid(True, color=GRID, linestyle=":", linewidth=0.6, alpha=0.8)


def _nearest_index(sorted_vals, target):
    if not sorted_vals:
        return None
    lo, hi = 0, len(sorted_vals) - 1
    while lo < hi:
        mid = (lo + hi) // 2
        if sorted_vals[mid] < target:
            lo = mid + 1
        else:
            hi = mid
    return lo


def render_charts(results_dir, summary, comparison, enhanced, raw):
    try:
        import matplotlib
        matplotlib.use("Agg")
        import matplotlib.pyplot as plt
        from matplotlib.ticker import FuncFormatter  # noqa: F401
    except ImportError:
        print("matplotlib not installed; skipping chart rendering "
              "(pip install matplotlib to enable)")
        return []

    matplotlib.rcParams["font.family"] = "serif"
    matplotlib.rcParams["savefig.facecolor"] = BG
    matplotlib.rcParams["figure.facecolor"] = BG
    matplotlib.rcParams["axes.edgecolor"] = GRID
    matplotlib.rcParams["text.color"] = FG

    by_proto = (summary or {}).get("by_protocol", {}) or {}
    tcp_summary = by_proto.get(TCP, {}) or {}
    quic_summary = by_proto.get(QUIC, {}) or {}
    imp = _improvement_map(comparison, enhanced)
    saved = []

    # ------------------------------------------------------------
    # Chart 1: latency_percentiles.png
    # ------------------------------------------------------------
    try:
        pcts = ["p50", "p75", "p90", "p95", "p99"]
        # Prefer values computed from raw.csv so we can include p75/p90.
        tcp_raw_pcts = _raw_percentiles(raw, TCP) if raw.get(TCP) else {}
        quic_raw_pcts = _raw_percentiles(raw, QUIC) if raw.get(QUIC) else {}

        def pick(raw_pcts, summary_slot, key):
            if raw_pcts.get(key) is not None:
                return float(raw_pcts[key])
            v = _safe_get(summary_slot, "segment_latency_ms", key)
            try:
                return float(v) if v is not None else 0.0
            except (TypeError, ValueError):
                return 0.0

        tcp_vals = [pick(tcp_raw_pcts, tcp_summary, k) for k in pcts]
        quic_vals = [pick(quic_raw_pcts, quic_summary, k) for k in pcts]

        fig, ax = plt.subplots(figsize=(9, 5.5))
        fig.patch.set_facecolor(BG)
        _style_axes(ax)

        x = list(range(len(pcts)))
        width = 0.38
        tcp_x = [i - width / 2 for i in x]
        quic_x = [i + width / 2 for i in x]

        tcp_err = None
        quic_err = None
        p95_info = imp.get("segment_latency_p95_ms")
        if p95_info and p95_info.get("ci_lower_pct") is not None and p95_info.get("ci_upper_pct") is not None:
            # CI is in % terms; convert to absolute ms bounds around the TCP/QUIC p95 values.
            idx = pcts.index("p95")
            try:
                lo = float(p95_info["ci_lower_pct"]) / 100.0
                hi = float(p95_info["ci_upper_pct"]) / 100.0
                tcp_err = [[0] * len(pcts), [0] * len(pcts)]
                quic_err = [[0] * len(pcts), [0] * len(pcts)]
                quic_base = quic_vals[idx]
                # Error around QUIC bar expressing CI on the improvement.
                quic_err[0][idx] = abs(quic_base * lo) if lo < 0 else 0
                quic_err[1][idx] = abs(quic_base * hi) if hi > 0 else 0
            except (TypeError, ValueError):
                tcp_err = None
                quic_err = None

        ax.bar(tcp_x, tcp_vals, width, label="TCP/H2",
               color=TCP_COLOR, edgecolor=BG,
               yerr=tcp_err, ecolor=FG, capsize=4)
        ax.bar(quic_x, quic_vals, width, label="QUIC/H3",
               color=QUIC_COLOR, edgecolor=BG,
               yerr=quic_err, ecolor=FG, capsize=4)

        for xi, v in zip(tcp_x, tcp_vals):
            ax.text(xi, v, f"{v:.0f}", ha="center", va="bottom",
                    color=FG, fontsize=8)
        for xi, v in zip(quic_x, quic_vals):
            ax.text(xi, v, f"{v:.0f}", ha="center", va="bottom",
                    color=FG, fontsize=8)

        ax.set_xticks(x)
        ax.set_xticklabels([p.upper() for p in pcts])
        ax.set_ylabel("Latency (ms)")
        ax.set_xlabel("Percentile")
        ax.set_title("Segment Latency Percentiles", fontsize=14, pad=12)
        leg = ax.legend(facecolor=BG, edgecolor=GRID, labelcolor=FG, loc="upper left")
        for text in leg.get_texts():
            text.set_color(FG)
        fig.tight_layout()
        out = os.path.join(results_dir, "latency_percentiles.png")
        fig.savefig(out, dpi=300, facecolor=BG)
        plt.close(fig)
        saved.append("latency_percentiles.png")
    except Exception as e:
        print(f"warn: latency_percentiles.png failed: {e}", file=sys.stderr)

    # ------------------------------------------------------------
    # Chart 2: latency_cdf.png  (the flagship chart)
    # ------------------------------------------------------------
    try:
        tcp_l = _raw_latencies(raw, TCP)
        quic_l = _raw_latencies(raw, QUIC)
        if tcp_l or quic_l:
            fig, ax = plt.subplots(figsize=(9.5, 6))
            fig.patch.set_facecolor(BG)
            _style_axes(ax)

            def plot_cdf(vals, color, label, style):
                if not vals:
                    return None, None
                ys = [(i + 1) / len(vals) for i in range(len(vals))]
                # Guard against zeros in log scale.
                xs = [max(v, 0.5) for v in vals]
                ax.plot(xs, ys, color=color, linewidth=2.2,
                        linestyle=style, label=label)
                p50 = _percentile(vals, 50)
                p95 = _percentile(vals, 95)
                return p50, p95

            tcp_p50, tcp_p95 = plot_cdf(tcp_l, TCP_COLOR, "TCP/H2", "--")
            quic_p50, quic_p95 = plot_cdf(quic_l, QUIC_COLOR, "QUIC/H3", "-")

            def mark(val, color, label):
                if val is None:
                    return
                ax.axvline(max(val, 0.5), color=color, linestyle=":",
                           linewidth=1.2, alpha=0.8)
                ax.text(max(val, 0.5), 0.02, f" {label}\n {val:.0f} ms",
                        color=color, fontsize=8, rotation=90,
                        ha="left", va="bottom")

            mark(tcp_p50, TCP_COLOR, "TCP p50")
            mark(tcp_p95, TCP_COLOR, "TCP p95")
            mark(quic_p50, QUIC_COLOR, "QUIC p50")
            mark(quic_p95, QUIC_COLOR, "QUIC p95")

            ax.set_xscale("log")
            ax.set_xlabel("Segment Latency (ms, log scale)")
            ax.set_ylabel("Cumulative Probability")
            ax.set_ylim(0, 1.02)
            ax.set_title("Segment Latency Distribution (CDF)", fontsize=14, pad=12)

            legend_lines = []
            if tcp_p50 is not None:
                legend_lines.append(f"TCP/H2   median={tcp_p50:.0f} ms")
            if quic_p50 is not None:
                legend_lines.append(f"QUIC/H3  median={quic_p50:.0f} ms")
            leg = ax.legend(
                legend_lines or None, loc="lower right",
                facecolor=BG, edgecolor=GRID,
            )
            if leg is not None:
                for text in leg.get_texts():
                    text.set_color(FG)
            fig.tight_layout()
            out = os.path.join(results_dir, "latency_cdf.png")
            fig.savefig(out, dpi=300, facecolor=BG)
            plt.close(fig)
            saved.append("latency_cdf.png")
    except Exception as e:
        print(f"warn: latency_cdf.png failed: {e}", file=sys.stderr)

    # ------------------------------------------------------------
    # Chart 3: bitrate_timeline.png
    # ------------------------------------------------------------
    try:
        def series(proto):
            bucket = defaultdict(list)
            rebuf_idx = set()
            for r in raw.get(proto, []):
                try:
                    idx = int(r.get("segment_index", ""))
                    kbps = float(r.get("bitrate_kbps", ""))
                except (TypeError, ValueError):
                    continue
                bucket[idx].append(kbps)
                if str(r.get("rebuffered", "")).strip().lower() in ("true", "1", "yes"):
                    rebuf_idx.add(idx)
            xs = sorted(bucket.keys())
            ys = [sum(bucket[i]) / len(bucket[i]) for i in xs]
            return xs, ys, rebuf_idx

        tcp_x, tcp_y, tcp_rebuf = series(TCP)
        quic_x, quic_y, quic_rebuf = series(QUIC)

        if tcp_x or quic_x:
            fig, ax = plt.subplots(figsize=(9.5, 5.5))
            fig.patch.set_facecolor(BG)
            _style_axes(ax)

            if tcp_x:
                ax.plot(tcp_x, tcp_y, color=TCP_COLOR, linewidth=2.0, label="TCP/H2")
                ax.fill_between(tcp_x, 0, tcp_y, color=TCP_COLOR, alpha=0.3)
                if tcp_rebuf:
                    rx = [i for i in tcp_x if i in tcp_rebuf]
                    ry = [tcp_y[tcp_x.index(i)] for i in rx]
                    ax.scatter(rx, ry, color="#ff0055", s=18, zorder=5,
                               label="TCP rebuffered")
            if quic_x:
                ax.plot(quic_x, quic_y, color=QUIC_COLOR, linewidth=2.0, label="QUIC/H3")
                ax.fill_between(quic_x, 0, quic_y, color=QUIC_COLOR, alpha=0.3)
                if quic_rebuf:
                    rx = [i for i in quic_x if i in quic_rebuf]
                    ry = [quic_y[quic_x.index(i)] for i in rx]
                    ax.scatter(rx, ry, color="#ffbe0b", s=18, zorder=5,
                               label="QUIC rebuffered")

            ax.set_xlabel("Segment Index")
            ax.set_ylabel("Bitrate (Kbps)")
            ax.set_title("Bitrate Over Time", fontsize=14, pad=12)
            leg = ax.legend(facecolor=BG, edgecolor=GRID, loc="best")
            for text in leg.get_texts():
                text.set_color(FG)
            fig.tight_layout()
            out = os.path.join(results_dir, "bitrate_timeline.png")
            fig.savefig(out, dpi=300, facecolor=BG)
            plt.close(fig)
            saved.append("bitrate_timeline.png")
    except Exception as e:
        print(f"warn: bitrate_timeline.png failed: {e}", file=sys.stderr)

    # ------------------------------------------------------------
    # Chart 4: improvement_summary.png
    # ------------------------------------------------------------
    try:
        if imp:
            items = []
            for key, d in imp.items():
                try:
                    pct = float(d.get("improvement_pct"))
                except (TypeError, ValueError):
                    continue
                items.append((key, d, pct))
            # Sort by magnitude
            items.sort(key=lambda t: t[2])

            labels = [t[0].replace("_", " ") for t in items]
            vals = [t[2] for t in items]
            los = []
            his = []
            colors = []
            effect_texts = []
            for _, d, pct in items:
                try:
                    lo = float(d.get("ci_lower_pct")) if d.get("ci_lower_pct") is not None else pct
                    hi = float(d.get("ci_upper_pct")) if d.get("ci_upper_pct") is not None else pct
                except (TypeError, ValueError):
                    lo, hi = pct, pct
                los.append(max(0.0, pct - lo))
                his.append(max(0.0, hi - pct))
                sig = d.get("statistically_significant")
                if sig is True and pct > 0:
                    colors.append(POS_COLOR)
                elif sig is True and pct < 0:
                    colors.append(NEG_COLOR)
                else:
                    colors.append(NEUTRAL_COLOR)
                effect_texts.append(d.get("effect_interpretation") or "")

            fig, ax = plt.subplots(figsize=(10, max(4, 0.5 * len(items) + 2)))
            fig.patch.set_facecolor(BG)
            _style_axes(ax)

            y = list(range(len(items)))
            ax.barh(y, vals, xerr=[los, his], color=colors,
                    ecolor=FG, capsize=4, edgecolor=BG)
            ax.axvline(0, color=FG, linewidth=0.8)
            ax.set_yticks(y)
            ax.set_yticklabels(labels, fontsize=9, color=FG)
            ax.set_xlabel("Improvement % (positive = QUIC wins)")
            ax.set_title("QUIC vs TCP Improvement Summary", fontsize=14, pad=12)

            xmax = max([abs(v) for v in vals] + [1.0])
            ax.set_xlim(-xmax * 1.4, xmax * 1.4)

            for yi, v, eff in zip(y, vals, effect_texts):
                offset = xmax * 0.04 * (1 if v >= 0 else -1)
                ha = "left" if v >= 0 else "right"
                label = f"{v:+.1f}%"
                if eff:
                    label += f" ({eff})"
                ax.text(v + offset, yi, label, color=FG, fontsize=8,
                        va="center", ha=ha)

            fig.tight_layout()
            out = os.path.join(results_dir, "improvement_summary.png")
            fig.savefig(out, dpi=300, facecolor=BG)
            plt.close(fig)
            saved.append("improvement_summary.png")
    except Exception as e:
        print(f"warn: improvement_summary.png failed: {e}", file=sys.stderr)

    return saved


# ---------------------------------------------------------------------------
# Entrypoint
# ---------------------------------------------------------------------------

def main(argv):
    args = argv[1:]
    no_charts = False
    if "--no-charts" in args:
        no_charts = True
        args = [a for a in args if a != "--no-charts"]
    if len(args) != 1:
        _fail("usage: compare.py <results_directory> [--no-charts]")
    results_dir = args[0]
    if not os.path.isdir(results_dir):
        _fail(f"not a directory: {results_dir}")

    summary = _load_json(os.path.join(results_dir, "summary.json")) or {}
    comparison = _load_json(os.path.join(results_dir, "comparison.json")) or {}
    enhanced = _load_json(os.path.join(results_dir, "enhanced_comparison.json"))
    raw = _load_raw(os.path.join(results_dir, "raw.csv"))

    if not summary:
        _fail(f"summary.json not found in {results_dir}")

    print_ascii_table(summary, comparison, enhanced)

    if no_charts:
        return 0

    saved = render_charts(results_dir, summary, comparison, enhanced, raw)
    if saved:
        print(f"Charts saved to: {results_dir}")
        for name in saved:
            print(f"  - {name}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
