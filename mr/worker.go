package mr

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/rpc"
	"os"
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

// mr-main/mrworker.go calls this function.
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	// The worker runs in a loop, asking for tasks and executing them.
	for {
		reply := requestTask()

		switch reply.TaskType {
		case MapTask:
			// A failed task is left unreported so the coordinator times it out
			// and hands it to another worker.
			if err := doMapTask(mapf, &reply); err != nil {
				log.Printf("map task %d failed: %v", reply.TaskID, err)
				continue
			}
			reportTask(&reply)
		case ReduceTask:
			if err := doReduceTask(reducef, &reply); err != nil {
				log.Printf("reduce task %d failed: %v", reply.TaskID, err)
				continue
			}
			reportTask(&reply)
		case WaitTask:
			// No tasks available, wait before asking again.
			time.Sleep(1 * time.Second)
		case ExitTask:
			// Job is done, worker can exit.
			return
		default:
			log.Fatalf("Unknown task type received: %v", reply.TaskType)
		}
	}
}

// doMapTask runs the map function over one input file and writes one
// intermediate file per reduce partition.
func doMapTask(mapf func(string, string) []KeyValue, reply *RequestTaskReply) error {
	content, err := os.ReadFile(reply.InputFile)
	if err != nil {
		return fmt.Errorf("read input %v: %w", reply.InputFile, err)
	}

	kva := mapf(reply.InputFile, string(content))

	nReduce := reply.NReduce
	tmpFiles := make([]*os.File, nReduce)
	encoders := make([]*json.Encoder, nReduce)

	// Temp files are created in the output directory so the rename below cannot
	// cross a filesystem boundary.
	for i := 0; i < nReduce; i++ {
		f, err := os.CreateTemp(".", fmt.Sprintf("mr-map-%d-%d-", reply.TaskID, i))
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
		if err := os.Rename(tmpFiles[i].Name(), fmt.Sprintf("mr-%d-%d", reply.TaskID, i)); err != nil {
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
		filename := fmt.Sprintf("mr-%d-%d", i, reply.TaskID)
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

	tmpFile, err := os.CreateTemp(".", fmt.Sprintf("mr-out-%d-", reply.TaskID))
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
	if err := os.Rename(tmpFile.Name(), fmt.Sprintf("mr-out-%d", reply.TaskID)); err != nil {
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

// requestTask calls the coordinator to request a task.
func requestTask() RequestTaskReply {
	args := RequestTaskArgs{}
	reply := RequestTaskReply{}
	ok := call("Coordinator.RequestTask", &args, &reply)
	if !ok {
		// Coordinator has likely exited, so the worker should too.
		os.Exit(0)
	}
	return reply
}

// reportTask calls the coordinator to report task completion.
func reportTask(task *RequestTaskReply) {
	args := ReportTaskArgs{
		TaskID:   task.TaskID,
		TaskType: task.TaskType,
		Attempt:  task.Attempt,
	}
	reply := ReportTaskReply{}
	call("Coordinator.ReportTask", &args, &reply)
}

// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
func call(rpcname string, args interface{}, reply interface{}) bool {
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
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
