#!/usr/bin/env python3
"""Capture MERGE §3 through public reports, terminal reads and read-only SQL."""
import csv
import io
import json
import os
from pathlib import Path
import re
import sqlite3

import herdr
import observation_lifecycle as lifecycle
from observation_lifecycle import birth_presence, carried, reload_ready, read_actual_codex_session, read_actual_codex_children, read_actual_sidechains
import observation_picker
from observation_bundle import bundle, foreign_projection
from observation_transition import ProcessExited
from observation_tree import process_tree
from managed_unit import observe as managed_unit
from observation_process import (account_stats_cache, environment, expected_environment_denial,
                                 is_indicator_process, process_cwd, process_state, process_metrics, reporter_activity)
from runtime import command, read_json


def report(arguments, **options):
    environment = {**os.environ, "PFM_BEAT": ""}
    environment.pop("PFM_BEAT_FILE", None)
    return command(arguments, env=environment, **options)


def process_snapshot():
    # ps's portable named columns preserve the process start and complete command.
    rows = report(["ps", "-eo", "pid=,ppid=,stat=,lstart=,args="]).splitlines()
    processes = {}
    for line in rows:
        fields = line.split(None, 8)
        if len(fields) != 9:
            raise ValueError(f"OBSERVE-PROCESS-SHAPE: {line!r}")
        pid, parent = int(fields[0]), int(fields[1])
        processes[pid] = {"pid": pid, "parent": parent, "start": " ".join(fields[3:8]), "argv": fields[8],
                          "stat": fields[2], "state": fields[2][0]}
    return processes


def indicator_daemon(snapshot=None, pane=None):
    """Select one owned indicator process, then read its actual kernel counters."""
    processes = process_snapshot() if snapshot is None else snapshot
    identity = {key: os.environ[key] for key in ("HERDR_SOCKET_PATH", "PFM_RUN")
                if os.environ.get(key)}
    matches = []
    for row in processes.values():
        if not is_indicator_process(row.get("argv", "")):
            continue
        if not identity:
            continue
        try:
            observed = environment(row["pid"])
        except FileNotFoundError:
            continue
        except PermissionError as error:
            raise ValueError(f"OBSERVE-INDICATOR-ENV-PERMISSION: pid={row['pid']}: {error}") from error
        except ValueError as error:
            raise ValueError(f"OBSERVE-INDICATOR-ENV-SHAPE: pid={row['pid']}: {error}") from error
        if all(observed.get(key) == value for key, value in identity.items()) and (pane is None or observed.get("HERDR_PANE_ID") == pane):
            matches.append(row)
    if not matches:
        return "n/a:indicator-daemon-absent"
    if len(matches) != 1:
        raise ValueError(f"OBSERVE-INDICATOR-DAEMON-MULTIPLE: {[row.get('pid') for row in matches]!r}")
    row = matches[0]
    fresh = process_snapshot().get(row["pid"])
    if fresh is None:
        return "n/a:indicator-daemon-absent"
    if fresh["start"] != row["start"]:
        raise ValueError(f"OBSERVE-INDICATOR-DAEMON-BIRTH: pid={row['pid']}")
    return {"pid": row["pid"], "start": row["start"], **process_metrics(row["pid"])}



def pane_text(result, pane):
    reading = result.get("read", {})
    text = reading.get("text")
    if not isinstance(text, str) or reading.get("pane_id") != pane:
        raise ValueError(f"OBSERVE-PANE-TEXT: {result!r}")
    return text


def entity(kind, payload, identity):
    item = payload.get(kind, {})
    if item.get(kind + "_id") != identity or not isinstance(item.get("label"), str) or not isinstance(item.get("focused"), bool):
        raise ValueError(f"OBSERVE-{kind.upper()}-IDENTITY: {payload!r}")
    return item


def screen(target):
    if target["backend"] == "pty":
        from terminal_pty import read
        return read(target)["text"]
    if target["backend"] == "herdr":
        reading = herdr.request(os.environ["HERDR_SOCKET_PATH"], "pane.read", {"pane_id": target["pane"], "source": "visible"})["result"]
        return pane_text(reading, target["pane"])
    if target["backend"] == "tmux":
        return report(["tmux", "-S", target["socket"], "capture-pane", "-p", "-t", target["pane"]])
    raise ValueError(f"OBSERVE-TERMINAL: unsupported backend {target['backend']!r}")


