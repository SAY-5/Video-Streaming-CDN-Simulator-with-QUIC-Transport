// cdnsim is the command-line entry point to the CDN simulator. It loads a
// scenario YAML, runs the experiment, and writes JSON, CSV, and human-readable
// summaries to the configured output directory.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"

	"github.com/cdn-sim/cdn-sim/internal/experiment"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]

	switch sub {
	case "run", "validate":
		runOrValidate(sub, os.Args[2:])
	case "sweep":
		runSweep(os.Args[2:])
	case "analyze":
		runAnalyze(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

// runOrValidate handles the existing "run" and "validate" subcommands.
func runOrValidate(sub string, args []string) {
	fs := flag.NewFlagSet(sub, flag.ExitOnError)
	configPath := fs.String("config", "", "path to YAML config (required)")
	outputDir := fs.String("output-dir", "", "override output directory")
	verbose := fs.Bool("verbose", false, "enable debug logging")
	profile := fs.Bool("profile", false, "write cpu.prof and mem.prof to output dir")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "--config is required")
		usage()
		os.Exit(2)
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := experiment.LoadConfig(*configPath)
	if err != nil {
		logger.Error("load config", "err", err)
		os.Exit(1)
	}

	switch sub {
	case "validate":
		fmt.Printf("config %s is valid\n", *configPath)
		return
	case "run":
		if *outputDir != "" {
			cfg.Output.Dir = *outputDir
		}
		if cfg.Output.Dir == "" {
			cfg.Output.Dir = filepath.Join("results", sanitise(cfg.Name))
		}
		if *profile {
			os.MkdirAll(cfg.Output.Dir, 0o755)
			cpuF, err := os.Create(filepath.Join(cfg.Output.Dir, "cpu.prof"))
			if err != nil {
				logger.Error("create cpu.prof", "err", err)
				os.Exit(1)
			}
			pprof.StartCPUProfile(cpuF)
			defer func() {
				pprof.StopCPUProfile()
				cpuF.Close()
				memF, err := os.Create(filepath.Join(cfg.Output.Dir, "mem.prof"))
				if err != nil {
					logger.Error("create mem.prof", "err", err)
					return
				}
				runtime.GC()
				pprof.WriteHeapProfile(memF)
				memF.Close()
				logger.Info("profiles written", "cpu", filepath.Join(cfg.Output.Dir, "cpu.prof"), "mem", filepath.Join(cfg.Output.Dir, "mem.prof"))
			}()
		}
		runner := experiment.NewRunner(*cfg, logger)
		ctx := context.Background()
		results, err := runner.Run(ctx)
		if err != nil {
			logger.Error("run", "err", err)
			os.Exit(1)
		}
		if cfg.Output.EmitJSON || (!cfg.Output.EmitJSON && !cfg.Output.EmitCSV) {
			if err := experiment.WriteJSON(results, cfg.Output.Dir); err != nil {
				logger.Error("write json", "err", err)
				os.Exit(1)
			}
		}
		if cfg.Output.EmitCSV {
			if err := experiment.WriteCSV(results, cfg.Output.Dir); err != nil {
				logger.Error("write csv", "err", err)
				os.Exit(1)
			}
		}
		if err := experiment.WriteSummary(results, cfg.Output.Dir, os.Stdout); err != nil {
			logger.Error("write summary", "err", err)
			os.Exit(1)
		}
	}
}

// runSweep handles the "sweep" subcommand.
func runSweep(args []string) {
	fs := flag.NewFlagSet("sweep", flag.ExitOnError)
	configPath := fs.String("config", "", "path to sweep YAML config (required)")
	outputDir := fs.String("output-dir", "", "override sweep output directory")
	verbose := fs.Bool("verbose", false, "enable debug logging")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "--config is required")
		usage()
		os.Exit(2)
	}
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := experiment.LoadSweepConfig(*configPath)
	if err != nil {
		logger.Error("load sweep config", "err", err)
		os.Exit(1)
	}
	if *outputDir != "" {
		cfg.OutputDir = *outputDir
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = filepath.Join("results", sanitise(cfg.Name))
	}
	ctx := context.Background()
	res, err := experiment.RunSweep(ctx, *cfg, logger)
	if err != nil {
		logger.Error("sweep", "err", err)
		os.Exit(1)
	}
	fmt.Printf("sweep %q: %d combinations written to %s\n", cfg.Name, len(res.Index.Results), cfg.OutputDir)
	if res.Heatmap != nil {
		fmt.Printf("heatmap (%s x %s) written to %s/heatmap.json\n",
			res.Heatmap.XLabel, res.Heatmap.YLabel, cfg.OutputDir)
	}
}

// runAnalyze delegates to the Python compare.py analysis script.
func runAnalyze(args []string) {
	fs := flag.NewFlagSet("analyze", flag.ExitOnError)
	resultsDir := fs.String("results-dir", "", "path to a results directory (required)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *resultsDir == "" {
		fmt.Fprintln(os.Stderr, "--results-dir is required")
		usage()
		os.Exit(2)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		fmt.Fprintln(os.Stderr, "python3 not found in PATH; install Python 3 to use analyze")
		os.Exit(1)
	}
	script := filepath.Join("scripts", "analysis", "compare.py")
	cmd := exec.Command("python3", script, *resultsDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "analyze failed: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  cdnsim run      --config <path> [--output-dir <dir>] [--verbose]")
	fmt.Fprintln(os.Stderr, "  cdnsim validate --config <path>")
	fmt.Fprintln(os.Stderr, "  cdnsim sweep    --config <path> [--output-dir <dir>] [--verbose]")
	fmt.Fprintln(os.Stderr, "  cdnsim analyze  --results-dir <dir>")
}

func sanitise(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "_")
	return s
}
