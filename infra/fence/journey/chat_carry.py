#!/usr/bin/env python3
"""Observed identities and lineage for public journey chat entries."""
from contextlib import contextmanager
import csv
import json
import os
from pathlib import Path
import re
import sqlite3
from observation import terminal as observe_terminal, terminal_births
from observation_process import engine_births
from runtime import read_json, write_json
from beatstate import owner_receipt, receipt_path
from observation_hostless import temporary_hostless
from chat_capture import capture, seat_bound, seat_terminal, workspace_id
from chat_registry import remember, _retain_lineage

ENGINES = {"claude": "cc", "codex": "cx", "opencode": "ox"}
_EXPECTATION_ABSENT = object()


def branch_parts(branch, origin=None):
    parts = branch.split("+")
    if (len(parts) != 4 or parts[0] not in {"linux", "macos"}
            or parts[1] not in {"herdr-shield", "herdr-native", "tmux"}
            or parts[2] not in {"project", "subfolder", "worktree"} or parts[3] not in ENGINES
            or (origin is not None and not re.fullmatch(r"[a-z][a-z0-9-]*", origin))):
        raise ValueError(f"CHAT-BRANCH: {branch!r} origin={origin!r}")
    return parts


def carry():
    return read_json(os.environ["JOURNEY_CARRY"])


def expects_live(current):
    return current.get("expect", {}).get("lifecycle", "live") == "live"


def select(branch, origin):
    branch_parts(branch, origin)
    matches = [chat for chat in carry()["chats"] if chat.get("branch") == branch and chat.get("role", chat.get("origin")) == origin]
    if len(matches) > 1:
        raise ValueError(f"CHAT-CARRY-AMBIGUOUS: {branch}:{origin}")
    return matches[0] if matches else None


def stored(query, parameters=(), cache=False):
    path = Path(os.environ["PFM_CACHE_DB" if cache else "PFM_STATE_DB"])
    if not path.is_file():
        raise ValueError(f"CHAT-STORE-ABSENT: {path}")
    with sqlite3.connect(path.as_uri() + "?mode=ro", uri=True) as connection:
        connection.row_factory = sqlite3.Row
        return [dict(row) for row in connection.execute(query, parameters)]


def seats():
    return stored("SELECT seat.*, launch.account, launch.cache1h FROM seat "
                  "LEFT JOIN launch ON launch.session_id = seat.session_id")


def owned(row, target):
    if target["backend"] == "herdr":
        return row["backend"] == "herdr" and row["herdr_socket"] == os.environ["HERDR_SOCKET_PATH"]
    if target["backend"] == "tmux":
        return row["backend"] == "tmux" and socket_path(row["tmux_socket"]) == socket_path(target["socket"])
    raise ValueError(f"CHAT-TERMINAL: {target!r}")


def socket_path(value):
    path = Path(value)
    return str(path if path.is_absolute() else Path(os.environ["PFM_TMUX_DIR"]) / path)


def run_owned(row):
    if row["backend"] == "herdr":
        return owned(row, {"backend": "herdr"})
    return row["backend"] == "tmux" and Path(socket_path(row["tmux_socket"])).resolve().is_relative_to(Path(os.environ["PFM_TMUX_DIR"]).resolve())


def before(target):
    return {"target": target, "seats": [row["seat_id"] for row in seats()
            if (run_owned(row) if target["backend"] == "pty" else owned(row, target))]}


def metadata_path(branch, origin):
    branch_parts(branch, origin)
    return Path(os.environ["JOURNEY_ROOT"]) / "branches" / branch / (origin + ".json")


def _child_role(parent, role, unique=False):
    branch_parts(parent["branch"], role)
    if unique and any(chat.get("branch") == parent["branch"] and chat.get("role", chat.get("origin")) == role
           for chat in carry()["chats"]):
        raise ValueError(f"CHAT-CHILD-ROLE: {parent['branch']}:{role}: already carried")


