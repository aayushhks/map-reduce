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

const (
	// taskTimeout is how long a task may run before it is assumed lost.
	taskTimeout = 10 * time.Second
	// timeoutCheckInterval is how often the coordinator looks for lost tasks.
	timeoutCheckInterval = 2 * time.Second
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
	Attempt   int    // Incremented on every assignment, including reassignments
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
}

// jobDone reports whether every task has finished. Callers must hold c.mu.
func (c *Coordinator) jobDone() bool {
	return c.mapTasksCompleted == c.nMap && c.reduceTasksCompleted == c.nReduce
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
				c.mapTasks[i].Attempt++
				reply.Attempt = c.mapTasks[i].Attempt
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
				c.reduceTasks[i].Attempt++
				reply.Attempt = c.reduceTasks[i].Attempt
				return nil
			}
		}
		// If no idle tasks, tell worker to wait
		reply.TaskType = WaitTask
		return nil
	}

	// If all map and reduce tasks are done, tell worker to exit
	reply.TaskType = ExitTask
	return nil
}

// ReportTask is the RPC handler for workers reporting task completion.
func (c *Coordinator) ReportTask(args *ReportTaskArgs, reply *ReportTaskReply) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var tasks []TaskInfo
	switch args.TaskType {
	case MapTask:
		tasks = c.mapTasks
	case ReduceTask:
		tasks = c.reduceTasks
	default:
		log.Printf("Unknown task type reported: %v", args.TaskType)
		return nil
	}

	if args.TaskID < 0 || args.TaskID >= len(tasks) {
		log.Printf("Task id out of range reported: %v", args.TaskID)
		return nil
	}

	// A task that timed out has already been handed to another worker, so only
	// the attempt currently in progress is allowed to complete it.
	task := &tasks[args.TaskID]
	if task.State != InProgress || task.Attempt != args.Attempt {
		return nil
	}

	task.State = Completed
	if args.TaskType == MapTask {
		c.mapTasksCompleted++
	} else {
		c.reduceTasksCompleted++
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

	return c.jobDone()
}

// checkTimeouts periodically returns tasks from crashed or stalled workers to
// the idle pool.
func (c *Coordinator) checkTimeouts() {
	for {
		c.mu.Lock()
		done := c.jobDone()
		c.mu.Unlock()
		if done {
			return
		}
		c.reapTimeouts()
		time.Sleep(timeoutCheckInterval)
	}
}

// reapTimeouts makes one pass over the in-progress tasks and reassigns any that
// have run past the timeout.
func (c *Coordinator) reapTimeouts() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.mapTasks {
		if c.mapTasks[i].State == InProgress && time.Since(c.mapTasks[i].StartTime) > taskTimeout {
			log.Printf("Map task %d timed out. Reassigning.", i)
			c.mapTasks[i].State = Idle
		}
	}

	for i := range c.reduceTasks {
		if c.reduceTasks[i].State == InProgress && time.Since(c.reduceTasks[i].StartTime) > taskTimeout {
			log.Printf("Reduce task %d timed out. Reassigning.", i)
			c.reduceTasks[i].State = Idle
		}
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
