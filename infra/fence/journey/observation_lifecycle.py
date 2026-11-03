#!/usr/bin/env python3
"""Evidence required before a retained chat's live reports become inapplicable."""
import csv
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import socket
import stat
import sys

from observation_transition import ProjectionMismatch, birth_presence, carried, reload_ready

from observation_hostless import hostless, hostless_scope


def engine_id(name):
    return {"claude": "cc", "codex": "cx", "opencode": "ox"}.get(name, name)


def expectations(chat):
    values = {"lifecycle": "live", "terminal": "live", "visibility": "visible", **chat.get("expect", {})}
    choices = {"lifecycle": {"live", "transient", "ended", "archived"}, "terminal": {"live", "shell", "absent"},
               "visibility": {"visible", "killed", "hidden", "archived"}}
    if any(key not in choices or value not in choices[key] for key, value in values.items()):
        raise ValueError(f"OBSERVE-LIFECYCLE-EXPECTATION: {values!r}")
    if values["lifecycle"] in {"live", "transient"} and values["terminal"] != "live":
        raise ValueError(f"OBSERVE-LIFECYCLE-EXPECTATION: {values!r}")
    if values["lifecycle"] == "transient" and values["visibility"] != "visible":
        raise ValueError(f"OBSERVE-LIFECYCLE-EXPECTATION: {values!r}")
    return values


def absent(path):
    try:
        Path(path).lstat()
    except FileNotFoundError:
        return True
    return False


def dead_births(chat, processes):
    prior = chat.get("expected_prior_births")
    if not isinstance(prior, list) or not prior:
        raise ValueError(f"OBSERVE-DEATH-PROOF-ABSENT: {chat['session_id']}")
    result = []
    for birth in prior:
        if type(birth.get("pid")) is not int or birth["pid"] <= 0 or not isinstance(birth.get("start"), str) or not birth["start"]:
            raise ValueError(f"OBSERVE-DEATH-BIRTH-SHAPE: {birth!r}")
        current = processes.get(birth["pid"])
        if current and current["start"] == birth["start"]:
            raise ValueError(f"OBSERVE-ENGINE-STILL-LIVE: {birth!r}")
        result.append({"prior": birth, "current": current or "n/a:process-absent"})
    return result


def archive(chat):
    directory = Path(os.environ["HOME"]) / ".claude-archive"
    manifest = directory / "MANIFEST.tsv"
    with manifest.open(newline="") as source:
        records = list(csv.DictReader(source, delimiter="\t"))
    matches = [row for row in records if row.get("uuid") == chat["session_id"]]
    if len(matches) != 1:
        raise ValueError(f"OBSERVE-ARCHIVE-ROW: {chat['session_id']}: {matches!r}")
    row = matches[0]
    if row["engine"] != engine_id(chat["engine"]):
        raise ValueError(f"OBSERVE-ARCHIVE-ENGINE: {row!r}")
    target = Path(row["archived_path"])
    target.resolve().relative_to(directory.resolve())
    content, metadata = target.read_bytes(), target.stat()
    if (len(content) != int(row["bytes"]) or not absent(row["orig_path"])
            or (row.get("state") and row.get("file_id") != f"{metadata.st_dev}:{metadata.st_ino}")):
        raise ValueError(f"OBSERVE-ARCHIVE-PUBLICATION: {row!r}")
    return {"manifest": str(manifest), "row": row, "sha256": hashlib.sha256(content).hexdigest(),
            "bytes": len(content), "original": "absent"}


def visibility(chat, selected, storage, expected):
    mode = expected["visibility"]
    if mode == "archived":
        if selected:
            raise ValueError(f"OBSERVE-ARCHIVED-PICKER-ROW: {chat['session_id']}")
        return archive(chat)
    if mode == "hidden":
        if not storage["hidden"]:
            raise ValueError(f"OBSERVE-HIDDEN-MARKER-ABSENT: {chat['session_id']}")
    elif not selected:
        raise ValueError(f"OBSERVE-PICKER-ROW-ABSENT: {chat['name']} {chat['session_id']}")
    elif mode == "killed" and any(row["killed"] != "true" for row in selected):
        raise ValueError(f"OBSERVE-KILLED-MARKER-ABSENT: {chat['session_id']}")
    elif mode == "visible" and (storage["hidden"] or any(row["killed"] == "true" for row in selected)):
        raise ValueError(f"OBSERVE-VISIBILITY-MISMATCH: {chat['session_id']}")
    return {"expected": mode, "rows": selected, "hidden": storage["hidden"]}