def _adopt_actual_child(parent, session_id, role, origin, expected_output=None):
    _child_role(parent, role)
    matches = [row for row in seats() if row["session_id"] == session_id and row["state"] == "live"]
    if len(matches) != 1:
        raise ValueError(f"CHAT-CHILD-SEAT: {session_id}: {matches!r}")
    row = matches[0]
    if session_id == parent["session_id"]:
        raise ValueError("CHAT-CHILD-SELF: parent cannot be its own child")
    identities = {value for value in (session_id, row["seat_id"], row["tmux_socket"]) if value}
    if row["tmux_socket"]:
        identities.add(Path(row["tmux_socket"]).name)
    linked = stored("SELECT val FROM children WHERE kind='new' AND key = ?", (parent["session_id"],))
    if not any(edge["val"] in identities for edge in linked):
        raise ValueError(f"CHAT-CHILD-PARENT: {session_id}: expected parent={parent['session_id']}")
    if not run_owned(row):
        raise ValueError(f"CHAT-CHILD-FOREIGN: {session_id}")
    if expected_output is not None and (row["backend"] != "tmux" or
            socket_path(row["tmux_socket"]) != expected_output["socket"] or
            row["engine"] != ENGINES.get(expected_output["engine"]) or row["name"] != expected_output["name"]):
        raise ValueError(f"CHAT-CHILD-RECEIPT: {session_id}: output socket does not own the actual seat")
    engine = next(name for name, code in ENGINES.items() if code == row["engine"])
    metadata = observed_scenario(seat_terminal(row), engine)
    return remember(row, parent["branch"], origin, {**metadata,
        "project": parent.get("project", "atlas"), "workspace": row["herdr_workspace"],
        "key": "chat-mesh:" + parent["branch"] + ":" + role, "role": role, "parent_key": parent["key"], "parent": parent["session_id"]})


def adopt_child(parent, session_id, response, role="scout"):
    output = response.get("structuredContent", response)
    if response.get("isError") or output.get("status") != "ok" or output.get("code") != 0:
        raise ValueError(f"CHAT-CHILD-REFUSED: {response!r}")
    return _adopt_actual_child(parent, session_id, role, "spawn-child-from-mcp")


def _cli_child_output(parent, session_id, receipt):
    path = receipt_path(receipt)
    status = Path(str(path) + ".status").read_text()
    if status != "0\n":
        raise ValueError(f"CHAT-CHILD-REFUSED: public CLI receipt status={status!r}")
    if Path(str(path) + ".dispatch.json").exists():
        dispatch = read_json(str(path) + ".dispatch.json")
        if os.environ.get("JOURNEY_OUT"):
            state_path = Path(os.environ["JOURNEY_OUT"]) / (os.environ.get("JOURNEY_STATE_LANE", os.environ.get("JOURNEY_LANE", "")) + ".state.json")
            if state_path.is_file():
                owner_receipt(read_json(state_path), path, json.dumps(dispatch.get("argv"), separators=(",", ":")), completed_status="0\n")
        if dispatch.get("target") != parent.get("terminal"):
            raise ValueError(f"CHAT-CHILD-OWNER: receipt target does not match parent terminal: {dispatch!r}")
        argv = dispatch.get("argv")
        if not isinstance(argv, list) or any(not isinstance(value, str) for value in argv) or argv[:3] != ["pfm", "chat", "new"]:
            raise ValueError(f"CHAT-CHILD-ARGV: expected public pfm chat new, actual={argv!r}")
    output = Path(str(path) + ".out").read_text()
    rows = [row for row in csv.reader(output.splitlines(), delimiter="\t") if len(row) in {5, 6}]
    if len(rows) != 1 or len(rows[0]) != 5:
        raise ValueError(f"CHAT-CHILD-RECEIPT: expected tmux launch TSV with session, actual={output!r}")
    engine, name, socket, state, listing = rows[0]
    if not engine or not name or state not in {"named", "UNNAMED"} or not listing:
        raise ValueError(f"CHAT-CHILD-RECEIPT: malformed launch TSV: {rows[0]!r}")
    attach = re.findall(r"^attach: tmux -L (\S+) attach -t (\S+)$", output, re.MULTILINE)
    if len(attach) != 1 or attach[0][0] != socket or attach[0][1] != session_id:
        raise ValueError(f"CHAT-CHILD-RECEIPT: requested session is not in public output: {output!r}")
    if engine not in ENGINES:
        raise ValueError(f"CHAT-CHILD-RECEIPT: unsupported launch engine={engine!r}")
    expected_socket = socket_path(socket)
    return {"socket": expected_socket, "engine": engine, "name": name}


def adopt_cli_child(parent, session_id, receipt, role):
    if not role:
        raise ValueError("CHAT-CHILD-ROLE: CLI adoption requires a role")
    _child_role(parent, role, unique=True)
    if not parent.get("terminal"):
        raise ValueError("CHAT-CHILD-OWNER: parent terminal is absent")
    expected_output = _cli_child_output(parent, session_id, receipt)
    return _adopt_actual_child(parent, session_id, role, "spawn-child-from-cli", expected_output)


