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
	ID         string // Identifies this worker in coordinator logs and traces
	SocketPath string // Coordinator socket, defaults to the standalone path
}

// worker holds the state one worker needs for its request and report loop.
type worker struct {
	id      string
	sock    string
	mapf    func(string, string) []KeyValue
	reducef func(string, []string) string
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

	w := &worker{id: opts.ID, sock: opts.SocketPath, mapf: mapf, reducef: reducef}
	if w.sock == "" {
		w.sock = coordinatorSock()
	}
	w.run()
}

// run asks for work until the job is done or the coordinator goes away.
func (w *worker) run() {
	for {
		reply, ok := w.requestTask()
		if !ok {
			// Coordinator has exited, so this worker is finished too.
			return
		}

		switch reply.TaskType {
		case MapTask:
			// A failed task is left unreported so the coordinator times it out
			// and hands it to another worker.
			if err := doMapTask(w.mapf, &reply); err != nil {
				log.Printf("map task %d failed: %v", reply.TaskID, err)
				continue
			}
			w.reportTask(&reply)
		case ReduceTask:
			if err := doReduceTask(w.reducef, &reply); err != nil {
				log.Printf("reduce task %d failed: %v", reply.TaskID, err)
				continue
			}
			w.reportTask(&reply)
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

// backoff is how long to sleep after a WaitTask reply.
func (r *RequestTaskReply) backoff() time.Duration {
	if r.WaitBackoff <= 0 {
		return time.Second
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
func doMapTask(mapf func(string, string) []KeyValue, reply *RequestTaskReply) error {
	content, err := ReadSplit(reply.Split)
	if err != nil {
		return fmt.Errorf("read split: %w", err)
	}

	kva := mapf(reply.Split.File, content)

	nReduce := reply.NReduce
	tmpFiles := make([]*os.File, nReduce)
	encoders := make([]*json.Encoder, nReduce)

	// Temp files are created in the output directory so the rename below cannot
	// cross a filesystem boundary.
	for i := 0; i < nReduce; i++ {
		f, err := os.CreateTemp(reply.dir(), fmt.Sprintf("mr-map-%d-%d-", reply.TaskID, i))
		if err != nil {
			discard(tmpFiles)
			return fmt.Errorf("create temp file: %w", err)
		}
		tmpFiles[i] = f
		encoders[i] = json.NewEncoder(f)
	}

	for _, kv := range kva {
		if err := encoders[ihash(kv.Key)%nReduce].Encode(&kv); err != nil {
			discard(tmpFiles)
			return fmt.Errorf("write intermediate: %w", err)
		}
	}

	for i := 0; i < nReduce; i++ {
		if err := tmpFiles[i].Close(); err != nil {
			discard(tmpFiles)
			return fmt.Errorf("close intermediate: %w", err)
		}
		if err := os.Rename(tmpFiles[i].Name(), filepath.Join(reply.dir(), fmt.Sprintf("mr-%d-%d", reply.TaskID, i))); err != nil {
			discard(tmpFiles)
			return fmt.Errorf("rename intermediate: %w", err)
		}
	}
	return nil
}

// doReduceTask reads every intermediate partition belonging to this reduce
// task and writes the final mr-out-N file.
func doReduceTask(reducef func(string, []string) string, reply *RequestTaskReply) error {
	intermediate := []KeyValue{}

	for i := 0; i < reply.NMap; i++ {
		filename := filepath.Join(reply.dir(), fmt.Sprintf("mr-%d-%d", i, reply.TaskID))
		file, err := os.Open(filename)
		if err != nil {
			// Every map task completed before this reduce task was handed out,
			// so a missing partition means data was lost, not that it can be skipped.
			return fmt.Errorf("open intermediate %v: %w", filename, err)
		}
		dec := json.NewDecoder(file)
		for {
			var kv KeyValue
			if err := dec.Decode(&kv); err != nil {
				if err == io.EOF {
					break
				}
				file.Close()
				return fmt.Errorf("decode intermediate %v: %w", filename, err)
			}
			intermediate = append(intermediate, kv)
		}
		file.Close()
	}

	sort.Sort(ByKey(intermediate))

	tmpFile, err := os.CreateTemp(reply.dir(), fmt.Sprintf("mr-out-%d-", reply.TaskID))
	if err != nil {
		return fmt.Errorf("create temp output: %w", err)
	}
	tmp := []*os.File{tmpFile}

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
		if _, err := fmt.Fprintf(tmpFile, "%v %v\n", intermediate[i].Key, output); err != nil {
			discard(tmp)
			return fmt.Errorf("write output: %w", err)
		}
		i = j
	}

	if err := tmpFile.Close(); err != nil {
		discard(tmp)
		return fmt.Errorf("close output: %w", err)
	}
	if err := os.Rename(tmpFile.Name(), filepath.Join(reply.dir(), fmt.Sprintf("mr-out-%d", reply.TaskID))); err != nil {
		discard(tmp)
		return fmt.Errorf("rename output: %w", err)
	}
	return nil
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

// reportTask tells the coordinator a task finished.
func (w *worker) reportTask(task *RequestTaskReply) {
	args := ReportTaskArgs{
		TaskID:   task.TaskID,
		TaskType: task.TaskType,
		Attempt:  task.Attempt,
		WorkerID: w.id,
	}
	w.call("Coordinator.ReportTask", &args, &ReportTaskReply{})
}

// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
func (w *worker) call(rpcname string, args interface{}, reply interface{}) bool {
	c, err := rpc.DialHTTP("unix", w.sock)
	if err != nil {
		return false // Return false if coordinator is not reachable
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	log.Printf("rpc %v failed: %v", rpcname, err)
	return false
}