def terminal_absent(target, report, request):
    if target["backend"] == "herdr":
        rows = request(os.environ["HERDR_SOCKET_PATH"], "pane.list", {})["result"]["panes"]
        present = any(row["pane_id"] == target["pane"] for row in rows)
    elif target["backend"] == "tmux":
        present = False
        if not absent(target["socket"]):
            try:
                panes = report(["tmux", "-S", target["socket"], "list-panes", "-a", "-F", "#{pane_id}"]).splitlines()
                present = target["pane"] in panes
            except RuntimeError:
                if not stat.S_ISSOCK(Path(target["socket"]).stat().st_mode):
                    raise
                with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as endpoint:
                    endpoint.settimeout(1)
                    try:
                        endpoint.connect(target["socket"])
                    except ConnectionRefusedError:
                        pass  # A stale socket with no listener cannot own a tmux pane.
                    else:
                        raise
    else:
        raise ValueError(f"OBSERVE-TERMINAL: unsupported backend {target['backend']!r}")
    if present:
        raise ValueError(f"OBSERVE-TERMINAL-STILL-PRESENT: {target!r}")
    return "n/a:ended-" + target["backend"] + "-pane"


def ended(chat, expected, status, storage, processes, terminal, report, request, reports_available):
    if reports_available and status.get("state") != "dead":
        raise ValueError(f"OBSERVE-END-STATE: {chat['session_id']}: {status!r}")
    if any(seat["state"] == "live" for seat in storage["seat"]):
        raise ValueError(f"OBSERVE-SEAT-STILL-LIVE: {chat['session_id']}")
    proof = {"births": dead_births(chat, processes)}
    if expected["lifecycle"] == "archived":
        proof["archive"] = archive(chat)
    if expected["terminal"] == "absent":
        display = terminal_absent(chat["terminal"], report, request)
    else:
        display = terminal(chat["terminal"])
        births = [*display["births"], *display.get("shield", {}).get("births", [])] if isinstance(display.get("shield"), dict) else display["births"]
        if expected["terminal"] == "shell" and not any(Path(shlex.split(birth["chain"][0]["argv"])[0]).name in {"sh", "bash", "zsh", "fish"} for birth in births):
            raise ValueError(f"OBSERVE-SHELL-ABSENT: {chat['session_id']}")
    return display, proof


def transient(chat, status, storage, processes):
    if not isinstance(status, dict) or status.get("state") not in {"dead", "lost"}:
        raise ValueError(f"OBSERVE-TRANSIENT-STATE: {chat['session_id']}: {status!r}")
    seats = [seat for seat in storage["seat"] if seat.get("seat_id") == chat.get("seat_id")]
    if chat.get("seat_id") and (len(seats) != 1 or seats[0].get("state") not in {"dead", "lost"}):
        raise ValueError(f"OBSERVE-TRANSIENT-SEAT: {chat['session_id']}: {seats!r}")
    return {"births": dead_births(chat, processes)}


def machine(carry, report):
    if carry.get("phase") != "uninstalled":
        return {"phase": "installed"}, True
    root = Path(os.environ["HOME"])
    binary = root / ".local/bin/pfm"
    managed = root / ".local/share/pfm/install"
    files = {}
    if not absent(managed):
        for path in sorted(managed.rglob("*")):
            if path.is_symlink():
                files[str(path.relative_to(managed))] = "link:" + os.readlink(path)
            elif path.is_file():
                files[str(path.relative_to(managed))] = hashlib.sha256(path.read_bytes()).hexdigest()
    prefix = os.environ["PFM_UNIT_PREFIX"]
    if sys.platform == "darwin":
        units = report(["launchctl", "list"])
        units = [line for line in units.splitlines() if line.split() and line.split()[-1].startswith("com.professor." + prefix + ".")]
    else:
        units = report(["systemctl", "--user", "list-units", "--all", "--no-legend", "--plain", prefix + "-*"]).splitlines()
    available = not absent(binary)
    return {"phase": "uninstalled", "binary": str(binary) if available else "absent", "managed_files": files, "units": units}, available


def transcript(chat):
    path = Path(chat["transcript_path"])
    path.resolve().relative_to(Path(os.environ["JOURNEY_ROOT"]).resolve())
    content = path.read_bytes()
    return {"path": str(path), "bytes": len(content), "sha256": hashlib.sha256(content).hexdigest(),
            "public_read": "n/a:uninstalled"}


