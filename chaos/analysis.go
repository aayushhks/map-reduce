package main

import (
	"sort"
	"time"

	"cs651/mr"
)

// Result is one scenario's measured outcome, written to the report.
type Result struct {
	Scenario    string `json:"scenario"`
	Description string `json:"description"`
	Trials      int    `json:"trials"`

	Completed   bool   `json:"completed"`
	OutputHash  string `json:"output_hash"`
	OutputKeys  int    `json:"output_keys"`
	CorrectHash bool   `json:"output_matches_clean_run"`

	WallMS float64 `json:"wall_ms"`

	Kills           int     `json:"workers_killed"`
	RecoveryMinMS   float64 `json:"recovery_min_ms"`
	RecoveryMedMS   float64 `json:"recovery_median_ms"`
	RecoveryMaxMS   float64 `json:"recovery_max_ms"`
	TasksReassigned int     `json:"tasks_reassigned_after_failure"`

	TasksTotal        int     `json:"tasks_total"`
	CommittedAtFail   int     `json:"tasks_committed_before_failure"`
	PreservedShare    float64 `json:"work_preserved_share"`
	RedundantAttempts int     `json:"redundant_attempts"`
	WorkLostMS        float64 `json:"work_lost_ms"`
}

// analyse turns every trial of one scenario into the numbers the report needs.
// A scenario counts as correct only if every trial finished with the right
// output, so one bad run cannot hide behind a median.
func analyse(sc Scenario, outcomes []Outcome, cleanHash string) Result {
	r := Result{
		Scenario:    sc.Name,
		Description: sc.Description,
		Trials:      len(outcomes),
		Completed:   true,
		CorrectHash: true,
	}

	walls := []float64{}
	recoveries := []time.Duration{}
	preserved := []float64{}
	var kills, reassigned, redundant, committedAtFail int
	var lost time.Duration

	for _, o := range outcomes {
		if !o.Completed {
			r.Completed = false
		}
		if o.OutputHash != cleanHash {
			r.CorrectHash = false
		}
		r.OutputHash = o.OutputHash
		r.OutputKeys = o.OutputKeys
		r.TasksTotal = o.Trace.NMap + o.Trace.NReduce
		walls = append(walls, ms(o.WallClock))
		kills += len(o.Kills)

		assigned, committed := indexEvents(o.Trace)
		extra := len(assigned) - r.TasksTotal
		if extra > 0 {
			redundant += extra
		}
		lost += workLost(o.Trace, assigned, committed)

		if len(o.Kills) == 0 {
			preserved = append(preserved, 1)
			continue
		}

		first := o.Kills[0].At
		done := 0
		for _, e := range o.Trace.Events {
			if e.Kind == mr.EventCommitted && e.At.Before(first) {
				done++
			}
		}
		committedAtFail += done
		if r.TasksTotal > 0 {
			preserved = append(preserved, float64(done)/float64(r.TasksTotal))
		}

		found := recoveryTimes(o)
		reassigned += len(found)
		recoveries = append(recoveries, found...)
	}

	r.Kills = kills
	r.TasksReassigned = reassigned
	r.RedundantAttempts = redundant
	r.CommittedAtFail = committedAtFail
	r.WorkLostMS = ms(lost / time.Duration(max(1, len(outcomes))))
	r.WallMS = medianFloat(walls)
	r.PreservedShare = round(medianFloat(preserved))

	if len(recoveries) > 0 {
		sort.Slice(recoveries, func(a, b int) bool { return recoveries[a] < recoveries[b] })
		r.RecoveryMinMS = ms(recoveries[0])
		r.RecoveryMedMS = ms(recoveries[len(recoveries)/2])
		r.RecoveryMaxMS = ms(recoveries[len(recoveries)-1])
	}

	return r
}

// medianFloat returns the median of a set of measurements.
func medianFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return round(sorted[len(sorted)/2])
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// taskKey identifies one task across attempts.
type taskKey struct {
	kind mr.TaskType
	id   int
}

// attemptKey identifies one attempt of one task.
type attemptKey struct {
	task    taskKey
	attempt int
}

// indexEvents collects when each attempt was assigned and which ones committed.
func indexEvents(t mr.JobTrace) (map[attemptKey]mr.TaskEvent, map[attemptKey]bool) {
	assigned := map[attemptKey]mr.TaskEvent{}
	committed := map[attemptKey]bool{}

	for _, e := range t.Events {
		key := attemptKey{taskKey{e.TaskType, e.TaskID}, e.Attempt}
		switch e.Kind {
		case mr.EventAssigned:
			assigned[key] = e
		case mr.EventCommitted:
			committed[key] = true
		}
	}
	return assigned, committed
}

// workLost totals the running time of attempts that never published anything,
// which is the work the job had to throw away and do again.
func workLost(t mr.JobTrace, assigned map[attemptKey]mr.TaskEvent, committed map[attemptKey]bool) time.Duration {
	reaped := map[attemptKey]time.Time{}
	for _, e := range t.Events {
		if e.Kind == mr.EventReaped {
			reaped[attemptKey{taskKey{e.TaskType, e.TaskID}, e.Attempt}] = e.At
		}
	}

	var total time.Duration
	for key, start := range assigned {
		if committed[key] {
			continue
		}
		end, ok := reaped[key]
		if !ok {
			end = t.End
		}
		if end.After(start.At) {
			total += end.Sub(start.At)
		}
	}
	return total
}

// recoveryTimes measures, for every task a killed worker was running, how long
// until that task was handed to somebody else.
func recoveryTimes(o Outcome) []time.Duration {
	assigned, committed := indexEvents(o.Trace)

	// Tasks each kill interrupted: assigned to that worker, never committed.
	type interrupted struct {
		task taskKey
		at   time.Time
	}
	lost := []interrupted{}

	for _, kill := range o.Kills {
		for key, e := range assigned {
			if e.WorkerID != kill.WorkerID || committed[key] {
				continue
			}
			if e.At.After(kill.At) {
				continue
			}
			lost = append(lost, interrupted{task: key.task, at: kill.At})
		}
	}

	recoveries := []time.Duration{}
	for _, item := range lost {
		var next time.Time
		for _, e := range o.Trace.Events {
			if e.Kind != mr.EventAssigned || e.TaskID != item.task.id || e.TaskType != item.task.kind {
				continue
			}
			if e.At.After(item.at) && (next.IsZero() || e.At.Before(next)) {
				next = e.At
			}
		}
		if !next.IsZero() {
			recoveries = append(recoveries, next.Sub(item.at))
		}
	}
	return recoveries
}

// ms converts a duration to milliseconds, rounded for stable output.
func ms(d time.Duration) float64 {
	return round(float64(d) / float64(time.Millisecond))
}

// round trims a measurement to three decimal places.
func round(v float64) float64 {
	return float64(int64(v*1000+0.5)) / 1000
}
