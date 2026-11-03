#!/usr/bin/env python3
"""Hash-bound journey ledgers; never writes Git's index or object store."""
import csv
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import sys

from runtime import command, now_ms, read_json, write_json

FIELDS = ("row", "verdict", "assertions-by-id", "expected", "ops", "goldens", "wall_ms", "t+s")


def subject(tree):
    reader = ["python3", str(Path(tree) / "scripts/suite-checks/checks.py")]
    return {"tree": command(reader + ["--subject"], cwd=tree).strip(),
            "dirty": command(reader + ["--dirty"], cwd=tree).strip()}


def begin(directory, tree, root_hash, mode, order, command_line, config, before):
    directory = Path(directory)
    directory.mkdir(parents=True, exist_ok=True)
    header = {"command": command_line, "mode": mode, "lanes-before": before,
              "boot": "process", "runner_pid": os.getppid(), "order": order,
              "config": config, "root": root_hash, "start": datetime.now(timezone.utc).isoformat(),
              "start_ms": now_ms(),
              "load": list(os.getloadavg()), "log": os.environ.get("JOURNEY_LEDGER_LOG", str(directory / "activity.jsonl")),
              "artifacts": os.environ.get("JOURNEY_LEDGER_ARTIFACTS", ""), "subject_start": subject(tree),
              "subject_end": None, "status": "RUNNING"}
    write_json(directory / "header.json", header)
    with (directory / "ledger.tsv").open("w") as output:
        writer = csv.writer(output, delimiter="\t", lineterminator="\n")
        for key, value in header.items():
            if key not in {"subject_start", "subject_end"}:
                output.write(f"{key}: {value if isinstance(value, str) else json.dumps(value, sort_keys=True)}\n")
        for key, value in header["subject_start"].items():
            output.write(f"{key}: {value}\n")
        writer.writerow(FIELDS)


def finish(directory, tree, status):
    directory = Path(directory)
    header = read_json(directory / "header.json")
    header["subject_end"] = subject(tree)
    header["end"] = now_ms()
    stable = header["subject_start"] == header["subject_end"]
    header["status"] = status if stable else "TREE-CHANGED"
    write_json(directory / "header.json", header)
    with (directory / "ledger.tsv").open("a") as output:
        if status != "PASS" or not stable:
            csv.writer(output, delimiter="\t", lineterminator="\n").writerow([
                "journey/run-failed", "FAIL", "", header["status"], "", "", 0, 0])
        for key, value in header["subject_end"].items():
            output.write(f"{key}_end: {value}\n")
        output.write(f"result: {header['status']}\n")
    if not stable:
        raise ValueError("TREE-CHANGED: run does not prove the current tree")


if __name__ == "__main__":
    try:
        operation, *args = sys.argv[1:]
        if operation == "subject":
            print(json.dumps(subject(*args), sort_keys=True))
        elif operation == "begin":
            begin(*args)
        elif operation == "finish":
            finish(*args)
        else:
            raise ValueError("usage: ledger.py subject|begin|finish ...")
    except (ValueError, OSError, RuntimeError) as error:
        print(error, file=sys.stderr)
        sys.exit(2)
