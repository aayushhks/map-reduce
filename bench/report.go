package main

import (
	"sort"
	"time"

	"cs651/mr"
)

// summarizeTrial turns one job trace into a trial record.
func summarizeTrial(index int, r RunResult) Trial {
	t := r.Trace

	mapDurations := []time.Duration{}
	reduceDurations := []time.Duration{}
	var shuffled, records int64

	for _, task := range t.Tasks {
		if task.TaskType == mr.MapTask {
			mapDurations = append(mapDurations, task.Duration())
			shuffled += task.OutputBytes
			records += task.Records
			continue
		}
		reduceDurations = append(reduceDurations, task.Duration())
	}

	return Trial{
		Index:             index,
		WallMS:            millis(t.End.Sub(t.Start)),
		MapPhaseMS:        millis(t.MapPhaseEnd.Sub(t.Start)),
		ShuffleStallMS:    millis(t.ReducePhaseStart.Sub(t.MapPhaseEnd)),
		ReducePhaseMS:     millis(t.End.Sub(t.ReducePhaseStart)),
		BytesShuffled:     shuffled,
		IntermediateFiles: t.NMap * t.NReduce,
		RecordsEmitted:    records,
		OutputKeys:        r.OutputKeys,
		BackupsLaunched:   t.BackupsLaunched,
		BackupsWon:        t.BackupsWon,
		OutputHash:        r.OutputHash,
		MapTaskMS:         describe(mapDurations),
		ReduceTaskMS:      describe(reduceDurations),
	}
}

// aggregate reduces one measurement across trials to a median and its spread.
func aggregate(values []float64) Aggregate {
	lo, hi := minMax(values)
	return Aggregate{Median: medianOf(values), Min: lo, Max: hi}
}

// medianTrial returns the index of the trial with the median wall clock, which
// is the one whose timeline the report publishes.
func medianTrial(trials []Trial) int {
	if len(trials) == 0 {
		return 0
	}
	order := make([]int, len(trials))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return trials[order[a]].WallMS < trials[order[b]].WallMS
	})
	return order[(len(order)-1)/2]
}

// buildSummary aggregates every trial and pools the task distributions.
func buildSummary(trials []Trial, results []RunResult, inputBytes int64) Summary {
	wall := []float64{}
	mapPhase := []float64{}
	stall := []float64{}
	reducePhase := []float64{}
	recordsPerSec := []float64{}
	bytesPerSec := []float64{}

	for _, t := range trials {
		wall = append(wall, t.WallMS)
		mapPhase = append(mapPhase, t.MapPhaseMS)
		stall = append(stall, t.ShuffleStallMS)
		reducePhase = append(reducePhase, t.ReducePhaseMS)

		seconds := t.WallMS / 1000
		if seconds > 0 {
			recordsPerSec = append(recordsPerSec, round(float64(t.RecordsEmitted)/seconds))
			bytesPerSec = append(bytesPerSec, round(float64(inputBytes)/seconds))
		}
	}

	mapDurations := []time.Duration{}
	reduceDurations := []time.Duration{}
	for _, r := range results {
		for _, task := range r.Trace.Tasks {
			if task.TaskType == mr.MapTask {
				mapDurations = append(mapDurations, task.Duration())
				continue
			}
			reduceDurations = append(reduceDurations, task.Duration())
		}
	}

	hash := ""
	stable := true
	for i, t := range trials {
		if i == 0 {
			hash = t.OutputHash
			continue
		}
		if t.OutputHash != hash {
			stable = false
		}
	}

	return Summary{
		WallMS:         aggregate(wall),
		MapPhaseMS:     aggregate(mapPhase),
		ShuffleStallMS: aggregate(stall),
		ReducePhaseMS:  aggregate(reducePhase),
		RecordsPerSec:  aggregate(recordsPerSec),
		BytesPerSec:    aggregate(bytesPerSec),
		MapTaskMS:      describe(mapDurations),
		ReduceTaskMS:   describe(reduceDurations),
		OutputHash:     hash,
		HashStable:     stable,
		Workers:        workerUtilization(results[medianTrial(trials)]),
	}
}

// workerUtilization reports how much of the job each worker spent running tasks.
func workerUtilization(r RunResult) []WorkerUtil {
	wall := r.Trace.End.Sub(r.Trace.Start)

	busy := map[string]time.Duration{}
	counts := map[string]int{}
	for _, task := range r.Trace.Tasks {
		busy[task.WorkerID] += task.Duration()
		counts[task.WorkerID]++
	}

	ids := make([]string, 0, len(busy))
	for id := range busy {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]WorkerUtil, 0, len(ids))
	for _, id := range ids {
		share := 0.0
		if wall > 0 {
			share = round(float64(busy[id]) / float64(wall))
		}
		out = append(out, WorkerUtil{
			WorkerID:  id,
			Tasks:     counts[id],
			BusyMS:    millis(busy[id]),
			BusyShare: share,
		})
	}
	return out
}

// timelineOf lays out one trial's tasks relative to the job start.
func timelineOf(r RunResult) []Interval {
	start := r.Trace.Start

	intervals := make([]Interval, 0, len(r.Trace.Tasks))
	for _, task := range r.Trace.Tasks {
		intervals = append(intervals, Interval{
			WorkerID: task.WorkerID,
			Phase:    phaseName(task.TaskType),
			TaskID:   task.TaskID,
			Attempt:  task.Attempt,
			StartMS:  millis(task.Start.Sub(start)),
			EndMS:    millis(task.End.Sub(start)),
		})
	}

	sort.SliceStable(intervals, func(a, b int) bool {
		return intervals[a].StartMS < intervals[b].StartMS
	})
	return intervals
}