def reload_records(chat):
    if not chat.get("reload_records"):
        return "n/a:not-touched"
    directory = Path(os.environ["PFM_SID_DIR"])
    if chat["terminal"]["backend"] == "herdr":
        keys = ["reload-herdr-" + identity for identity in [chat["seat_id"], *chat.get("prior_seat_ids", [])]]
    else:
        keys = ["reload-" + Path(chat["terminal"]["socket"]).name]
    if any(not re.fullmatch(r"[a-zA-Z0-9_.-]+", key) for key in keys):
        raise ValueError(f"OBSERVE-RELOAD-IDENTITY: {keys!r}")
    result = {}
    for key in dict.fromkeys(keys):
        for suffix in (".pid", ".log"):
            path = directory / (key + suffix)
            if absent(path):
                result[path.name] = "absent"
            else:
                path.resolve().relative_to(directory.resolve())
                result[path.name] = path.read_text()
    return result


def _actual_engine_transcript(chat, label):
    from observation import report
    payload = json.loads(report(["pfm", "chat", "read", chat["session_id"], "--tail", "1", "--json"]))
    if not isinstance(payload, dict):
        raise ValueError(f"OBSERVE-{label}-TRANSCRIPT-PATH: {chat['session_id']}: {payload!r}")
    transcript = payload.get("path")
    if not isinstance(transcript, str) or not transcript:
        raise ValueError(f"OBSERVE-{label}-TRANSCRIPT-PATH: {chat['session_id']}: {payload!r}")
    transcript_path = Path(transcript)
    if transcript_path.suffix != ".jsonl":
        raise ValueError(f"OBSERVE-{label}-TRANSCRIPT-PATH: {transcript!r}")
    try:
        metadata = transcript_path.lstat()
    except OSError as error:
        raise ValueError(f"OBSERVE-{label}-TRANSCRIPT-READ: {transcript}: {error}") from error
    if not stat.S_ISREG(metadata.st_mode):
        raise ValueError(f"OBSERVE-{label}-TRANSCRIPT-SHAPE: {transcript!r}")
    return transcript_path


def read_actual_sidechains(chat):
    """Read Claude's engine-owned subagent files and their recorded agent IDs."""
    if chat.get("engine") not in {"claude", "cc"}:
        return "n/a:non-claude-sidechains"
    transcript_path = _actual_engine_transcript(chat, "SIDECHAIN")
    directory = transcript_path.with_suffix("") / "subagents"
    try:
        directory_metadata = directory.lstat()
    except FileNotFoundError:
        entries = []
    except OSError as error:
        raise ValueError(f"OBSERVE-SIDECHAIN-DIRECTORY: {directory}: {error}") from error
    else:
        if stat.S_ISLNK(directory_metadata.st_mode) or not stat.S_ISDIR(directory_metadata.st_mode):
            raise ValueError(f"OBSERVE-SIDECHAIN-DIRECTORY: {directory}")
        try:
            entries = sorted(directory.iterdir(), key=lambda item: item.name)
        except OSError as error:
            raise ValueError(f"OBSERVE-SIDECHAIN-DIRECTORY: {directory}: {error}") from error
    sidechains = []
    for path in entries:
        if path.suffix != ".jsonl":
            continue
        try:
            metadata = path.lstat()
            if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISREG(metadata.st_mode):
                raise ValueError(f"OBSERVE-SIDECHAIN-SHAPE: {path}")
            records = path.read_text().splitlines()
        except OSError as error:
            raise ValueError(f"OBSERVE-SIDECHAIN-READ: {path}: {error}") from error
        agent_ids = set()
        for line_number, line in enumerate(records, 1):
            try:
                record = json.loads(line)
            except ValueError as error:
                raise ValueError(f"OBSERVE-SIDECHAIN-JSON: {path}:{line_number}: {error}") from error
            if (
                not isinstance(record, dict)
                or record.get("isSidechain") is not True
                or not isinstance(record.get("agentId"), str)
                or not record["agentId"]
            ):
                raise ValueError(f"OBSERVE-SIDECHAIN-IDENTITY: {path}:{line_number}: {record!r}")
            agent_ids.add(record["agentId"])
        if len(agent_ids) != 1 or path.stem != next(iter(agent_ids), ""):
            raise ValueError(f"OBSERVE-SIDECHAIN-IDENTITY: {path}: {sorted(agent_ids)!r}")
        sidechains.append({"path": str(path), "agent_id": next(iter(agent_ids)), "records": len(records)})
    return {"path": str(transcript_path), "subagents": sidechains}


