package mr

import (
	"testing"
	"time"
)

// newTestCoordinator builds a coordinator without starting its RPC server.
func newTestCoordinator(t *testing.T, nMap, nReduce int, cfg Config) *Coordinator {
	t.Helper()

	cfg.NReduce = nReduce
	cfg = cfg.withDefaults()

	c := &Coordinator{
		cfg:         cfg,
		jobStart:    time.Now(),
		nMap:        nMap,
		nReduce:     nReduce,
		mapTasks:    make([]TaskInfo, nMap),
		reduceTasks: make([]TaskInfo, nReduce),
	}
	for i := 0; i < nMap; i++ {
		c.mapTasks[i] = TaskInfo{ID: i, State: Idle, Split: Split{File: "input"}}
	}
	for i := 0; i < nReduce; i++ {
		c.reduceTasks[i] = TaskInfo{ID: i, State: Idle}
	}
	return c
}

// handout is a task the coordinator gave to a named worker, so a test can
// report back as that worker.
type handout struct {
	reply  RequestTaskReply
	worker string
}

func request(t *testing.T, c *Coordinator, workerID string) handout {
	t.Helper()
	reply := RequestTaskReply{}
	if err := c.RequestTask(&RequestTaskArgs{WorkerID: workerID}, &reply); err != nil {
		t.Fatalf("RequestTask: %v", err)
	}
	return handout{reply: reply, worker: workerID}
}

// askToCommit runs the first half of the commit protocol.
func askToCommit(t *testing.T, c *Coordinator, h handout) bool {
	t.Helper()
	r := h.reply
	args := ReportTaskArgs{TaskID: r.TaskID, TaskType: r.TaskType, Attempt: r.Attempt, WorkerID: h.worker}
	reply := ReportTaskReply{}
	if err := c.ReportTask(&args, &reply); err != nil {
		t.Fatalf("ReportTask: %v", err)
	}
	return reply.Commit
}

// confirm runs the second half, standing in for a worker that published.
func confirm(t *testing.T, c *Coordinator, h handout) {
	t.Helper()
	r := h.reply
	args := CommitTaskArgs{
		TaskID:   r.TaskID,
		TaskType: r.TaskType,
		Attempt:  r.Attempt,
		WorkerID: h.worker,
		Metrics: TaskMetrics{
			WorkerID: h.worker,
			TaskType: r.TaskType,
			TaskID:   r.TaskID,
			Attempt:  r.Attempt,
			Backup:   r.Backup,
			Start:    time.Now().Add(-time.Millisecond),
			End:      time.Now(),
		},
	}
	if err := c.CommitTask(&args, &CommitTaskReply{}); err != nil {
		t.Fatalf("CommitTask: %v", err)
	}
}

func finish(t *testing.T, c *Coordinator, h handout) {
	t.Helper()
	if !askToCommit(t, c, h) {
		t.Fatalf("task %d attempt %d was refused permission to commit", h.reply.TaskID, h.reply.Attempt)
	}
	confirm(t, c, h)
}

// A worker whose attempt timed out must not be able to publish over the
// attempt that replaced it.
func TestTimedOutAttemptIsRefusedCommit(t *testing.T) {
	c := newTestCoordinator(t, 1, 1, Config{})

	first := request(t, c, "worker-a")
	if first.reply.TaskType != MapTask {
		t.Fatalf("want a map task, got %v", first.reply.TaskType)
	}

	// Age the attempt past the timeout and let the reaper drop it.
	c.mu.Lock()
	c.mapTasks[0].Live[0].Start = time.Now().Add(-time.Hour)
	c.mu.Unlock()
	c.reapTimeouts()

	second := request(t, c, "worker-b")
	if second.reply.Attempt == first.reply.Attempt {
		t.Fatalf("reassignment should issue a new attempt, both were %d", first.reply.Attempt)
	}

	if askToCommit(t, c, first) {
		t.Fatal("a timed out attempt was cleared to publish")
	}

	finish(t, c, second)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mapTasks[0].State != Completed || c.mapTasksCompleted != 1 {
		t.Fatalf("live attempt did not complete the task: state=%v completed=%d",
			c.mapTasks[0].State, c.mapTasksCompleted)
	}
}

