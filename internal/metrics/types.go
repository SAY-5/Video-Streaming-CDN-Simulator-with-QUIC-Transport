// Package metrics provides statistical aggregation over PlaybackResult data.
// It computes percentiles, means/stddev, comparison deltas, bootstrap
// confidence intervals, and Cohen's d effect sizes so experimental claims
// ship with defensible uncertainty bounds.
package metrics

// PercentileSet holds summary statistics for a latency-like metric where
// lower values are better.
type PercentileSet struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// StatSet holds basic descriptive statistics for a metric.
type StatSet struct {
	Mean   float64 `json:"mean"`
	StdDev float64 `json:"std_dev"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

// AggregatedMetrics is the full per-protocol result set of an experiment.
type AggregatedMetrics struct {
	Protocol         string        `json:"protocol"`
	ABR              string        `json:"abr"`
	NumSessions      int           `json:"num_sessions"`
	SegmentTTFB      PercentileSet `json:"segment_ttfb_ms"`
	SegmentLatency   PercentileSet `json:"segment_latency_ms"`
	StartupLatency   PercentileSet `json:"startup_latency_ms"`
	RebufferCount    StatSet       `json:"rebuffer_count"`
	RebufferDuration StatSet       `json:"rebuffer_duration_ms"`
	AvgBitrate       StatSet       `json:"avg_bitrate_kbps"`
	BitrateChanges   StatSet       `json:"bitrate_changes"`
	CacheHitRate     StatSet       `json:"cache_hit_rate_pct"`
	GoodputMbps      PercentileSet `json:"goodput_mbps"`
	HOLBlockRate     StatSet       `json:"hol_block_events_per_session"`
	ZeroRTTRate      float64       `json:"zero_rtt_success_rate,omitempty"`
}

// ImprovementResult is one comparison row with confidence interval and
// effect size.
type ImprovementResult struct {
	Metric       string  `json:"metric"`
	TCPValue     float64 `json:"tcp"`
	QUICValue    float64 `json:"quic"`
	ImprovePct   float64 `json:"improvement_pct"`
	CILowerPct   float64 `json:"ci_lower_pct"`
	CIUpperPct   float64 `json:"ci_upper_pct"`
	CohensD      float64 `json:"cohens_d"`
	EffectLabel  string  `json:"effect_label"`
	HigherIsGood bool    `json:"higher_is_good"`
}

// ComparisonReport is the top-level comparison between two protocols.
type ComparisonReport struct {
	Scenario    string              `json:"scenario"`
	TCP         AggregatedMetrics   `json:"tcp"`
	QUIC        AggregatedMetrics   `json:"quic"`
	Improvement []ImprovementResult `json:"improvement"`
}
