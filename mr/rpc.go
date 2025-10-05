package mr

// RPC definitions.
// remember to capitalize all names.

import "os"
import "strconv"

// example to show how to declare the arguments
// and reply for an RPC.

type ExampleArgs struct {
	X int
}

type ExampleReply struct {
	Y int
}

// TaskType defines the type of task.
// I'm using an iota for an enum-like definition.
type TaskType int

const (
	MapTask    TaskType = iota // 0
	ReduceTask                 // 1
	WaitTask                   // 2, tells the worker to wait
	ExitTask                   // 3, tells the worker the job is done
)

// RequestTaskArgs is the argument struct for the worker's request for a task.
// It can be empty as the coordinator knows which tasks are available.
type RequestTaskArgs struct {
	// WorkerID could be added here for debugging, but is not essential.
}

// RequestTaskReply is the reply struct from the coordinator for a task request.
type RequestTaskReply struct {
	TaskType  TaskType // The type of task (Map, Reduce, etc.)
	TaskID    int      // A unique ID for this task
	InputFile string   // The input file for a Map task
	NReduce   int      // The number of reduce partitions, needed by Map tasks
	NMap      int      // The number of map tasks, needed by Reduce tasks
}

// ReportTaskArgs is the argument struct for the worker to report a completed task.
type ReportTaskArgs struct {
	TaskID   int      // The ID of the completed task
	TaskType TaskType // The type of the completed task
	WorkerID string   // Optional: The ID of the worker reporting completion
}

// ReportTaskReply is the reply from the coordinator after a worker reports a task.
// It can be empty; the RPC call itself is the acknowledgment.
type ReportTaskReply struct {
}

// Cook up a unique-ish UNIX-domain socket name
// in /var/tmp, for the coordinator.
// Can't use the current directory since
// Athena AFS doesn't support UNIX-domain sockets.
func coordinatorSock() string {
	s := "/var/tmp/824-mr-"
	s += strconv.Itoa(os.Getuid())
	return s
}
