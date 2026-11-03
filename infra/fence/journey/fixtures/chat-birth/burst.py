#!/usr/bin/env python3
"""Submit the eight owner commands concurrently and retain their actual send times."""
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import sys

from runtime import now_ms, read_json, write_json
from terminal import type_command


def launch(path):
    target = read_json(path)
    prefix = str(Path(path).with_name("cli"))
    start = now_ms()
    type_command(target, ["pfm", "chat", "new", "--engine", "claude", "--name", Path(path).parent.name], prefix)
    return {"target": target, "start": start, "end": now_ms(), "receipt": prefix}


def main(output, *targets):
    if len(targets) != 8 or len(set(targets)) != 8:
        raise ValueError("BIRTH-STORM-TARGETS: eight distinct staged shells required")
    with ThreadPoolExecutor(max_workers=8) as pool:
        sent = list(pool.map(launch, targets))
    write_json(output, {"sent": sent, "span_ms": max(row["end"] for row in sent) - min(row["start"] for row in sent)})


if __name__ == "__main__":
    try:
        main(*sys.argv[1:])
    except (OSError, ValueError, RuntimeError) as error:
        print(f"BIRTH-STORM-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
