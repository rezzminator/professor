#!/usr/bin/env python3
"""Cancel a real public birth before SessionStart, preserving leaks before cleanup."""
from pathlib import Path
import json
import os
from shlex import quote, split
import sys

from boot import actual_birth, native_records
from bootstrap import wait
from chat import before, capture, seats, shell_ready, socket_path
from chat_carry import stored
from faults import owned_path, owned_target
from observation import process_snapshot, report, terminal
from observation_process import environment, reporter_activity
from runtime import now_ms, read_json, write_json
from terminal import keys, text, type_command

FACTORS = "multiplexer=tmux,runtime_event=spawn_cancelled"
EXPECTED = {"actual_child_born": True, "cancelled_before_session_start": True,
            "owner_exit_status": "130", "launcher_gone": True, "child_gone": True,
            "pane_gone": True, "seat_gone": True, "chat_gone": True, "launch_gone": True,
            "native_session_gone": True, "activity": "n/a:reporter-activity",
            "recovery_captured": True}


def scope(factors, directory):
    if factors != FACTORS:
        raise ValueError("BIRTH-CANCEL-FACTORS: " + factors)
    if not os.environ["PFM_RUN"]:
        raise ValueError("BIRTH-CANCEL-RUN: nonempty run required")
    directory = owned_path(directory)
    if not directory.is_dir():
        raise ValueError("BIRTH-CANCEL-STAGING: existing owned directory required")
    return directory


def prepare(factors, directory):
    directory = scope(factors, directory)
    path = directory / "cancel-input.json"
    gate = directory / "cancel-ready.json"
    if path.exists() or gate.exists():
        raise ValueError("BIRTH-CANCEL-REUSED: staging directory already prepared")
    write_json(path, {**read_json(Path(__file__).with_name("scenario.json")),
                     "ready_hold": str(gate), "ready_timeout_ms": 30000})
    return str(path)


def launcher_birth(target):
    matches = []
    for row in terminal(target)["births"]:
        argv = split(row["chain"][0]["argv"])
        if (argv and Path(argv[0]).name == "pfm" and argv[1:3] == ["chat", "new"]
                and row["environment"].get("PFM_RUN") == os.environ["PFM_RUN"]):
            matches.append(row)
    if len(matches) != 1:
        raise ValueError(f"BIRTH-CANCEL-LAUNCHER: expected one actual public CLI, got {matches!r}")
    return matches[0]


def receipt(prefix):
    return {"status": Path(str(prefix) + ".status").read_text().strip(),
            "output": Path(str(prefix) + ".out").read_text()}


def public(target, arguments, prefix):
    type_command(target, arguments, str(prefix))
    wait("public cancellation fixture receipt " + str(prefix),
         lambda: Path(str(prefix) + ".status").is_file(), 35)
    return receipt(prefix)


def cancel_owner(target, prefix):
    keys(target, ["C-c"])
    wait("owner prompt after Ctrl-C", lambda: shell_ready(target), 5)
    status = Path(str(prefix) + ".status")
    if not status.exists():
        # Interactive shells may abandon the receipt suffix on SIGINT; read their actual status first.
        text(target, "printf '%s\\n' \"$?\" > " + quote(str(status)))
        keys(target, ["enter"])
    wait("cancelled public CLI receipt", status.is_file, 5)
    return receipt(prefix)


def launch_seats(metadata):
    return [row for row in seats() if row["backend"] == "tmux" and
            socket_path(row["tmux_socket"]) == metadata["launch_socket"]]


def snapshot(metadata, child, launcher, presence, session_id):
    processes = process_snapshot()
    # The actual socket is an input from chat.scenario; only its own panes are read.
    socket = owned_path(metadata["launch_socket"], preserve=True)
    panes = report(["tmux", "-S", str(socket), "list-panes", "-a", "-F", "#{pane_id}"]).splitlines() if socket.exists() else []
    current = processes.get(child["pid"])
    native = Path(metadata["records"]) / "startup.jsonl"
    events = [row for row in native_records(native) if row["pid"] == child["pid"]] if native.exists() else []
    return {"panes": panes, "seats": launch_seats(metadata), "child": current,
            "chats": stored("SELECT * FROM chat WHERE uuid = ?", (session_id,)),
            "launches": stored("SELECT * FROM launch WHERE session_id = ?", (session_id,)),
            "activity": reporter_activity({"engine": "claude", "session_id": session_id,
                                           "terminal": {"backend": "tmux"}}),
            "launcher": processes.get(launcher["pid"]),
            "native_session": read_json(presence) if presence.exists() else None,
            "startup_events": events, "at_ms": now_ms()}


def projection(evidence):
    held, after = evidence["held"], evidence["after_cancel"]
    return {"actual_child_born": held["child"] is not None,
            "cancelled_before_session_start": held["native_session"] is None and
            held["startup_events"] == [] and not evidence["gate_open_at_cancel"],
            "owner_exit_status": evidence["cancel_receipt"]["status"],
            "launcher_gone": after["launcher"] is None,
            "child_gone": after["child"] is None,
            "pane_gone": after["panes"] == [], "seat_gone": after["seats"] == [],
            "chat_gone": after["chats"] == [], "launch_gone": after["launches"] == [],
            "activity": after["activity"],
            "native_session_gone": after["native_session"] is None,
            "recovery_captured": evidence.get("recovery") is not None}