def _codex_home(chat, transcript_path):
    environment = chat.get("engine_environment") or chat.get("environment")
    configured = environment.get("CODEX_HOME") if isinstance(environment, dict) else None
    if not configured:
        recipe = chat.get("stored", {}).get("recipe", {})
        values = recipe.get("env", []) if isinstance(recipe, dict) else []
        configured = next((value.partition("=")[2] for value in values
                           if isinstance(value, str) and value.startswith("CODEX_HOME=")), "")
    if configured:
        home = Path(configured)
    else:
        parents = transcript_path.parents
        if len(parents) < 5 or parents[3].name != "sessions":
            raise ValueError(f"OBSERVE-CODEX-TRANSCRIPT-SCOPE: {transcript_path}")
        home = parents[4]
    try:
        transcript_path.relative_to(home / "sessions")
    except ValueError as error:
        raise ValueError(f"OBSERVE-CODEX-TRANSCRIPT-SCOPE: {transcript_path}") from error
    return home


def _codex_session_meta(path, label):
    malformed = 0
    try:
        with path.open() as source:
            for _ in range(20):
                line = source.readline()
                if not line:
                    break
                try:
                    record = json.loads(line)
                except ValueError:
                    malformed += 1
                    continue
                if not isinstance(record, dict) or record.get("type") != "session_meta":
                    continue
                payload = record.get("payload", {})
                if not isinstance(payload, dict):
                    raise ValueError(f"OBSERVE-CODEX-{label}-IDENTITY: {path}")
                return record, payload
    except OSError as error:
        raise ValueError(f"OBSERVE-CODEX-{label}-READ: {path}: {error}") from error
    raise ValueError(f"OBSERVE-CODEX-{label}-IDENTITY: {path}: session_meta absent; malformed={malformed}")


def _codex_field(record, payload, key):
    value = record.get(key)
    if isinstance(value, str) and value:
        return value
    value = payload.get(key)
    return value if isinstance(value, str) and value else ""


def _codex_child_parent(record, payload):
    source = record.get("source")
    if not isinstance(source, dict):
        source = payload.get("source")
    nested = source.get("subagent") if isinstance(source, dict) else None
    spawn = nested.get("thread_spawn") if isinstance(nested, dict) else None
    nested_parent = spawn.get("parent_thread_id") if isinstance(spawn, dict) else ""
    direct_parent = _codex_field(record, payload, "parent_thread_id")
    if not isinstance(nested_parent, str) or not nested_parent or direct_parent != nested_parent:
        return ""
    return direct_parent


def read_actual_codex_session(chat):
    transcript_path = _actual_engine_transcript(chat, "CODEX")
    _codex_home(chat, transcript_path)
    parent_record, parent_payload = _codex_session_meta(transcript_path, "PARENT")
    parent_id = _codex_field(parent_record, parent_payload, "id")
    if parent_id != chat.get("session_id"):
        raise ValueError(f"OBSERVE-CODEX-PARENT-IDENTITY: {transcript_path}: {parent_id!r}")
    return {"path": str(transcript_path), "id": parent_id}


def read_actual_codex_children(chat):
    """Read child Codex rollouts from the carried parent's native session directory."""
    if chat.get("engine") not in {"codex", "cx"}:
        return "n/a:non-codex-children"
    native = read_actual_codex_session(chat)
    transcript_path, parent_id = Path(native["path"]), native["id"]
    children = []
    try:
        entries = sorted(transcript_path.parent.iterdir(), key=lambda item: item.name)
    except OSError as error:
        raise ValueError(f"OBSERVE-CODEX-DIRECTORY: {transcript_path.parent}: {error}") from error
    for path in entries:
        if path == transcript_path or not path.name.startswith("rollout-") or path.suffix != ".jsonl":
            continue
        try:
            metadata = path.lstat()
        except OSError as error:
            raise ValueError(f"OBSERVE-CODEX-CHILD-READ: {path}: {error}") from error
        if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISREG(metadata.st_mode):
            raise ValueError(f"OBSERVE-CODEX-CHILD-SHAPE: {path}")
        child_record, child_payload = _codex_session_meta(path, "CHILD")
        child_id = _codex_field(child_record, child_payload, "id")
        child_parent = _codex_child_parent(child_record, child_payload)
        if _codex_field(child_record, child_payload, "thread_source") != "subagent":
            continue
        if not child_id or not child_parent:
            raise ValueError(f"OBSERVE-CODEX-CHILD-IDENTITY: {path}")
        if child_parent != parent_id:
            continue
        if not path.stem.endswith("-" + child_id):
            raise ValueError(f"OBSERVE-CODEX-CHILD-PATH: {path}: {child_id!r}")
        children.append({"id": child_id, "path": str(path), "parent_thread_id": child_parent})
    return {"path": str(transcript_path), "children": children}
