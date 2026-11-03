#!/usr/bin/env python3
"""Judge observed rows, lane totals and the design's paper wall budgets."""
import csv
import json
import os
from pathlib import Path
import sys

from runtime import read_json, write_json

sys.path.insert(0, str(Path(os.environ.get("JOURNEY_TREE", ".")) / "scripts/suite-checks"))
from core import Context, Unavailable, Violation


def rows(path):
    with Path(path).open() as source:
        table = [line for line in source if "\t" in line and not line.startswith("#")]
    return list(csv.DictReader(table, delimiter="\t"))


def judge(directory, design, elapsed):
    directory, design = Path(directory), Path(design)
    actual = rows(directory / "ledger.tsv")
    if not actual:
        raise ValueError("RUN-EMPTY: no beat produced a result")
    mapping = {}
    with (design / "map.tsv").open() as source:
        for row in csv.reader(source, delimiter="\t"):
            if len(row) >= 2:
                mapping.setdefault(row[1], set()).add(row[0])
    failed = False
    lanes = {}
    context = Context(os.environ.get("JOURNEY_TREE", "."), suite=design)
    declared = context.beats()
    headroom = int(context.setting("budget-headroom-pct"))
    target = int(context.setting("target-sequence-ms"))
    if headroom < 0 or target <= 0:
        raise ValueError("BUDGET-POLICY: nonnegative headroom and positive target required")
    totals = read_json(directory / "lanes.json")
    owners = [None] * len(actual)
    for lane, total in totals.items():
        if "row_start" not in total and "row_end" not in total:
            continue
        first, last = total["row_start"], total["row_end"]
        if 0 <= first == last <= len(actual) and total.get("exit", 0) != 0:
            lanes[lane] = {"beats": 0, "wall_ms": 0, "paper_ms": 0, "verdicts": {"NOT-RUN": 1}}
            failed = True
            continue
        if not (0 <= first < last <= len(actual)) or any(owners[first:last]):
            raise ValueError(f"LANE-ROW-SPAN: {lane}: {first}:{last} rows={len(actual)}")
        owners[first:last] = [lane] * (last - first)
    seen = set()
    for index, row in enumerate(actual):
        if row["row"] in seen:
            raise ValueError(f"RUN-DUPLICATE: {row['row']}")
        seen.add(row["row"])
        if row["row"] not in mapping:
            print(f"UNMAPPED {row['row']}")
            failed = True
        unknown_ids = set(row["assertions-by-id"].split(",")) - mapping.get(row["row"], set())
        if unknown_ids:
            print(f"UNMAPPED-ID {row['row']} {sorted(unknown_ids)}")
            failed = True
        failed |= row["verdict"] not in {"PASS", "known", "skip-because"}
        prefix = row["row"].split("/", 1)[0]
        lane = owners[index] or prefix
        if prefix != ("machine-lifecycle" if lane == "machine-lifecycle-final" else lane):
            raise ValueError(f"LANE-ROW-OWNER: {lane}: {row['row']}")
        cost = declared.get(row["row"], {}).get("cost_ms", "")
        if not cost.isdigit():
            print(f"BUDGET-PAPER-MISSING: {row['row']}")
            failed, cost = True, "0"
        lanes.setdefault(lane, {"beats": 0, "wall_ms": 0, "paper_ms": 0, "verdicts": {}})
        lanes[lane]["paper_ms"] += int(cost)
        lanes[lane]["beats"] += 1
        lanes[lane]["wall_ms"] += int(row["wall_ms"])
        verdicts = lanes[lane]["verdicts"]
        verdicts[row["verdict"]] = verdicts.get(row["verdict"], 0) + 1
    # Owner ruling: walked canonical rows set the budget; §11 stays a comparison.
    original = {"machine-lifecycle": 108000, "terminal-open": 12000, "chat-birth": 438000,
             "chat-reload": 550000, "chat-attention": 195000, "chat-mesh": 324000,
             "fleet-reader": 128000, "machine-restart": 51000, "chat-ending": 665000,
             "machine-lifecycle-final": 29000}
    missing = set(totals) ^ set(lanes)
    if missing:
        raise ValueError(f"LANE-EMPTY: {sorted(missing)}")
    for lane, result in lanes.items():
        wall = totals[lane]["wall_ms"]
        if lane not in original:
            raise ValueError(f"BUDGET-ABSENT: {lane}")
        budget = result["paper_ms"]
        limit = budget * (100 + headroom) // 100
        passed = result["beats"] > 0 and wall <= limit
        failed |= not passed
        result.update({"lane_wall_ms": wall, "idle_ms": max(0, wall - result["wall_ms"]),
                       "original_ms": original[lane], "limit_ms": limit})
        print(f"BUDGET {'PASS' if passed else 'FAIL'} {lane} wall_ms={wall} paper_ms={budget} original_ms={original[lane]} limit_ms={limit}")
    failed |= elapsed > target
    print(f"SEQUENCE wall_ms={elapsed} target_ms={target}")
    write_json(directory / "timing.json", {"lanes": lanes, "wall_ms": elapsed, "target_ms": target})
    return int(failed)


if __name__ == "__main__":
    try:
        if sys.argv[1] == "count":
            print(len(rows(sys.argv[2])))
            sys.exit(0)
        sys.exit(judge(sys.argv[1], sys.argv[2], int(sys.argv[3])))
    except (OSError, ValueError, KeyError, Unavailable, Violation) as error:
        print(f"RESULTS-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
