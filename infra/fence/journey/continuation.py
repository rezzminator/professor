#!/usr/bin/env python3
"""Observe Claude continuation identity from pfm's cached transcript index."""
import os
from pathlib import Path

from chat_carry import stored
from faults import owned_path


def _launch_id(chat):
    if not isinstance(chat, dict) or not isinstance(chat.get("session_id"), str) or not chat["session_id"]:
        raise ValueError(f"CONTINUATION-CHAT: expected a carried session_id, got {chat!r}")
    return chat["session_id"]


def _row(row, index):
    if any(not isinstance(row.get(key), str) for key in ("uuid", "path", "continued_in")):
        raise ValueError(f"CONTINUATION-ROW: row {index} has invalid uuid/path/continued_in: {row!r}")
    if not row["uuid"]:
        raise ValueError(f"CONTINUATION-ROW: row {index} has an empty uuid")
    return {key: row[key] for key in ("uuid", "path", "continued_in")}


def _owned_path(path, uuid):
    if not path:
        return
    try:
        owned_path(path, preserve=True)
    except (OSError, ValueError) as error:
        raise ValueError(f"CONTINUATION-PATH: {uuid}: {path}: {error}") from error


def indexed(chat):
    """Return the cached launch/tail identity and the indexed chain proving it.

    The production rule is Store.ContinuationTail in
    ``pfm/internal/store/continuation.go``: a row is superseded only when its
    continued-in UUID is different and indexed, matching
    ``transcriptSupersededSQL`` in ``pfm/internal/store/queries.go``.
    """
    launch_id = _launch_id(chat)
    try:
        rows = stored("""WITH RECURSIVE chain(uuid,path,continued_in) AS (
            SELECT uuid,path,continued_in FROM transcripts WHERE uuid=?
            UNION SELECT t.uuid,t.path,t.continued_in FROM transcripts t
            JOIN chain c ON t.uuid=c.continued_in)
            SELECT uuid,path,continued_in FROM chain""", (launch_id,), cache=True)
    except Exception as error:
        raise ValueError(f"CONTINUATION-INDEX: {launch_id}: {error}") from error

    transcripts = {}
    for index, raw in enumerate(rows):
        row = _row(raw, index)
        if row["uuid"] in transcripts:
            raise ValueError(f"CONTINUATION-ROW: duplicate uuid {row['uuid']!r}")
        transcripts[row["uuid"]] = row

    if launch_id not in transcripts:
        return {"launch_id": launch_id, "tail_id": launch_id, "path": "", "chain": []}

    chain = []
    seen = {launch_id}
    current = launch_id
    while True:
        row = transcripts[current]
        _owned_path(row["path"], row["uuid"])
        chain.append(row)
        successor = row["continued_in"]
        if not successor or successor == current or successor not in transcripts or successor in seen:
            break
        seen.add(successor)
        current = successor

    path = transcripts[current]["path"]
    if not path:
        raise ValueError(f"CONTINUATION-PATH-EMPTY: tail {current!r} from launch {launch_id!r}")
    return {"launch_id": launch_id, "tail_id": current, "path": path, "chain": chain}


def native(chat):
    """Read the live Claude process's native sessions/<pid>.json record.

    The PID comes from the carried terminal birth, never from a directory scan;
    the process birth is checked again after the file read before its identity is
    returned.
    """
    from observation import process_snapshot, terminal, terminal_births
    from observation_process import engine_births, environment
    from runtime import read_json

    if not isinstance(chat, dict) or not isinstance(chat.get("terminal"), dict):
        raise ValueError(f"CONTINUATION-NATIVE-CHAT: expected a carried terminal, got {chat!r}")
    try:
        display = terminal(chat["terminal"])
        births = terminal_births(display)
        if not isinstance(births, list):
            raise ValueError(f"CONTINUATION-NATIVE-BIRTH: terminal has no live births: {births!r}")
        candidates = engine_births({"terminal": display, "process_births": births}, "claude")
    except (OSError, ValueError, KeyError, TypeError) as error:
        if str(error).startswith("CONTINUATION-NATIVE-"):
            raise
        raise ValueError(f"CONTINUATION-NATIVE-BIRTH: {error}") from error
    if len(candidates) != 1:
        raise ValueError(f"CONTINUATION-NATIVE-BIRTH: expected one Claude birth, got {candidates!r}")

    birth = candidates[0]
    birth_environment = birth.get("environment")
    run = os.environ.get("PFM_RUN")
    config_dir = birth_environment.get("CLAUDE_CONFIG_DIR") if isinstance(birth_environment, dict) else None
    if not run or not isinstance(config_dir, str) or not config_dir:
        raise ValueError(f"CONTINUATION-NATIVE-ENVIRONMENT: invalid carried Claude environment: {birth!r}")
    if birth_environment.get("PFM_RUN") != run:
        raise ValueError(f"CONTINUATION-NATIVE-RUN: pid={birth['pid']}")
    try:
        current_environment = environment(birth["pid"])
    except FileNotFoundError as error:
        raise ValueError(f"CONTINUATION-NATIVE-BIRTH: pid={birth['pid']} disappeared before read") from error
    except (OSError, ValueError) as error:
        raise ValueError(f"CONTINUATION-NATIVE-ENVIRONMENT: pid={birth['pid']}: {error}") from error
    if (current_environment.get("PFM_RUN") != run or
            current_environment.get("CLAUDE_CONFIG_DIR") != config_dir):
        raise ValueError(f"CONTINUATION-NATIVE-ENVIRONMENT: pid={birth['pid']} changed")

    try:
        config = owned_path(config_dir, preserve=True)
    except (OSError, ValueError) as error:
        raise ValueError(f"CONTINUATION-NATIVE-PATH: {config_dir}: {error}") from error
    path = config / "sessions" / (str(birth["pid"]) + ".json")
    try:
        path = owned_path(path, preserve=True)
    except FileNotFoundError:
        # The config directory itself is owned; a missing sessions directory is
        # the same native absence as a missing per-process record.
        path = config / "sessions" / (str(birth["pid"]) + ".json")
    except (OSError, ValueError) as error:
        raise ValueError(f"CONTINUATION-NATIVE-PATH: {path}: {error}") from error

    try:
        record = read_json(path)
    except FileNotFoundError:
        record = None
    except (OSError, ValueError) as error:
        raise ValueError(f"CONTINUATION-NATIVE-READ: {path}: {error}") from error

    fresh = process_snapshot().get(birth["pid"])
    if fresh is None or fresh.get("start") != birth.get("start"):
        raise ValueError(f"CONTINUATION-NATIVE-BIRTH: pid={birth['pid']}")
    if record is None:
        return "n/a:native-session-absent"
    if (not isinstance(record, dict) or type(record.get("pid")) is not int or
            record["pid"] != birth["pid"] or not isinstance(record.get("sessionId"), str) or
            not record["sessionId"] or not isinstance(record.get("cwd"), str) or
            not Path(record["cwd"]).is_absolute()):
        raise ValueError(f"CONTINUATION-NATIVE-SHAPE: {path}: {record!r}")
    return {"path": str(path), "pid": record["pid"], "sessionId": record["sessionId"], "cwd": record["cwd"]}
