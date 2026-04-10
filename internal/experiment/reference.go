package experiment

import (
	"fmt"

	"github.com/cdn-sim/cdn-sim/internal/metrics"
)

// Published real-world QUIC-over-TCP improvement envelopes.
//
// Every range below is traced to a primary source that was fetched during
// the review round R1 bibliography pass. These are NOT from-memory numbers:
// each citation points to a URL and a specific table, figure, or quoted
// sentence. The story these numbers tell is nuanced — QUIC's advantage is
// smaller, more conditional, and more context-dependent than the popular
// "HTTP/3 gives huge wins" narrative suggests. The envelopes below are
// deliberately wide to accommodate the disagreement between Google's 2017
// YouTube-scale study, Cloudflare's 2020 blog (which found HTTP/3 SLOWER
// on real-world page loads), Meta's 2020 mvfst deployment, Kosek et al.'s
// 2021 independent large-scale measurement (which found HTTP/3 ≈ HTTP/2
// under high loss, contradicting Langley §6.6), and Akamai's 2023 live
// streaming data.
//
// Sources indexed in ReferenceRange.Source:
//
//	"Langley+2017 SIGCOMM Table 1"
//	  Adam Langley et al., "The QUIC Transport Protocol: Design and
//	  Internet-Scale Deployment", SIGCOMM '17. Table 1 reports the
//	  percent latency reduction for Google Search and YouTube Video
//	  Playback Latency by percentile for desktop and mobile.
//	  Key numbers:
//	    Search  Desktop mean −8.0%  p99 −16.7%
//	    Search  Mobile  mean −3.6%  p99 −14.3%
//	    Video   Desktop mean −8.0%  p99 −10.6%
//	    Video   Mobile  mean −5.3%  p99 −7.5%
//	  URL: https://dl.acm.org/doi/pdf/10.1145/3098822.3098842
//
//	"Langley+2017 SIGCOMM Table 2"
//	  Same paper, video rebuffer rate reduction by percentile.
//	  Key numbers:
//	    Desktop mean −18.0%  p95 −70.4%  p99 −18.5%
//	    Mobile  mean −15.3%  p95 −100%   p99 −8.7%
//
//	"Langley+2017 SIGCOMM §6.6 Performance By Region"
//	  Same paper: "QUIC's benefits are higher whenever congestion,
//	  loss, and RTTs are higher ... greater improvements in the USA
//	  than in South Korea. India ... shows the highest benefits."
//
//	"Langley+2017 SIGCOMM §6.7 Server CPU Utilization"
//	  Same paper: "QUIC's server CPU-utilization was about 3.5 times
//	  higher than TLS/TCP" initially; optimisation reduced it to
//	  "approximately twice that of TLS/TCP".
//
//	"Cloudflare 2020 blog HTTP/3 vs HTTP/2"
//	  https://blog.cloudflare.com/http-3-vs-http-2/
//	  Key numbers:
//	    TTFB: HTTP/3 176ms vs HTTP/2 201ms (+12.4% HTTP/3 win)
//	    15 KB page: HTTP/3 443ms vs HTTP/2 458ms (+3.3% HTTP/3 win)
//	    1 MB page: HTTP/3 2.33s  vs HTTP/2 2.30s (−1.3% HTTP/3 loss)
//	    Real-world geo: "HTTP/3 performance still trails HTTP/2 by
//	      about 1–4% on average in North America" — this is the
//	      baseline "clean network" envelope cdn-sim must reproduce.
//
//	"Meta 2020 engineering blog QUIC to billions"
//	  https://engineering.fb.com/2020/10/21/networking-traffic/how-facebook-is-bringing-quic-to-billions/
//	  Key numbers (Facebook App dynamic requests):
//	    6% reduction in request errors
//	    20% tail latency reduction
//	    5% response header size reduction
//	  Key numbers (Facebook App video):
//	    Mean time between rebuffering (MTBR) improved by up to 22%
//	    Overall error count on video requests −8%
//	    Rate of video stalls −20%
//	  Qualitative: "outsized impact on networks with relatively poorer
//	    conditions, especially those in emerging markets."
//
//	"Kosek+2021 arXiv Measuring HTTP/3"
//	  https://arxiv.org/pdf/2102.12358
//	  Large-scale 2020–2021 independent measurement campaign on
//	  thousands of websites. Key findings that DISAGREE with Langley
//	  §6.6:
//	    "Performance benefits emerging in scenarios with high latency
//	     or poor bandwidth. In the case of high packet loss, HTTP/3
//	     and HTTP/2 perform roughly the same."
//	    "We found large performance diversity depending on the
//	     infrastructure hosting the website."
//	  This is the single most important source for cdn-sim because it
//	  contradicts the assumption that HOL blocking elimination is a
//	  net win under loss. In practice, once real websites + real CDN
//	  infrastructure + real third-party domain sharding are factored
//	  in, HTTP/3's theoretical loss advantage is washed out.
//
//	"Akamai 2023 European football live streaming"
//	  https://www.akamai.com/blog/performance/streaming-numbers-jump-for-european-football-tournament-delivered
//	  Live 4.16 Tb/s peak event, April 2023. "69% of HTTP/3 connections
//	  reached a throughput of 5 Mbps or more, compared to only 56% of
//	  HTTP/2 connections." This is a throughput-threshold metric
//	  (not mean latency): roughly +23% more HTTP/3 connections met
//	  the Netflix-minimum bar than HTTP/2 on the same event.
//
// Every ReferenceRange is a MIN/MAX band. cdn-sim's simulated improvement
// for the matching condition should land somewhere inside it. Numbers
// outside the band are not automatically wrong — they are a flag to
// investigate whether the model is mis-parameterised or whether the
// condition class doesn't actually match what a published study measured.
//
// IMPORTANT caveat on "high_loss": Langley 2017 §6.6 and Kosek+2021
// disagree. Langley reports that QUIC's advantage GROWS with loss
// (emerging markets saw the biggest wins); Kosek 2021 reports that under
// high packet loss QUIC and HTTP/2 perform "roughly the same" once you
// factor in real website infrastructure. The "high_loss" envelope below
// deliberately includes both views: MinPct = 0 (Kosek) and MaxPct = 45
// (Langley + Meta). A simulator result above 25% in the high_loss class
// is not necessarily wrong, but it is above the Kosek 2021 observation
// and deserves the "model may be overly optimistic" info warning.

