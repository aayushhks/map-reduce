# Speculative execution

A task can run slowly for reasons the coordinator cannot see. Speculation runs a
second attempt for a task that is taking far longer than its peers, and keeps
whichever attempt finishes first.

## How it works

The coordinator hands out a backup only when a worker asks for work and no idle
task is left, so backups appear naturally at the tail of a phase and never
compete with real work. A task qualifies when its elapsed time exceeds
`SpeculationThreshold` times the median duration of completed tasks in the same
phase, it has exactly one attempt running, and the asking worker is not the one
already running it. The median needs `SpeculationMinSamples` completed tasks
before it is trusted. Defaults: threshold 2.0, minimum 5 samples, speculation
off.

## The commit race

Two attempts finish with two copies of the same output. Publishing both would be
a duplicate write, and a losing attempt that finished late could overwrite good
output after the fact.

The coordinator arbitrates instead. A finished attempt leaves its output in temp
files and asks permission to publish. Under the coordinator lock exactly one
attempt per task is named the committer; every other attempt is told it lost and
deletes its temp files without touching the final names. The committer renames
its temp files into place, which is atomic, then confirms. Only that confirmation
marks the task complete, so a worker that dies between permission and rename
leaves the task unfinished and it is handed out again rather than being counted
as done with output that was never written.

This holds regardless of whether the map and reduce functions are deterministic,
which is the part that relying on identical bytes would not give.

`TestOnlyOneAttemptMayCommit` runs two live attempts and asserts the second is
refused. `TestCommitterThatNeverConfirmsIsRerun` asserts the stalled committer
case. Both run under `-race`.

## Measured effect

One worker is made 5 times slower than the others (`-slow-workers 1
-slow-factor 5`), which stretches every task it runs. 4 workers, 16 reduce
tasks, 66.0 MB corpus of 8 concatenated Project Gutenberg books, 5 trials,
seed 1, on 4 vCPU. Only the split size changes between rows, so map task count
is the single variable.

| map tasks | speculation off | speculation on | gain |
|---:|---|---|---:|
| 8   | 2036 ms [1486-2768] | 1055 ms [802-1310]  | **1.93x** |
| 36  | 1784 ms [1399-1850] | 1344 ms [1223-1531] | 1.33x |
| 130 | 2793 ms [2674-2927] | 2717 ms [2693-2860] | 1.03x |

Output hash is identical in every pair.

The headline number is the 8 task row: job completion under a single 5x slow
worker improved from 2036 ms to 1055 ms, and the trial ranges do not overlap.
Five of six backups launched went on to win.

The other two rows matter more than the headline. The gain shrinks as tasks get
smaller and disappears by 130 tasks, where the ranges overlap completely and
1.03x is noise. Fine grained tasks with dynamic assignment already handle a slow
worker: it simply claims fewer tasks. Speculation only pays when a single task is
large enough that losing it to a slow worker holds up the whole phase. Reported
without the granularity sweep, the 1.93x would suggest a general result that the
measurements do not support.

## What it costs when nothing is wrong

Same configuration, 8 map tasks, no slow worker:

| | wall clock | backups |
|---|---|---:|
| speculation off | 909 ms [841-1113] | 0 |
| speculation on  | 996 ms [783-1228] | 2 launched, 0 won |

The median is 9.6 percent worse with speculation on, but the ranges overlap
almost entirely, so this is a hint of a cost rather than a measurement of one.
Two backups were still launched on a healthy cluster, which is the real finding:
a threshold of twice the phase median fires on ordinary variance near the tail.
That is why the default is off.

## Limitations

The slow worker is simulated by stretching task duration, not by real CPU or IO
contention, so it models a uniformly slow machine rather than one that is slow
only at certain operations. At most one backup per task is allowed. The straggler
scan walks every task in the phase while holding the coordinator lock, which is
fine at 130 tasks and would not be at a million. Worker utilization in the
reports is built from committed tasks, so a worker whose attempts all lost the
commit race does not appear in that list at all.
