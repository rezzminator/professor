#!/usr/bin/env python3
"""The beat library's only activity-log reader, including rename rotation."""
from contextlib import contextmanager
import fcntl
import json
import re
from pathlib import Path
import sys

from runtime import read_json, write_json


@contextmanager
def locked(path):
    # The product rotator holds this same lock through rename and append.
    with Path(str(path) + ".lock").open("rb") as lock:
        fcntl.flock(lock, fcntl.LOCK_SH)
        yield


def cursor(path):
    with locked(path):
        stat = Path(path).stat()
        return {"device": stat.st_dev, "inode": stat.st_ino, "offset": stat.st_size}


def records(path, start):
    path = Path(path)
    with locked(path):
        generations = {0: path}
        for candidate in path.parent.glob(path.name + ".*"):
            suffix = candidate.name.removeprefix(path.name + ".")
            if suffix.isdecimal():
                generations[int(suffix)] = candidate
        origin = None
        for generation, candidate in generations.items():
            stat = candidate.stat()
            if (stat.st_dev, stat.st_ino) == (start["device"], start["inode"]):
                origin = generation
                break
        if origin is None:
            raise ValueError("LOG-ABSENT: starting log inode was removed")
        source = generations[origin]
        if source.stat().st_size < start["offset"]:
            raise ValueError("LOG-TRUNCATED: starting log offset no longer exists")
        with source.open("rb") as stream:
            stream.seek(start["offset"])
            content = stream.read()
        for generation in reversed(range(origin)):
            if generation not in generations:
                raise ValueError(f"LOG-ROTATION-ABSENT: generation {generation}")
            content += generations[generation].read_bytes()
    return [json.loads(line) for line in content.splitlines() if line.strip()]


def sweep(path, start, run, beat, expected):
    whole_slice = records(path, start)
    errors = [row for row in whole_slice if str(row.get("level", "")).lower() == "error"]
    unexpected = [row for row in errors if not any(re.search(pattern, json.dumps(row)) for pattern in expected)]
    attributed = [row for row in whole_slice if row.get("run") == run and row.get("beat") == beat and row.get("action")]
    missing = [pattern for pattern in expected if not any(re.search(pattern, json.dumps(row)) for row in errors)]
    return {"records": whole_slice, "attributed": attributed,
            "ops": sorted({row["action"] for row in attributed}),
            "unexpected": unexpected, "missing_expected": missing}


def export(directory, destination):
    directory = Path(directory)
    results = [read_json(path) for path in directory.glob("beats/*/*/result.json")]
    if not results:
        raise ValueError("LOG-ABSENT: no beat results to export")
    records = []
    missing = []
    for result in sorted(results, key=lambda row: row["start"]):
        path = directory / "beats" / result["row"] / "activity.json"
        if not path.exists():
            missing.append(result["row"])
            continue
        records.extend(read_json(path)["records"])
    Path(destination).write_text("".join(json.dumps(row, sort_keys=True) + "\n" for row in records))
    if missing:
        raise ValueError(f"LOG-ABSENT: {missing}")


def observed_action(bundle, action, door):
    matches = {row["action"] + "@" + row["door"] for row in read_json(bundle)["activity"]["attributed"]
               if row.get("action") == action and row.get("door") == door}
    return ",".join(sorted(matches))


if __name__ == "__main__":
    try:
        operation, *args = sys.argv[1:]
        if operation == "cursor":
            write_json(args[1], cursor(args[0]))
        elif operation == "slice":
            path, start, run, beat, patterns, output = args
            write_json(output, sweep(path, read_json(start), run, beat, Path(patterns).read_text().splitlines()))
        elif operation == "export":
            export(*args)
        elif operation == "observed-action":
            print(observed_action(*args))
        else:
            raise ValueError("usage: activity.py cursor|slice|export ...")
    except (OSError, ValueError) as error:
        print(f"LOG-ABSENT: {error}", file=sys.stderr)
        sys.exit(2)
