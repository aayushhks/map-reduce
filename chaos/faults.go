package main

import (
	"math/rand"
	"time"

	"cs651/mr"
)

// injectFaults carries out the scenario's kill plan and returns the kills it
// performed, each stamped with the moment the process was signalled.
func injectFaults(sc Scenario, cfg JobConfig, c *mr.Coordinator, p *pool) []Kill {
	if sc.KillCount == 0 && sc.KillRepeat == 0 {
		return nil
	}

	random := rand.New(rand.NewSource(cfg.Seed))
	kills := []Kill{}

	// Kill somewhere in the middle of the phase rather than at a fixed point,
	// so the failure does not always land on the same task boundary.
	progress := 0.2 + random.Float64()*0.3

	if !waitForPhase(c, sc.KillPhase, progress, cfg.Deadline) {
		return kills
	}

	rounds := sc.KillRepeat
	if rounds == 0 {
		rounds = sc.KillCount
	}

	for round := 0; round < rounds; round++ {
		if round > 0 {
			// Give the replacement time to claim a task before killing again.
			time.Sleep(4 * cfg.ReapInterval)
			if c.Done() {
				break
			}
		}

		slot := round
		if sc.KillRepeat > 0 {
			slot = p.count() - 1 // Always the newest worker, the replaced slot
		}

		id, ok := p.kill(slot)
		if !ok {
			continue
		}
		kills = append(kills, Kill{WorkerID: id, At: time.Now()})

		if sc.Restart {
			if err := p.spawn(slot); err != nil {
				return kills
			}
		}
	}

	return kills
}

// waitForPhase blocks until the named phase has made the given fraction of
// progress and is still running, or the deadline passes.
func waitForPhase(c *mr.Coordinator, phase string, fraction float64, deadline time.Duration) bool {
	limit := time.Now().Add(deadline)

	for time.Now().Before(limit) {
		trace := c.Trace()
		if reached(trace, phase, fraction) {
			return true
		}
		if c.Done() {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// reached reports whether the phase is far enough along to break it.
func reached(t mr.JobTrace, phase string, fraction float64) bool {
	done := map[mr.TaskType]int{}
	for _, e := range t.Events {
		if e.Kind == mr.EventCommitted {
			done[e.TaskType]++
		}
	}

	if phase == "reduce" {
		if t.ReducePhaseStart.IsZero() || !t.End.IsZero() {
			return false
		}
		return float64(done[mr.ReduceTask]) >= fraction*float64(t.NReduce)
	}

	if !t.MapPhaseEnd.IsZero() {
		return false
	}
	return float64(done[mr.MapTask]) >= fraction*float64(t.NMap)
}
