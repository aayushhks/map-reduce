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

	// Speculation runs a backup attempt for a task whose elapsed time exceeds
	// SpeculationThreshold times the median for its phase. The median needs
	// SpeculationMinSamples completed tasks before it means anything.
	Speculation           bool
	SpeculationThreshold  float64
	SpeculationMinSamples int
}

// DefaultConfig returns the settings the standalone mrcoordinator binary uses.
func DefaultConfig(nReduce int) Config {
	return Config{
		NReduce:      nReduce,
		TaskTimeout:  10 * time.Second,
		ReapInterval: 2 * time.Second,
		WaitBackoff:  10 * time.Millisecond,
		SocketPath:   coordinatorSock(),

		Speculation:           false,
		SpeculationThreshold:  2.0,
		SpeculationMinSamples: 5,
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
	if c.SpeculationThreshold <= 0 {
		c.SpeculationThreshold = d.SpeculationThreshold
	}
	if c.SpeculationMinSamples <= 0 {
		c.SpeculationMinSamples = d.SpeculationMinSamples
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