def shield(pane, processes, socket):
    if not any(process.get("name") == "herdr-shield" for process in processes["foreground_processes"]):
        return "n/a:native-pane"
    listing = json.loads(report(["herdr-shield", "list", "--json"]))
    if listing.get("enumeration") != "OK" or listing.get("error"):
        raise ValueError(f"OBSERVE-SHIELD-UNREADABLE: {listing!r}")
    matches = [holder for holder in listing["holders"]
               if holder.get("binding", {}).get("pane") == pane["pane_id"]
               and holder["binding"].get("socket") == socket]
    if len(matches) != 1 or not matches[0]["holder_live"] or matches[0]["state"] != "running":
        raise ValueError(f"OBSERVE-SHIELD-HOLDER: {pane['pane_id']}: {matches!r}")
    holder = matches[0]
    if (holder["binding"]["workspace"], holder["binding"]["tab"]) != (pane["workspace_id"], pane["tab_id"]):
        raise ValueError(f"OBSERVE-SHIELD-BINDING: {holder['binding']!r}")
    return {"holder": holder, "births": process_tree([holder["child"]["pid"]], descendants=True),
            "config": read_json(Path(os.environ["HOME"]) / ".config/herdr-shield/config.json")}


def terminal(target):
    from bootstrap import wait
    races, failures = [], []
    def stable():
        try:
            return _terminal(target)
        except ProcessExited as error:
            races.append(error.process)
            failures.append(error.evidence)
            return None
    try:
        observed = wait("terminal process snapshot stabilizes", stable, 2)
    except ValueError as error:
        if races:
            raise ValueError(f"OBSERVE-PROCESS-UNSTABLE: exited={races!r}: {error}") from error
        raise
    if races:
        observed["process_read_exits"] = races
        observed["process_read_failures"] = failures
    return observed


def _terminal(target):
    backend = target["backend"]
    if backend == "pty":
        from terminal_pty import read
        display = read(target)
        return {"backend": backend, "workspace": "n/a:pty", "tab": "n/a:pty",
                "agent_status": "n/a:pty", "indicators": "n/a:pty", "shield": "n/a:pty",
                "pane": {"pane_id": target["pane"], "focused": True, "foreground_pid": display["foreground_pid"]},
                "tmux": "n/a:pty", "text": display["text"], "raw": display["raw"], "raw_base64": display["raw_base64"],
                "births": process_tree([display["pid"]], descendants=True),
                "identity": {"socket": target["socket"], "pane": target["pane"], "pty": display["pty"]}}
    if backend == "herdr":
        socket = os.environ["HERDR_SOCKET_PATH"]
        pane = herdr.request(socket, "pane.get", {"pane_id": target["pane"]})["result"]["pane"]
        required = ("pane_id", "workspace_id", "tab_id", "terminal_id", "focused")
        if any(key not in pane for key in required) or pane["pane_id"] != target["pane"]:
            raise ValueError(f"OBSERVE-PANE-IDENTITY: {pane!r}")
        workspace = herdr.request(socket, "workspace.get", {"workspace_id": pane["workspace_id"]})["result"]
        tab = herdr.request(socket, "tab.get", {"tab_id": pane["tab_id"]})["result"]
        for kind, payload, identity in (("workspace", workspace, pane["workspace_id"]), ("tab", tab, pane["tab_id"])):
            entity(kind, payload, identity)
        text = screen(target)
        processes = herdr.request(socket, "pane.process_info", {"pane_id": target["pane"]})["result"]["process_info"]
        if processes.get("pane_id") != target["pane"] or not isinstance(processes.get("foreground_processes"), list):
            raise ValueError(f"OBSERVE-PANE-PROCESSES: {processes!r}")
        pids = [process["pid"] for process in processes["foreground_processes"]]
        return {"backend": backend, "workspace": workspace, "tab": tab, "pane": pane,
                "agent_status": {"pane": pane.get("agent_status", "n/a:not-reported"), "tab": tab["tab"].get("agent_status", "n/a:not-reported"),
                                 "workspace": workspace["workspace"].get("agent_status", "n/a:not-reported")},
                "indicators": {"pane": pane.get("tokens", {}), "workspace": workspace["workspace"].get("tokens", {})},
                "shield": shield(pane, processes, socket),
                "text": text, "tmux": "n/a:herdr", "processes": processes,
                "births": process_tree(pids, descendants=True), "identity": {"socket": socket, "workspace": pane["workspace_id"],
                "tab": pane["tab_id"], "pane": pane["pane_id"]}}
    if backend != "tmux":
        raise ValueError(f"OBSERVE-TERMINAL: unsupported backend {backend!r}")
    arguments = ["tmux", "-S", target["socket"]]
    fields = ("session_name", "window_id", "window_name", "pane_id", "pane_active", "pane_current_path", "pane_pid")
    output = report(arguments + ["display-message", "-p", "-t", target["pane"],
                                "\t".join("#{" + key + "}" for key in fields)]).rstrip("\n")
    values = output.split("\t")
    if len(values) != len(fields):
        raise ValueError(f"OBSERVE-TMUX-IDENTITY: {output!r}")
    pane = dict(zip(fields, values))
    pane["focused"] = pane["pane_active"] == "1"
    text = screen(target)
    return {"backend": backend, "workspace": "n/a:tmux", "tab": "n/a:tmux",
            "agent_status": "n/a:tmux", "indicators": "n/a:tmux",
            "shield": "n/a:tmux",
            "pane": pane, "tmux": output, "text": text, "births": process_tree([int(pane["pane_pid"])], descendants=True),
            "identity": {"socket": target["socket"], "session": pane["session_name"],
                         "window": pane["window_id"], "pane": pane["pane_id"]}}


