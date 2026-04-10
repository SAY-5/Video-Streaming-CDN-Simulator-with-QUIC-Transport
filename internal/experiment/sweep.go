package experiment

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SweepParameter defines one axis of a parameter sweep.
//
// The Path field is a dotted path into the ExperimentConfig that the sweeper
// mutates before running. Supported paths (all that Phase 3 requires):
//
//	topology.edges[*].client_profile.loss_model.uniform_percent
//	topology.edges[*].client_profile.base_rtt_ms
//	topology.edges[*].client_profile.bandwidth_mbps
//	content.duration_seconds
//	clients.count
//
// Unsupported paths cause applyOverride to return an error.
type SweepParameter struct {
	Name   string    `yaml:"name" json:"name"`
	Path   string    `yaml:"path" json:"path"`
	Values []float64 `yaml:"values" json:"values"`
}

// SweepConfig is the YAML schema for a sweep experiment.
type SweepConfig struct {
	Name        string           `yaml:"name"`
	Description string           `yaml:"description"`
	BaseConfig  ExperimentConfig `yaml:"base"`
	Parameters  []SweepParameter `yaml:"parameters"`
	OutputDir   string           `yaml:"output_dir"`
}

// SweepEntry is one combination of parameter values along with the subdir
// where its per-run artifacts were written.
type SweepEntry struct {
	Params map[string]float64 `json:"params"`
	Subdir string             `json:"subdir"`
}

// SweepIndex is the manifest written to <output_dir>/sweep_index.json.
type SweepIndex struct {
	Scenario   string           `json:"scenario"`
	Parameters []SweepParameter `json:"parameters"`
	Results    []SweepEntry     `json:"results"`
}

// HeatmapData is a 2-parameter heatmap representation. Values[y][x] holds the
// segment_latency_p95_ms improvement percentage (QUIC vs TCP) for the
// combination (XValues[x], YValues[y]).
type HeatmapData struct {
	XLabel  string      `json:"x_label"`
	YLabel  string      `json:"y_label"`
	XValues []float64   `json:"x_values"`
	YValues []float64   `json:"y_values"`
	Values  [][]float64 `json:"values"`
}

// SweepResult is the in-memory result of a sweep run.
type SweepResult struct {
	Config  SweepConfig
	Index   *SweepIndex
	Heatmap *HeatmapData // nil if not exactly two parameters
}

// LoadSweepConfig loads a SweepConfig from a YAML file and applies default
// validation on the embedded base ExperimentConfig.
func LoadSweepConfig(path string) (*SweepConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sweep config %s: %w", path, err)
	}
	var cfg SweepConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse sweep config %s: %w", path, err)
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("sweep: name is required")
	}
	if len(cfg.Parameters) == 0 {
		return nil, fmt.Errorf("sweep: at least one parameter required")
	}
	for _, p := range cfg.Parameters {
		if p.Name == "" {
			return nil, fmt.Errorf("sweep: parameter name is required")
		}
		if len(p.Values) == 0 {
			return nil, fmt.Errorf("sweep: parameter %q needs at least one value", p.Name)
		}
	}
	if err := cfg.BaseConfig.Validate(); err != nil {
		return nil, fmt.Errorf("sweep base config: %w", err)
	}
	return &cfg, nil
}

// applyOverride mutates cfg by setting the value at the dotted path. See the
// SweepParameter doc comment for the supported set of paths.
func applyOverride(cfg *ExperimentConfig, path string, value float64) error {
	switch path {
	case "topology.edges[*].client_profile.loss_model.uniform_percent":
		for i := range cfg.Topology.Edges {
			if cfg.Topology.Edges[i].ClientProfile.LossModel.Type != "uniform" {
				cfg.Topology.Edges[i].ClientProfile.LossModel.Type = "uniform"
			}
			cfg.Topology.Edges[i].ClientProfile.LossModel.UniformPercent = value
		}
		return nil
	case "topology.edges[*].client_profile.base_rtt_ms":
		for i := range cfg.Topology.Edges {
			cfg.Topology.Edges[i].ClientProfile.BaseRTTMs = value
		}
		return nil
	case "topology.edges[*].client_profile.bandwidth_mbps":
		for i := range cfg.Topology.Edges {
			cfg.Topology.Edges[i].ClientProfile.BandwidthMbps = value
		}
		return nil
	case "content.duration_seconds":
		cfg.Content.DurationSeconds = int(value)
		return nil
	case "clients.count":
		cfg.Clients.Count = int(value)
		return nil
	default:
		return fmt.Errorf("applyOverride: unsupported path %q", path)
	}
}

