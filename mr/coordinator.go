package mr

import (
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync"
	"time"
)

// TaskState defines the possible states of a task.
type TaskState int

const (
	Idle       TaskState = iota // 0
	InProgress                  // 1
	Completed                   // 2
)

// TaskInfo holds metadata for a single task.
type TaskInfo struct {
	ID        int
	State     TaskState
	StartTime time.Time
	InputFile string // Only for Map tasks
}
type Coordinator struct {
	mu sync.Mutex // Mutex to protect shared state

	mapTasks    []TaskInfo
	reduceTasks []TaskInfo

	nReduce              int
	nMap                 int
	mapTasksCompleted    int
	reduceTasksCompleted int
	isJobDone            bool
}

// RequestTask is the RPC handler for workers asking for a task.
func (c *Coordinator) RequestTask(args *RequestTaskArgs, reply *RequestTaskReply) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// First, assign any available Map tasks
	if c.mapTasksCompleted < c.nMap {
		for i := range c.mapTasks {
			if c.mapTasks[i].State == Idle {
				// Found an idle map task, assign it
				reply.TaskType = MapTask
				reply.TaskID = c.mapTasks[i].ID
				reply.InputFile = c.mapTasks[i].InputFile
				reply.NReduce = c.nReduce

				c.mapTasks[i].State = InProgress
				c.mapTasks[i].StartTime = time.Now()
				return nil
			}
		}
		// If no idle tasks, tell worker to wait
		reply.TaskType = WaitTask
		return nil
	}

	// If all map tasks are done, assign Reduce tasks
	if c.reduceTasksCompleted < c.nReduce {
		for i := range c.reduceTasks {
			if c.reduceTasks[i].State == Idle {
				// Found an idle reduce task, assign it
				reply.TaskType = ReduceTask
				reply.TaskID = c.reduceTasks[i].ID
				reply.NMap = c.nMap

				c.reduceTasks[i].State = InProgress
				c.reduceTasks[i].StartTime = time.Now()
				return nil
			}
		}
		// If no idle tasks, tell worker to wait
		reply.TaskType = WaitTask
		return nil
	}

	// If all map and reduce tasks are done, tell worker to exit
	reply.TaskType = ExitTask
	c.isJobDone = true
	return nil
}

// ReportTask is the RPC handler for workers reporting task completion.
func (c *Coordinator) ReportTask(args *ReportTaskArgs, reply *ReportTaskReply) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	taskID := args.TaskID
	taskType := args.TaskType

	switch taskType {
	case MapTask:
		// Only mark as completed if it was still in progress (not timed out and reassigned)
		if c.mapTasks[taskID].State == InProgress {
			c.mapTasks[taskID].State = Completed
			c.mapTasksCompleted++
		}
	case ReduceTask:
		if c.reduceTasks[taskID].State == InProgress {
			c.reduceTasks[taskID].State = Completed
			c.reduceTasksCompleted++
		}
	default:
		log.Printf("Unknown task type reported: %v", taskType)
	}

	return nil
}

// an example RPC handler.
// the RPC argument and reply types are defined in rpc.go.

func (c *Coordinator) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	return nil
}

// start a thread that listens for RPCs from worker.go
func (c *Coordinator) server() {
	rpc.Register(c)
	rpc.HandleHTTP()
	sockname := coordinatorSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

// mr-main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.

func (c *Coordinator) Done() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.isJobDone
}

// checkTimeouts periodically checks for tasks that have taken too long.
func (c *Coordinator) checkTimeouts() {
	for {
		c.mu.Lock()
		if c.isJobDone {
			c.mu.Unlock()
			return
		}

		// Check for timed-out map tasks
		for i := range c.mapTasks {
			if c.mapTasks[i].State == InProgress && time.Since(c.mapTasks[i].StartTime) > 10*time.Second {
				log.Printf("Map task %d timed out. Reassigning.", i)
				c.mapTasks[i].State = Idle
			}
		}

		// Check for timed-out reduce tasks
		for i := range c.reduceTasks {
			if c.reduceTasks[i].State == InProgress && time.Since(c.reduceTasks[i].StartTime) > 10*time.Second {
				log.Printf("Reduce task %d timed out. Reassigning.", i)
				c.reduceTasks[i].State = Idle
			}
		}

		c.mu.Unlock()
		time.Sleep(2 * time.Second)
	}
}

// create a Coordinator.
// mr-main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.

func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := Coordinator{
		nReduce:     nReduce,
		nMap:        len(files),
		mapTasks:    make([]TaskInfo, len(files)),
		reduceTasks: make([]TaskInfo, nReduce),
	}

	// Initialize map tasks
	for i, file := range files {
		c.mapTasks[i] = TaskInfo{
			ID:        i,
			State:     Idle,
			InputFile: file,
		}
	}

	// Initialize reduce tasks
	for i := 0; i < nReduce; i++ {
		c.reduceTasks[i] = TaskInfo{
			ID:    i,
			State: Idle,
		}
	}

	log.Printf("Coordinator initialized with %d map tasks and %d reduce tasks.", c.nMap, c.nReduce)

	c.server()

	// Start a background goroutine to check for task timeouts
	go c.checkTimeouts()

	return &c
}
