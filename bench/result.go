package main

import (
	"runtime"
	"time"

	"cs651/mr"
)

// Report is the structured result of a benchmark run, written as JSON.
type Report struct {
	Schema      string      `json:"schema"`
	GeneratedAt string      `json:"generated_at"`
	Environment Environment `json:"environment"`
	Config      RunReport   `json:"config"`
	Trials      []Trial     `json:"trials"`
	Summary     Summary     `json:"summary"`
	Timeline    []Interval  `json:"median_trial_timeline"`
}

// Environment records what the numbers were measured on.
type Environment struct {
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"go_version"`
	NumCPU    int    `json:"num_cpu"`
	Placement string `json:"placement"`
}

// RunReport is the configuration every number in this report belongs to.
type RunReport struct {
	Dataset       string  `json:"dataset"`
	Workload      string  `json:"workload"`
	Workers       int     `json:"workers"`
	NReduce       int     `json:"n_reduce"`
	SplitBytes    int     `json:"split_bytes"`
	WaitBackoffMS float64 `json:"wait_backoff_ms"`
	Seed          int64   `json:"seed"`
	Trials        int     `json:"trials"`
	InputFiles    int     `json:"input_files"`
	InputBytes    int64   `json:"input_bytes"`
	MapTasks      int     `json:"map_tasks"`
}

// Trial is one complete run of the job.
type Trial struct {
	Index             int          `json:"index"`
	WallMS            float64      `json:"wall_ms"`
	MapPhaseMS        float64      `json:"map_phase_ms"`
	ShuffleStallMS    float64      `json:"shuffle_stall_ms"`
	ReducePhaseMS     float64      `json:"reduce_phase_ms"`
	BytesShuffled     int64        `json:"bytes_shuffled"`
	IntermediateFiles int          `json:"intermediate_files"`
	RecordsEmitted    int64        `json:"records_emitted"`
	OutputKeys        int          `json:"output_keys"`
	OutputHash        string       `json:"output_hash"`
	MapTaskMS         Distribution `json:"map_task_ms"`
	ReduceTaskMS      Distribution `json:"reduce_task_ms"`
}

// Aggregate is the median with the observed spread across trials.
type Aggregate struct {
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

// Summary aggregates the trials. The median is the headline; min and max show
// the spread that the median hides.
type Summary struct {
	WallMS         Aggregate    `json:"wall_ms"`
	MapPhaseMS     Aggregate    `json:"map_phase_ms"`
	ShuffleStallMS Aggregate    `json:"shuffle_stall_ms"`
	ReducePhaseMS  Aggregate    `json:"reduce_phase_ms"`
	RecordsPerSec  Aggregate    `json:"records_per_sec"`
	BytesPerSec    Aggregate    `json:"input_bytes_per_sec"`
	MapTaskMS      Distribution `json:"map_task_ms_pooled"`
	ReduceTaskMS   Distribution `json:"reduce_task_ms_pooled"`
	OutputHash     string       `json:"output_hash"`
	HashStable     bool         `json:"output_hash_stable_across_trials"`
	Workers        []WorkerUtil `json:"worker_utilization"`
}

// WorkerUtil is how busy one worker was over the life of the job.
type WorkerUtil struct {
	WorkerID  string  `json:"worker_id"`
	Tasks     int     `json:"tasks"`
	BusyMS    float64 `json:"busy_ms"`
	BusyShare float64 `json:"busy_share"`
}

// Interval is one task occupying one worker, relative to the job start.
type Interval struct {
	WorkerID string  `json:"worker_id"`
	Phase    string  `json:"phase"`
	TaskID   int     `json:"task_id"`
	Attempt  int     `json:"attempt"`
	StartMS  float64 `json:"start_ms"`
	EndMS    float64 `json:"end_ms"`
}

// environment captures the machine the benchmark ran on.
func environment() Environment {
	return Environment{
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		GoVersion: runtime.Version(),
		NumCPU:    runtime.NumCPU(),
		Placement: "single machine, coordinator and workers in one process over unix socket rpc",
	}
}

// phaseName labels a task type for the report.
func phaseName(t mr.TaskType) string {
	if t == mr.MapTask {
		return "map"
	}
	return "reduce"
}

// millis converts a duration to milliseconds.
func millis(d time.Duration) float64 {
	return round(float64(d) / float64(time.Millisecond))
}
