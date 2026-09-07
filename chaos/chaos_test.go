package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// buildWorker compiles the worker binary the scenarios launch as processes.
func buildWorker(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "chaosworker")
	cmd := exec.Command("go", "build", "-o", bin, "./worker")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v\n%s", err, out)
	}
	return bin
}

func testJob(t *testing.T, workerBin string) JobConfig {
	t.Helper()

	inputs, err := filepath.Glob("../data/*.txt")
	if err != nil || len(inputs) == 0 {
		t.Fatalf("no input corpus found: %v", err)
	}

	return JobConfig{
		Inputs:       inputs,
		Workload:     "invertedindex",
		NReduce:      4,
		SplitBytes:   262144,
		WorkDir:      filepath.Join(t.TempDir(), "work"),
		WorkerBin:    workerBin,
		TaskTimeout:  400 * time.Millisecond,
		ReapInterval: 100 * time.Millisecond,
		WaitBackoff:  10 * time.Millisecond,
		Deadline:     90 * time.Second,
		Seed:         1,
	}
}

// Every fault scenario must finish with output identical to a clean run.
// Killing workers is allowed to cost time, never correctness.
func TestScenariosProduceCleanOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("scenarios launch worker processes")
	}

	workerBin := buildWorker(t)
	cfg := testJob(t, workerBin)

	clean, err := run(scenarios[0], cfg)
	if err != nil {
		t.Fatalf("clean run: %v", err)
	}
	if !clean.Completed {
		t.Fatal("clean run did not finish")
	}
	if clean.OutputKeys == 0 {
		t.Fatal("clean run produced no output")
	}

	for _, name := range []string{"kill-1-map", "kill-1-reduce", "kill-repeat-map", "rpc-drop-30pct"} {
		sc, ok := lookup(name)
		if !ok {
			t.Fatalf("unknown scenario %q", name)
		}

		t.Run(name, func(t *testing.T) {
			jobCfg := cfg
			jobCfg.WorkDir = filepath.Join(t.TempDir(), "work")

			outcome, err := run(sc, jobCfg)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if !outcome.Completed {
				t.Fatal("job did not finish")
			}
			if outcome.OutputHash != clean.OutputHash {
				t.Fatalf("output hash %s, want %s from the clean run",
					outcome.OutputHash, clean.OutputHash)
			}
		})
	}
}

// A killed worker's task must be handed to somebody else, and that must show up
// in the trace as a reassignment rather than a silently dropped task.
func TestKilledWorkerTaskIsReassigned(t *testing.T) {
	if testing.Short() {
		t.Skip("scenarios launch worker processes")
	}

	cfg := testJob(t, buildWorker(t))
	sc, _ := lookup("kill-1-map")

	outcome, err := run(sc, cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(outcome.Kills) == 0 {
		t.Fatal("no worker was killed, the scenario did not exercise recovery")
	}

	recoveries := recoveryTimes(outcome)
	if len(recoveries) == 0 {
		t.Fatal("no task was reassigned after the kill")
	}
	for _, r := range recoveries {
		if r <= 0 {
			t.Fatalf("recovery time %v is not positive", r)
		}
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
