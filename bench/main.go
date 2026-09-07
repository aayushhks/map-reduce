package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cs651/workload"
)

func main() {
	var (
		inputGlob  = flag.String("input", "data/*.txt", "glob matching the input files")
		dataset    = flag.String("dataset", "gutenberg-8", "human readable dataset name for the report")
		app        = flag.String("workload", "invertedindex", "workload to run: "+strings.Join(workload.Names(), ", "))
		workers    = flag.Int("workers", 4, "number of concurrent workers")
		nReduce    = flag.Int("reduce", 10, "number of reduce partitions")
		splitBytes = flag.Int("split-bytes", 0, "target map split size in bytes, 0 means one split per file")
		trials     = flag.Int("trials", 5, "number of trials to run")
		seed       = flag.Int64("seed", 1, "seed controlling input ordering")
		backoff    = flag.Duration("wait-backoff", 10*time.Millisecond, "worker sleep when no task is available")
		workDir    = flag.String("workdir", "bench-tmp", "directory for intermediate and output files")
		sweep      = flag.String("sweep", "", "comma separated worker counts to sweep, e.g. 1,2,4,8,16")
		out        = flag.String("out", "", "write the JSON report to this path instead of stdout")
	)
	flag.Parse()

	if *trials < 1 {
		fail(fmt.Errorf("need at least one trial, got %d", *trials))
	}

	inputs, err := filepath.Glob(*inputGlob)
	if err != nil {
		fail(fmt.Errorf("bad input glob: %w", err))
	}
	if len(inputs) == 0 {
		fail(fmt.Errorf("no input files matched %q", *inputGlob))
	}
	sort.Strings(inputs)

	inputBytes, err := totalBytes(inputs)
	if err != nil {
		fail(err)
	}

	cfg := RunConfig{
		Inputs:      inputs,
		Workload:    *app,
		Workers:     *workers,
		NReduce:     *nReduce,
		SplitBytes:  *splitBytes,
		WaitBackoff: *backoff,
		Seed:        *seed,
		WorkDir:     *workDir,
	}

	if *sweep != "" {
		counts, err := parseCounts(*sweep)
		if err != nil {
			fail(err)
		}
		runSweep(cfg, counts, *trials, RunReport{
			Dataset:       *dataset,
			InputGlob:     *inputGlob,
			Workload:      *app,
			NReduce:       *nReduce,
			SplitBytes:    *splitBytes,
			WaitBackoffMS: millis(*backoff),
			Seed:          *seed,
			Trials:        *trials,
			InputFiles:    len(inputs),
			InputBytes:    inputBytes,
		}, *out, *workDir)
		return
	}

	results := make([]RunResult, 0, *trials)
	trialReports := make([]Trial, 0, *trials)
	for i := 0; i < *trials; i++ {
		result, err := runJob(cfg)
		if err != nil {
			fail(fmt.Errorf("trial %d: %w", i, err))
		}
		results = append(results, result)
		trialReports = append(trialReports, summarizeTrial(i, result))
		fmt.Fprintf(os.Stderr, "trial %d/%d: %.1f ms, hash %s\n",
			i+1, *trials, trialReports[i].WallMS, result.OutputHash[:12])
	}

	median := medianTrial(trialReports)
	report := Report{
		Schema:      "mapreduce-bench/v1",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Environment: environment(),
		Config: RunReport{
			Dataset:       *dataset,
			InputGlob:     *inputGlob,
			Workload:      *app,
			Workers:       *workers,
			NReduce:       *nReduce,
			SplitBytes:    *splitBytes,
			WaitBackoffMS: millis(*backoff),
			Seed:          *seed,
			Trials:        *trials,
			InputFiles:    len(inputs),
			InputBytes:    inputBytes,
			MapTasks:      results[0].Trace.NMap,
		},
		Trials:   trialReports,
		Summary:  buildSummary(trialReports, results, inputBytes),
		Timeline: timelineOf(results[median]),
	}

	if err := writeReport(report, *out); err != nil {
		fail(err)
	}

	if err := os.RemoveAll(*workDir); err != nil {
		fail(fmt.Errorf("clean work dir: %w", err))
	}
}

// parseCounts reads a comma separated list of worker counts.
func parseCounts(spec string) ([]int, error) {
	counts := []int{}
	for _, field := range strings.Split(spec, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil {
			return nil, fmt.Errorf("bad worker count %q: %w", field, err)
		}
		if n < 1 {
			return nil, fmt.Errorf("worker count must be at least 1, got %d", n)
		}
		counts = append(counts, n)
	}
	if len(counts) == 0 {
		return nil, fmt.Errorf("no worker counts given")
	}
	sort.Ints(counts)
	return counts, nil
}

// runSweep measures every worker count on the same input and writes one report.
func runSweep(cfg RunConfig, counts []int, trials int, base RunReport, out, workDir string) {
	points := make([]ScalingPoint, 0, len(counts))

	for _, workers := range counts {
		cfg.Workers = workers

		results := make([]RunResult, 0, trials)
		trialReports := make([]Trial, 0, trials)
		for i := 0; i < trials; i++ {
			result, err := runJob(cfg)
			if err != nil {
				fail(fmt.Errorf("workers=%d trial %d: %w", workers, i, err))
			}
			results = append(results, result)
			trialReports = append(trialReports, summarizeTrial(i, result))
		}

		point := buildScalingPoint(workers, results, trialReports)
		points = append(points, point)
		base.MapTasks = results[0].Trace.NMap
		fmt.Fprintf(os.Stderr, "workers=%-3d wall=%.0f ms  cpu=%.2f  busy=%.2f  coord=%.3f  hash=%s\n",
			workers, point.WallMS.Median, point.CPUSaturation, point.MeanBusyShare,
			point.CoordinatorShare, point.OutputHash[:8])
	}

	fillSpeedups(points)

	report := ScalingReport{
		Schema:      "mapreduce-scaling/v1",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Environment: environment(),
		Config:      base,
		Points:      points,
	}

	if err := writeScalingReport(report, out); err != nil {
		fail(err)
	}
	if err := os.RemoveAll(workDir); err != nil {
		fail(fmt.Errorf("clean work dir: %w", err))
	}
}

// writeScalingReport emits the sweep as indented JSON.
func writeScalingReport(r ScalingReport, path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	data = append(data, '\n')

	if path == "" {
		_, err := os.Stdout.Write(data)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// totalBytes is the size of the whole input set.
func totalBytes(files []string) (int64, error) {
	var total int64
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			return 0, fmt.Errorf("stat %v: %w", f, err)
		}
		total += info.Size()
	}
	return total, nil
}

// writeReport emits the report as indented JSON.
func writeReport(r Report, path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	data = append(data, '\n')

	if path == "" {
		_, err := os.Stdout.Write(data)
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "bench:", err)
	os.Exit(1)
}
