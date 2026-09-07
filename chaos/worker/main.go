package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"cs651/mr"
	"cs651/workload"
)

// A worker process the chaos harness can start, stall and kill for real.
func main() {
	var (
		socket    = flag.String("socket", "", "coordinator socket path")
		id        = flag.String("id", "worker", "worker id reported to the coordinator")
		app       = flag.String("workload", "invertedindex", "workload to run")
		slow      = flag.Float64("slow-factor", 1, "stretch every task by this factor")
		dropRate  = flag.Float64("rpc-drop-rate", 0, "fraction of outgoing rpcs to drop")
		rpcDelay  = flag.Duration("rpc-delay", 0, "delay added before every rpc")
		faultSeed = flag.Int64("fault-seed", 1, "seed for rpc fault injection")
	)
	flag.Parse()

	if *socket == "" {
		fmt.Fprintln(os.Stderr, "worker: -socket is required")
		os.Exit(2)
	}

	job, err := workload.Lookup(*app)
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(2)
	}

	mr.RunWorker(job.Map, job.Reduce, mr.WorkerOptions{
		ID:          *id,
		SocketPath:  *socket,
		SlowFactor:  *slow,
		Interceptor: interceptor(*dropRate, *rpcDelay, *faultSeed),
	})
}

// interceptor builds the rpc fault hook, or nil when no faults are configured.
func interceptor(dropRate float64, delay time.Duration, seed int64) func(string) bool {
	if dropRate <= 0 && delay <= 0 {
		return nil
	}

	random := rand.New(rand.NewSource(seed))
	return func(string) bool {
		if delay > 0 {
			time.Sleep(delay)
		}
		return random.Float64() >= dropRate
	}
}
