# Scaling study

All numbers below were measured on one machine: 4 vCPU, 15 GB RAM, x86_64,
Linux 6.18, Go 1.24.7. Coordinator and workers run in one process and talk over
unix socket RPC. Input is the 8 committed Project Gutenberg books replicated 20
times under distinct document names (`data/scale.sh 20 <dir>`), 66.0 MB in 160
files, giving 160 map tasks and 16 reduce tasks. Workload is the inverted index.
Every point is the median of 5 trials; min and max are in the JSON.

Reproduce with:

    data/scale.sh 20 /tmp/corpus20
    go run ./bench -input '/tmp/corpus20/*.txt' -workload invertedindex \
        -reduce 16 -split-bytes 4194304 -trials 5 -seed 1 \
        -wait-backoff 10ms -sweep 1,2,4,8,16

## Speedup

`bench/results/scaling-backoff-10ms.json`

| workers | wall (ms) | speedup | ideal | efficiency |
|--------:|----------:|--------:|------:|-----------:|
| 1  | 6337 | 1.00 | 1  | 1.00 |
| 2  | 3994 | 1.59 | 2  | 0.79 |
| 4  | 2479 | 2.56 | 4  | 0.64 |
| 8  | 2231 | 2.84 | 8  | 0.36 |
| 16 | 2057 | 3.08 | 16 | 0.19 |

Speedup never reaches the ideal line. It diverges from the first added worker
and flattens after 4, which is the core count.

All 10 sweep points, across both backoff settings, produced the identical output
hash, so nothing in the comparison changed except the schedule.

## Where the speedup goes

Two causes, each measured rather than assumed.

**The map phase is bound by cores.** Per task work is identical at every worker
count, so any growth in task duration is contention. Map task p50 rises 31.0 ms
to 104.3 ms from 1 to 16 workers, a factor of 3.37. Dividing the worker count by
that inflation predicts the speedup closely at low worker counts: 1.59 predicted
against 1.59 observed at 2 workers, 2.66 against 2.56 at 4.

The direct test is restricting the core budget. If the map phase is CPU bound,
the knee should follow `GOMAXPROCS`, and it does
(`bench/results/scaling-gomaxprocs-*.json`, 3 trials each):

| workers | 1 core | 2 cores | 4 cores |
|--------:|-------:|--------:|--------:|
| 1 | 1.00 | 1.00 | 1.00 |
| 2 | 0.98 | 1.52 | 1.58 |
| 4 | 0.97 | 1.70 | 2.43 |
| 8 | 0.98 | 1.84 | 2.77 |

On one core, adding workers buys nothing at all. On two, the curve plateaus after
two workers. On four, after four.

**The reduce phase is bound by shuffle reads.** Reduce tasks spend 88 to 94
percent of their time in IO, because each of the 16 reduce tasks reads all 160
intermediate partitions: 2560 file opens per job. The phase floors near 465 ms
however many workers are added, and reduce task p50 inflates from 87.7 ms to
328.6 ms as 16 concurrent readers contend. This is the residual the CPU model
does not explain at 8 and 16 workers, where it over-predicts (3.62 against 2.84,
4.75 against 3.08).

**The coordinator is not the bottleneck.** Coordinator RPC handler time,
measured from handler entry so it includes waiting for the coordinator lock,
never exceeds 0.2 percent of job wall clock at any worker count. The single
global mutex and the linear scan for an idle task cost nothing at this scale.
That may not hold on a real cluster, but on this machine it is not what limits
the curve.

## Measured optimization: idle backoff

A worker with no task available sleeps before asking again. That sleep was 1
second. Workers that hit the wait during the map phase were still asleep when the
reduce phase began, so one worker did most of the early reduce work alone.

Single variable change, same binary, same input, same seed, 5 trials
(`scaling-backoff-1s.json` against `scaling-backoff-10ms.json`):

| workers | wall 1 s | wall 10 ms | gain | efficiency 1 s | efficiency 10 ms |
|--------:|---------:|-----------:|-----:|---------------:|-----------------:|
| 1  | 6287 | 6337 | 0.99x | 1.00 | 1.00 |
| 2  | 4317 | 3994 | 1.08x | 0.73 | 0.79 |
| 4  | 3293 | 2479 | 1.33x | 0.48 | 0.64 |
| 8  | 2832 | 2231 | 1.27x | 0.28 | 0.36 |
| 16 | 2822 | 2057 | 1.37x | 0.14 | 0.19 |

Output hash is unchanged at every point. The single worker case is unchanged
within noise, which is the control: a lone worker never waits, so the setting
cannot help it.

The value was chosen by measurement, not taste. At 16 workers the reduce phase
takes 1136 ms at 1 s, 460 ms at 100 ms, 449 ms at 10 ms and 446 ms at 1 ms, while
1 ms costs slightly more wall clock than 10 ms from polling overhead. 10 ms takes
essentially all of the gain without busy waiting.

## Limitations

The knee sits at 4 workers because the machine has 4 vCPU. A multi machine
measurement would move it, and would also expose coordinator RPC cost that is
invisible here. Polling with a short backoff is the wrong long term answer; the
coordinator should hold the request open until work exists. The reduce fan-in of
160 files per task is the next thing to attack, not the scheduler.
