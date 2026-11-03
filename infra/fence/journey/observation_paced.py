#!/usr/bin/env python3
"""Observe one product-owned paced statusline worker and its native input."""
import os
from pathlib import Path
import shlex
import stat

from observation import process_snapshot
from observation_process import environment
from runtime import read_json


def _paced_input_directory():
    # Keep this spelling aligned with statusline.CodexStatuslineCachePath and paths.TempBase.
    base = Path(os.environ["PFM_HOME"]) / "tmp" if os.environ.get("PFM_HOME") else Path(os.environ.get("TMPDIR", "/tmp"))
    path = base / ("cc-sl-paced-" + str(os.getuid()))
    from faults import owned_path
    try:
        return owned_path(path, preserve=True)
    except FileNotFoundError:
        # A front has not created TempBase yet; validate its run-owned parent and report absence.
        owned_path(path.parent, preserve=True)
        return path


def _input_records(directory):
    try:
        metadata = directory.lstat()
    except FileNotFoundError:
        return []
    except OSError as error:
        raise ValueError(f"OBSERVE-PACED-INPUT-DIRECTORY-READ: {directory}: {error}") from error
    if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISDIR(metadata.st_mode):
        raise ValueError(f"OBSERVE-PACED-INPUT-DIRECTORY-SHAPE: {directory}")
    try:
        entries = sorted(directory.iterdir(), key=lambda item: item.name)
    except OSError as error:
        raise ValueError(f"OBSERVE-PACED-INPUT-DIRECTORY-READ: {directory}: {error}") from error
    records = []
    for path in entries:
        if path.suffix != ".input":
            continue
        try:
            metadata = path.lstat()
        except OSError as error:
            raise ValueError(f"OBSERVE-PACED-INPUT-READ: {path}: {error}") from error
        if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISREG(metadata.st_mode):
            raise ValueError(f"OBSERVE-PACED-INPUT-SHAPE: {path}")
        try:
            payload = read_json(path)
        except (OSError, ValueError) as error:
            raise ValueError(f"OBSERVE-PACED-INPUT-READ: {path}: {error}") from error
        if not isinstance(payload, dict) or not isinstance(payload.get("session_id"), str) or not payload["session_id"]:
            raise ValueError(f"OBSERVE-PACED-INPUT-SESSION: {path}: {payload!r}")
        records.append((path.stem, path, payload))
    return records


def _paced_argv(value):
    try:
        words = shlex.split(value) if isinstance(value, str) else list(value)
    except (TypeError, ValueError):
        return None
    if len(words) != 4 or Path(words[0]).name != "pfm" or words[1:] != ["statusline", "--paced", words[3]]:
        return None
    return words


def paced_worker(chat):
    """Return the actual worker/input proof for one carried statusline session."""
    session = chat.get("session_id") if isinstance(chat, dict) else None
    if not isinstance(session, str) or not session:
        raise ValueError(f"OBSERVE-PACED-SESSION: {chat!r}")
    directory = _paced_input_directory()
    matching = [(key, path, payload) for key, path, payload in _input_records(directory)
                if payload["session_id"] == session]
    if not matching:
        return "n/a:statusline-paced-input-absent"
    by_key = {key: (path, payload) for key, path, payload in matching}
    candidates = []
    for row in process_snapshot().values():
        words = _paced_argv(row.get("argv", ""))
        if words is None or words[3] not in by_key:
            continue
        try:
            actual = environment(row["pid"])
        except FileNotFoundError:
            continue
        except PermissionError as error:
            raise ValueError(f"OBSERVE-PACED-WORKER-ENV-READ: pid={row['pid']}: {error}") from error
        if actual.get("PFM_RUN") != os.environ.get("PFM_RUN"):
            continue
        path, payload = by_key[words[3]]
        candidates.append(({**row, "key": words[3], "input_path": str(path), "input": payload,
                            "environment": actual}, row["pid"], row["start"]))
    if len(candidates) > 1:
        raise ValueError(f"OBSERVE-PACED-WORKER-AMBIGUOUS: {[row[0] for row in candidates]!r}")
    if not candidates:
        return "n/a:statusline-paced-worker-absent"
    result, pid, birth = candidates[0]
    fresh = process_snapshot().get(pid)
    if fresh is None:
        return "n/a:statusline-paced-worker-absent"
    if fresh.get("start") != birth:
        raise ValueError(f"OBSERVE-PACED-WORKER-BIRTH: pid={pid}")
    return result
