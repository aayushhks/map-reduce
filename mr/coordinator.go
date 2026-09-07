package mr

import (
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sort"
	"sync"
	"time"
)

// TaskState defines the possible states of a task.
type TaskState int

const (
	Idle       TaskState = iota // 0, no attempt is running
	InProgress                  // 1, at least one attempt is running
	Committing                  // 2, one attempt has been cleared to publish output
	Completed                   // 3
)

// attempt is one execution of a task, either the first or a speculative backup.
type attempt struct {
	ID       int
	WorkerID string
	Start    time.Time
	Backup   bool
}

// TaskInfo holds metadata for a single task. A task may have more than one
// attempt running at once, but only the committer may publish its output.
type TaskInfo struct {
	ID          int
	State       TaskState
	Split       Split // The input byte range, only for Map tasks
	NextAttempt int   // Attempt id to hand out next
	Live        []attempt
	Committer   int       // Attempt cleared to publish, meaningful while Committing
	CommitStart time.Time // When that clearance was given
}

type Coordinator struct {
	mu sync.Mutex // Mutex to protect shared state

	cfg            Config
	listener       net.Listener
	statusListener net.Listener

	mapTasks    []TaskInfo
	reduceTasks []TaskInfo

	nReduce              int
	nMap                 int
	mapTasksCompleted    int
	reduceTasksCompleted int

	rpc              RPCStats
	backupsLaunched  int64
	backupsWon       int64
	jobStart         time.Time
	mapPhaseEnd      time.Time
	reducePhaseStart time.Time
	jobEnd           time.Time
	taskMetrics      []TaskMetrics
	events           []TaskEvent
}

// record appends one task event and logs it. Callers must hold c.mu.
func (c *Coordinator) record(kind string, phase TaskType, taskID, attempt int, workerID string, backup bool) {
	c.events = append(c.events, TaskEvent{
		Kind:     kind,
		TaskType: phase,
		TaskID:   taskID,
		Attempt:  attempt,
		WorkerID: workerID,
		Backup:   backup,
		At:       time.Now(),
	})

	c.cfg.Logger.Info("task",
		"event", kind,
		"phase", phase.String(),
		"task_id", taskID,
		"attempt", attempt,
		"worker_id", workerID,
		"backup", backup,
		"elapsed_ms", time.Since(c.jobStart).Milliseconds(),
	)
}

// phase logs a job level transition.
func (c *Coordinator) phase(name string) {
	c.cfg.Logger.Info("phase",
		"transition", name,
		"map_completed", c.mapTasksCompleted,
		"map_total", c.nMap,
		"reduce_completed", c.reduceTasksCompleted,
		"reduce_total", c.nReduce,
		"elapsed_ms", time.Since(c.jobStart).Milliseconds(),
	)
}

// jobDone reports whether every task has finished. Callers must hold c.mu.
func (c *Coordinator) jobDone() bool {
	return c.mapTasksCompleted == c.nMap && c.reduceTasksCompleted == c.nReduce
}

// Trace returns the recorded timeline of the job so far.
func (c *Coordinator) Trace() JobTrace {
	c.mu.Lock()
	defer c.mu.Unlock()

	tasks := make([]TaskMetrics, len(c.taskMetrics))
	copy(tasks, c.taskMetrics)

	events := make([]TaskEvent, len(c.events))
	copy(events, c.events)

	return JobTrace{
		Start:            c.jobStart,
		MapPhaseEnd:      c.mapPhaseEnd,
		ReducePhaseStart: c.reducePhaseStart,
		End:              c.jobEnd,
		NMap:             c.nMap,
		NReduce:          c.nReduce,
		RPC:              c.rpc,
		BackupsLaunched:  c.backupsLaunched,
		BackupsWon:       c.backupsWon,
		Tasks:            tasks,
		Events:           events,
	}
}

