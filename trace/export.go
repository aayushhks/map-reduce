package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"cs651/mr"
)

// Document is a recorded job. Every time is milliseconds from the job start, so
// a replay can play it back without knowing when it originally ran.
type Document struct {
	Schema     string   `json:"schema"`
	RecordedAt string   `json:"recorded_at"`
	Label      string   `json:"label"`
	Job        Job      `json:"job"`
	Workers    []string `json:"workers"`
	Tasks      []Task   `json:"tasks"`
	Events     []Event  `json:"events"`
}

// Job is the shape of the run as a whole.
type Job struct {
	DurationMS         float64 `json:"duration_ms"`
	MapPhaseEndMS      float64 `json:"map_phase_end_ms"`
	ReducePhaseStartMS float64 `json:"reduce_phase_start_ms"`
	MapTasks           int     `json:"map_tasks"`
	ReduceTasks        int     `json:"reduce_tasks"`
	BackupsLaunched    int64   `json:"backups_launched"`
	BackupsWon         int64   `json:"backups_won"`
}

// Task is one attempt that completed, laid out on the worker timeline.
type Task struct {
	WorkerID    string  `json:"worker_id"`
	Phase       string  `json:"phase"`
	TaskID      int     `json:"task_id"`
	Attempt     int     `json:"attempt"`
	Backup      bool    `json:"backup"`
	StartMS     float64 `json:"start_ms"`
	EndMS       float64 `json:"end_ms"`
	InputBytes  int64   `json:"input_bytes"`
	OutputBytes int64   `json:"output_bytes"`
	Records     int64   `json:"records"`
}

// Event is one thing that happened to a task attempt, including the ones that
// produced no output: a timed out attempt, or a backup that lost the race.
type Event struct {
	Kind     string  `json:"kind"`
	Phase    string  `json:"phase"`
	TaskID   int     `json:"task_id"`
	Attempt  int     `json:"attempt"`
	WorkerID string  `json:"worker_id"`
	Backup   bool    `json:"backup"`
	AtMS     float64 `json:"at_ms"`
}

// Build converts a coordinator trace into a replayable document.
func Build(t mr.JobTrace, label string) Document {
	doc := Document{
		Schema:     "mapreduce-trace/v1",
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
		Label:      label,
		Job: Job{
			DurationMS:         since(t.Start, t.End),
			MapPhaseEndMS:      since(t.Start, t.MapPhaseEnd),
			ReducePhaseStartMS: since(t.Start, t.ReducePhaseStart),
			MapTasks:           t.NMap,
			ReduceTasks:        t.NReduce,
			BackupsLaunched:    t.BackupsLaunched,
			BackupsWon:         t.BackupsWon,
		},
	}

	seen := map[string]struct{}{}
	for _, m := range t.Tasks {
		doc.Tasks = append(doc.Tasks, Task{
			WorkerID:    m.WorkerID,
			Phase:       m.TaskType.String(),
			TaskID:      m.TaskID,
			Attempt:     m.Attempt,
			Backup:      m.Backup,
			StartMS:     since(t.Start, m.Start),
			EndMS:       since(t.Start, m.End),
			InputBytes:  m.InputBytes,
			OutputBytes: m.OutputBytes,
			Records:     m.Records,
		})
		seen[m.WorkerID] = struct{}{}
	}

	for _, e := range t.Events {
		doc.Events = append(doc.Events, Event{
			Kind:     e.Kind,
			Phase:    e.TaskType.String(),
			TaskID:   e.TaskID,
			Attempt:  e.Attempt,
			WorkerID: e.WorkerID,
			Backup:   e.Backup,
			AtMS:     since(t.Start, e.At),
		})
		if e.WorkerID != "" {
			seen[e.WorkerID] = struct{}{}
		}
	}

	for id := range seen {
		doc.Workers = append(doc.Workers, id)
	}
	sort.Strings(doc.Workers)

	sort.SliceStable(doc.Tasks, func(a, b int) bool { return doc.Tasks[a].StartMS < doc.Tasks[b].StartMS })
	sort.SliceStable(doc.Events, func(a, b int) bool { return doc.Events[a].AtMS < doc.Events[b].AtMS })

	return doc
}

// Write saves a trace document as indented JSON.
func Write(path string, doc Document) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode trace: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create trace dir: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// since is milliseconds from the job start, or zero for an unset time.
func since(start, at time.Time) float64 {
	if at.IsZero() || start.IsZero() {
		return 0
	}
	ms := float64(at.Sub(start)) / float64(time.Millisecond)
	return float64(int64(ms*1000+0.5)) / 1000
}