def observed_scenario(target, engine, workspace=None, display=None):
    display = observe_terminal(target) if display is None else display
    if target["backend"] == "herdr" and workspace and workspace.startswith("label:"):
        actual = display["workspace"]["workspace"]["label"]
        if actual != workspace.removeprefix("label:"):
            return {"scenario": "n/a:workspace-label-mismatch", "records": "n/a:workspace-label-mismatch"}
    observed = {"terminal": display, "process_births": terminal_births(display)}
    births = engine_births(observed, engine)
    paths = {birth["environment"]["MOCK_ENGINE_SCENARIO"] for birth in births
             if birth.get("environment", {}).get("MOCK_ENGINE_SCENARIO")}
    if not paths:
        return {"scenario": "n/a:scenario-not-exposed", "records": "n/a:scenario-not-exposed"}
    if len(paths) != 1:
        raise ValueError(f"CHAT-SCENARIO-AMBIGUOUS: {sorted(paths)!r}")
    path = Path(paths.pop())
    if not path.is_absolute():
        raise ValueError(f"CHAT-SCENARIO-PATH: {path}")
    path.resolve().relative_to(Path(os.environ["JOURNEY_ROOT"]).resolve())
    records = Path(read_json(path)["record_dir"])
    if not records.is_absolute():
        raise ValueError(f"CHAT-RECORDS-PATH: {records}")
    records.resolve().relative_to(Path(os.environ["JOURNEY_ROOT"]).resolve())
    return {"scenario": str(path), "records": str(records)}


def refresh(branch, origin):
    current = select(branch, origin)
    if current is None:
        return None
    target = current["terminal"]
    if not current.get("seat_id") and current.get("tmux_launch"):
        from chat_seatless import capture_codex
        metadata = read_json(metadata_path(branch, origin))
        return capture_codex(branch, origin, metadata, current["project"], current["workspace"],
                             metadata_path(branch, origin).parent / origin / "capture.json", {}, current)
    matches = [row for row in seats() if row["state"] == "live" and run_owned(row)
               and row["engine"] == ENGINES[current["engine"]]
               and (row["session_id"] == current["session_id"] or (owned(row, target) and
                    (row["seat_id"] == current["seat_id"] or
                     (row["herdr_pane"] if row["backend"] == "herdr" else row["tmux_pane"]) == target["pane"])))]
    matches = [row for row in matches if row["session_id"] == current["session_id"] or row["seat_id"] == current["seat_id"]
               or observed_scenario(seat_terminal(row), current["engine"])["scenario"] == current.get("scenario")]
    if not matches:
        return None
    if len(matches) != 1:
        raise ValueError(f"CHAT-REFRESH-AMBIGUOUS: {current['key']}")
    row = matches[0]
    if not row["session_id"]:
        return None
    updated = {**current, "name": row["name"], "seat_id": row["seat_id"], "session_id": row["session_id"], "account": row["account"],
               "cache1h": row["cache1h"], "terminal": seat_terminal(row)}
    if row["backend"] == "herdr":
        updated["workspace"] = row["herdr_workspace"]
    updated = _retain_lineage(current, updated)
    declared = carry()
    declared["chats"] = [updated if chat.get("key") == current["key"] else chat for chat in declared["chats"]]
    write_json(os.environ["JOURNEY_CARRY"], declared)
    return updated


def adopt_continuation(target, branch, role, staged_origin, previous, project=None, workspace=None):
    branch_parts(branch, role)
    branch_parts(branch, staged_origin)
    current = select(branch, role)
    if current is None:
        raise ValueError(f"CHAT-CONTINUATION-ABSENT: {branch}:{role}")
    live = [row for row in seats() if row["state"] == "live"
            and row["seat_id"] == current["seat_id"]]
    if live:
        raise ValueError(f"CHAT-CONTINUATION-LIVE: {current['key']}: {live!r}")
    return capture(target, branch, staged_origin, previous, project, workspace, replacement=current)


def annotate(branch, origin, values):
    if set(values) != {"reload_records"} or type(values["reload_records"]) is not bool:
        raise ValueError("CHAT-ANNOTATE: expected reload_records boolean")
    current = select(branch, origin)
    if current is None:
        raise ValueError(f"CHAT-ANNOTATE-ABSENT: {branch}:{origin}")
    updated = {**current, **values}
    declared = carry()
    declared["chats"] = [updated if chat.get("key") == current["key"] else chat for chat in declared["chats"]]
    write_json(os.environ["JOURNEY_CARRY"], declared)
    return updated


def _carried_row(declared, key):
    matches = [row for row in [*declared["chats"], declared.get("foreign", {})]
               if row.get("key", row.get("session_id")) == key]
    if len(matches) != 1:
        raise ValueError(f"CHAT-SCOPE-IDENTITY: {key}: expected one carried chat, got {len(matches)}")
    return matches[0]


def _write_expectation(key, expectation, **evidence):
    declared = carry()
    updated = _carried_row(declared, key)
    updated.update(evidence)
    if expectation is _EXPECTATION_ABSENT:
        updated.pop("expect", None)
    else:
        updated["expect"] = expectation
    write_json(os.environ["JOURNEY_CARRY"], declared)
    return updated