def run(factors, directory, target_file, evidence_file):
    directory = scope(factors, directory)
    evidence_file = owned_path(evidence_file, preserve=True)
    target = read_json(owned_path(target_file))
    owned_target(target)
    if target["backend"] != "tmux":
        raise ValueError("BIRTH-CANCEL-CALLER: carried tmux owner shell required")
    metadata = read_json(directory / "scenario.json")
    scenario_file = owned_path(metadata["scenario"])
    scenario = read_json(scenario_file)
    gate = directory / "cancel-ready.json"
    if scenario.get("ready_hold") != str(gate) or gate.exists():
        raise ValueError("BIRTH-CANCEL-INPUT: unopened pre-SessionStart gate required")
    branch, origin = os.environ["JOURNEY_BRANCH"], directory.name
    arguments = ["pfm", "chat", "new", "--engine", "claude", "--backend", "tmux",
                 "--account", "1", "--name", "atlas-cancelled-birth"]
    evidence = {"factors": factors, "target": target, "projection_expected": EXPECTED}
    child, presence = None, None
    type_command(target, arguments, str(directory / "cancelled"))
    try:
        child = actual_birth(Path(metadata["records"]) / "birth.json")
        child_environment = environment(child["pid"])
        if (child_environment.get("PFM_RUN") != os.environ["PFM_RUN"] or
                child_environment.get("MOCK_ENGINE_SCENARIO") != str(scenario_file)):
            raise ValueError("BIRTH-CANCEL-CHILD: actual child is outside the staged run")
        presence = owned_path(Path(child_environment["CLAUDE_CONFIG_DIR"]) /
                              "sessions" / (str(child["pid"]) + ".json"), preserve=True)
        launcher = launcher_birth(target)
        held_seats = wait("actual product seat for held child", lambda: launch_seats(metadata), 5)
        if len(held_seats) != 1 or not held_seats[0]["session_id"]:
            raise ValueError("BIRTH-CANCEL-SEAT: one actual preallocated native session required")
        session_id = held_seats[0]["session_id"]
        evidence.update(child_birth=child, launcher_birth=launcher,
                        cancelled_session_id=session_id,
                        held=snapshot(metadata, child, launcher, presence, session_id),
                        gate_open_at_cancel=gate.exists())
        if (evidence["held"]["child"] is None or not evidence["held"]["panes"] or
                evidence["held"]["launcher"] is None or evidence["held"]["native_session"] is not None or
                evidence["held"]["startup_events"]):
            raise ValueError("BIRTH-CANCEL-ORDER: child must be alive before native registration")
        # Ctrl-C is the owner's cancellation entry, delivered to its actual foreground group.
        evidence["cancel_receipt"] = cancel_owner(target, directory / "cancelled")
        evidence["after_cancel"] = snapshot(metadata, child, launcher, presence, session_id)
        evidence["projection"] = projection(evidence)
        write_json(evidence_file, evidence)
    finally:
        try:
            if child is not None:
                if not shell_ready(target):
                    keys(target, ["C-c"])
                wait("owner shell after cancellation", lambda: shell_ready(target), 5)
                for index, row in enumerate(launch_seats(metadata)):
                    result = public(target, ["pfm", "chat", "kill", row["session_id"] or row["seat_id"], "--exit"],
                                    directory / ("cleanup-" + str(index)))
                    evidence.setdefault("cleanup", []).append(result)
                    write_json(evidence_file, evidence)
                    if result["status"] != "0":
                        raise ValueError("BIRTH-CANCEL-CLEANUP: public kill failed: " + json.dumps(result))
        finally:
            write_json(gate, {"release": True})
    if child is not None:
        wait("cancelled birth removed by public cleanup", lambda: child["pid"] not in process_snapshot(), 15)
    restored = dict(scenario)
    for name in ("ready_hold", "ready_timeout_ms"):
        restored.pop(name, None)
    write_json(scenario_file, restored)
    wait("owner shell for recovery", lambda: shell_ready(target), 5)
    # Keep the original metadata/origin so normal capture retains the successful recovery.
    previous = before(target)
    recovery = public(target, [*arguments[:-1], "atlas-cancel-recovery"], directory / "recovery")
    evidence["recovery_receipt"] = recovery
    if recovery["status"] != "0":
        write_json(evidence_file, evidence)
        raise ValueError("BIRTH-CANCEL-RECOVERY: public chat new failed: " + json.dumps(recovery))
    evidence["recovery"] = wait("successful public recovery birth retained", lambda:
                                capture(target, branch, origin, previous), 20)
    evidence["projection"] = projection(evidence)
    write_json(evidence_file, evidence)
    return evidence["projection"]


if __name__ == "__main__":
    try:
        operation, *arguments = sys.argv[1:]
        result = {"prepare": prepare, "run": run}[operation](*arguments)
        print(result if isinstance(result, str) else json.dumps(result, sort_keys=True))
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print("BIRTH-CANCEL-ERROR: " + str(error), file=sys.stderr)
        sys.exit(2)
