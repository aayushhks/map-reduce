package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
		backoff    = flag.Duration("wait-backoff", time.Second, "worker sleep when no task is available")
		workDir    = flag.String("workdir", "bench-tmp", "directory for intermediate and output files")
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
