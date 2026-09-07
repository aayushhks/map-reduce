#!/usr/bin/env python3
"""Rebuild the data blob embedded in site/index.html from committed results.

Every number on the report page comes from this blob, so the page can only show
what the benchmark and chaos harnesses actually recorded.
"""

import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def load(*parts):
    with open(os.path.join(ROOT, *parts)) as f:
        return json.load(f)


def sweep(name):
    d = load("bench", "results", name)
    base = d["points"][0]["wall_ms"]["median"]
    return {
        "config": d["config"],
        "points": [{
            "workers": p["workers"], "wall": p["wall_ms"],
            "map_phase": p["map_phase_ms"]["median"],
            "reduce_phase": p["reduce_phase_ms"]["median"],
            "speedup": round(base / p["wall_ms"]["median"], 3),
            "efficiency": round(base / p["wall_ms"]["median"] / p["workers"], 3),
            "map_p50": p["map_task_ms"]["p50_ms"], "reduce_p50": p["reduce_task_ms"]["p50_ms"],
            "coord_share": p["coordinator_handler_share"], "map_io": p["map_io_share"],
            "reduce_io": p["reduce_io_share"], "hash": p["output_hash"][:16],
        } for p in d["points"]],
    }


def spec(name):
    d = load("bench", "results", name)
    s = d["summary"]
    return {
        "map_tasks": d["config"]["map_tasks"], "wall": s["wall_ms"],
        "launched": sum(t["backups_launched"] for t in d["trials"]),
        "won": sum(t["backups_won"] for t in d["trials"]),
        "hash": s["output_hash"][:16], "trials": d["config"]["trials"],
        "slow_workers": d["config"]["slow_workers"], "slow_factor": d["config"]["slow_factor"],
    }


def slim_trace(path):
    d = load(*path)
    return {
        "label": d["label"], "job": d["job"], "workers": d["workers"],
        "tasks": [{"w": t["worker_id"], "p": t["phase"], "id": t["task_id"], "a": t["attempt"],
                   "b": t["backup"], "s": round(t["start_ms"], 1), "e": round(t["end_ms"], 1)}
                  for t in d["tasks"]],
        "events": [{"k": e["kind"], "p": e["phase"], "id": e["task_id"], "a": e["attempt"],
                    "w": e["worker_id"], "b": e["backup"], "t": round(e["at_ms"], 1)}
                   for e in d["events"] if e["kind"] in ("reaped", "refused", "assigned")],
    }


def build():
    baseline = load("bench", "results", "baseline-invertedindex-4w.json")
    wc = load("bench", "results", "baseline-wordcount-4w.json")
    chaos = load("bench", "results", "chaos-matrix.json")

    data = {
        "baseline": {
            "config": baseline["config"], "env": baseline["environment"],
            "summary": {k: baseline["summary"][k] for k in (
                "wall_ms", "map_phase_ms", "shuffle_stall_ms", "reduce_phase_ms",
                "records_per_sec", "input_bytes_per_sec", "map_task_ms_pooled",
                "reduce_task_ms_pooled", "output_hash", "output_hash_stable_across_trials")},
            "trial0": {k: baseline["trials"][0][k] for k in (
                "bytes_shuffled", "intermediate_files", "output_keys")},
        },
        "baseline_wc": {
            "wall_ms": wc["summary"]["wall_ms"], "records_per_sec": wc["summary"]["records_per_sec"],
            "bytes_shuffled": wc["trials"][0]["bytes_shuffled"], "hash": wc["summary"]["output_hash"],
        },
        "scaling_10ms": sweep("scaling-backoff-10ms.json"),
        "scaling_1s": sweep("scaling-backoff-1s.json"),
        "cores": {},
        "spec": {},
        "chaos": {
            "config": chaos["config"],
            "results": [{k: r[k] for k in (
                "scenario", "description", "trials", "completed", "output_matches_clean_run",
                "wall_ms", "recovery_min_ms", "recovery_median_ms", "recovery_max_ms",
                "redundant_attempts", "work_preserved_share", "tasks_total")} for r in chaos["results"]],
        },
        "timeouts": [],
    }

    for n in (1, 2, 4):
        d = load("bench", "results", "scaling-gomaxprocs-%d.json" % n)
        base = d["points"][0]["wall_ms"]["median"]
        data["cores"][str(n)] = [{"workers": p["workers"],
                                  "speedup": round(base / p["wall_ms"]["median"], 3),
                                  "map_p50": p["map_task_ms"]["p50_ms"]} for p in d["points"]]

    for key, name in (("coarse_off", "spec-off-coarse8"), ("coarse_on", "spec-on-coarse8"),
                      ("mid_off", "spec-off-mid32"), ("mid_on", "spec-on-mid32"),
                      ("fine_off", "spec-off-fine136"), ("fine_on", "spec-on-fine136"),
                      ("healthy_off", "spec-off-coarse8-healthy"),
                      ("healthy_on", "spec-on-coarse8-healthy")):
        data["spec"][key] = spec(name + ".json")

    for t in ("250ms", "500ms", "1s", "2s", "4s"):
        r = load("bench", "results", "chaos-timeout-%s.json" % t)["results"][0]
        data["timeouts"].append({"timeout": t, "min": r["recovery_min_ms"], "med": r["recovery_median_ms"],
                                 "max": r["recovery_max_ms"], "wall": r["wall_ms"],
                                 "redundant": r["redundant_attempts"],
                                 "correct": r["output_matches_clean_run"]})

    return {"d": data, "traces": {"kill": slim_trace(("traces", "kill-1-map.json")),
                                  "spec": slim_trace(("traces", "speculation.json"))}}


def main():
    page = os.path.join(ROOT, "site", "index.html")
    with open(page) as f:
        html = f.read()

    blob = json.dumps(build(), separators=(",", ":"))
    pattern = r'(<script id="data" type="application/json">).*?(</script>)'
    updated, count = re.subn(pattern, lambda m: m.group(1) + blob + m.group(2), html, flags=re.S)
    if count != 1:
        sys.exit("could not find the data block in site/index.html")

    with open(page, "w") as f:
        f.write(updated)
    print("embedded %d bytes of measurement data" % len(blob))


if __name__ == "__main__":
    main()
