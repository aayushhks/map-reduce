package trace

import (
	"testing"
	"time"

	"cs651/mr"
)

// A trace must be expressed relative to the job start, so a replay does not
// depend on when the job originally ran.
func TestBuildIsRelativeToJobStart(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	in := mr.JobTrace{
		Start:            start,
		MapPhaseEnd:      start.Add(300 * time.Millisecond),
		ReducePhaseStart: start.Add(310 * time.Millisecond),
		End:              start.Add(500 * time.Millisecond),
		NMap:             2,
		NReduce:          1,
		BackupsLaunched:  1,
		BackupsWon:       1,
		Tasks: []mr.TaskMetrics{{
			WorkerID: "worker-1",
			TaskType: mr.MapTask,
			TaskID:   0,
			Attempt:  2,
			Backup:   true,
			Start:    start.Add(100 * time.Millisecond),
			End:      start.Add(180 * time.Millisecond),
		}},
		Events: []mr.TaskEvent{
			{Kind: mr.EventRefused, TaskType: mr.MapTask, TaskID: 0, Attempt: 1, WorkerID: "worker-0", At: start.Add(400 * time.Millisecond)},
			{Kind: mr.EventAssigned, TaskType: mr.MapTask, TaskID: 0, Attempt: 1, WorkerID: "worker-0", At: start.Add(10 * time.Millisecond)},
		},
	}

	doc := Build(in, "example")

	if doc.Job.DurationMS != 500 || doc.Job.MapPhaseEndMS != 300 {
		t.Fatalf("job times: %+v", doc.Job)
	}
	if len(doc.Tasks) != 1 || doc.Tasks[0].StartMS != 100 || doc.Tasks[0].EndMS != 180 {
		t.Fatalf("task times: %+v", doc.Tasks)
	}
	if !doc.Tasks[0].Backup {
		t.Fatal("backup attempt lost its backup flag")
	}

	// Events must come out in time order whatever order they went in.
	if len(doc.Events) != 2 || doc.Events[0].AtMS != 10 || doc.Events[1].AtMS != 400 {
		t.Fatalf("events not ordered by time: %+v", doc.Events)
	}
	if doc.Events[0].Phase != "map" {
		t.Fatalf("phase name %q", doc.Events[0].Phase)
	}

	// Workers named only by an event still belong in the worker list.
	if len(doc.Workers) != 2 || doc.Workers[0] != "worker-0" || doc.Workers[1] != "worker-1" {
		t.Fatalf("workers: %v", doc.Workers)
	}
}

// An unfinished job has a zero end time, which must not become a negative
// offset in the document.
func TestBuildHandlesAnUnfinishedJob(t *testing.T) {
	start := time.Now()
	doc := Build(mr.JobTrace{Start: start, NMap: 1, NReduce: 1}, "partial")

	if doc.Job.DurationMS != 0 || doc.Job.MapPhaseEndMS != 0 {
		t.Fatalf("unfinished job reported %+v", doc.Job)
	}
}
