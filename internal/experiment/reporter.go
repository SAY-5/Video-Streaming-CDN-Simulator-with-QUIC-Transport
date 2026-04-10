package experiment

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// WriteJSON writes aggregated metrics and comparison to JSON files.
func WriteJSON(results *ExperimentResults, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	summary := map[string]any{
		"config":       results.Config,
		"duration_ms":  results.Duration.Milliseconds(),
		"by_protocol":  results.ByProtocol,
	}
	if err := writeJSONFile(filepath.Join(dir, "summary.json"), summary); err != nil {
		return err
	}
	if results.Comparison != nil {
		if err := writeJSONFile(filepath.Join(dir, "comparison.json"), results.Comparison); err != nil {
			return err
		}
	}
	if results.EnhancedComparison != nil {
		if err := writeJSONFile(filepath.Join(dir, "enhanced_comparison.json"), results.EnhancedComparison); err != nil {
			return err
		}
	}
	return nil
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// WriteCSV writes per-segment raw data as CSV.
func WriteCSV(results *ExperimentResults, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "raw.csv")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	header := []string{
		"protocol", "abr", "content_id", "segment_index", "bitrate_kbps",
		"size_bytes", "ttfb_ms", "latency_ms", "goodput_mbps",
		"cache_hit", "shield_hit", "hol_blocked",
		"buffer_before_ms", "buffer_after_ms", "rebuffered",
	}
	if err := w.Write(header); err != nil {
		return err
	}
	for _, r := range results.RawResults {
		for _, s := range r.Segments {
			row := []string{
				r.Protocol, r.ABRName, r.ContentID,
				strconv.Itoa(s.Index),
				strconv.Itoa(s.BitrateKbps),
				strconv.FormatInt(s.SizeBytes, 10),
				strconv.FormatInt(s.TTFB.Milliseconds(), 10),
				strconv.FormatInt(s.TotalLatency.Milliseconds(), 10),
				strconv.FormatFloat(s.GoodputMbps, 'f', 3, 64),
				strconv.FormatBool(s.CacheHit),
				strconv.FormatBool(s.ShieldHit),
				strconv.FormatBool(s.HOLBlocked),
				strconv.FormatInt(s.BufferBefore.Milliseconds(), 10),
				strconv.FormatInt(s.BufferAfter.Milliseconds(), 10),
				strconv.FormatBool(s.Rebuffered),
			}
			if err := w.Write(row); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteSummary writes a human-readable comparison to the given writer and to
// summary.txt in the output dir.
func WriteSummary(results *ExperimentResults, dir string, w io.Writer) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "summary.txt"))
	if err != nil {
		return err
	}
	defer f.Close()
	mw := io.MultiWriter(f, w)
	if results.EnhancedComparison != nil {
		fmt.Fprintf(mw, "Experiment: %s\n", results.Config.Name)
		fmt.Fprintf(mw, "Description: %s\n", results.Config.Description)
		fmt.Fprintf(mw, "Seed: %d  Runs: %d  Duration: %v\n\n", results.Config.Seed, results.Config.Runs, results.Duration)
		return WriteEnhancedSummary(results.EnhancedComparison, mw)
	}
	fmt.Fprintf(mw, "Experiment: %s\n", results.Config.Name)
	fmt.Fprintf(mw, "Description: %s\n", results.Config.Description)
	fmt.Fprintf(mw, "Seed: %d  Runs: %d  Duration: %v\n\n", results.Config.Seed, results.Config.Runs, results.Duration)

	// Deterministic protocol iteration (MED-10 fix): map range order is
	// randomized in Go, so we sort keys before printing.
	protos := make([]string, 0, len(results.ByProtocol))
	for p := range results.ByProtocol {
		protos = append(protos, p)
	}
	sort.Strings(protos)
	for _, proto := range protos {
		m := results.ByProtocol[proto]
		fmt.Fprintf(mw, "== %s ==\n", proto)
		fmt.Fprintf(mw, "  sessions=%d abr=%s\n", m.NumSessions, m.ABR)
		fmt.Fprintf(mw, "  segment_latency_ms p50=%.1f p95=%.1f p99=%.1f\n",
			m.SegmentLatency.P50, m.SegmentLatency.P95, m.SegmentLatency.P99)
		fmt.Fprintf(mw, "  startup_latency_ms p50=%.1f p95=%.1f p99=%.1f\n",
			m.StartupLatency.P50, m.StartupLatency.P95, m.StartupLatency.P99)
		fmt.Fprintf(mw, "  avg_bitrate_kbps mean=%.1f std=%.1f\n",
			m.AvgBitrate.Mean, m.AvgBitrate.StdDev)
		fmt.Fprintf(mw, "  rebuffer_count mean=%.2f rebuffer_duration_ms mean=%.1f\n",
			m.RebufferCount.Mean, m.RebufferDuration.Mean)
		fmt.Fprintf(mw, "  cache_hit_rate_pct mean=%.1f hol_events/session mean=%.2f\n",
			m.CacheHitRate.Mean, m.HOLBlockRate.Mean)
		fmt.Fprintln(mw)
	}

	if results.Comparison != nil {
		fmt.Fprintln(mw, "== Comparison (QUIC vs TCP) ==")
		for _, imp := range results.Comparison.Improvement {
			fmt.Fprintf(mw,
				"  %-30s  tcp=%-10.2f quic=%-10.2f  improvement=%+6.2f%%  "+
					"[95%% CI: %+.2f%% .. %+.2f%%]  d=%.2f (%s)\n",
				imp.Metric, imp.TCPValue, imp.QUICValue, imp.ImprovePct,
				imp.CILowerPct, imp.CIUpperPct, imp.CohensD, imp.EffectLabel,
			)
		}
	}
	return nil
}
