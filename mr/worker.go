package mr

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/rpc"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Map functions return a slice of KeyValue.
type KeyValue struct {
	Key   string
	Value string
}

// for sorting by key.
type ByKey []KeyValue

func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

// WorkerOptions configures a single worker run.
type WorkerOptions struct {
	ID         string  // Identifies this worker in coordinator logs and traces
	SocketPath string  // Coordinator socket, defaults to the standalone path
	SlowFactor float64 // Stretches every task by this factor, for straggler tests

	// Interceptor runs before every RPC. Returning false drops the call, as if
	// the request never reached the coordinator. Used by fault injection.
	Interceptor func(rpcName string) bool
}

// rpcAttempts is how many times a worker retries an RPC before deciding the
// coordinator is gone. Retries make a dropped message survivable; the commit
// grant is idempotent so a retried report cannot lose work.
const rpcAttempts = 4

// Temp files are named so they cannot be mistaken for finished output. A worker
// killed mid task leaves its temp file behind, and anything matching mr-out-*
// would then be read as job output by whatever consumes the results.
//
// pendingOutput is finished task output waiting for permission to publish.
// Output stays in temp files until the coordinator names one attempt the
// committer, so a losing backup never writes over a good result.
type pendingOutput struct {
	temps  []string
	finals []string
}

// publish moves each temp file into place. Rename is atomic, so a reader either
// sees the whole previous file or the whole new one.
func (p pendingOutput) publish() error {
	for i, temp := range p.temps {
		if err := os.Rename(temp, p.finals[i]); err != nil {
			return fmt.Errorf("rename %v: %w", p.finals[i], err)
		}
	}
	return nil
}

// abandon throws away the output of an attempt that lost the race.
func (p pendingOutput) abandon() {
	for _, temp := range p.temps {
		os.Remove(temp)
	}
}

// worker holds the state one worker needs for its request and report loop.
type worker struct {
	id        string
	sock      string
	slow      float64
	intercept func(string) bool
	mapf      func(string, string) []KeyValue
	reducef   func(string, []string) string
}

// mr-main/mrworker.go calls this function.
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {
	RunWorker(mapf, reducef, WorkerOptions{})
}

// RunWorker runs the request, execute and report loop until the job finishes or
// the coordinator becomes unreachable.
func RunWorker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string, opts WorkerOptions) {

	w := &worker{
		id:        opts.ID,
		sock:      opts.SocketPath,
		slow:      opts.SlowFactor,
		intercept: opts.Interceptor,
		mapf:      mapf,
		reducef:   reducef,
	}
	if w.sock == "" {
		w.sock = coordinatorSock()
	}
	w.run()
}

// run asks for work until the job is done or the coordinator goes away.
func (w *worker) run() {
	for {
		asked := time.Now()
		reply, ok := w.requestTask()
		dispatch := time.Since(asked)
		if !ok {
			// Coordinator has exited, so this worker is finished too.
			return
		}

		switch reply.TaskType {
		case MapTask, ReduceTask:
			metrics, output, err := w.execute(&reply)
			if err != nil {
				// A failed task is left unreported so the coordinator times it
				// out and hands it to another worker.
				log.Printf("task %d failed: %v", reply.TaskID, err)
				output.abandon()
				continue
			}
			metrics.Dispatch = dispatch
			w.finish(&reply, metrics, output)
		case WaitTask:
			// No tasks available, wait before asking again.
			time.Sleep(reply.backoff())
		case ExitTask:
			// Job is done, worker can exit.
			return
		default:
			log.Printf("Unknown task type received: %v", reply.TaskType)
			return
		}
	}
}

// execute runs one task, leaving its output in temp files.
func (w *worker) execute(reply *RequestTaskReply) (TaskMetrics, pendingOutput, error) {
	started := time.Now()

	var metrics TaskMetrics
	var output pendingOutput
	var err error

	if reply.TaskType == MapTask {
		metrics, output, err = doMapTask(w.mapf, reply)
	} else {
		metrics, output, err = doReduceTask(w.reducef, reply)
	}
	if err != nil {
		return metrics, output, err
	}

	// A slow factor stretches the task so one worker behaves like a machine
	// running the same work several times slower.
	if w.slow > 1 {
		time.Sleep(time.Duration(float64(time.Since(started)) * (w.slow - 1)))
	}
	return metrics, output, nil
}

