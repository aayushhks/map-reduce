package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"cs651/mr"
	"cs651/verify"
	"cs651/workload"
)

// RunConfig describes a single benchmark job.
type RunConfig struct {
	Inputs      []string
	Workload    string
	Workers     int
	NReduce     int
	SplitBytes  int
	WaitBackoff time.Duration
	Seed        int64
	WorkDir     string

	Speculation          bool
	SpeculationThreshold float64
	SlowWorkers          int     // How many workers are made artificially slow
	SlowFactor           float64 // How many times slower those workers run
}

// RunResult is everything one job produced.
type RunResult struct {
	Trace      mr.JobTrace
	OutputHash string
	OutputKeys int
}

// runJob runs one job to completion with in-process workers and returns its
// trace and a hash of the output.
func runJob(cfg RunConfig) (RunResult, error) {
	app, err := workload.Lookup(cfg.Workload)
	if err != nil {
		return RunResult{}, err
	}

	// The input order is the one thing the seed varies, so a given seed always
	// schedules the same splits in the same order.
	inputs := append([]string(nil), cfg.Inputs...)
	rand.New(rand.NewSource(cfg.Seed)).Shuffle(len(inputs), func(i, j int) {
		inputs[i], inputs[j] = inputs[j], inputs[i]
	})

	if err := os.RemoveAll(cfg.WorkDir); err != nil {
		return RunResult{}, fmt.Errorf("clear work dir: %w", err)
	}
	if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		return RunResult{}, fmt.Errorf("create work dir: %w", err)
	}

	// Unix socket paths are short, so the socket lives outside the work dir.
	sockDir, err := os.MkdirTemp("", "mrbench")
	if err != nil {
		return RunResult{}, fmt.Errorf("create socket dir: %w", err)
	}
	defer os.RemoveAll(sockDir)

	coordinator := mr.MakeCoordinatorWithConfig(inputs, mr.Config{
		NReduce:     cfg.NReduce,
		SplitBytes:  cfg.SplitBytes,
		WaitBackoff: cfg.WaitBackoff,
		WorkDir:     cfg.WorkDir,
		SocketPath:  filepath.Join(sockDir, "mr.sock"),

		Speculation:          cfg.Speculation,
		SpeculationThreshold: cfg.SpeculationThreshold,
	})
	defer coordinator.Shutdown()

	var wg sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			slow := 0.0
			if id < cfg.SlowWorkers {
				slow = cfg.SlowFactor
			}
			mr.RunWorker(app.Map, app.Reduce, mr.WorkerOptions{
				ID:         fmt.Sprintf("worker-%d", id),
				SocketPath: filepath.Join(sockDir, "mr.sock"),
				SlowFactor: slow,
			})
		}(i)
	}
	wg.Wait()

	trace := coordinator.Trace()
	if trace.End.IsZero() {
		return RunResult{}, fmt.Errorf("job finished with %d of %d reduce tasks complete",
			len(trace.Tasks), cfg.NReduce)
	}

	hash, keys, err := verify.OutputHash(cfg.WorkDir)
	if err != nil {
		return RunResult{}, err
	}

	return RunResult{Trace: trace, OutputHash: hash, OutputKeys: keys}, nil
}
