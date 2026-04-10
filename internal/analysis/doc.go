// Package analysis provides statistical primitives used by Phase 3
// components of cdn-sim: bootstrap confidence intervals, Cohen's d
// effect sizes, the Mann-Whitney U test, ECDFs, and common summary
// statistics.
//
// These routines are intentionally implemented on top of the Go
// standard library only so they can be vendored into any experiment
// pipeline without introducing third-party dependencies. Latency
// distributions observed in CDN experiments are right-skewed with
// heavy tails, so non-parametric tools (bootstrap, Mann-Whitney) are
// preferred over Gaussian-assuming alternatives.
package analysis
