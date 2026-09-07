package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"cs651/mr"
	"cs651/verify"
)

// JobConfig is the job every scenario runs, held fixed so only the faults differ.
type JobConfig struct {
	Inputs     []string
	Workload   string
	NReduce    int
	SplitBytes int
	WorkDir    string
	WorkerBin  string

	TaskTimeout  time.Duration
	ReapInterval time.Duration
	WaitBackoff  time.Duration
	Deadline     time.Duration
	Seed         int64
}

// Outcome is what one scenario run produced.
type Outcome struct {
	Trace      mr.JobTrace
	Kills      []Kill
	OutputHash string
	OutputKeys int
	WallClock  time.Duration
	Completed  bool
}

// Kill records one worker being killed, so recovery can be measured from it.
type Kill struct {
	WorkerID string
	At       time.Time
}

// worker is one running worker process. reap makes sure Wait is called exactly
// once, since calling it twice on the same command is a race.
type worker struct {
	id   string
	cmd  *exec.Cmd
	once sync.Once
}

// stop signals the process and reaps it, at most once.
func (w *worker) stop() bool {
	if w.cmd.Process == nil {
		return false
	}
	if err := w.cmd.Process.Kill(); err != nil {
		return false
	}
	w.once.Do(func() { go w.cmd.Wait() })
	return true
}

// run executes one scenario end to end and returns what happened.
func run(sc Scenario, cfg JobConfig) (Outcome, error) {
	if err := resetDir(cfg.WorkDir); err != nil {
		return Outcome{}, err
	}

	sockDir, err := os.MkdirTemp("", "mrchaos")
	if err != nil {
		return Outcome{}, fmt.Errorf("create socket dir: %w", err)
	}
	defer os.RemoveAll(sockDir)
	socket := filepath.Join(sockDir, "mr.sock")

	coordinator := mr.MakeCoordinatorWithConfig(cfg.Inputs, mr.Config{
		NReduce:      cfg.NReduce,
		SplitBytes:   cfg.SplitBytes,
		TaskTimeout:  cfg.TaskTimeout,
		ReapInterval: cfg.ReapInterval,
		WaitBackoff:  cfg.WaitBackoff,
		WorkDir:      cfg.WorkDir,
		SocketPath:   socket,
	})
	defer coordinator.Shutdown()

	pool := &pool{cfg: cfg, sc: sc, socket: socket}
	started := time.Now()
	for i := 0; i < sc.Workers; i++ {
		if err := pool.spawn(i); err != nil {
			return Outcome{}, err
		}
	}

	kills := injectFaults(sc, cfg, coordinator, pool)
	completed := awaitJob(coordinator, cfg.Deadline)
	wall := time.Since(started)

	pool.stopAll()

	hash, keys, err := verify.OutputHash(cfg.WorkDir)
	if err != nil {
		return Outcome{}, err
	}

	return Outcome{
		Trace:      coordinator.Trace(),
		Kills:      kills,
		OutputHash: hash,
		OutputKeys: keys,
		WallClock:  wall,
		Completed:  completed,
	}, nil
}

// pool owns the worker processes of one run.
type pool struct {
	mu      sync.Mutex
	cfg     JobConfig
	sc      Scenario
	socket  string
	workers []*worker
	nextID  int
}

// spawn starts one worker process in the given slot.
func (p *pool) spawn(slot int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	id := fmt.Sprintf("worker-%d-%d", slot, p.nextID)
	p.nextID++

	args := []string{
		"-socket", p.socket,
		"-id", id,
		"-workload", p.cfg.Workload,
	}
	if p.sc.DropRate > 0 {
		args = append(args, "-rpc-drop-rate", fmt.Sprintf("%v", p.sc.DropRate))
	}
	if p.sc.RPCDelay > 0 {
		args = append(args, "-rpc-delay", p.sc.RPCDelay.String())
	}
	args = append(args, "-fault-seed", fmt.Sprintf("%d", p.cfg.Seed+int64(slot)))

	cmd := exec.Command(p.cfg.WorkerBin, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start worker %v: %w", id, err)
	}

	p.workers = append(p.workers, &worker{id: id, cmd: cmd})
	return nil
}

// kill sends SIGKILL to the most recently started live worker in a slot.
func (p *pool) kill(index int) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if index < 0 || index >= len(p.workers) {
		return "", false
	}
	w := p.workers[index]
	if !w.stop() {
		return "", false
	}
	return w.id, true
}

// live counts the worker slots started so far.
func (p *pool) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.workers)
}

// stopAll ends every worker process still running.
func (p *pool) stopAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, w := range p.workers {
		w.stop()
	}
}

// awaitJob waits for the coordinator to report the job done, or gives up.
func awaitJob(c *mr.Coordinator, deadline time.Duration) bool {
	limit := time.Now().Add(deadline)
	for time.Now().Before(limit) {
		if c.Done() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// resetDir empties the job's working directory.
func resetDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clear work dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}
	return nil
}