// finish asks for permission to publish, then publishes and confirms. A worker
// that is told it lost discards its output and never touches the final files.
func (w *worker) finish(reply *RequestTaskReply, metrics TaskMetrics, output pendingOutput) {
	granted, ok := w.reportTask(reply)
	if !ok || !granted {
		output.abandon()
		return
	}

	if err := output.publish(); err != nil {
		log.Printf("task %d could not publish: %v", reply.TaskID, err)
		output.abandon()
		return
	}

	metrics.End = time.Now()
	w.commitTask(reply, metrics)
}

// backoff is how long to sleep after a WaitTask reply.
func (r *RequestTaskReply) backoff() time.Duration {
	if r.WaitBackoff <= 0 {
		return 10 * time.Millisecond
	}
	return r.WaitBackoff
}

// dir is the directory holding this job's intermediate and output files.
func (r *RequestTaskReply) dir() string {
	if r.WorkDir == "" {
		return "."
	}
	return r.WorkDir
}

// doMapTask runs the map function over one input file and writes one
// intermediate file per reduce partition.
func doMapTask(mapf func(string, string) []KeyValue, reply *RequestTaskReply) (TaskMetrics, pendingOutput, error) {
	m := TaskMetrics{Start: time.Now(), Backup: reply.Backup}
	var out pendingOutput

	readStart := time.Now()
	content, err := ReadSplit(reply.Split)
	if err != nil {
		return m, out, fmt.Errorf("read split: %w", err)
	}
	m.IO += time.Since(readStart)
	m.InputBytes = int64(len(content))

	computeStart := time.Now()
	kva := mapf(reply.Split.File, content)
	m.Compute = time.Since(computeStart)
	m.Records = int64(len(kva))

	writeStart := time.Now()
	nReduce := reply.NReduce
	tmpFiles := make([]*os.File, nReduce)
	counters := make([]*countingWriter, nReduce)
	encoders := make([]*json.Encoder, nReduce)

	// Temp files are created in the output directory so the rename below cannot
	// cross a filesystem boundary.
	for i := 0; i < nReduce; i++ {
		f, err := os.CreateTemp(reply.dir(), fmt.Sprintf(".mrtmp-map-%d-%d-", reply.TaskID, i))
		if err != nil {
			discard(tmpFiles)
			return m, out, fmt.Errorf("create temp file: %w", err)
		}
		tmpFiles[i] = f
		counters[i] = &countingWriter{w: f}
		encoders[i] = json.NewEncoder(counters[i])
	}

	for _, kv := range kva {
		if err := encoders[ihash(kv.Key)%nReduce].Encode(&kv); err != nil {
			discard(tmpFiles)
			return m, out, fmt.Errorf("write intermediate: %w", err)
		}
	}

	for i := 0; i < nReduce; i++ {
		if err := tmpFiles[i].Close(); err != nil {
			discard(tmpFiles)
			return m, out, fmt.Errorf("close intermediate: %w", err)
		}
		out.temps = append(out.temps, tmpFiles[i].Name())
		out.finals = append(out.finals, filepath.Join(reply.dir(), fmt.Sprintf("mr-%d-%d", reply.TaskID, i)))
		m.OutputBytes += counters[i].n
	}

	m.IO += time.Since(writeStart)
	m.End = time.Now()
	return m, out, nil
}

