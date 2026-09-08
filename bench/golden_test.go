package main

import (
	"path/filepath"
	"testing"
	"time"
)

// Golden output hashes for the committed corpus in data/. The hash digests
// every output line from every reduce partition, sorted, so it depends on the
// job's results and not on how the work was divided.
//
// The word count digest was produced independently by mrsequential, the
// single process reference implementation, over the same corpus. The
// distributed pipeline agreeing with it is the point of the test: a change
// that alters results will fail here even if the job still runs clean.
const (
	goldenWordCount     = "08d25f8329d8b7054a8330033736775249ffd98ca861f12e24e0cda079322c19"
	goldenInvertedIndex = "53ae1a3d57d7a8141976e7729c6344836892999e44317e81da336681e68ebcc1"

	goldenWordCountKeys     = 22107
	goldenInvertedIndexKeys = 19436
)

// The same input and seed must always produce the same output, whatever
// configuration produced it.
func TestGoldenOutputHash(t *testing.T) {
	inputs, err := filepath.Glob("../data/*.txt")
	if err != nil || len(inputs) == 0 {
		t.Fatalf("committed corpus not found: %v", err)
	}

	cases := []struct {
		workload   string
		wantHash   string
		wantKeys   int
		workers    int
		nReduce    int
		splitBytes int
	}{
		{"wordcount", goldenWordCount, goldenWordCountKeys, 1, 5, 0},
		{"wordcount", goldenWordCount, goldenWordCountKeys, 4, 5, 65536},
		{"invertedindex", goldenInvertedIndex, goldenInvertedIndexKeys, 1, 5, 0},
		{"invertedindex", goldenInvertedIndex, goldenInvertedIndexKeys, 4, 5, 65536},
		{"invertedindex", goldenInvertedIndex, goldenInvertedIndexKeys, 3, 8, 1 << 20},
	}

	for _, tc := range cases {
		name := tc.workload
		t.Run(name, func(t *testing.T) {
			result, err := runJob(RunConfig{
				Inputs:      inputs,
				Workload:    tc.workload,
				Workers:     tc.workers,
				NReduce:     tc.nReduce,
				SplitBytes:  tc.splitBytes,
				WaitBackoff: 5 * time.Millisecond,
				Seed:        1,
				WorkDir:     filepath.Join(t.TempDir(), "work"),
			})
			if err != nil {
				t.Fatalf("run: %v", err)
			}

			if result.OutputKeys != tc.wantKeys {
				t.Errorf("produced %d output keys, want %d", result.OutputKeys, tc.wantKeys)
			}
			if result.OutputHash != tc.wantHash {
				t.Fatalf("output hash\n got  %s\n want %s\n(workers=%d reduce=%d split=%d)",
					result.OutputHash, tc.wantHash, tc.workers, tc.nReduce, tc.splitBytes)
			}
		})
	}
}

// Speculation runs some tasks twice. The winner's output must still be the
// golden output, because only one attempt per task is ever published.
func TestGoldenOutputHashUnderSpeculation(t *testing.T) {
	inputs, err := filepath.Glob("../data/*.txt")
	if err != nil || len(inputs) == 0 {
		t.Fatalf("committed corpus not found: %v", err)
	}

	result, err := runJob(RunConfig{
		Inputs:               inputs,
		Workload:             "invertedindex",
		Workers:              4,
		NReduce:              5,
		SplitBytes:           65536,
		WaitBackoff:          5 * time.Millisecond,
		Seed:                 1,
		WorkDir:              filepath.Join(t.TempDir(), "work"),
		Speculation:          true,
		SpeculationThreshold: 1.2,
		SlowWorkers:          1,
		SlowFactor:           4,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.OutputHash != goldenInvertedIndex {
		t.Fatalf("output hash under speculation\n got  %s\n want %s",
			result.OutputHash, goldenInvertedIndex)
	}
}