// RequestTask is the RPC handler for workers asking for a task.
func (c *Coordinator) RequestTask(args *RequestTaskArgs, reply *RequestTaskReply) error {
	entered := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	c.rpc.RequestCalls++
	defer func() {
		c.rpc.RequestTime += time.Since(entered)
		if reply.TaskType == WaitTask {
			c.rpc.RequestWaits++
		}
	}()

	reply.WorkDir = c.cfg.WorkDir

	if c.mapTasksCompleted < c.nMap {
		if c.handOut(c.mapTasks, MapTask, args.WorkerID, reply) {
			return nil
		}
		reply.TaskType = WaitTask
		reply.WaitBackoff = c.cfg.WaitBackoff
		return nil
	}

	if c.reduceTasksCompleted < c.nReduce {
		if c.reducePhaseStart.IsZero() {
			c.reducePhaseStart = time.Now()
			c.phase("reduce_phase_start")
		}
		if c.handOut(c.reduceTasks, ReduceTask, args.WorkerID, reply) {
			return nil
		}
		reply.TaskType = WaitTask
		reply.WaitBackoff = c.cfg.WaitBackoff
		return nil
	}

	// All map and reduce tasks are done, tell the worker to exit
	reply.TaskType = ExitTask
	return nil
}

// handOut gives the worker an idle task, or a backup for a straggler when no
// idle task is left. Callers must hold c.mu.
func (c *Coordinator) handOut(tasks []TaskInfo, kind TaskType, workerID string, reply *RequestTaskReply) bool {
	for i := range tasks {
		if tasks[i].State == Idle {
			c.start(&tasks[i], kind, workerID, false, reply)
			return true
		}
	}

	if straggler := c.straggler(tasks, kind, workerID); straggler != nil {
		c.start(straggler, kind, workerID, true, reply)
		c.backupsLaunched++
		return true
	}

	return false
}

// start records a new attempt on a task and fills in the reply.
func (c *Coordinator) start(task *TaskInfo, kind TaskType, workerID string, backup bool, reply *RequestTaskReply) {
	task.NextAttempt++
	task.State = InProgress
	task.Live = append(task.Live, attempt{
		ID:       task.NextAttempt,
		WorkerID: workerID,
		Start:    time.Now(),
		Backup:   backup,
	})

	c.record(EventAssigned, kind, task.ID, task.NextAttempt, workerID, backup)

	reply.TaskType = kind
	reply.TaskID = task.ID
	reply.Attempt = task.NextAttempt
	reply.Backup = backup
	if kind == MapTask {
		reply.Split = task.Split
		reply.NReduce = c.nReduce
		return
	}
	reply.NMap = c.nMap
}

// straggler returns a task worth running a second time: one running well past
// the median for its phase, not already being run by this worker, and not
// already backed up. Callers must hold c.mu.
func (c *Coordinator) straggler(tasks []TaskInfo, kind TaskType, workerID string) *TaskInfo {
	if !c.cfg.Speculation {
		return nil
	}

	cutoff, ok := c.stragglerCutoff(kind)
	if !ok {
		return nil
	}

	var worst *TaskInfo
	var worstElapsed time.Duration

	for i := range tasks {
		task := &tasks[i]
		if task.State != InProgress || len(task.Live) != 1 {
			continue
		}
		if task.Live[0].WorkerID == workerID {
			continue
		}

		elapsed := time.Since(task.Live[0].Start)
		if elapsed > cutoff && elapsed > worstElapsed {
			worst, worstElapsed = task, elapsed
		}
	}

	return worst
}

// stragglerCutoff is the elapsed time past which a running task of this kind
// counts as a straggler. It needs enough completed tasks for the median to mean
// something. Callers must hold c.mu.
func (c *Coordinator) stragglerCutoff(kind TaskType) (time.Duration, bool) {
	durations := []time.Duration{}
	for _, m := range c.taskMetrics {
		if m.TaskType == kind {
			durations = append(durations, m.Duration())
		}
	}
	if len(durations) < c.cfg.SpeculationMinSamples {
		return 0, false
	}

	sort.Slice(durations, func(a, b int) bool { return durations[a] < durations[b] })
	median := durations[len(durations)/2]

	return time.Duration(float64(median) * c.cfg.SpeculationThreshold), true
}