// deepCopyConfig produces a deep copy of an ExperimentConfig via YAML
// round-trip. This is simple and safe enough given the size of the struct.
func deepCopyConfig(in ExperimentConfig) (ExperimentConfig, error) {
	data, err := yaml.Marshal(&in)
	if err != nil {
		return ExperimentConfig{}, err
	}
	var out ExperimentConfig
	if err := yaml.Unmarshal(data, &out); err != nil {
		return ExperimentConfig{}, err
	}
	return out, nil
}

// combination is one cross-product entry: parallel slices of names and values.
type combination struct {
	names  []string
	values []float64
}

// crossProduct enumerates all combinations of parameter values, preserving
// the parameter order given.
func crossProduct(params []SweepParameter) []combination {
	if len(params) == 0 {
		return nil
	}
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = p.Name
	}
	var out []combination
	idx := make([]int, len(params))
	for {
		vals := make([]float64, len(params))
		for i, p := range params {
			vals[i] = p.Values[idx[i]]
		}
		out = append(out, combination{names: names, values: vals})
		// Increment indices (rightmost fastest).
		k := len(params) - 1
		for k >= 0 {
			idx[k]++
			if idx[k] < len(params[k].Values) {
				break
			}
			idx[k] = 0
			k--
		}
		if k < 0 {
			break
		}
	}
	return out
}

// combinationLabel returns the stable subdirectory label for a combination,
// with parameters sorted by name so ordering is deterministic regardless of
// YAML declaration order.
func combinationLabel(c combination) string {
	type kv struct {
		k string
		v float64
	}
	pairs := make([]kv, len(c.names))
	for i := range c.names {
		pairs[i] = kv{c.names[i], c.values[i]}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].k < pairs[j].k })
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = fmt.Sprintf("%s=%s", p.k, formatSweepValue(p.v))
	}
	label := strings.Join(parts, ",")
	label = strings.ReplaceAll(label, "/", "_")
	// Preserve '.' inside numeric values by using formatSweepValue above with 'g'.
	return label
}

// formatSweepValue formats a float using minimal digits while keeping integers
// integer-looking, so labels like "loss_pct=3" and "rtt_ms=100" look clean.
//
// MED-4 fix: use math.Trunc + a small epsilon tolerance instead of raw
// float equality when deciding "is this an integer?". Direct == would
// misclassify 100.0001 as 100 and produce misleading directory names.
func formatSweepValue(v float64) string {
	if math.Abs(v-math.Trunc(v)) < 1e-9 && math.Abs(v) < math.MaxInt64 {
		return strconv.FormatInt(int64(math.Trunc(v)), 10)
	}
	// Replace '.' with '_' for filesystem friendliness.
	s := strconv.FormatFloat(v, 'g', -1, 64)
	return strings.ReplaceAll(s, ".", "_")
}

// combinationSeed computes baseSeed XOR hash(label) so each combination has
// a deterministic but distinct seed independent of execution order.
func combinationSeed(baseSeed int64, label string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(label))
	return baseSeed ^ int64(h.Sum64())
}

