# Observability

Three things a running job exposes: a structured log of every task transition, a
status endpoint for live state, and a trace file that replays a finished run.

## Structured logging

The coordinator emits one JSON object per line through `log/slog`. Every task
transition carries the task id, attempt number, worker id, phase, whether the
attempt was a speculative backup, and milliseconds since the job started.

    {"time":"...","level":"INFO","msg":"job_start","map_tasks":17,"reduce_tasks":8,
     "split_bytes":262144,"task_timeout_ms":10000,"speculation":false}
    {"time":"...","level":"INFO","msg":"task","event":"assigned","phase":"map",
     "task_id":0,"attempt":1,"worker_id":"worker-1","backup":false,"elapsed_ms":2}

Event kinds are `assigned`, `committed`, `refused` and `reaped`. A `refused`
record is a losing attempt being denied permission to publish; a `reaped` record
is an attempt dropped for running past the timeout. Phase transitions log as
`msg:"phase"` with `map_phase_end`, `reduce_phase_start` or `job_end`.

Logging is opt in. `Config.Logger` defaults to a discard handler, so a benchmark
measuring the system is not measuring its own log writes. The standalone
`mrcoordinator` attaches a stderr JSON logger.

## Live status

The coordinator serves `/status` on its own socket, and on a TCP address when
`Config.StatusAddr` is set:

    curl --unix-socket /var/tmp/824-mr-0 http://x/status
    curl http://127.0.0.1:18080/status

It answers with the live state: the current phase, per phase task counts by
state, every attempt currently running with its worker and how long it has been
going, per worker totals, speculation counters, and the coordinator's own RPC
load. Mid job it looks like this:

    {
      "phase": "reduce",
      "elapsed_ms": 123,
      "map":    {"total": 17, "idle": 0, "in_progress": 0, "completed": 17},
      "reduce": {"total": 8,  "idle": 5, "in_progress": 3, "completed": 0},
      "running_tasks": [
        {"phase":"reduce","task_id":0,"attempt":1,"worker_id":"worker-0","backup":false,"elapsed_ms":11}
      ]
    }

## Replayable traces

`trace.Build` turns a finished job's coordinator trace into a document where
every timestamp is milliseconds from the job start, so it replays without
knowing when it originally ran. Both harnesses can write one with `-trace-out`.

    go run ./bench  -trace-out traces/run.json ...
    go run ./chaos  -trace-out traces/ ...

The document holds the job shape, the worker list, every committed attempt with
its start and end offsets and bytes moved, and the full event list including the
attempts that produced nothing. That last part is what makes a failure legible
in a replay rather than just a gap.

Committed traces in `traces/`:

| file | what it shows |
|---|---|
| `clean.json` | 48 tasks, 4 workers, no faults, 509 ms |
| `kill-1-map.json` | a worker killed during the map phase, 1326 ms |
| `kill-1-reduce.json` | a worker killed during the reduce phase, 1559 ms |
| `kill-repeat-map.json` | the same worker slot killed three times |
| `speculation.json` | a backup attempt overtaking a 10x slow worker |

The recovery in `kill-1-map.json` reads directly off the events: map task 16 is
assigned to `worker-0-0` at 99.5 ms, reaped at 1105.4 ms once the 1 s timeout
expires, and reassigned to `worker-3-3` at 1113.9 ms.

`speculation.json` shows the other half of the story. Task 1's backup runs on
`worker-1` from 866 ms to 976 ms and commits, and the original attempt on the
slow worker finishes at 2420 ms and is refused permission to publish. The refusal
is in the trace, so a replay can show the losing attempt and its discarded work
rather than pretending it never ran.

## Limitations

Status is a snapshot with no history, so watching a job means polling it. The
event log lives in memory and grows with attempts, which is fine for the job
sizes here and would need bounding for long jobs. Workers log through the same
`slog` handler only when running in process; the chaos worker binary writes its
own diagnostics to stderr in plain text. There is no metrics endpoint in any
standard format, and no tracing across the RPC boundary: every timestamp in a
trace is taken on the coordinator or reported by a worker, not correlated by a
span id.
