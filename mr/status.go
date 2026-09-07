package mr

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

// Status is a snapshot of the running job, served as JSON from /status.
type Status struct {
	Phase     string         `json:"phase"`
	ElapsedMS int64          `json:"elapsed_ms"`
	Done      bool           `json:"done"`
	Map       PhaseStatus    `json:"map"`
	Reduce    PhaseStatus    `json:"reduce"`
	Running   []RunningTask  `json:"running_tasks"`
	Workers   []WorkerStatus `json:"workers"`
	Backups   BackupStatus   `json:"speculation"`
	RPC       RPCStatus      `json:"rpc"`
}

// PhaseStatus counts the tasks of one phase by state.
type PhaseStatus struct {
	Total      int `json:"total"`
	Idle       int `json:"idle"`
	InProgress int `json:"in_progress"`
	Committing int `json:"committing"`
	Completed  int `json:"completed"`
}

// RunningTask is one attempt currently occupying a worker.
type RunningTask struct {
	Phase     string `json:"phase"`
	TaskID    int    `json:"task_id"`
	Attempt   int    `json:"attempt"`
	WorkerID  string `json:"worker_id"`
	Backup    bool   `json:"backup"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

// WorkerStatus is what one worker has done so far.
type WorkerStatus struct {
	WorkerID  string `json:"worker_id"`
	Committed int    `json:"tasks_committed"`
	Running   int    `json:"tasks_running"`
}

// BackupStatus reports speculative execution activity.
type BackupStatus struct {
	Enabled  bool  `json:"enabled"`
	Launched int64 `json:"backups_launched"`
	Won      int64 `json:"backups_won"`
}

// RPCStatus reports the coordinator's own load.
type RPCStatus struct {
	RequestCalls  int64 `json:"request_calls"`
	RequestWaits  int64 `json:"request_waits"`
	ReportCalls   int64 `json:"report_calls"`
	HandlerTimeMS int64 `json:"handler_time_ms"`
}

// Status returns a snapshot of the job as it stands right now.
func (c *Coordinator) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()

	s := Status{
		ElapsedMS: time.Since(c.jobStart).Milliseconds(),
		Done:      c.jobDone(),
		Map:       phaseStatus(c.mapTasks),
		Reduce:    phaseStatus(c.reduceTasks),
		Backups: BackupStatus{
			Enabled:  c.cfg.Speculation,
			Launched: c.backupsLaunched,
			Won:      c.backupsWon,
		},
		RPC: RPCStatus{
			RequestCalls:  c.rpc.RequestCalls,
			RequestWaits:  c.rpc.RequestWaits,
			ReportCalls:   c.rpc.ReportCalls,
			HandlerTimeMS: (c.rpc.RequestTime + c.rpc.ReportTime).Milliseconds(),
		},
	}

	switch {
	case s.Done:
		s.Phase = "done"
	case c.mapTasksCompleted == c.nMap:
		s.Phase = "reduce"
	default:
		s.Phase = "map"
	}

	s.Running = append(runningTasks(c.mapTasks, MapTask), runningTasks(c.reduceTasks, ReduceTask)...)
	s.Workers = c.workerStatus()

	return s
}

// phaseStatus counts one phase's tasks by state.
func phaseStatus(tasks []TaskInfo) PhaseStatus {
	p := PhaseStatus{Total: len(tasks)}
	for _, t := range tasks {
		switch t.State {
		case Idle:
			p.Idle++
		case InProgress:
			p.InProgress++
		case Committing:
			p.Committing++
		case Completed:
			p.Completed++
		}
	}
	return p
}

// runningTasks lists the attempts currently in flight for one phase.
func runningTasks(tasks []TaskInfo, phase TaskType) []RunningTask {
	running := []RunningTask{}
	for _, t := range tasks {
		for _, a := range t.Live {
			running = append(running, RunningTask{
				Phase:     phase.String(),
				TaskID:    t.ID,
				Attempt:   a.ID,
				WorkerID:  a.WorkerID,
				Backup:    a.Backup,
				ElapsedMS: time.Since(a.Start).Milliseconds(),
			})
		}
	}
	return running
}

// workerStatus summarises each worker the coordinator has heard from.
// Callers must hold c.mu.
func (c *Coordinator) workerStatus() []WorkerStatus {
	committed := map[string]int{}
	for _, m := range c.taskMetrics {
		committed[m.WorkerID]++
	}

	running := map[string]int{}
	for _, tasks := range [][]TaskInfo{c.mapTasks, c.reduceTasks} {
		for _, t := range tasks {
			for _, a := range t.Live {
				running[a.WorkerID]++
			}
		}
	}

	ids := map[string]struct{}{}
	for id := range committed {
		ids[id] = struct{}{}
	}
	for id := range running {
		ids[id] = struct{}{}
	}

	names := make([]string, 0, len(ids))
	for id := range ids {
		names = append(names, id)
	}
	sort.Strings(names)

	out := make([]WorkerStatus, 0, len(names))
	for _, id := range names {
		out = append(out, WorkerStatus{
			WorkerID:  id,
			Committed: committed[id],
			Running:   running[id],
		})
	}
	return out
}

// handleStatus serves the live job state.
func (c *Coordinator) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(c.Status()); err != nil {
		c.cfg.Logger.Warn("status_write_failed", "error", err.Error())
	}
}