def select(connection, table, predicate, values):
    return [dict(row) for row in connection.execute(f'SELECT * FROM "{table}" WHERE {predicate}', values)]


def reopenable(chat):
    expected = lifecycle.expectations(chat)
    if expected["lifecycle"] != "live" or expected["terminal"] != "live":
        return False
    path = Path(os.environ["PFM_STATE_DB"])
    if not path.is_file():
        raise ValueError(f"OBSERVE-STORE-ABSENT: {path}")
    with sqlite3.connect(path.as_uri() + "?mode=ro", uri=True) as connection:
        connection.row_factory = sqlite3.Row
        seats = select(connection, "seat", "seat_id = ? AND session_id = ?", [chat.get("seat_id", ""), chat["session_id"]])
    if not seats and chat["terminal"]["backend"] == "tmux" and not chat.get("seat_id"):
        receipt_path = Path(os.environ["JOURNEY_ROOT"]) / "checkpoint-births.json"
        receipts = {} if lifecycle.absent(receipt_path) and chat.get("tmux_launch") else read_json(receipt_path)
        saved = receipts.get(chat["session_id"], chat.get("tmux_launch"))
        terminal = saved["terminal"]
        if terminal != chat["terminal"]:
            relocated = read_json(Path(os.environ["JOURNEY_ROOT"]) / "checkpoint-terminals.json")[chat["session_id"]]
            if relocated["source"] != terminal:
                raise ValueError(f"OBSERVE-REOPEN-CHECKPOINT-MISMATCH: {chat['session_id']}")
            terminal = relocated["current"]
        if (saved["status"]["session_id"] != chat["session_id"] or saved["status"]["state"] not in {"idle", "working", "blocked"}
                or terminal != chat["terminal"] or not saved["births"]):
            raise ValueError(f"OBSERVE-REOPEN-CHECKPOINT-MISMATCH: {chat['session_id']}")
        return True
    if len(seats) != 1:
        raise ValueError(f"OBSERVE-REOPEN-SEAT-ABSENT: {chat['session_id']}")
    return seats[0]["state"] in {"live", "lost"}


