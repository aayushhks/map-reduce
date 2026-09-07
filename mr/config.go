package mr

import (
	"path/filepath"
	"time"
)

// Config holds the coordinator settings that benchmarks and tests vary.
// The zero value of each field falls back to the standalone defaults.
type Config struct {
	NReduce      int           // Number of reduce partitions
	SplitBytes   int           // Target size of one map input split, 0 means one split per file
	TaskTimeout  time.Duration // How long a task may run before it is assumed lost
	ReapInterval time.Duration // How often the coordinator looks for lost tasks
	WaitBackoff  time.Duration // How long a worker sleeps when no task is available
	WorkDir      string        // Directory holding intermediate and output files
	SocketPath   string        // Unix socket the coordinator listens on
}

// DefaultConfig returns the settings the standalone mrcoordinator binary uses.
func DefaultConfig(nReduce int) Config {
	return Config{
		NReduce:      nReduce,
		TaskTimeout:  10 * time.Second,
		ReapInterval: 2 * time.Second,
		WaitBackoff:  10 * time.Millisecond,
		SocketPath:   coordinatorSock(),
	}
}

// withDefaults fills any field left at its zero value.
func (c Config) withDefaults() Config {
	d := DefaultConfig(c.NReduce)
	if c.TaskTimeout == 0 {
		c.TaskTimeout = d.TaskTimeout
	}
	if c.ReapInterval == 0 {
		c.ReapInterval = d.ReapInterval
	}
	if c.WaitBackoff == 0 {
		c.WaitBackoff = d.WaitBackoff
	}
	if c.SocketPath == "" {
		c.SocketPath = d.SocketPath
	}
	return c
}

// path resolves a file name against the configured working directory.
func (c Config) path(name string) string {
	if c.WorkDir == "" {
		return name
	}
	return filepath.Join(c.WorkDir, name)
}
