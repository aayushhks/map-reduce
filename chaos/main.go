package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

// Report is the full fault matrix, written as JSON.
type Report struct {
	Schema      string   `json:"schema"`
	GeneratedAt string   `json:"generated_at"`
	Environment Env      `json:"environment"`
	Config      Settings `json:"config"`
	Results     []Result `json:"results"`
}

// Env records the machine the scenarios ran on.
type Env struct {
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"go_version"`
	NumCPU    int    `json:"num_cpu"`
	Placement string `json:"placement"`
}

// Settings is the job configuration every scenario shares.
type Settings struct {
	Dataset        string  `json:"dataset"`
	InputGlob      string  `json:"input_glob"`
	Workload       string  `json:"workload"`
	InputFiles     int     `json:"input_files"`
	InputBytes     int64   `json:"input_bytes"`
	MapTasks       int     `json:"map_tasks"`
	NReduce        int     `json:"n_reduce"`
	Workers        int     `json:"workers"`
	TaskTimeoutMS  float64 `json:"task_timeout_ms"`
	ReapIntervalMS float64 `json:"reap_interval_ms"`
	Trials         int     `json:"trials_per_scenario"`
	Seed           int64   `json:"seed"`
}

func main() {
	var (
		inputGlob  = flag.String("input", "data/*.txt", "glob matching the input files")
		dataset    = flag.String("dataset", "gutenberg-8", "human readable dataset name")
		app        = flag.String("workload", "invertedindex", "workload to run")
		nReduce    = flag.Int("reduce", 8, "number of reduce partitions")
		splitBytes = flag.Int("split-bytes", 0, "target map split size in bytes")
		timeout    = flag.Duration("task-timeout", 2*time.Second, "how long a task may run before it is assumed lost")
		reap       = flag.Duration("reap-interval", 250*time.Millisecond, "how often the coordinator looks for lost tasks")
		backoff    = flag.Duration("wait-backoff", 10*time.Millisecond, "worker sleep when no task is available")
		deadline   = flag.Duration("deadline", 90*time.Second, "give up on a scenario after this long")
		trials     = flag.Int("trials", 3, "how many times to run each scenario")
		seed       = flag.Int64("seed", 1, "seed for fault timing")
		only       = flag.String("only", "", "run just this scenario")
		workerBin  = flag.String("worker", "", "path to the chaos worker binary")
		workDir    = flag.String("workdir", "chaos-tmp", "directory for intermediate and output files")
		out        = flag.String("out", "", "write the JSON report here instead of stdout")
	)
	flag.Parse()

	if *workerBin == "" {
		fail(fmt.Errorf("-worker is required, build it with: go build -o /tmp/chaosworker ./chaos/worker"))
	}
	if _, err := os.Stat(*workerBin); err != nil {
		fail(fmt.Errorf("worker binary: %w", err))
	}

	inputs, err := filepath.Glob(*inputGlob)
	if err != nil {
		fail(fmt.Errorf("bad input glob: %w", err))
	}
	if len(inputs) == 0 {
		fail(fmt.Errorf("no input files matched %q", *inputGlob))
	}
	sort.Strings(inputs)

	bytes, err := totalBytes(inputs)
	if err != nil {
		fail(err)
	}

	cfg := JobConfig{
		Inputs:       inputs,
		Workload:     *app,
		NReduce:      *nReduce,
		SplitBytes:   *splitBytes,
		WorkDir:      *workDir,
		WorkerBin:    *workerBin,
		TaskTimeout:  *timeout,
		ReapInterval: *reap,
		WaitBackoff:  *backoff,
		Deadline:     *deadline,
		Seed:         *seed,
	}

	plan := scenarios
	if *only != "" {
		sc, ok := lookup(*only)
		if !ok {
			fail(fmt.Errorf("unknown scenario %q", *only))
		}
		plan = []Scenario{sc}
	}

	// The clean run defines the correct output every other scenario is checked
	// against, so it has to go first.
	clean, err := run(scenarios[0], cfg)
	if err != nil {
		fail(fmt.Errorf("clean run: %w", err))
	}
	if !clean.Completed {
		fail(fmt.Errorf("clean run did not finish, nothing to compare against"))
	}
	cleanHash := clean.OutputHash
	fmt.Fprintf(os.Stderr, "clean run: %s, %d keys\n", cleanHash[:12], clean.OutputKeys)

	results := []Result{}
	for _, sc := range plan {
		outcomes := []Outcome{}
		for i := 0; i < *trials; i++ {
			trialCfg := cfg
			trialCfg.Seed = cfg.Seed + int64(i)

			outcome, err := run(sc, trialCfg)
			if err != nil {
				fail(fmt.Errorf("scenario %s trial %d: %w", sc.Name, i, err))
			}
			outcomes = append(outcomes, outcome)
		}

		r := analyse(sc, outcomes, cleanHash)
		results = append(results, r)
		fmt.Fprintf(os.Stderr, "%-20s completed=%-5v correct=%-5v wall=%7.0fms recovery_med=%7.1fms preserved=%.2f\n",
			r.Scenario, r.Completed, r.CorrectHash, r.WallMS, r.RecoveryMedMS, r.PreservedShare)
	}

	report := Report{
		Schema:      "mapreduce-chaos/v1",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Environment: Env{
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
			GoVersion: runtime.Version(),
			NumCPU:    runtime.NumCPU(),
			Placement: "single machine, in process coordinator with worker processes over unix socket rpc",
		},
		Config: Settings{
			Dataset:        *dataset,
			InputGlob:      *inputGlob,
			Workload:       *app,
			InputFiles:     len(inputs),
			InputBytes:     bytes,
			MapTasks:       clean.Trace.NMap,
			NReduce:        *nReduce,
			Workers:        scenarios[0].Workers,
			TaskTimeoutMS:  ms(*timeout),
			ReapIntervalMS: ms(*reap),
			Trials:         *trials,
			Seed:           *seed,
		},
		Results: results,
	}

	if err := writeReport(report, *out); err != nil {
		fail(err)
	}
	if err := os.RemoveAll(*workDir); err != nil {
		fail(fmt.Errorf("clean work dir: %w", err))
	}

	for _, r := range results {
		if !r.Completed || !r.CorrectHash {
			fail(fmt.Errorf("scenario %s did not finish with correct output", r.Scenario))
		}
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
	fmt.Fprintln(os.Stderr, "chaos:", err)
	os.Exit(1)
}