def stored(chat, engine=None, reports_available=True):
    path = Path(os.environ["PFM_STATE_DB"])
    if not path.is_file():
        raise ValueError(f"OBSERVE-STORE-ABSENT: {path}")
    sessions = list(dict.fromkeys([chat["session_id"], *chat.get("lineage_ids", [])]))
    marks = ",".join("?" for _ in sessions)
    with sqlite3.connect(path.as_uri() + "?mode=ro", uri=True) as connection:
        connection.row_factory = sqlite3.Row
        queries = {
            "seat": (f"session_id IN ({marks}) OR seat_id = ?", sessions + [chat.get("seat_id", "")]),
            "chat": (f"uuid IN ({marks})", sessions),
            "launch": (f"session_id IN ({marks})", sessions),
            "hidden": (f"uuid IN ({marks})", sessions),
            "children": (f"key IN ({marks}) OR val IN ({marks})", sessions + sessions),
        }
        projection = {table: select(connection, table, predicate, values) for table, (predicate, values) in queries.items()}
        sockets = {chat.get("socket", ""), chat.get("terminal", {}).get("socket", "")}
        sockets.update(row["tmux_socket"] for row in projection["seat"] if row.get("tmux_socket"))
        keys = ["branch-seat:" + socket for socket in sorted(sockets) if socket]
        projection["lineage"] = select(connection, "meta", "key IN (" + ",".join("?" for _ in keys) + ")", keys) if keys else []
        projection["schedules"] = "n/a:not-touched"
        if "schedule_ids" in chat:
            schedules = []
            for identity in chat["schedule_ids"]:
                owned = select(connection, "schedules", f"id = ? AND session_id IN ({marks})", [identity, *sessions])
                if len(owned) != 1:
                    raise ValueError(f"OBSERVE-SCHEDULE-OWNERSHIP: {identity}")
                schedules.extend(owned)
            projection["schedules"] = schedules
    seat_id = chat.get("seat_id")
    if seat_id and chat.get("terminal", {}).get("backend") != "tmux":
        if not re.fullmatch(r"[a-zA-Z0-9_-]+", seat_id):
            raise ValueError(f"OBSERVE-SEAT-ID: {seat_id!r}")
        recipe_path = path.parent / "seats" / (seat_id + ".json")
        if recipe_path.is_symlink():
            raise ValueError(f"OBSERVE-RECIPE-SYMLINK: {recipe_path}")
        ended = projection["seat"] and all(seat["state"] != "live" for seat in projection["seat"])
        projection["recipe"] = "n/a:ended-seat-recipe" if ended and not recipe_path.exists() else read_json(recipe_path)
    else:
        projection["recipe"] = "n/a:tmux-launch-record"
    if chat.get("expect", {}).get("lifecycle") == "archived":
        projection["transcript"] = "n/a:archived-chat"
    elif not reports_available and engine != "ox":
        projection["transcript"] = lifecycle.transcript(chat)
    elif engine == "ox":
        cache = Path(os.environ["PFM_CACHE_DB"])
        with sqlite3.connect(cache.as_uri() + "?mode=ro", uri=True) as connection:
            connection.row_factory = sqlite3.Row
            projection["transcript"] = {"engine": engine, "attribution": select(connection, "oc_sessions", f"id IN ({marks})", sessions),
                                         "entries": "n/a:opencode-public-read-unsupported"}
    else:
        projection["transcript"] = json.loads(report(["pfm", "chat", "read", chat["session_id"], "--tail", "1", "--json"]))
    if engine == "cx":
        cache = Path(os.environ["PFM_CACHE_DB"])
        with sqlite3.connect(cache.as_uri() + "?mode=ro", uri=True) as connection:
            connection.row_factory = sqlite3.Row
            projection["engine_lineage"] = select(connection, "rollouts",
                f"id IN ({marks}) OR session_id IN ({marks}) OR (lineage_root <> '' AND lineage_root IN (SELECT lineage_root FROM rollouts WHERE id IN ({marks})))",
                sessions * 3)
    else:
        projection["engine_lineage"] = "n/a:no-codex-rollout-lineage"
    projection["reload_records"] = lifecycle.reload_records(chat)
    return projection


def fleet_rows(capture=None):
    text = report(["pfm", "ls", "--all", "--tsv"], capture=capture)
    reader = csv.DictReader(io.StringIO(text), delimiter="\t")
    required = {"kind", "id", "project", "cwd", "name", "account", "killed", "socket"}
    if not reader.fieldnames or not required.issubset(reader.fieldnames):
        raise ValueError(f"OBSERVE-PICKER-SCHEMA: {reader.fieldnames!r}")
    rows = list(reader)
    if any(None in row or any(value is None for value in row.values()) for row in rows):
        raise ValueError("OBSERVE-PICKER-SCHEMA: malformed TSV row")
    return rows


def badge(name, screen):
    return observation_picker.badge(name, screen)


def terminal_births(display):
    if not isinstance(display, dict):
        return "n/a:ended-terminal"
    births = list(display.get("births", []))
    if isinstance(display.get("shield"), dict):
        births.extend(display["shield"]["births"])
    return list({(birth["pid"], birth["start"]): birth for birth in births}.values())


