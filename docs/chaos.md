# Fault injection

`chaos/` runs a job while deliberately breaking it, then checks the output
against a clean run of the same job. Workers are real OS processes and kills are
real `SIGKILL`, so a killed worker dies wherever it happens to be, including
part way through writing its output.

The coordinator runs in the harness process, which is what makes the
measurements possible: the harness reads the coordinator's event log directly
and knows exactly when each attempt was assigned, reaped and committed.

## Fault matrix

40 map tasks, 8 reduce tasks, 4 workers, 16.5 MB corpus (8 Project Gutenberg
books replicated 5 times), task timeout 2 s, reap interval 250 ms, 3 trials per
scenario, on 4 vCPU. A scenario counts as correct only if **every** trial
finished with output identical to the clean run.

| scenario | completed | correct | wall (ms) | recovery median (ms) | work preserved |
|---|---|---|---:|---:|---:|
| clean | yes | yes | 516 | n/a | 1.00 |
| kill 1 worker during map | yes | yes | 2487 | 2100 | 0.35 |
| kill 2 workers during map | yes | yes | 2520 | 2123 | 0.33 |
| kill 1 during map, replaced | yes | yes | 2448 | 2104 | 0.33 |
| kill 1 worker during reduce | yes | yes | 2560 | 2066 | 0.92 |
| kill same slot 3 times | yes | yes | 2452 | 2109 | 0.33 |
| 10 percent of rpcs dropped | yes | yes | 578 | n/a | 1.00 |
| 30 percent of rpcs dropped | yes | yes | 1109 | n/a | 1.00 |
| every rpc delayed 25 ms | yes | yes | 1361 | n/a | 1.00 |

Every scenario finished with the correct output hash. Recovery is measured from
the moment the kill signal is sent to the moment the interrupted task is handed
to another worker. Work preserved is the share of tasks already committed when
the first kill landed; those results survive because a committed task is never
re-run.

Redundant attempts stayed at 3 per scenario, meaning a kill costs exactly the
tasks that were in flight on the dead worker and nothing else.

Dropped and delayed RPCs cost time but never correctness. A worker retries an
RPC 4 times before concluding the coordinator is gone, and the commit grant is
idempotent, so a dropped reply to a commit request does not throw away the work
the reply was about. At a 30 percent drop rate the job took 2.1 times as long as
clean and still produced identical output.

## Recovery time is a policy choice

Recovery is dominated by detection, and detection is a timeout. Sweeping the
task timeout on the same scenario (kill one worker during map, reap interval
100 ms, 3 trials):

| task timeout | recovery min | median | max | job wall clock | redundant attempts |
|---|---:|---:|---:|---:|---:|
| 250 ms | 249 | 300 | 354 | 615 | 3 |
| 500 ms | 506 | 545 | 549 | 891 | 3 |
| 1 s | 1012 | 1038 | 1049 | 1420 | 3 |
| 2 s | 2016 | 2039 | 2072 | 2428 | 3 |
| 4 s | 3978 | 4030 | 4066 | 4363 | 3 |

Recovery tracks the configured timeout almost exactly, with the reap interval
adding the small remainder. At a 250 ms timeout, killing a worker costs about
100 ms on a 516 ms job. Nothing about the system makes recovery slow; the
timeout is simply how long the coordinator waits before deciding a silent worker
is dead.

The cost of tightening it is false positives: a task that is merely slow gets
reassigned while still running. That did not happen here at any setting, because
map tasks run about 40 ms and even the 250 ms timeout is far above them. On a
workload with tasks near the timeout it would, and the redundant attempt count
is the number to watch.

## A bug this found

The first version of the harness failed `kill-1-reduce` with a wrong output
hash. Reduce tasks wrote to a temp file named by `os.CreateTemp` with the prefix
`mr-out-3-`, and a worker killed mid task left that file behind. Anything reading
the job's results by the obvious `mr-out-*` glob then picked up the debris as
though it were output.

Temp files are now named `.mrtmp-out-N-...`, which no output glob matches, and
the verifier matches `mr-out-` followed by digits and nothing else. This is the
kind of fault that only shows up when a process is killed for real at an
arbitrary point, which is the reason the harness kills processes rather than
simulating it.

`chaos/chaos_test.go` runs four scenarios under `-race` and asserts output
matches the clean run, so the regression cannot come back quietly.

## Not covered

Killing and restarting the coordinator is not implemented. The coordinator holds
all task state in memory, so surviving its own death needs that state written
somewhere durable and replayed at startup: task states, attempt numbers and the
committer of any task mid commit. Without that, a coordinator restart loses the
job, and claiming otherwise would be untested. It is the obvious next piece of
work and the design above is what it would take.

Faults are also single machine only. Real network partitions, disk failures and
clock skew are not modelled, and the RPC faults are injected in the client rather
than at the network layer.
