package experiment

import (
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/cdn-sim/cdn-sim/internal/metrics"
)

const (
	prettyDoubleBar = "════════════════════════════════════════════════════════════════════"
	prettySingleBar = "────────────────────────────────────────────────────────────────────"
)

// WriteEnhancedSummary prints a publication-quality comparison summary
// to w, including per-metric improvements with 95% bootstrap CIs, Cohen's
// d effect sizes, Mann-Whitney p-values, an overall QUIC advantage label
// and key plain-English findings.
func WriteEnhancedSummary(report *metrics.EnhancedComparison, w io.Writer) error {
	if report == nil {
		return nil
	}

	fmt.Fprintln(w, prettyDoubleBar)
	fmt.Fprintf(w, "CDN-SIM RESULTS: %s\n", report.Scenario)
	fmt.Fprintln(w, prettyDoubleBar)
	fmt.Fprintln(w)

	writeLatencyBlock(w, "Segment Latency (p50)", report, "segment_latency_p50_ms", unitMs)
	writeLatencyBlock(w, "Segment Latency (p95)", report, "segment_latency_p95_ms", unitMs)
	writeLatencyBlock(w, "Segment Latency (p99)", report, "segment_latency_p99_ms", unitMs)
	writeLatencyBlock(w, "Startup Latency", report, "startup_latency_ms", unitMs)
	writeLatencyBlock(w, "Rebuffer Count", report, "rebuffer_count", unitCount)
	writeLatencyBlock(w, "Rebuffer Duration", report, "rebuffer_duration_ms", unitMs)
	writeLatencyBlock(w, "Avg Bitrate", report, "avg_bitrate_kbps", unitKbps)
	writeCacheBlock(w, report)
	writeHOLBlock(w, report)

	fmt.Fprintln(w, prettySingleBar)
	fmt.Fprintf(w, "VERDICT: %s\n", verdictLine(report.QUICAdvantage))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "KEY FINDINGS:")
	if len(report.KeyFindings) == 0 {
		fmt.Fprintln(w, "• (no notable findings)")
	}
	for _, f := range report.KeyFindings {
		fmt.Fprintf(w, "• %s\n", f)
	}
	fmt.Fprintln(w, prettyDoubleBar)
	return nil
}

type unit int

const (
	unitMs unit = iota
	unitCount
	unitKbps
)

// formatPrettyValue formats a raw per-protocol mean with the right unit suffix.
func formatPrettyValue(v float64, u unit) string {
	switch u {
	case unitMs:
		return fmt.Sprintf("%.1fms", v)
	case unitKbps:
		return fmt.Sprintf("%.0f kbps", v)
	case unitCount:
		return fmt.Sprintf("%.2f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// writeLatencyBlock prints one standard metric block. Omitted when the
// metric key is absent from the report.
func writeLatencyBlock(w io.Writer, title string, report *metrics.EnhancedComparison, key string, u unit) {
	imp, ok := report.Improvements[key]
	if !ok {
		return
	}
	fmt.Fprintf(w, "%s:\n", title)
	fmt.Fprintf(w, "  TCP (H2):  %s\n", formatPrettyValue(imp.TCPValue, u))
	fmt.Fprintf(w, "  QUIC (H3): %s\n", formatPrettyValue(imp.QUICValue, u))
	fmt.Fprintf(w, "  Improvement: %.1f%% [95%% CI: %.1f%% — %.1f%%]\n",
		imp.ImprovePct, imp.CILower, imp.CIUpper)
	fmt.Fprintf(w, "  Effect size: %.2f (%s), %s\n",
		math.Abs(imp.EffectSize), imp.EffectInterp, formatPStars(imp.PValue))
	fmt.Fprintln(w)
}

// writeCacheBlock prints the cache-hit-rate block, using the special
// "no significant difference" formatting when appropriate.
func writeCacheBlock(w io.Writer, report *metrics.EnhancedComparison) {
	imp, ok := report.Improvements["cache_hit_rate_pct"]
	if !ok {
		return
	}
	fmt.Fprintln(w, "Cache Hit Rate:")
	fmt.Fprintf(w, "  TCP (H2):  %.1f%%\n", imp.TCPValue)
	fmt.Fprintf(w, "  QUIC (H3): %.1f%%\n", imp.QUICValue)
	if !imp.Significant && math.Abs(imp.ImprovePct) <= 5 {
		fmt.Fprintf(w,
			"  No significant difference (p = %.2f) — cache is transport-independent ✓\n",
			imp.PValue,
		)
	} else {
		fmt.Fprintf(w, "  Improvement: %.1f%% [95%% CI: %.1f%% — %.1f%%]\n",
			imp.ImprovePct, imp.CILower, imp.CIUpper)
		fmt.Fprintf(w, "  Effect size: %.2f (%s), %s\n",
			math.Abs(imp.EffectSize), imp.EffectInterp, formatPStars(imp.PValue))
	}
	fmt.Fprintln(w)
}

// writeHOLBlock prints the HOL-blocking block.
func writeHOLBlock(w io.Writer, report *metrics.EnhancedComparison) {
	imp, ok := report.Improvements["hol_block_events"]
	if !ok {
		return
	}
	fmt.Fprintln(w, "HOL Block Events:")
	fmt.Fprintf(w, "  TCP (H2):  %.1f events/session\n", imp.TCPValue)
	if imp.QUICValue == 0 {
		fmt.Fprintln(w, "  QUIC (H3): 0 events/session (by design)")
	} else {
		fmt.Fprintf(w, "  QUIC (H3): %.1f events/session\n", imp.QUICValue)
	}
	fmt.Fprintln(w)
}

// formatPStars renders a p-value with significance stars as used in
// scientific papers.
func formatPStars(p float64) string {
	switch {
	case p < 0.001:
		return "p < 0.001 ***"
	case p < 0.01:
		return "p < 0.01 **"
	case p < 0.05:
		return "p < 0.05 *"
	default:
		return fmt.Sprintf("p = %.3f", p)
	}
}

// verdictLine produces the human-readable VERDICT sentence from the
// QUICAdvantage tag.
func verdictLine(tag string) string {
	switch strings.ToLower(tag) {
	case "dominant":
		return "QUIC is DOMINANT under these network conditions"
	case "large":
		return "QUIC shows LARGE advantage under these network conditions"
	case "moderate":
		return "QUIC shows MODERATE advantage under these network conditions"
	default:
		return "QUIC shows NEGLIGIBLE advantage under these network conditions"
	}
}