def chat_projection(chat, rows, picker_screen="", reports_available=True):
    expected = lifecycle.expectations(chat)
    identities = {chat["session_id"], chat.get("seat_id", ""), *chat.get("lineage_ids", [])}
    selected = [row for row in rows if row["id"] in identities]
    if not selected and expected == {"lifecycle": "live", "terminal": "live", "visibility": "visible"} and reports_available:
        raise ValueError(f"OBSERVE-PICKER-ROW-ABSENT: {chat['name']} {chat['session_id']}")
    status_available = reports_available and expected["lifecycle"] != "archived"
    status = json.loads(report(["pfm", "chat", "status", chat["session_id"], "--json"], allowed_codes=(0, 3))) if status_available else "n/a:" + ("archived-chat" if reports_available else "uninstalled")
    engine = status["engine"] if status_available else lifecycle.engine_id(chat.get("engine"))
    storage = stored(chat, engine) if reports_available else stored(chat, engine, False)
    proof = {"visibility": lifecycle.visibility(chat, selected, storage, expected)} if reports_available else {"visibility": "n/a:uninstalled"}
    if lifecycle.hostless_scope(chat) is not None:
        display, proof["hostless"] = lifecycle.hostless(chat, status, storage, process_snapshot(), terminal, herdr.request)
    elif expected["lifecycle"] == "live":
        if not reports_available:
            raise ValueError(f"OBSERVE-LIVE-AFTER-UNINSTALL: {chat['session_id']}")
        if status["state"] in {"dead", "lost"}:
            raise ValueError(f"OBSERVE-LIVE-STATE: {chat['session_id']}: {status!r}")
        display = terminal(chat["terminal"])
    elif expected["lifecycle"] == "transient":
        if not reports_available:
            raise ValueError(f"OBSERVE-TRANSIENT-AFTER-UNINSTALL: {chat['session_id']}")
        display = terminal(chat["terminal"])
        proof["transient"] = lifecycle.transient(chat, status, storage, process_snapshot())
    else:
        display, proof["ended"] = lifecycle.ended(chat, expected, status, storage, process_snapshot(), terminal, report, herdr.request, status_available)
    seats = [seat for seat in storage["seat"] if seat["seat_id"] == chat.get("seat_id")]
    seat = seats[0] if len(seats) == 1 else {}
    address = {"socket": seat.get("herdr_socket") or (status.get("socket") if isinstance(status, dict) else None) or chat["terminal"].get("socket"),
               "pane": seat.get("herdr_pane") or seat.get("tmux_pane") or (display["identity"]["pane"] if isinstance(display, dict) else chat["terminal"]["pane"])}
    applicable = reports_available and expected["lifecycle"] == "live" and expected["visibility"] == "visible"
    if applicable:
        if callable(picker_screen):
            picker_screen = picker_screen()
        if not picker_screen:
            raise ValueError(f"OBSERVE-PICKER-BADGE-ABSENT: {chat['session_id']}")
        observed_badge = picker_screen if isinstance(picker_screen, dict) else badge(chat["name"], picker_screen)
    else:
        reason = "uninstalled" if not reports_available else expected["lifecycle"] if expected["lifecycle"] != "live" else expected["visibility"]
        observed_badge = {"state": "n/a:" + reason, "line": "n/a:" + reason}
    normalized = [{"engine": engine, "project": row["project"], "name": row["name"],
                   "account": int(row["account"]), "kind": row["kind"], "state": status["state"] if isinstance(status, dict) else status,
                   "killed": row["killed"] == "true", "hidden": row["killed"] == "true" or bool(storage["hidden"]),
                   "backend": seat.get("backend", chat["terminal"]["backend"]), "address": address,
                   "badge": observed_badge} for row in selected]
    projection = {"name": chat["name"], "rows": selected if reports_available else "n/a:uninstalled", "picker": normalized if selected else "n/a:" + (expected["visibility"] if reports_available else "uninstalled"), "status": status,
            "stored": storage, "terminal": display, "lifecycle": proof,
            "process_births": terminal_births(display)}
    if expected["lifecycle"] == "live" and not observed_badge["state"].startswith("n/a:") and observed_badge["state"] != status["state"]:
        raise lifecycle.ProjectionMismatch(f"OBSERVE-BADGE-MISMATCH: {chat['name']} badge={observed_badge['state']} status={status['state']}", projection)
    return projection
