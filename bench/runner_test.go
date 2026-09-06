package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCorpus builds a small deterministic corpus to run jobs against.
func writeCorpus(t *testing.T, files, lines int) []string {
	t.Helper()

	dir := t.TempDir()
	paths := make([]string, 0, files)
	for f := 0; f < files; f++ {
		content := ""
		for l := 0; l < lines; l++ {
			content += fmt.Sprintf("alpha beta%d gamma%d delta\n", l%13, (f*lines+l)%29)
		}
		path := filepath.Join(dir, fmt.Sprintf("doc-%d.txt", f))
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write corpus: %v", err)
		}
		paths = append(paths, path)
	}
	return paths
}

func testConfig(t *testing.T, inputs []string) RunConfig {
	t.Helper()
	return RunConfig{
		Inputs:      inputs,
		Workload:    "invertedindex",
		Workers:     3,
		NReduce:     4,
		WaitBackoff: 5 * time.Millisecond,
		Seed:        1,
		WorkDir:     filepath.Join(t.TempDir(), "work"),
	}
}

// Splitting the input more finely changes how many map tasks run but must not
// change a single byte of the output.
func TestOutputHashIsStableAcrossSplitSizes(t *testing.T) {
	inputs := writeCorpus(t, 3, 400)

	want := ""
	seenTaskCounts := map[int]bool{}

	for _, splitBytes := range []int{0, 2048, 8192, 1 << 20} {
		cfg := testConfig(t, inputs)
		cfg.SplitBytes = splitBytes

		result, err := runJob(cfg)
		if err != nil {
			t.Fatalf("splitBytes=%d: %v", splitBytes, err)
		}
		seenTaskCounts[result.Trace.NMap] = true

		if want == "" {
			want = result.OutputHash
			continue
		}
		if result.OutputHash != want {
			t.Fatalf("splitBytes=%d produced hash %s, want %s",
				splitBytes, result.OutputHash, want)
		}
	}

	if len(seenTaskCounts) < 2 {
		t.Fatalf("split sizes did not change the map task count: %v", seenTaskCounts)
	}
}

// Adding workers changes the schedule but must not change the output.
func TestOutputHashIsStableAcrossWorkerCounts(t *testing.T) {
	inputs := writeCorpus(t, 3, 400)

	want := ""
	for _, workers := range []int{1, 2, 5} {
		cfg := testConfig(t, inputs)
		cfg.Workers = workers
		cfg.SplitBytes = 4096

		result, err := runJob(cfg)
		if err != nil {
			t.Fatalf("workers=%d: %v", workers, err)
		}

		if want == "" {
			want = result.OutputHash
			continue
		}
		if result.OutputHash != want {
			t.Fatalf("workers=%d produced hash %s, want %s", workers, result.OutputHash, want)
		}
	}
}

// Every trial of one configuration must produce the same output.
func TestOutputHashIsStableAcrossTrials(t *testing.T) {
	inputs := writeCorpus(t, 2, 300)
	cfg := testConfig(t, inputs)
	cfg.SplitBytes = 4096

	want := ""
	for trial := 0; trial < 3; trial++ {
		result, err := runJob(cfg)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if want == "" {
			want = result.OutputHash
			continue
		}
		if result.OutputHash != want {
			t.Fatalf("trial %d produced hash %s, want %s", trial, result.OutputHash, want)
		}
	}
}
