#!/usr/bin/env python3
"""Stage fresh-child mock inputs while the already running caller keeps its script."""
from pathlib import Path
import json
import os
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from faults import owned_path
from runtime import read_json, write_json

operation, caller_file, prefix = sys.argv[1:]
scenario = owned_path(read_json(caller_file)["scenario"])
saved = prefix + ".scenario-before.json"
gate = prefix + ".ready"
if operation == "stage":
    if Path(gate).exists():
        raise ValueError("MESH-READINESS: release gate already exists")
    original = read_json(scenario)
    write_json(saved, original)
    write_json(scenario, {**original, "ready_hold": gate, "record_dir": prefix + ".child-records"})
elif operation == "release":
    write_json(gate, {"release": True})
elif operation == "held":
    records = [json.loads(line) for line in Path(prefix + ".child-records/birth.json").read_text().splitlines()]
    held = []
    for record in records:
        if not isinstance(record["pid"], int) or record["pid"] <= 1:
            raise ValueError("MESH-READINESS: invalid native child pid")
        path = Path(os.environ["HOME"]) / ".cc/3/sessions" / (str(record["pid"]) + ".json")
        held.append({**record, "native_session_present": path.exists()})
    write_json(prefix + ".held-births.json", held)
elif operation == "restore":
    # Release even after a refused MCP call so a started child cannot remain held.
    write_json(gate, {"release": True})
    write_json(scenario, read_json(saved))
else:
    raise ValueError(f"MESH-READINESS-OPERATION: {operation}")
