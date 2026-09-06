package mr

import (
	"testing"
	"time"
)

// newTestCoordinator builds a coordinator without starting its RPC server.
func newTestCoordinator(nMap, nReduce int) *Coordinator {
	c := &Coordinator{
		nMap:        nMap,
		nReduce:     nReduce,
		mapTasks:    make([]TaskInfo, nMap),
		reduceTasks: make([]TaskInfo, nReduce),
	}
	for i := 0; i < nMap; i++ {
		c.mapTasks[i] = TaskInfo{ID: i, State: Idle, InputFile: "input"}
	}
	for i := 0; i < nReduce; i++ {
		c.reduceTasks[i] = TaskInfo{ID: i, State: Idle}
	}
	return c
}

func request(t *testing.T, c *Coordinator) RequestTaskReply {
	t.Helper()
	reply := RequestTaskReply{}
	if err := c.RequestTask(&RequestTaskArgs{}, &reply); err != nil {
		t.Fatalf("RequestTask: %v", err)
	}
	return reply
}

func report(t *testing.T, c *Coordinator, r RequestTaskReply) {
	t.Helper()
	args := ReportTaskArgs{TaskID: r.TaskID, TaskType: r.TaskType, Attempt: r.Attempt}
	if err := c.ReportTask(&args, &ReportTaskReply{}); err != nil {
		t.Fatalf("ReportTask: %v", err)
	}
}

// A worker whose task timed out must not be able to complete the attempt that
// replaced it.
func TestStaleAttemptIsRejected(t *testing.T) {
	c := newTestCoordinator(1, 1)

	first := request(t, c)
	if first.TaskType != MapTask {
		t.Fatalf("want a map task, got %v", first.TaskType)
	}

	// Age the task past the timeout and let the reaper reassign it.
	c.mu.Lock()
	c.mapTasks[0].StartTime = time.Now().Add(-time.Hour)
	c.mu.Unlock()
	c.reapTimeouts()

	second := request(t, c)
	if second.Attempt == first.Attempt {
		t.Fatalf("reassignment should issue a new attempt, both were %d", first.Attempt)
	}

	// The stale worker finishes late and reports the attempt it was handed.
	report(t, c, first)

	c.mu.Lock()
	state, completed := c.mapTasks[0].State, c.mapTasksCompleted
	c.mu.Unlock()
	if state == Completed || completed != 0 {
		t.Fatalf("stale report was accepted: state=%v completed=%d", state, completed)
	}

	// The live attempt still completes the task.
	report(t, c, second)
	c.mu.Lock()
	state, completed = c.mapTasks[0].State, c.mapTasksCompleted
	c.mu.Unlock()
	if state != Completed || completed != 1 {
		t.Fatalf("live report was not accepted: state=%v completed=%d", state, completed)
	}
}

// A malformed report must not panic the coordinator.
func TestReportOutOfRangeTaskID(t *testing.T) {
	c := newTestCoordinator(1, 1)
	for _, id := range []int{-1, 99} {
		args := ReportTaskArgs{TaskID: id, TaskType: MapTask, Attempt: 1}
		if err := c.ReportTask(&args, &ReportTaskReply{}); err != nil {
			t.Fatalf("ReportTask(%d): %v", id, err)
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
	c := newTestCoordinator(1, 1)
	if c.Done() {
		t.Fatal("job reported done before any task ran")
	}

	m := request(t, c)
	report(t, c, m)
	if c.Done() {
		t.Fatal("job reported done with the reduce phase outstanding")
	}

	r := request(t, c)
	if r.TaskType != ReduceTask {
		t.Fatalf("want a reduce task, got %v", r.TaskType)
	}
	report(t, c, r)

	// No worker asks for another task after this point.
	if !c.Done() {
		t.Fatal("job did not report done after every task completed")
	}
}
