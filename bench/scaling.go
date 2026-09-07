package main

import (
	"runtime"
	"time"

	"cs651/mr"
)

// ScalingReport is a sweep over worker counts on one fixed input.
type ScalingReport struct {
	Schema      string         `json:"schema"`
	GeneratedAt string         `json:"generated_at"`
	Environment Environment    `json:"environment"`
	Config      RunReport      `json:"config"`
	Points      []ScalingPoint `json:"points"`
}

// ScalingPoint is one worker count, with the measurements that explain where
// the observed speedup went.
type ScalingPoint struct {
	Workers int `json:"workers"`

	WallMS        Aggregate `json:"wall_ms"`
	MapPhaseMS    Aggregate `json:"map_phase_ms"`
	ReducePhaseMS Aggregate `json:"reduce_phase_ms"`

	Speedup      float64 `json:"speedup"`
	IdealSpeedup float64 `json:"ideal_speedup"`
	Efficiency   float64 `json:"efficiency"`

	// CPUSaturation is task busy time over the machine's total core time. It
	// approaches 1.0 when the job has run out of cores.
	CPUSaturation float64 `json:"cpu_saturation"`
	// MeanBusyShare is the average fraction of the job a worker spent running
	// tasks. It falls when workers sit idle rather than when cores run out.
	MeanBusyShare float64 `json:"mean_busy_share"`

	// DispatchMS is the round trip of the RPC that handed out a task, and
	// CoordinatorShare is coordinator handler time over job wall clock. Both
	// rise if the single coordinator lock becomes the bottleneck.
	DispatchMS       Distribution `json:"dispatch_ms"`
	CoordinatorMS    float64      `json:"coordinator_handler_ms"`
	CoordinatorShare float64      `json:"coordinator_handler_share"`
	RPCCalls         int64        `json:"rpc_calls"`
	WaitReplies      int64        `json:"wait_replies"`

	MapTaskMS    Distribution `json:"map_task_ms"`
	ReduceTaskMS Distribution `json:"reduce_task_ms"`

	// IO share is time spent reading inputs and writing outputs over total task
	// time. A phase that stays slow while its IO share stays high is bound by
	// the shuffle, not by cores.
	MapIOShare    float64 `json:"map_io_share"`
	ReduceIOShare float64 `json:"reduce_io_share"`
	OutputHash    string  `json:"output_hash"`
}

// buildScalingPoint summarises every trial at one worker count.
func buildScalingPoint(workers int, results []RunResult, trials []Trial) ScalingPoint {
	wall := []float64{}
	mapPhase := []float64{}
	reducePhase := []float64{}
	for _, t := range trials {
		wall = append(wall, t.WallMS)
		mapPhase = append(mapPhase, t.MapPhaseMS)
		reducePhase = append(reducePhase, t.ReducePhaseMS)
	}

	median := results[medianTrial(trials)]
	trace := median.Trace
	jobWall := trace.End.Sub(trace.Start)

	var busy, dispatchTotal time.Duration
	var mapIO, mapTotal, reduceIO, reduceTotal time.Duration
	dispatches := []time.Duration{}
	mapDurations := []time.Duration{}
	reduceDurations := []time.Duration{}
	perWorker := map[string]time.Duration{}

	for _, task := range trace.Tasks {
		busy += task.Duration()
		dispatchTotal += task.Dispatch
		dispatches = append(dispatches, task.Dispatch)
		perWorker[task.WorkerID] += task.Duration()

		if task.TaskType == mr.MapTask {
			mapDurations = append(mapDurations, task.Duration())
			mapIO += task.IO
			mapTotal += task.Duration()
			continue
		}
		reduceDurations = append(reduceDurations, task.Duration())
		reduceIO += task.IO
		reduceTotal += task.Duration()
	}

	meanBusyShare := 0.0
	if len(perWorker) > 0 && jobWall > 0 {
		total := 0.0
		for _, d := range perWorker {
			total += float64(d) / float64(jobWall)
		}
		meanBusyShare = round(total / float64(len(perWorker)))
	}

	saturation := 0.0
	if jobWall > 0 {
		saturation = round(float64(busy) / (float64(jobWall) * float64(runtime.NumCPU())))
	}

	handler := trace.RPC.RequestTime + trace.RPC.ReportTime
	handlerShare := 0.0
	if jobWall > 0 {
		handlerShare = round(float64(handler) / float64(jobWall))
	}

	return ScalingPoint{
		Workers:          workers,
		WallMS:           aggregate(wall),
		MapPhaseMS:       aggregate(mapPhase),
		ReducePhaseMS:    aggregate(reducePhase),
		IdealSpeedup:     float64(workers),
		CPUSaturation:    saturation,
		MeanBusyShare:    meanBusyShare,
		DispatchMS:       describe(dispatches),
		CoordinatorMS:    millis(handler),
		CoordinatorShare: handlerShare,
		RPCCalls:         trace.RPC.RequestCalls + trace.RPC.ReportCalls,
		WaitReplies:      trace.RPC.RequestWaits,
		MapTaskMS:        describe(mapDurations),
		ReduceTaskMS:     describe(reduceDurations),
		MapIOShare:       shareOf(mapIO, mapTotal),
		ReduceIOShare:    shareOf(reduceIO, reduceTotal),
		OutputHash:       median.OutputHash,
	}
}

// shareOf is part over whole, guarding against an empty phase.
func shareOf(part, whole time.Duration) float64 {
	if whole <= 0 {
		return 0
	}
	return round(float64(part) / float64(whole))
}

// fillSpeedups sets speedup and efficiency relative to the smallest worker
// count in the sweep, which is the sequential reference point.
func fillSpeedups(points []ScalingPoint) {
	if len(points) == 0 {
		return
	}
	base := points[0].WallMS.Median
	baseWorkers := float64(points[0].Workers)

	for i := range points {
		if points[i].WallMS.Median > 0 {
			points[i].Speedup = round(base / points[i].WallMS.Median)
		}
		points[i].IdealSpeedup = round(float64(points[i].Workers) / baseWorkers)
		if points[i].IdealSpeedup > 0 {
			points[i].Efficiency = round(points[i].Speedup / points[i].IdealSpeedup)
		}
	}
}