// doReduceTask reads every intermediate partition belonging to this reduce
// task and writes the final mr-out-N file.
func doReduceTask(reducef func(string, []string) string, reply *RequestTaskReply) (TaskMetrics, pendingOutput, error) {
	m := TaskMetrics{Start: time.Now(), Backup: reply.Backup}
	var out pendingOutput

	readStart := time.Now()
	intermediate := []KeyValue{}

	for i := 0; i < reply.NMap; i++ {
		filename := filepath.Join(reply.dir(), fmt.Sprintf("mr-%d-%d", i, reply.TaskID))
		file, err := os.Open(filename)
		if err != nil {
			// Every map task completed before this reduce task was handed out,
			// so a missing partition means data was lost, not that it can be skipped.
			return m, out, fmt.Errorf("open intermediate %v: %w", filename, err)
		}
		if info, err := file.Stat(); err == nil {
			m.InputBytes += info.Size()
		}
		dec := json.NewDecoder(file)
		for {
			var kv KeyValue
			if err := dec.Decode(&kv); err != nil {
				if err == io.EOF {
					break
				}
				file.Close()
				return m, out, fmt.Errorf("decode intermediate %v: %w", filename, err)
			}
			intermediate = append(intermediate, kv)
		}
		file.Close()
	}

	m.IO += time.Since(readStart)

	sortStart := time.Now()
	sort.Sort(ByKey(intermediate))
	m.Compute += time.Since(sortStart)

	writeStart := time.Now()
	tmpFile, err := os.CreateTemp(reply.dir(), fmt.Sprintf(".mrtmp-out-%d-", reply.TaskID))
	if err != nil {
		return m, out, fmt.Errorf("create temp output: %w", err)
	}
	tmp := []*os.File{tmpFile}
	counter := &countingWriter{w: tmpFile}

	// Group values by key and call the reduce function once per key.
	for i := 0; i < len(intermediate); {
		j := i + 1
		for j < len(intermediate) && intermediate[j].Key == intermediate[i].Key {
			j++
		}
		values := make([]string, 0, j-i)
		for k := i; k < j; k++ {
			values = append(values, intermediate[k].Value)
		}
		output := reducef(intermediate[i].Key, values)
		if _, err := fmt.Fprintf(counter, "%v %v\n", intermediate[i].Key, output); err != nil {
			discard(tmp)
			return m, out, fmt.Errorf("write output: %w", err)
		}
		m.Records++
		i = j
	}

	if err := tmpFile.Close(); err != nil {
		discard(tmp)
		return m, out, fmt.Errorf("close output: %w", err)
	}
	out.temps = []string{tmpFile.Name()}
	out.finals = []string{filepath.Join(reply.dir(), fmt.Sprintf("mr-out-%d", reply.TaskID))}

	m.OutputBytes = counter.n
	m.IO += time.Since(writeStart)
	m.End = time.Now()
	return m, out, nil
}

// discard removes the temp files left behind by a task that failed partway.
func discard(files []*os.File) {
	for _, f := range files {
		if f == nil {
			continue
		}
		f.Close()
		os.Remove(f.Name())
	}
}

// requestTask asks the coordinator for a task. The second result is false when
// the coordinator is no longer reachable.
func (w *worker) requestTask() (RequestTaskReply, bool) {
	args := RequestTaskArgs{WorkerID: w.id}
	reply := RequestTaskReply{}
	ok := w.call("Coordinator.RequestTask", &args, &reply)
	return reply, ok
}

// reportTask asks the coordinator for permission to publish this attempt's
// output. The first result says whether permission was granted, the second
// whether the coordinator answered at all.
func (w *worker) reportTask(task *RequestTaskReply) (bool, bool) {
	args := ReportTaskArgs{
		TaskID:   task.TaskID,
		TaskType: task.TaskType,
		Attempt:  task.Attempt,
		WorkerID: w.id,
	}
	reply := ReportTaskReply{}
	ok := w.call("Coordinator.ReportTask", &args, &reply)
	return reply.Commit, ok
}

// commitTask tells the coordinator the output is in place.
func (w *worker) commitTask(task *RequestTaskReply, metrics TaskMetrics) {
	metrics.WorkerID = w.id
	metrics.TaskType = task.TaskType
	metrics.TaskID = task.TaskID
	metrics.Attempt = task.Attempt

	args := CommitTaskArgs{
		TaskID:   task.TaskID,
		TaskType: task.TaskType,
		Attempt:  task.Attempt,
		WorkerID: w.id,
		Metrics:  metrics,
	}
	w.call("Coordinator.CommitTask", &args, &CommitTaskReply{})
}

// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
func (w *worker) call(rpcname string, args interface{}, reply interface{}) bool {
	for attempt := 0; attempt < rpcAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 20 * time.Millisecond)
		}
		if w.dial(rpcname, args, reply) {
			return true
		}
	}
	return false
}

// dial makes one attempt at an RPC.
func (w *worker) dial(rpcname string, args interface{}, reply interface{}) bool {
	if w.intercept != nil && !w.intercept(rpcname) {
		return false // Injected fault, the call never reaches the coordinator
	}

	c, err := rpc.DialHTTP("unix", w.sock)
	if err != nil {
		return false // Coordinator is not reachable
	}
	defer c.Close()

	if err := c.Call(rpcname, args, reply); err != nil {
		log.Printf("rpc %v failed: %v", rpcname, err)
		return false
	}
	return true
}