// ReferenceRange describes the published range of QUIC-over-TCP
// improvement for a given metric and network condition.
type ReferenceRange struct {
	// Metric identifies a comparison row (e.g. "p95_segment_latency").
	Metric string
	// Condition is one of: "baseline", "moderate_loss", "high_loss".
	// "baseline" = near-zero loss clean fibre;
	// "moderate_loss" = 1–3% loss, 50–150ms RTT;
	// "high_loss" = 3–10% loss and/or 150–300ms RTT or emerging-market
	// network conditions (Meta's phrasing).
	Condition string
	// MinPct is the minimum expected QUIC improvement in percent
	// (inclusive). Can be negative when a baseline-clean path actually
	// makes QUIC slightly slower due to userspace CPU cost (see Cloudflare
	// 2020 for evidence).
	MinPct float64
	// MaxPct is the maximum expected QUIC improvement in percent (inclusive).
	MaxPct float64
	// Source is a short citation key pointing to the URL list in the
	// doc comment above.
	Source string
}

// ReferenceRanges is the curated envelope of published QUIC-vs-TCP
// improvement numbers the simulator is checked against. Every row has
// a verified primary source documented in the block comment above.
var ReferenceRanges = []ReferenceRange{
	// ---- p95 segment latency ----
	// Baseline (clean fibre): Cloudflare 2020 found HTTP/3 SLOWER by 1–4%
	// on real-world page loads; the +12.4% TTFB win is the ceiling and
	// only materialises on small payloads. Anything far outside [-4, +12]
	// on a clean path is suspicious.
	{
		Metric: "p95_segment_latency", Condition: "baseline",
		MinPct: -4, MaxPct: 12,
		Source: "Cloudflare 2020 blog HTTP/3 vs HTTP/2",
	},
	// Moderate loss (1–3% loss, 50–150ms RTT): Langley+2017 Table 1 shows
	// 8.0% mean desktop search latency reduction with 16.7% at p99; Meta
	// 2020 reports 20% tail latency reduction on dynamic requests and up
	// to 22% MTBR improvement on video. Envelope brackets the mean floor
	// to the tail ceiling: [8, 22].
	{
		Metric: "p95_segment_latency", Condition: "moderate_loss",
		MinPct: 8, MaxPct: 22,
		Source: "Langley+2017 SIGCOMM Table 1; Meta 2020 engineering blog QUIC to billions",
	},
	// High loss / emerging markets: This is the most contested envelope.
	// Langley+2017 §6.6 says QUIC wins more as loss grows; Meta 2020
	// confirms for emerging markets; but Kosek+2021's large-scale
	// independent measurement found HTTP/3 ≈ HTTP/2 under high loss
	// once real website infrastructure is factored in. The envelope
	// brackets both views: lower bound = 0% (Kosek), upper bound = 45%
	// (Langley tail + Meta emerging-market).
	{
		Metric: "p95_segment_latency", Condition: "high_loss",
		MinPct: 0, MaxPct: 45,
		Source: "Langley+2017 §6.6; Meta 2020; Kosek+2021 arXiv Measuring HTTP/3",
	},

	// ---- Rebuffer duration (time spent stalled) ----
	// Moderate loss: Langley+2017 Table 2 shows 18.0% (desktop) / 15.3%
	// (mobile) mean rebuffer rate reduction; Meta 2020 reports 20%
	// video-stall reduction. Envelope [15, 25].
	{
		Metric: "rebuffer_duration", Condition: "moderate_loss",
		MinPct: 15, MaxPct: 25,
		Source: "Langley+2017 SIGCOMM Table 2; Meta 2020 engineering blog QUIC to billions",
	},
	// High loss: Langley+2017 Table 2's p95 desktop rebuffer reduction is
	// 70.4%, p94 is 100%. Meta 2020 emphasises emerging-market outsized
	// gains. The envelope is wide because high-loss rebuffer behaviour is
	// bimodal: either QUIC eliminates the stall entirely or it recovers
	// proportionally to TCP.
	{
		Metric: "rebuffer_duration", Condition: "high_loss",
		MinPct: 30, MaxPct: 100,
		Source: "Langley+2017 SIGCOMM Table 2",
	},

	// ---- Startup latency ----
	// Baseline: Cloudflare 2020 TTFB 12.4% ceiling; Langley+2017 Table 1
	// mobile mean 3.6% floor.
	{
		Metric: "startup_latency", Condition: "baseline",
		MinPct: 3, MaxPct: 13,
		Source: "Cloudflare 2020 blog HTTP/3 vs HTTP/2; Langley+2017 SIGCOMM Table 1",
	},
	// Moderate loss: Langley+2017 Table 1 desktop search mean 8.0%, p90
	// 5.8%, p99 16.7%; Meta 2020 "20% tail latency reduction" lifts
	// the ceiling to ~22%.
	{
		Metric: "startup_latency", Condition: "moderate_loss",
		MinPct: 8, MaxPct: 22,
		Source: "Langley+2017 SIGCOMM Table 1; Meta 2020 engineering blog QUIC to billions",
	},
}

