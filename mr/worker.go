package mr

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/ioutil"
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
		// Ask the coordinator for a task.
		reply := requestTask()

		// Execute the task based on its type.
		switch reply.TaskType {
		case MapTask:
			doMapTask(mapf, &reply)
			reportTask(&reply)
		case ReduceTask:
			doReduceTask(reducef, &reply)
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

// doMapTask executes a map task.
func doMapTask(mapf func(string, string) []KeyValue, reply *RequestTaskReply) {
	// Read input file content.
	filename := reply.InputFile
	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("cannot open %v", filename)
	}
	content, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatalf("cannot read %v", filename)
	}
	file.Close()

	// Execute the map function.
	kva := mapf(filename, string(content))

	// Create intermediate files and encoders for each reduce partition.
	nReduce := reply.NReduce
	intermediateFiles := make([]*os.File, nReduce)
	encoders := make([]*json.Encoder, nReduce)
	for i := 0; i < nReduce; i++ {
		// Use a temporary file that will be renamed atomically.
		tmpFile, err := ioutil.TempFile("", fmt.Sprintf("mr-tmp-%d-%d", reply.TaskID, i))
		if err != nil {
			log.Fatalf("cannot create temp file for map task %d", reply.TaskID)
		}
		intermediateFiles[i] = tmpFile
		encoders[i] = json.NewEncoder(tmpFile)
	}

	// Partition the map output into intermediate files.
	for _, kv := range kva {
		reduceIndex := ihash(kv.Key) % nReduce
		err := encoders[reduceIndex].Encode(&kv)
		if err != nil {
			log.Fatalf("cannot write to intermediate file for reduce task %d", reduceIndex)
		}
	}

	// Atomically rename temporary files to their final names.
	for i := 0; i < nReduce; i++ {
		tmpName := intermediateFiles[i].Name()
		finalName := fmt.Sprintf("mr-%d-%d", reply.TaskID, i)
		intermediateFiles[i].Close()
		os.Rename(tmpName, finalName)
	}
}

// doReduceTask executes a reduce task.
func doReduceTask(reducef func(string, []string) string, reply *RequestTaskReply) {
	reduceTaskID := reply.TaskID
	nMap := reply.NMap
	intermediate := []KeyValue{}

	// Read all intermediate files for this reduce task.
	for i := 0; i < nMap; i++ {
		filename := fmt.Sprintf("mr-%d-%d", i, reduceTaskID)
		file, err := os.Open(filename)
		if err != nil {
			// A map task might have failed, so its file won't exist.
			// This is okay, just continue.
			continue
		}
		dec := json.NewDecoder(file)
		for {
			var kv KeyValue
			if err := dec.Decode(&kv); err != nil {
				break
			}
			intermediate = append(intermediate, kv)
		}
		file.Close()
	}

	// Sort intermediate key-value pairs by key.
	sort.Sort(ByKey(intermediate))

	// Create a temporary output file.
	tmpFile, err := ioutil.TempFile("", fmt.Sprintf("mr-out-tmp-%d", reduceTaskID))
	if err != nil {
		log.Fatalf("cannot create temp output file for reduce task %d", reduceTaskID)
	}

	// Group values by key and call the reduce function.
	i := 0
	for i < len(intermediate) {
		j := i + 1
		for j < len(intermediate) && intermediate[j].Key == intermediate[i].Key {
			j++
		}
		values := []string{}
		for k := i; k < j; k++ {
			values = append(values, intermediate[k].Value)
		}
		output := reducef(intermediate[i].Key, values)

		// Write the result to the output file.
		fmt.Fprintf(tmpFile, "%v %v\n", intermediate[i].Key, output)

		i = j
	}

	// Atomically rename the temporary file to the final output file.
	tmpName := tmpFile.Name()
	finalName := fmt.Sprintf("mr-out-%d", reduceTaskID)
	tmpFile.Close()
	os.Rename(tmpName, finalName)
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
	}
	reply := ReportTaskReply{}
	call("Coordinator.ReportTask", &args, &reply)
}

// example function to show how to make an RPC call to the coordinator.
// the RPC argument and reply types are defined in rpc.go.

func CallExample() {

	// declare an argument structure.
	args := ExampleArgs{}

	// fill in the argument(s).
	args.X = 99

	// declare a reply structure.
	reply := ExampleReply{}

	// send the RPC request, wait for the reply.
	call("Coordinator.Example", &args, &reply)

	// reply.Y should be 100.
	fmt.Printf("reply.Y %v\n", reply.Y)
}

// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		// log.Fatal("dialing:", err)
		return false // Return false if coordinator is not reachable
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}
