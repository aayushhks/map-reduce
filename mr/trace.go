package mr

import (
	"io"
	"time"
)

// TaskMetrics is what a worker reports about one finished task attempt.
type TaskMetrics struct {
	WorkerID    string
	TaskType    TaskType
	TaskID      int
	Attempt     int
	Start       time.Time
	End         time.Time
	Compute     time.Duration // Time inside the map or reduce function
	IO          time.Duration // Time reading inputs and writing outputs
	Dispatch    time.Duration // Round trip of the RPC that handed out this task
	InputBytes  int64
	OutputBytes int64
	Records     int64
}

// Duration is how long the whole task attempt took.
func (m TaskMetrics) Duration() time.Duration {
	return m.End.Sub(m.Start)
}

// RPCStats is the coordinator side cost of serving workers. Handler time is
// measured from entry, so it includes waiting for the coordinator lock and
// rises when workers contend for it.
type RPCStats struct {
	RequestCalls int64
	RequestTime  time.Duration
	RequestWaits int64 // Replies that told a worker to wait for work
	ReportCalls  int64
	ReportTime   time.Duration
}

// JobTrace is the full record of one job, enough to rebuild a timeline.
type JobTrace struct {
	Start            time.Time
	MapPhaseEnd      time.Time // When the last map task completed
	ReducePhaseStart time.Time // When the first reduce task was assigned
	End              time.Time // When the last reduce task completed
	NMap             int
	NReduce          int
	RPC              RPCStats
	Tasks            []TaskMetrics
}

// countingWriter tallies the bytes written through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