// ReportTask is a worker asking permission to publish the output it just built.
// Exactly one attempt per task is cleared, so a losing backup never writes.
func (c *Coordinator) ReportTask(args *ReportTaskArgs, reply *ReportTaskReply) error {
	entered := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	c.rpc.ReportCalls++
	defer func() { c.rpc.ReportTime += time.Since(entered) }()

	task := c.task(args.TaskType, args.TaskID)
	if task == nil {
		return nil
	}

	// A retry from the attempt that already holds the clearance gets the same
	// answer, so a dropped reply does not cost the work.
	if task.State == Committing && task.Committer == args.Attempt {
		reply.Commit = true
		return nil
	}

	// Another attempt already won, or this attempt was reaped as timed out.
	if task.State != InProgress || !task.isLive(args.Attempt) {
		c.record(EventRefused, args.TaskType, args.TaskID, args.Attempt, args.WorkerID, false)
		reply.Commit = false
		return nil
	}

	task.State = Committing
	task.Committer = args.Attempt
	task.CommitStart = time.Now()
	reply.Commit = true

	return nil
}

// CommitTask is the committer confirming its output is in place. Only now is
// the task counted as done, so a worker that dies mid rename is re-run.
func (c *Coordinator) CommitTask(args *CommitTaskArgs, reply *CommitTaskReply) error {
	entered := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	c.rpc.ReportCalls++
	defer func() { c.rpc.ReportTime += time.Since(entered) }()

	task := c.task(args.TaskType, args.TaskID)
	if task == nil {
		return nil
	}
	if task.State != Committing || task.Committer != args.Attempt {
		return nil
	}

	if args.Metrics.Backup {
		c.backupsWon++
	}

	c.record(EventCommitted, args.TaskType, args.TaskID, args.Attempt, args.WorkerID, args.Metrics.Backup)

	task.State = Completed
	task.Live = nil
	c.taskMetrics = append(c.taskMetrics, args.Metrics)

	if args.TaskType == MapTask {
		c.mapTasksCompleted++
		if c.mapTasksCompleted == c.nMap {
			c.mapPhaseEnd = time.Now()
			c.phase("map_phase_end")
		}
		return nil
	}

	c.reduceTasksCompleted++
	if c.reduceTasksCompleted == c.nReduce {
		c.jobEnd = time.Now()
		c.phase("job_end")
	}
	return nil
}

// task looks up a task by kind and id, or nil if the id is out of range.
func (c *Coordinator) task(kind TaskType, id int) *TaskInfo {
	var tasks []TaskInfo
	switch kind {
	case MapTask:
		tasks = c.mapTasks
	case ReduceTask:
		tasks = c.reduceTasks
	default:
		c.cfg.Logger.Warn("bad_report", "reason", "unknown task type", "task_type", int(kind))
		return nil
	}

	if id < 0 || id >= len(tasks) {
		c.cfg.Logger.Warn("bad_report", "reason", "task id out of range", "task_id", id)
		return nil
	}
	return &tasks[id]
}

// isLive reports whether the attempt is still one of this task's running ones.
func (t *TaskInfo) isLive(id int) bool {
	for _, a := range t.Live {
		if a.ID == id {
			return true
		}
	}
	return false
}

// start a thread that listens for RPCs from worker.go
func (c *Coordinator) server() {
	server := rpc.NewServer()
	if err := server.Register(c); err != nil {
		log.Fatal("register error:", err)
	}

	mux := http.NewServeMux()
	mux.Handle(rpc.DefaultRPCPath, server)
	mux.HandleFunc("/status", c.handleStatus)

	os.Remove(c.cfg.SocketPath)
	l, e := net.Listen("unix", c.cfg.SocketPath)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	c.listener = l

	go http.Serve(l, mux)

	// The status page is also served over TCP when an address is configured,
	// so a browser or a curl from another machine can watch a job.
	if c.cfg.StatusAddr != "" {
		status, err := net.Listen("tcp", c.cfg.StatusAddr)
		if err != nil {
			log.Fatal("status listen error:", err)
		}
		c.statusListener = status
		c.cfg.Logger.Info("status_endpoint", "addr", status.Addr().String())
		go http.Serve(status, mux)
	}
}

