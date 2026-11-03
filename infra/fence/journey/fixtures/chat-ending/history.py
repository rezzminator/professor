#!/usr/bin/env python3
"""Read branch and recovery evidence from public receipts and native files."""
import hashlib
import json
import os
from pathlib import Path
import re
import sys

from observation import report
from observation_process import engine_births
from projection import selected
from runtime import read_json, write_json


def branch_session(receipt):
    output = Path(receipt + ".out").read_text()
    sockets = re.findall(r" into detached socket (\S+) — waiting in pfm ls;", output)
    if len(sockets) != 1:
        raise ValueError(f"ENDING-BRANCH-RECEIPT: {output!r}")
    status = json.loads(report(["pfm", "chat", "status", sockets[0], "--json"], allowed_codes=(0, 3)))
    return status["session_id"] if status["state"] in {"idle", "working", "blocked"} else None


def transcript(chat):
    result = json.loads(report(["pfm", "chat", "read", chat["session_id"], "--tail", "1000", "--json"]))
    path = Path(result["path"])
    path.resolve().relative_to(Path(os.environ["JOURNEY_ROOT"]).resolve())
    return {"path": str(path), "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
            "entries": [{"role": row["role"], "text": row["text"]} for row in result["entries"]]}


def branch_checks(before, after, parent, child, parent_history, child_history):
    rows = []
    def equal(field, expected, actual):
        rows.append({"field": field, "expected": expected, "actual": actual})
    old, current = selected(before, parent), selected(after, parent)
    equal("parent-session", parent["session_id"], current["status"]["session_id"])
    equal("parent-process", engine_births(old, parent["engine"]), engine_births(current, parent["engine"]))
    equal("foreign", before["foreign"], after["foreign"])
    for field in ("engine", "account"):
        equal("branch-" + field, parent[field], child[field])
    equal("branch-parent", parent["session_id"], child["parent"])
    equal("branch-parent-key", parent["key"], child["parent_key"])
    equal("distinct-session", True, parent["session_id"] != child["session_id"])
    child_births = engine_births(selected(after, child), child["engine"])
    equal("branch-process-count", 1, len(child_births))
    config = "CODEX_HOME" if child["engine"] == "codex" else "CLAUDE_CONFIG_DIR"
    equal("branch-account-home", [row["environment"][config] for row in engine_births(old, parent["engine"])],
          [row["environment"][config] for row in child_births])
    equal("transcript-prefix", parent_history["entries"], child_history["entries"][:len(parent_history["entries"])])
    equal("parent-bytes", parent_history["sha256"], hashlib.sha256(Path(parent_history["path"]).read_bytes()).hexdigest())
    return rows


def recovery(receipt):
    output = Path(receipt + ".out").read_text()
    matches = re.findall(r"^recovered thread (\S+)$", output, re.MULTILINE)
    files = [Path(line.strip()) for line in output.splitlines() if line.startswith("  /")]
    if len(matches) != 1 or sorted(path.name for path in files) != ["brief.md", "compaction-memory.md", "transcript.md"]:
        raise ValueError(f"ENDING-RECOVERY-RECEIPT: {output!r}")
    result = {}
    for path in files:
        path.resolve().relative_to(Path(os.environ["JOURNEY_ROOT"]).resolve())
        result[path.name] = {"bytes": path.stat().st_size, "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}
    return {"session_id": matches[0], "files": result}


def recovery_birth(before, after, chat):
    prior = {(row["pid"], row["start"]) for row in engine_births(selected(before, chat), chat["engine"])}
    current = engine_births(selected(after, chat), chat["engine"])
    return bool(current) and all((row["pid"], row["start"]) not in prior for row in current)


if __name__ == "__main__":
    try:
        mode, *args = sys.argv[1:]
        if mode == "branch-session":
            value = branch_session(args[0])
            if value is None:
                sys.exit(1)
            print(value)
        elif mode == "transcript":
            write_json(args[1], transcript(read_json(args[0])))
        elif mode == "branch-checks":
            write_json(args[-1], branch_checks(*(read_json(path) for path in args[:-1])))
        elif mode == "recovery":
            write_json(args[1], recovery(args[0]))
        elif mode == "recovery-birth":
            print(json.dumps(recovery_birth(*(read_json(path) for path in args))))
        else:
            raise ValueError(f"ENDING-HISTORY-OPERATION: {mode}")
    except (OSError, ValueError, KeyError, RuntimeError, TypeError) as error:
        print(f"ENDING-HISTORY: {error}", file=sys.stderr)
        sys.exit(2)