def _prior_evidence(current, before):
    matches = [row for row in [*before["chats"], before.get("foreign_full", {})] if isinstance(row.get("status"), dict)
               and row["status"].get("session_id") == current["session_id"]]
    if len(matches) != 1:
        raise ValueError(f"CHAT-END-BEFORE: expected one prior observation for {current['session_id']}")
    births = engine_births(matches[0], current["engine"])
    if not births:
        raise ValueError(f"CHAT-ENGINE-BIRTH-MISSING: {current['session_id']}")
    evidence = {"expected_prior_births": [{"pid": row["pid"], "start": row["start"]} for row in births]}
    transcript = matches[0].get("stored", {}).get("transcript", {})
    if isinstance(transcript, dict) and transcript.get("path"):
        Path(transcript["path"]).resolve().relative_to(Path(os.environ["JOURNEY_ROOT"]).resolve())
        evidence["transcript_path"] = transcript["path"]
    return evidence


def expect(chat, lifecycle, before=None, visibility="visible"):
    """Write expected state only; observation still proves the actual transition."""
    from observation_lifecycle import expectations
    key = chat.get("key", chat["session_id"])
    current = _carried_row(carry(), key)
    expected = expectations({"expect": {"lifecycle": lifecycle, "visibility": visibility,
        "terminal": "live" if lifecycle in {"live", "transient"} else "shell" if current["terminal"]["backend"] == "herdr" else "absent"}})
    return _write_expectation(key, expected, **(_prior_evidence(current, before) if before is not None else {}))


def expect_ended(branch, origin, before):
    """Retain a publicly ended role only after actual status and death proof."""
    import json
    import observation
    import observation_lifecycle as lifecycle
    current = select(branch, origin)
    if current is None:
        raise ValueError(f"CHAT-END-ABSENT: {branch}:{origin}")
    evidence = _prior_evidence(current, before)
    expected = {"lifecycle": "ended", "terminal": "shell" if current["terminal"]["backend"] == "herdr" else "absent",
                "visibility": current.get("expect", {}).get("visibility", "visible")}
    candidate = {**current, **evidence, "expect": expected}
    status = json.loads(observation.report(["pfm", "chat", "status", current["session_id"], "--json"], allowed_codes=(0, 3)))
    lifecycle.ended(candidate, expected, status, observation.stored(candidate, lifecycle.engine_id(current["engine"])),
                    observation.process_snapshot(), observe_terminal, observation.report, observation.herdr.request, True)
    return _write_expectation(current.get("key", current["session_id"]), expected, **evidence)


@contextmanager
def temporary_expectation(branch, origin, expectation, before=None):
    current = select(branch, origin)
    if current is None:
        raise ValueError(f"CHAT-SCOPE-ABSENT: {branch}:{origin}")
    if not isinstance(expectation, dict):
        raise ValueError(f"CHAT-SCOPE-EXPECTATION: {expectation!r}")
    key = current.get("key", current["session_id"])
    previous = current.get("expect", _EXPECTATION_ABSENT)
    _write_expectation(key, expectation, **(_prior_evidence(current, before) if before is not None else {}))
    try:
        yield
    finally:
        _write_expectation(key, previous)


def adopt_branch(parent, origin, session_id, receipt):
    branch_parts(parent["branch"], origin)
    Path(receipt).resolve().relative_to(Path(os.environ["JOURNEY_ROOT"]).resolve())
    if Path(receipt + ".status").read_text().strip() != "0":
        raise ValueError("CHAT-BRANCH-REFUSED: public branch receipt was not successful")
    output = Path(receipt + ".out").read_text()
    sockets = re.findall(r" into detached socket (\S+) — waiting in pfm ls;", output)
    if len(sockets) != 1:
        raise ValueError(f"CHAT-BRANCH-RECEIPT: {output!r}")
    socket = socket_path(sockets[0])
    Path(socket).resolve().relative_to(Path(os.environ["PFM_TMUX_DIR"]).resolve())
    matches = [row for row in seats() if row["backend"] == "tmux" and row["state"] == "live"
               and socket_path(row["tmux_socket"]) == socket and row["session_id"] == session_id
               and session_id != parent["session_id"]]
    if len(matches) != 1:
        raise ValueError(f"CHAT-BRANCH-SEAT: {session_id}: {matches!r}")
    edge = stored("SELECT val FROM meta WHERE key=?", ("branch-seat:" + sockets[0],))
    if edge != [{"val": parent["session_id"]}]:
        raise ValueError(f"CHAT-BRANCH-PARENT: {session_id}: {edge!r}")
    row = matches[0]
    metadata = observed_scenario(seat_terminal(row), next(name for name, code in ENGINES.items() if code == row["engine"]))
    return remember(row, parent["branch"], origin, {**metadata, "project": parent["project"],
        "parent": parent["session_id"], "parent_key": parent["key"]})
