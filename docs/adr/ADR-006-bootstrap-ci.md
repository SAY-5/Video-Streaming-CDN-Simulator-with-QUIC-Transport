# ADR-006: Bootstrap Confidence Intervals and Non-Parametric Testing

## Status
Accepted

## Context

The simulator produces per-session QoE metrics (p95 segment latency, startup latency, rebuffer count, average bitrate) for hundreds of viewing sessions under each protocol. To claim "QUIC improves p95 latency by 35%," we need:

1. A **confidence interval** on the improvement percentage, so decision-makers know the range of plausible values.
2. A **significance test**, so we can distinguish real effects from noise.
3. An **effect size measure**, so we can judge practical significance (a statistically significant 0.1% improvement is not worth deploying).

The standard approach -- compute means, assume normality, use a t-test -- fails here. Latency distributions are **right-skewed with heavy tails**: most segments arrive quickly, but a few (those hit by loss bursts or HOL blocking) take much longer. The mean is pulled by outliers, and the t-test's normality assumption is violated. P95 latency (our headline metric) is a quantile, not a mean, so the Central Limit Theorem does not apply directly.

We need distribution-free methods.

## Decision

### Bootstrap Confidence Intervals

We use the **percentile bootstrap** (Efron & Tibshirani, "An Introduction to the Bootstrap," 1993) with 1000 iterations to compute 95% CIs on all improvement percentages.

Implementation: `internal/analysis/statistics.go`, functions `Bootstrap` and `BootstrapImprovement`.

Algorithm for two-sample improvement CI:
1. Given TCP session-level values `a[1..n1]` and QUIC session-level values `b[1..n2]`.
2. For each iteration `i = 1..1000`:
   a. Draw `n1` values from `a` with replacement -> `a*`.
   b. Draw `n2` values from `b` with replacement -> `b*`.
   c. Compute `improvement[i] = (mean(a*) - mean(b*)) / mean(a*) * 100` (for lower-is-better metrics).
3. Sort `improvement[1..1000]`.
4. CI lower = `improvement[25]` (2.5th percentile).
5. CI upper = `improvement[975]` (97.5th percentile).

**1000 iterations** is the conventional choice (Efron & Tibshirani 1993, Ch. 13). Increasing to 10,000 narrows the CI bounds by ~3% but increases computation time 10x, which matters for the 20-point sweep. 1000 provides stable CI bounds for our sample sizes (100-200 sessions).

### Session-Level Resampling

**Critical design choice:** We resample at the **session level**, not the segment level. Each bootstrap sample draws complete sessions (each containing 15-30 segments), not individual segments.

Why: Segments within a session are **correlated**. They share the same loss model state, the same congestion controller state, the same ABR history, and the same buffer trajectory. Resampling individual segments from different sessions would break these within-session correlations and produce artificially narrow confidence intervals (the effective sample size would be inflated by the number of segments per session). Session-level resampling correctly treats each session as one independent observation.

This is implemented by having the `Collector.sessionsFor()` method return `[]PlaybackResult` (one per session) and having the metric extractors (e.g., `segmentLatencyP95`) compute the per-session statistic before the bootstrap loop resamples sessions.

### Mann-Whitney U Test

We use the **Mann-Whitney U test** (Wilcoxon rank-sum test) as the significance test (`internal/analysis/statistics.go`, `MannWhitneyU`). This is a non-parametric test for whether two independent samples come from the same distribution. It makes no assumption about the shape of the distribution.

The implementation uses the normal approximation with continuity correction for the p-value, which is accurate for our sample sizes (n > 20). Tie correction to the variance is omitted; this makes the reported p-value slightly conservative (larger) in the presence of ties, which is the safe direction.

We report significance at the p < 0.05 level with standard scientific notation (`***` for p < 0.001, `**` for p < 0.01, `*` for p < 0.05).

### Effect Size: Cohen's d

We compute **Cohen's d** with pooled standard deviation (`internal/analysis/statistics.go`, `CohensD`) as the effect size measure:

```
d = (mean1 - mean2) / sqrt(((n1-1)*s1^2 + (n2-1)*s2^2) / (n1+n2-2))
```

Interpretation follows Cohen (1988): |d| < 0.2 = negligible, < 0.5 = small, < 0.8 = medium, >= 0.8 = large.

**Cohen's d vs. Cliff's delta tradeoff:** Cohen's d assumes equal variances and is sensitive to outliers (it uses the mean and standard deviation). Cliff's delta is a non-parametric alternative based on the proportion of pairwise comparisons where group 1 > group 2. For skewed latency distributions, Cliff's delta would be more robust. We chose Cohen's d because it is more widely understood by engineering audiences, it has conventional thresholds for interpretation, and our session-level metrics (which are already aggregated from segment-level data) are closer to normally distributed than the raw segment latencies. If the distributions were extremely skewed, Cliff's delta would be preferred.

## Consequences

**What we gained:**
- CIs that are valid regardless of the underlying distribution shape.
- A significance test that does not assume normality.
- A single `EffectLabel` function (`analysis.go` line 340) that is the sole source of truth for interpreting effect sizes across the codebase, eliminating the divergence caught in review R1 (HIGH-3).

**What we gave up:**
- Bootstrap CIs are wider than parametric CIs when the data actually is normal. For our data, this is acceptable -- we prefer conservative intervals.
- 1000 bootstrap iterations add ~50ms per comparison. For a 20-point sweep with one comparison each, this is 1 second total -- negligible.

**Determinism:** All bootstrap and statistical functions take an explicit `*rand.Rand` parameter. No global state. Results are bit-identical across runs with the same seed.

## Alternatives Considered

**Parametric t-test with normal CI:** Assumes latency is normally distributed. It is not. Would produce invalid CIs and misleading p-values, especially for tail metrics like p95 and p99.

**BCa (bias-corrected and accelerated) bootstrap:** A refinement of the percentile bootstrap that corrects for bias and skewness in the bootstrap distribution. BCa produces second-order accurate CIs. We did not implement BCa because the additional complexity (computing the acceleration constant requires a jackknife) is not justified for our sample sizes and the improvement in CI accuracy is small.

**Permutation test instead of Mann-Whitney:** A permutation test computes the exact null distribution by enumerating all possible reassignments of sessions to groups. For n1 = n2 = 100 sessions, the number of permutations is astronomical, so an approximate permutation test (10,000 random permutations) would be needed. The Mann-Whitney U test with normal approximation is computationally cheaper and provides equivalent power for our sample sizes.