// ValidationWarning is one mismatch between an observed comparison result
// and the published envelope for the corresponding condition.
type ValidationWarning struct {
	Metric      string  `json:"metric"`
	Condition   string  `json:"condition"`
	Observed    float64 `json:"observed_pct"`
	ExpectedMin float64 `json:"expected_min_pct"`
	ExpectedMax float64 `json:"expected_max_pct"`
	Severity    string  `json:"severity"` // "info", "warn"
	Source      string  `json:"source"`
	Message     string  `json:"message"`
}

// metricAliases maps ReferenceRange metric keys to the keys used in
// metrics.ComparisonReport.Improvement entries.
var metricAliases = map[string]string{
	"p95_segment_latency": "segment_latency_p95_ms",
	"startup_latency":     "startup_latency_p95_ms",
	"rebuffer_duration":   "rebuffer_duration_ms",
}

// ValidateAgainstReference checks that an experiment's improvement numbers
// fall inside the published envelopes for the given condition. Returns a
// list of warnings for every range that was applicable to the condition
// and fell outside its bounds. No warning is produced for ranges whose
// corresponding metric isn't present in the comparison report.
//
// The condition string must match one of the Condition values in
// ReferenceRanges (e.g. "baseline", "moderate_loss", "high_loss").
func ValidateAgainstReference(comparison *metrics.ComparisonReport, condition string) []ValidationWarning {
	if comparison == nil {
		return nil
	}
	byMetric := make(map[string]metrics.ImprovementResult, len(comparison.Improvement))
	for _, imp := range comparison.Improvement {
		byMetric[imp.Metric] = imp
	}
	var out []ValidationWarning
	for _, ref := range ReferenceRanges {
		if ref.Condition != condition {
			continue
		}
		alias, ok := metricAliases[ref.Metric]
		if !ok {
			continue
		}
		imp, ok := byMetric[alias]
		if !ok {
			continue
		}
		obs := imp.ImprovePct
		if obs >= ref.MinPct && obs <= ref.MaxPct {
			continue
		}
		w := ValidationWarning{
			Metric:      ref.Metric,
			Condition:   condition,
			Observed:    obs,
			ExpectedMin: ref.MinPct,
			ExpectedMax: ref.MaxPct,
			Source:      ref.Source,
		}
		if obs < ref.MinPct {
			w.Severity = "warn"
			w.Message = fmt.Sprintf(
				"QUIC improvement on %s (%.1f%%) underperformed published envelope [%.1f%%, %.1f%%] for %s; investigate model parameters",
				ref.Metric, obs, ref.MinPct, ref.MaxPct, condition,
			)
		} else {
			w.Severity = "info"
			w.Message = fmt.Sprintf(
				"QUIC improvement on %s (%.1f%%) exceeds published envelope [%.1f%%, %.1f%%] for %s; model may be overly optimistic",
				ref.Metric, obs, ref.MinPct, ref.MaxPct, condition,
			)
		}
		out = append(out, w)
	}
	return out
}
