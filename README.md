# Distributed MapReduce in Go

A coordinator/worker MapReduce, instrumented until every claim about it is a
number with a stated configuration.

**[Report page with the plots and a replayable job trace](https://claude.ai/code/artifact/d6ddba67-39d1-447d-b750-bd353dfbdd0d)**

Every figure below is rendered from JSON committed under `bench/results/`.
Nothing here is projected, extrapolated or rounded in my favour, and the
measurements that came out unimpressive are reported next to the ones that
did not.

## Results

All measurements: 4 vCPU, 15 GB RAM, x86_64, Linux 6.18, Go 1.24.7, single
machine. Input is the eight committed Project Gutenberg books replicated to
66.0 MB by `data/scale.sh`, giving 160 map and 16 reduce tasks. Every run
digests its output (all lines from every partition, sorted, SHA-256) so a fast
run that is silently wrong cannot pass as a good one.

### Scaling

`bench/results/scaling-backoff-10ms.json` — median of 5 trials, seed 1.

| workers | wall | speedup | ideal | efficiency |
|--------:|-----:|--------:|------:|-----------:|
| 1  | 6337 ms | 1.00× | 1×  | 1.00 |
| 2  | 3994 ms | 1.59× | 2×  | 0.79 |
| 4  | 2479 ms | 2.56× | 4×  | 0.64 |
| 8  | 2231 ms | 2.84× | 8×  | 0.36 |
| 16 | 2057 ms | 3.08× | 16× | 0.19 |

Speedup diverges from the ideal line at the first added worker and flattens
after four, which is the core count. Three candidate causes, each measured:

**The map phase is bound by cores.** Per-task work is identical at every worker
count, so growth in task duration is contention: map task p50 rises 31.0 ms to
104.3 ms between 1 and 16 workers. Dividing worker count by that inflation
predicts the measured speedup at low counts — 1.59 predicted against 1.59
observed at two workers, 2.66 against 2.56 at four. The direct test is
restricting `GOMAXPROCS`, and the knee follows it
(`scaling-gomaxprocs-*.json`):

| workers | 1 core | 2 cores | 4 cores |
|--------:|-------:|--------:|--------:|
| 2 | 0.98× | 1.52× | 1.58× |
| 4 | 0.97× | 1.70× | 2.43× |
| 8 | 0.98× | 1.84× | 2.77× |

On one core, adding workers buys nothing at all.

**The reduce phase is bound by shuffle reads.** Reduce tasks spend 88–94% of
their time in I/O, because each of the 16 reads all 160 intermediate
partitions: 2,560 file opens per job. The phase floors near 465 ms however many
workers are added. That residual is what the CPU model fails to explain at 8
and 16 workers, where it over-predicts 3.62 against 2.84 and 4.75 against 3.08.

**Coordinator contention is not the cause.** Handler time is measured from
handler entry, so it includes waiting for the global coordinator lock, and it
never exceeds 0.2% of job wall clock at any worker count.

One optimization, A/B'd on the same binary with a single variable changed:
lowering the idle backoff from 1 s to 10 ms improved job completion at 4
workers from 3293 ms to 2479 ms (**1.33×**), with the output hash identical at
every point. The 1-worker row is the control — a lone worker never waits, so
the setting cannot help it, and it doesn't.

### Straggler mitigation

One worker made 5× slower; a task running past twice its phase median gets a
backup attempt, first completion wins. 4 workers, 66.0 MB, median of 5 trials.

| map tasks | speculation off | speculation on | gain |
|----------:|----------------:|---------------:|-----:|
| 8   | 2036 ms [1486–2768] | 1055 ms [802–1310]  | **1.93×** |
| 36  | 1784 ms [1399–1850] | 1344 ms [1223–1531] | 1.33× |
| 130 | 2793 ms [2674–2927] | 2717 ms [2693–2860] | 1.03× |

The gain vanishes as tasks shrink. At 130 tasks the trial ranges overlap
completely and 1.03× is noise: fine-grained dynamic assignment already absorbs
a slow worker, because it simply claims fewer tasks. Reported without the
granularity sweep, the 1.93× would imply a general result the measurements do
not support. On a healthy cluster speculation still launched 2 backups and cost
9.6% at the median, which is why it is off by default.

### Fault injection

Real worker processes, real `SIGKILL`. A scenario counts as correct only if
**every** trial finished with output identical to a clean run.
`bench/results/chaos-matrix.json` — 40 map / 8 reduce tasks, 4 workers,
2 s task timeout, 3 trials each.

| scenario | completed | correct | recovery median | work preserved |
|---|---|---|---:|---:|
| clean | yes | yes | — | 1.00 |
| kill 1 worker during map | yes | yes | 2100 ms | 0.35 |
| kill 2 workers during map | yes | yes | 2123 ms | 0.33 |
| kill 1 during map, replaced | yes | yes | 2104 ms | 0.33 |
| kill 1 worker during reduce | yes | yes | 2066 ms | 0.92 |
| kill same slot 3 times | yes | yes | 2109 ms | 0.33 |
| 10% of RPCs dropped | yes | yes | — | 1.00 |
| 30% of RPCs dropped | yes | yes | — | 1.00 |
| every RPC delayed 25 ms | yes | yes | — | 1.00 |

Recovery is detection, and detection is a timeout — sweeping it moves recovery
almost exactly with it. At a 250 ms task timeout, killing a worker costs about
100 ms on a 516 ms job (`chaos-timeout-*.json`). Nothing makes recovery
inherently slow; the timeout is how long the coordinator waits before deciding
a silent worker is dead.

### Baseline throughput

`bench/results/baseline-invertedindex-4w.json` — 4 workers, 9 trials.
Wall clock 3137 ms [3045–3229], 304,000 records/sec, 50.3 MB shuffled across
2,560 intermediate files. Map task p50 45.9 ms, p95 69.1, p99 81.2. Output
hash `337772595124868e…` identical across all 9 trials.

## Methodology

**Why the corpus is replicated.** On the raw 3.2 MB corpus a job finishes in
~140 ms and wall clock swings 46% between trials, which is too noisy to build a
scaling curve on. `data/scale.sh` copies the same books under distinct document
names, so per-record work is unchanged and only volume grows. At 66 MB the
trial spread is 5.9%.

**Why timing is taken in-process.** The standalone coordinator polls for
completion once a second and sleeps a second before exiting, so every
externally timed job lands on a one-second grid — 1, 2, 4 and 8 workers all
measured 3010 ms ±4 ms. That measures sleep constants, not the system. The
harness drives the coordinator in-process and reads phase boundaries from
inside it.

**Correctness of every measured run.** The output hash is invariant under split
size (verified across 8, 17 and 55 map tasks), under worker count, and across
trials. Golden hashes are pinned in `bench/golden_test.go`, and the word-count
digest was produced independently by `mrsequential` — the distributed pipeline
agreeing with the single-process reference is asserted in code, not just
claimed.

**Reproducing it:**

```sh
data/scale.sh 20 /tmp/corpus20
go run ./bench -input '/tmp/corpus20/*.txt' -workload invertedindex \
    -reduce 16 -split-bytes 4194304 -trials 5 -seed 1 \
    -wait-backoff 10ms -sweep 1,2,4,8,16

go build -o /tmp/chaosworker ./chaos/worker
go run ./chaos -input '/tmp/corpus5/*.txt' -worker /tmp/chaosworker \
    -reduce 8 -split-bytes 1048576 -trials 3 -task-timeout 2s
```

Full write-ups: [`docs/scaling.md`](docs/scaling.md),
[`docs/speculation.md`](docs/speculation.md), [`docs/chaos.md`](docs/chaos.md),
[`docs/observability.md`](docs/observability.md).

## How it works

The coordinator hands out map and reduce tasks over unix-socket RPC and
re-issues any task whose attempt runs past a timeout. The part worth reading is
what happens when two attempts of one task finish at once, or when a worker
dies holding one.

**Commit arbitration.** A finished attempt leaves its output in temp files and
asks permission to publish. Under the coordinator lock exactly one attempt per
task is named committer; every other attempt is told it lost and deletes its
temps without touching the final names. The committer renames its files into
place — atomic — then confirms. Only that confirmation marks the task complete,
so a worker that dies between permission and rename leaves the task to be
re-run rather than counted done with output that was never written. This holds
whether or not the map and reduce functions are deterministic, which relying on
identical bytes would not give.

**Input splitting.** Map tasks come from byte ranges, not whole files, so task
granularity is independent of how the input happens to be filed. A split that
starts mid-file leaves its first partial line to the split before it and runs
on to the end of the line crossing its last byte, so the splits of a file
reassemble into exactly that file.

**Observability.** One JSON record per task transition through `log/slog`
(task id, attempt, worker, phase, backup flag, elapsed); a `/status` endpoint
serving live job state; and `trace.Build`, which exports a finished job with
every timestamp relative to job start, so it replays. Committed traces are in
`traces/`.

| package | what it is |
|---|---|
| `mr/` | the framework: coordinator, worker, RPC, splitting, tracing |
| `workload/` | map and reduce implementations, split-safe |
| `bench/` | the benchmark harness and its JSON reports |
| `chaos/` | fault injection, with a worker binary it kills for real |
| `verify/` | output hashing, the basis of every correctness verdict |
| `trace/` | replayable trace export |
| `site/` | the report page, rendered from `bench/results/` |

## Running it

```sh
cd mrapps && go build -buildmode=plugin wc.go && cd ..
cd mr-main
go run mrcoordinator.go ../data/pg-*.txt &
go run mrworker.go ../mrapps/wc.so
```

`go test -race` covers `mr`, `bench`, `trace`, `verify`, `workload` and
`chaos`; CI runs build, vet, gofmt and the full raced suite on every push,
including the fault scenarios.

## Limitations

The weakest part of this work is that it is all one machine with four cores,
and that shapes nearly every number above.

- The knee sits at four workers because the box has four vCPU. A multi-machine
  measurement would move it, and would expose coordinator RPC cost that is
  invisible here at 0.2% of wall clock. The 16-worker point is oversubscription,
  not distribution.
- **Coordinator restart is not implemented**, so the chaos matrix cannot
  include it. All task state is in memory; surviving a restart needs task
  states, attempt numbers and the committer of any in-flight commit written
  durably and replayed at startup.
- Polling with a short backoff is the wrong long-term answer — the coordinator
  should hold a request open until work exists.
- The reduce fan-in, not the scheduler, is the next thing to attack: 160
  partitions read per reduce task is what floors the reduce phase.
- The straggler scan walks every task in the phase under the coordinator lock.
  Fine at 160 tasks, wrong at a million.
- The slow worker is simulated by stretching task duration, so it models a
  uniformly slow machine rather than one slow only at certain operations. RPC
  faults are injected in the client, not at the network layer.
