# ADR-006: Bootstrap Confidence Intervals and Non-Parametric Testing

## Context

The simulator produces per-session QoE metrics (p95 segment latency, startup latency, rebuffer count, average bitrate) for hundreds of viewing sessions under each protocol. To claim "QUIC improves p95 latency by 35%," we need a confidence interval on the improvement percentage, a significance test to distinguish real effects from noise, and an effect size measure so we can judge practical significance. (A statistically significant 0.1% improvement isn't worth deploying.)

The standard approach -- compute means, assume normality, use a t-test -- doesn't work here. Latency distributions are right-skewed with heavy tails: most segments arrive quickly, but a few (hit by loss bursts or HOL blocking) take much longer. The mean gets pulled by outliers, the t-test's normality assumption is violated. P95 latency (our headline metric) is a quantile, not a mean, so the Central Limit Theorem doesn't directly apply.

We need distribution-free methods.

## Decision

### Bootstrap Confidence Intervals

We use the percentile bootstrap (Efron & Tibshirani, "An Introduction to the Bootstrap," 1993) with 1000 iterations to compute 95% CIs on all improvement percentages.

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

1000 iterations is the conventional choice (Efron & Tibshirani 1993, Ch. 13). Going to 10,000 narrows the CI bounds by ~3% but takes 10x longer, which matters for the 20-point sweep. 1000 gives stable bounds for our sample sizes (100-200 sessions).

### Session-Level Resampling

This is a critical design choice: we resample at the session level, not the segment level. Each bootstrap sample draws complete sessions (each containing 15-30 segments), not individual segments.

Why: segments within a session are correlated. They share the same loss model state, congestion controller state, ABR history, and buffer trajectory. Resampling individual segments from different sessions would break these within-session correlations and produce artificially narrow confidence intervals -- the effective sample size would be inflated by the number of segments per session. Session-level resampling correctly treats each session as one independent observation.

Implemented by having `Collector.sessionsFor()` return `[]PlaybackResult` (one per session) and having the metric extractors (e.g., `segmentLatencyP95`) compute the per-session statistic before the bootstrap loop resamples.

### Mann-Whitney U Test

We use the Mann-Whitney U test (Wilcoxon rank-sum test) as the significance test (`internal/analysis/statistics.go`, `MannWhitneyU`). It's a non-parametric test for whether two independent samples come from the same distribution. No assumption about distribution shape.

The implementation uses the normal approximation with continuity correction for the p-value, which is accurate for our sample sizes (n > 20). Tie correction to the variance is omitted; this makes the reported p-value slightly conservative (larger) in the presence of ties, which is the safe direction.

We report significance at p < 0.05 with standard notation (`***` for p < 0.001, `**` for p < 0.01, `*` for p < 0.05).

### Effect Size: Cohen's d

We compute Cohen's d with pooled standard deviation (`internal/analysis/statistics.go`, `CohensD`):

```
d = (mean1 - mean2) / sqrt(((n1-1)*s1^2 + (n2-1)*s2^2) / (n1+n2-2))
```

Interpretation follows Cohen (1988): |d| < 0.2 = negligible, < 0.5 = small, < 0.8 = medium, >= 0.8 = large.

Cohen's d vs. Cliff's delta: Cohen's d assumes equal variances and is sensitive to outliers (uses mean and standard deviation). Cliff's delta is a non-parametric alternative based on pairwise comparisons. For skewed latency distributions, Cliff's delta would be more robust. We went with Cohen's d because it's more widely understood by engineering audiences, has conventional interpretation thresholds, and our session-level metrics (already aggregated from segment-level data) are closer to normal than the raw segment latencies. If the distributions were extremely skewed, Cliff's delta would be better.

## Consequences

We get CIs that are valid regardless of distribution shape, a significance test that doesn't assume normality, and a single `EffectLabel` function (`analysis.go` line 340) as the sole source of truth for interpreting effect sizes across the codebase -- eliminating the divergence caught in review R1 (HIGH-3).

Tradeoffs: bootstrap CIs are wider than parametric CIs when the data actually is normal. For our data that's fine -- we prefer conservative intervals. And 1000 bootstrap iterations add ~50ms per comparison. For a 20-point sweep, that's about 1 second total.

All bootstrap and statistical functions take an explicit `*rand.Rand` parameter. No global state. Results are bit-identical across runs with the same seed.

## Alternatives Considered

*Parametric t-test with normal CI:* Assumes latency is normally distributed. It isn't. Would produce invalid CIs and misleading p-values, especially for tail metrics like p95 and p99.

*BCa (bias-corrected and accelerated) bootstrap:* A refinement of the percentile bootstrap that corrects for bias and skewness. Produces second-order accurate CIs. We skipped it because computing the acceleration constant requires a jackknife, and the improvement in CI accuracy is small for our sample sizes.

*Permutation test instead of Mann-Whitney:* Computes the exact null distribution by enumerating all possible reassignments. For n1 = n2 = 100, that's astronomically many permutations, so you'd need an approximate version (10,000 random permutations). Mann-Whitney with normal approximation is cheaper and provides equivalent power for our sample sizes.