// Shutdown stops the RPC listener so an in-process caller can run another job.
func (c *Coordinator) Shutdown() {
	if c.listener != nil {
		c.listener.Close()
	}
	if c.statusListener != nil {
		c.statusListener.Close()
	}
	os.Remove(c.cfg.SocketPath)
}

// mr-main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
func (c *Coordinator) Done() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.jobDone()
}

// checkTimeouts periodically returns tasks from crashed or stalled workers to
// the idle pool.
func (c *Coordinator) checkTimeouts() {
	for {
		c.mu.Lock()
		done := c.jobDone()
		c.mu.Unlock()
		if done {
			return
		}
		c.reapTimeouts()
		time.Sleep(c.cfg.ReapInterval)
	}
}

// reapTimeouts makes one pass over the running tasks and drops any attempt that
// has run past the timeout. A task with no attempts left goes back to idle.
func (c *Coordinator) reapTimeouts() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.reap(c.mapTasks, "Map")
	c.reap(c.reduceTasks, "Reduce")
}

// kindOf maps a reaper label back to its task type.
func kindOf(label string) TaskType {
	if label == "Map" {
		return MapTask
	}
	return ReduceTask
}

// reap drops expired attempts from one phase. Callers must hold c.mu.
func (c *Coordinator) reap(tasks []TaskInfo, label string) {
	for i := range tasks {
		task := &tasks[i]

		// A committer that died mid rename leaves the task unfinished, so put
		// the whole task back rather than trusting output that may not exist.
		if task.State == Committing {
			if time.Since(task.CommitStart) > c.cfg.TaskTimeout {
				c.record(EventReaped, kindOf(label), task.ID, task.Committer, "", false)
				task.State = Idle
				task.Live = nil
			}
			continue
		}

		if task.State != InProgress {
			continue
		}

		live := task.Live[:0]
		for _, a := range task.Live {
			if time.Since(a.Start) > c.cfg.TaskTimeout {
				c.record(EventReaped, kindOf(label), task.ID, a.ID, a.WorkerID, a.Backup)
				continue
			}
			live = append(live, a)
		}
		task.Live = live

		if len(task.Live) == 0 {
			task.State = Idle
		}
	}
}

// create a Coordinator.
// mr-main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	cfg := DefaultConfig(nReduce)
	cfg.Logger = StderrLogger()
	return MakeCoordinatorWithConfig(files, cfg)
}

// MakeCoordinatorWithConfig creates a Coordinator with explicit settings.
func MakeCoordinatorWithConfig(files []string, cfg Config) *Coordinator {
	cfg = cfg.withDefaults()

	splits, err := PlanSplits(files, cfg.SplitBytes)
	if err != nil {
		log.Fatal("planning input splits: ", err)
	}

	c := Coordinator{
		cfg:         cfg,
		jobStart:    time.Now(),
		nReduce:     cfg.NReduce,
		nMap:        len(splits),
		mapTasks:    make([]TaskInfo, len(splits)),
		reduceTasks: make([]TaskInfo, cfg.NReduce),
	}

	// Initialize map tasks, one per input split
	for i, split := range splits {
		c.mapTasks[i] = TaskInfo{ID: i, State: Idle, Split: split}
	}

	// Initialize reduce tasks
	for i := 0; i < cfg.NReduce; i++ {
		c.reduceTasks[i] = TaskInfo{ID: i, State: Idle}
	}

	cfg.Logger.Info("job_start",
		"map_tasks", c.nMap,
		"reduce_tasks", c.nReduce,
		"split_bytes", cfg.SplitBytes,
		"task_timeout_ms", cfg.TaskTimeout.Milliseconds(),
		"speculation", cfg.Speculation,
	)

	c.server()

	// Start a background goroutine to check for task timeouts
	go c.checkTimeouts()

	return &c
}