// RunSweep executes the cross-product of all parameter values and writes
// results into cfg.OutputDir. Each combination produces a subdirectory whose
// name encodes the parameter values (for example "loss_pct=3,rtt_ms=100").
//
// Determinism: each combination is seeded as baseSeed XOR hash(label), so the
// order of execution does not affect individual run outputs.
//
// Concurrency: combinations run sequentially in Phase 3. A future phase can
// parallelise across goroutines.
func RunSweep(ctx context.Context, cfg SweepConfig, logger *slog.Logger) (*SweepResult, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.OutputDir == "" {
		return nil, fmt.Errorf("sweep: output_dir is required")
	}
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("sweep: mkdir %s: %w", cfg.OutputDir, err)
	}

	combos := crossProduct(cfg.Parameters)
	index := &SweepIndex{
		Scenario:   cfg.Name,
		Parameters: cfg.Parameters,
		Results:    make([]SweepEntry, 0, len(combos)),
	}

	// latencyP95Pct[label] -> improvement % for segment_latency_p95_ms
	latencyP95Pct := make(map[string]float64, len(combos))

	for ci, combo := range combos {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		label := combinationLabel(combo)
		logger.Info("sweep combination",
			"index", ci, "total", len(combos), "label", label)

		patched, err := deepCopyConfig(cfg.BaseConfig)
		if err != nil {
			return nil, fmt.Errorf("sweep: deep copy base config: %w", err)
		}
		for pi, p := range cfg.Parameters {
			if err := applyOverride(&patched, p.Path, combo.values[pi]); err != nil {
				return nil, fmt.Errorf("sweep: apply %s: %w", p.Path, err)
			}
		}
		patched.Seed = combinationSeed(cfg.BaseConfig.Seed, label)
		subdir := filepath.Join(cfg.OutputDir, label)
		patched.Output.Dir = subdir
		patched.Name = cfg.BaseConfig.Name + "/" + label

		if err := patched.Validate(); err != nil {
			return nil, fmt.Errorf("sweep: validate combination %s: %w", label, err)
		}

		runner := NewRunner(patched, logger)
		results, err := runner.Run(ctx)
		if err != nil {
			return nil, fmt.Errorf("sweep: run combination %s: %w", label, err)
		}
		if err := WriteJSON(results, subdir); err != nil {
			return nil, fmt.Errorf("sweep: write json for %s: %w", label, err)
		}
		if patched.Output.EmitCSV {
			if err := WriteCSV(results, subdir); err != nil {
				return nil, fmt.Errorf("sweep: write csv for %s: %w", label, err)
			}
		}

		params := make(map[string]float64, len(combo.names))
		for i, n := range combo.names {
			params[n] = combo.values[i]
		}
		index.Results = append(index.Results, SweepEntry{
			Params: params,
			Subdir: label,
		})

		if results.Comparison != nil {
			for _, imp := range results.Comparison.Improvement {
				if imp.Metric == "segment_latency_p95_ms" {
					latencyP95Pct[label] = imp.ImprovePct
					break
				}
			}
		}
	}

	// Write top-level index.
	indexPath := filepath.Join(cfg.OutputDir, "sweep_index.json")
	if err := writeJSONFileSweep(indexPath, index); err != nil {
		return nil, err
	}

	result := &SweepResult{
		Config: cfg,
		Index:  index,
	}

	// Heatmap for exactly two parameters.
	if len(cfg.Parameters) == 2 {
		hm := buildHeatmap(cfg.Parameters, latencyP95Pct)
		result.Heatmap = hm
		heatPath := filepath.Join(cfg.OutputDir, "heatmap.json")
		if err := writeJSONFileSweep(heatPath, hm); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// buildHeatmap constructs a HeatmapData from the per-combination improvement
// percentages. The first parameter is the X axis, the second is the Y axis.
// Values are sorted ascending.
func buildHeatmap(params []SweepParameter, latencyP95Pct map[string]float64) *HeatmapData {
	xVals := append([]float64(nil), params[0].Values...)
	yVals := append([]float64(nil), params[1].Values...)
	sort.Float64s(xVals)
	sort.Float64s(yVals)
	hm := &HeatmapData{
		XLabel:  params[0].Name,
		YLabel:  params[1].Name,
		XValues: xVals,
		YValues: yVals,
		Values:  make([][]float64, len(yVals)),
	}
	for y := range yVals {
		hm.Values[y] = make([]float64, len(xVals))
	}
	for y, yv := range yVals {
		for x, xv := range xVals {
			combo := combination{
				names:  []string{params[0].Name, params[1].Name},
				values: []float64{xv, yv},
			}
			label := combinationLabel(combo)
			hm.Values[y][x] = latencyP95Pct[label]
		}
	}
	return hm
}

func writeJSONFileSweep(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
