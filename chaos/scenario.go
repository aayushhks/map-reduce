package main

import "time"

// Scenario is one way of breaking a job.
type Scenario struct {
	Name        string
	Description string

	Workers int

	// KillCount workers are killed once the phase named by KillPhase is under
	// way. Restart brings a replacement worker up in place of each one.
	KillCount int
	KillPhase string
	Restart   bool

	// KillRepeat kills the same worker slot this many times in a row, each time
	// after a fresh replacement has had a moment to pick up work.
	KillRepeat int

	// RPC faults apply to every worker for the whole job.
	DropRate float64
	RPCDelay time.Duration
}

// scenarios is the fault matrix the harness runs.
var scenarios = []Scenario{
	{
		Name:        "clean",
		Description: "no faults, the reference run",
		Workers:     4,
	},
	{
		Name:        "kill-1-map",
		Description: "one worker killed during the map phase, not replaced",
		Workers:     4,
		KillCount:   1,
		KillPhase:   "map",
	},
	{
		Name:        "kill-2-map",
		Description: "two workers killed during the map phase, not replaced",
		Workers:     4,
		KillCount:   2,
		KillPhase:   "map",
	},
	{
		Name:        "kill-1-map-restart",
		Description: "one worker killed during the map phase and replaced",
		Workers:     4,
		KillCount:   1,
		KillPhase:   "map",
		Restart:     true,
	},
	{
		Name:        "kill-1-reduce",
		Description: "one worker killed during the reduce phase, not replaced",
		Workers:     4,
		KillCount:   1,
		KillPhase:   "reduce",
	},
	{
		Name:        "kill-repeat-map",
		Description: "one worker slot killed three times during the map phase, replaced each time",
		Workers:     4,
		KillPhase:   "map",
		KillRepeat:  3,
		Restart:     true,
	},
	{
		Name:        "rpc-drop-10pct",
		Description: "one in ten rpcs dropped for every worker, no kills",
		Workers:     4,
		DropRate:    0.10,
	},
	{
		Name:        "rpc-drop-30pct",
		Description: "three in ten rpcs dropped for every worker, no kills",
		Workers:     4,
		DropRate:    0.30,
	},
	{
		Name:        "rpc-delay-25ms",
		Description: "every rpc delayed by 25 ms, no kills",
		Workers:     4,
		RPCDelay:    25 * time.Millisecond,
	},
}

// lookup finds a scenario by name.
func lookup(name string) (Scenario, bool) {
	for _, s := range scenarios {
		if s.Name == name {
			return s, true
		}
	}
	return Scenario{}, false
}
