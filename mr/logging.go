package mr

import (
	"io"
	"log/slog"
	"os"
)

// String names a task type for logs and status output.
func (t TaskType) String() string {
	switch t {
	case MapTask:
		return "map"
	case ReduceTask:
		return "reduce"
	case WaitTask:
		return "wait"
	case ExitTask:
		return "exit"
	default:
		return "unknown"
	}
}

// String names a task state for status output.
func (s TaskState) String() string {
	switch s {
	case Idle:
		return "idle"
	case InProgress:
		return "in_progress"
	case Committing:
		return "committing"
	case Completed:
		return "completed"
	default:
		return "unknown"
	}
}

// DiscardLogger drops every record. A caller that does not ask for logs, such
// as a benchmark measuring the system, does not get any.
func DiscardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// StderrLogger writes one JSON object per line to stderr.
func StderrLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