// Two attempts of one task race to finish. Exactly one may publish.
func TestOnlyOneAttemptMayCommit(t *testing.T) {
	c := newTestCoordinator(t, 2, 1, Config{
		Speculation:           true,
		SpeculationThreshold:  1.0,
		SpeculationMinSamples: 1,
	})

	// Finish one map task so the phase has a median to compare against.
	finish(t, c, request(t, c, "worker-a"))

	original := request(t, c, "worker-a")
	c.mu.Lock()
	c.mapTasks[original.reply.TaskID].Live[0].Start = time.Now().Add(-time.Hour)
	c.mu.Unlock()

	backup := request(t, c, "worker-b")
	if !backup.reply.Backup || backup.reply.TaskID != original.reply.TaskID {
		t.Fatalf("want a backup for task %d, got task %d backup=%v",
			original.reply.TaskID, backup.reply.TaskID, backup.reply.Backup)
	}
	if backup.reply.Attempt == original.reply.Attempt {
		t.Fatal("backup reused the original attempt id")
	}

	if !askToCommit(t, c, backup) {
		t.Fatal("first attempt to report was refused")
	}
	if askToCommit(t, c, original) {
		t.Fatal("second attempt to report was also cleared to publish")
	}

	confirm(t, c, backup)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mapTasksCompleted != 2 {
		t.Fatalf("completed %d map tasks, want 2", c.mapTasksCompleted)
	}
	if c.backupsWon != 1 {
		t.Fatalf("backups won %d, want 1", c.backupsWon)
	}
}

// A task running near the phase median must not be backed up.
func TestNoBackupForATaskRunningAtTheMedian(t *testing.T) {
	c := newTestCoordinator(t, 2, 1, Config{
		Speculation:           true,
		SpeculationThreshold:  2.0,
		SpeculationMinSamples: 1,
	})

	finish(t, c, request(t, c, "worker-a"))
	request(t, c, "worker-a") // second map task, just started

	reply := request(t, c, "worker-b")
	if reply.reply.TaskType != WaitTask {
		t.Fatalf("want a wait, got task type %v backup=%v", reply.reply.TaskType, reply.reply.Backup)
	}
}

// Speculation off means no backup however slow a task gets.
func TestSpeculationOffLaunchesNoBackups(t *testing.T) {
	c := newTestCoordinator(t, 2, 1, Config{Speculation: false})

	finish(t, c, request(t, c, "worker-a"))
	slow := request(t, c, "worker-a")

	c.mu.Lock()
	c.mapTasks[slow.reply.TaskID].Live[0].Start = time.Now().Add(-time.Hour)
	c.mu.Unlock()

	if reply := request(t, c, "worker-b"); reply.reply.TaskType != WaitTask {
		t.Fatalf("want a wait with speculation off, got %v", reply.reply.TaskType)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.backupsLaunched != 0 {
		t.Fatalf("launched %d backups with speculation off", c.backupsLaunched)
	}
}

// A worker that is cleared to publish but dies before confirming must leave the
// task to be run again, not counted as done.
func TestCommitterThatNeverConfirmsIsRerun(t *testing.T) {
	c := newTestCoordinator(t, 1, 1, Config{})

	first := request(t, c, "worker-a")
	if !askToCommit(t, c, first) {
		t.Fatal("first attempt was refused permission to commit")
	}

	c.mu.Lock()
	c.mapTasks[0].CommitStart = time.Now().Add(-time.Hour)
	c.mu.Unlock()
	c.reapTimeouts()

	c.mu.Lock()
	state, completed := c.mapTasks[0].State, c.mapTasksCompleted
	c.mu.Unlock()
	if state != Idle || completed != 0 {
		t.Fatalf("stalled committer left task state=%v completed=%d", state, completed)
	}

	second := request(t, c, "worker-b")
	if second.reply.TaskType != MapTask {
		t.Fatalf("task was not handed out again, got %v", second.reply.TaskType)
	}
	finish(t, c, second)
}

// A malformed report must not panic the coordinator.
func TestReportOutOfRangeTaskID(t *testing.T) {
	c := newTestCoordinator(t, 1, 1, Config{})
	for _, id := range []int{-1, 99} {
		reply := ReportTaskReply{}
		args := ReportTaskArgs{TaskID: id, TaskType: MapTask, Attempt: 1}
		if err := c.ReportTask(&args, &reply); err != nil {
			t.Fatalf("ReportTask(%d): %v", id, err)
		}
		if reply.Commit {
			t.Fatalf("out of range task %d was cleared to publish", id)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mapTasksCompleted != 0 {
		t.Fatalf("out of range report was counted: %d", c.mapTasksCompleted)
	}
}

// Done must follow the task counters rather than waiting for a worker to ask
// for more work.
func TestDoneFollowsTaskCounters(t *testing.T) {
	c := newTestCoordinator(t, 1, 1, Config{})
	if c.Done() {
		t.Fatal("job reported done before any task ran")
	}

	finish(t, c, request(t, c, "worker-a"))
	if c.Done() {
		t.Fatal("job reported done with the reduce phase outstanding")
	}

	r := request(t, c, "worker-a")
	if r.reply.TaskType != ReduceTask {
		t.Fatalf("want a reduce task, got %v", r.reply.TaskType)
	}
	finish(t, c, r)

	// No worker asks for another task after this point.
	if !c.Done() {
		t.Fatal("job did not report done after every task completed")
	}
}
