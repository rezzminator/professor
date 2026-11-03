#!/usr/bin/env python3
"""Rename a native resumed Claude session before its registration completes."""
import copy
import json
import os
from pathlib import Path
import re
import shlex
import stat
import sys

from boot import native_records
from bootstrap import wait
from chat import (adopt_continuation, before, ensure_role, fresh_shell, inline,
                  refresh, scenario)
from edge import staging_path
from faults import owned_path, temporary_text
from observation import process_snapshot, report, terminal, terminal_births
from observation_process import engine_births
from runtime import command, now_ms, read_json, write_json
from terminal import keys, text, type_command

FACTORS = "rename_timing=before_session_registers,engine=claude_code,launch_mode=resume_in_place"
PRIOR = "atlas-early-prior"
NAME = "atlas-early-native"


def supported(factors):
    return {"kind": "early", "expected": expected(factors)}


def expected(factors):
    if factors != FACTORS:
        raise ValueError("BIRTH-EARLY-FACTORS: " + factors)
    return {"name": NAME, "prior_native_name": PRIOR,
            "same_native_session": True, "new_native_birth": True,
            "native_birth_bound": True, "actual_resume_argv": True,
            "no_name_argument": True, "registration_held_during_rename": True,
            "native_rename_recorded": True, "registration_completed": True,
            "settings_restored": True, "carried_origins_preserved": True}


def registration_settings(raw, argv):
    positions = [index for index, word in enumerate(argv) if word == "--settings"]
    if not positions:
        raise ValueError("BIRTH-EARLY-LAUNCH-SETTINGS: actual rendered settings required")
    rendered = json.loads(argv[positions[-1] + 1])
    handlers = [(entry, handler) for entry in rendered["hooks"]["SessionStart"]
                if not entry.get("matcher") or re.search(entry["matcher"], "resume")
                for handler in entry["hooks"]
                if shlex.split(handler.get("command", ""))[1:] ==
                ["internal", "herdr-state", "--engine", "claude", "start"]]
    if len(handlers) != 1:
        raise ValueError("BIRTH-EARLY-REGISTRATION: exactly one actual native registration command required")
    entry, original = handlers[0]
    document = json.loads(raw)
    entries = document.setdefault("hooks", {}).setdefault("SessionStart", [])
    matches = [handler for row in entries
               if not row.get("matcher") or re.search(row["matcher"], "resume")
               for handler in row.get("hooks", []) if handler.get("command") == original["command"]]
    if len(matches) > 1:
        raise ValueError("BIRTH-EARLY-REGISTRATION: ambiguous account registration")
    if matches:
        matches[0]["async"] = True
    else:
        # Account settings precede launch settings; the unchanged command deduplicates first.
        entries.append({**{key: value for key, value in entry.items() if key != "hooks"},
                        "hooks": [{**copy.deepcopy(original), "async": True}]})
    return json.dumps(document, sort_keys=True, indent=2) + "\n", original["command"]


def observed_birth(target):
    display = terminal(target)
    births = engine_births({"terminal": display, "process_births": terminal_births(display)}, "claude")
    if len(births) != 1:
        return None
    return {"birth": births[0], "terminal": display}


def native_title(path):
    titles = [row["customTitle"] for row in native_records(path) if row.get("type") == "custom-title"]
    return titles[-1] if titles else None


def prepare_resume(directory, current):
    branch, role = current["branch"], "early-resume"
    initial = ensure_role(branch, role, None, str(Path(__file__).with_name("scenario.json")),
                          current["project"], current.get("workspace"))
    observed = wait("initial native early-rename engine", lambda: observed_birth(initial["terminal"]), 20)
    text(initial["terminal"], "/rename " + PRIOR)
    keys(initial["terminal"], ["enter"])
    steps = directory / "early-prior-turn.json"
    write_json(steps, [{"type": "turn", "reply": "early-prior-ready", "busy_ms": 0}])
    inline(initial["terminal"], str(steps), "early-prior-turn")
    wait("native prior turn completed", lambda: "early-prior-ready" in terminal(initial["terminal"])["text"], 20)
    transcript = Path(json.loads(report(["pfm", "chat", "read", initial["session_id"], "--tail", "1", "--json"]))["path"])
    wait("native prior title recorded", lambda: native_title(transcript) == PRIOR, 10)
    initial = wait("native prior label visible in pfm", lambda: prior_label(branch, role), 15)
    initial_native = [row for row in native_records(Path(initial["records"]) / "birth.json")
                      if row["pid"] == observed["birth"]["pid"]]
    if len(initial_native) != 1:
        raise ValueError("BIRTH-EARLY-NATIVE-BIRTH: exactly one initial native birth required")
    evidence = {"initial": initial, "initial_birth": observed["birth"], "initial_native": initial_native[0],
                "initial_terminal": observed["terminal"], "prior_native_name": native_title(transcript),
                "transcript": str(transcript)}
    text(initial["terminal"], "/exit")
    keys(initial["terminal"], ["enter"])
    wait("native prior engine exited", lambda: observed["birth"]["pid"] not in process_snapshot(), 20)
    return evidence


def prior_label(branch, role):
    current = refresh(branch, role)
    return current if current and current["name"] == PRIOR else None


def gate_records(records, command, gate):
    path = Path(records) / "hook-gates.jsonl"
    return [row for row in native_records(path) if row["event"] == "SessionStart" and
            row["command"] == command and row["until_gone"] == str(gate)] if path.is_file() else []


def registration_completed(rows):
    return len(rows) == 2 and [(row["phase"], row["exit_code"], row["error"]) for row in rows] == [
        ("entered", -1, ""), ("completed", 0, "")] and rows[0]["input"] == rows[1]["input"]


def run(factors, directory, chat_file):
    supported(factors)
    directory = staging_path(directory)
    current = read_json(chat_file)
    carried = read_json(os.environ["JOURNEY_CARRY"])["chats"]
    if current["engine"] != "claude" or current not in carried:
        raise ValueError("BIRTH-EARLY-CAPTURE: actual carried Claude chat required")
    if (directory / "early-evidence.json").exists():
        raise ValueError("BIRTH-EARLY-REUSED: staging directory already used")
    evidence = {"factors": factors, "started_at_ms": now_ms()}
    initial = prepare_resume(directory, current)
    evidence.update(initial)
    account = owned_path(initial["initial_birth"]["environment"]["CLAUDE_CONFIG_DIR"])
    settings = account / "settings.json"
    owned_path(settings, preserve=True)
    original = settings.read_text()
    original_bytes, original_mode = settings.read_bytes(), stat.S_IMODE(settings.stat().st_mode)
    original_link = os.readlink(settings) if settings.is_symlink() else None
    faulted, registration = registration_settings(original, initial["initial_birth"]["argv"])
    gate = directory / "early-registration-gate.json"
    if gate.exists():
        raise ValueError("BIRTH-EARLY-REUSED: native registration gate already exists")
    write_json(gate, {"hold": True})
    fixture = directory / "early-resume-input.json"
    write_json(fixture, {**read_json(Path(__file__).with_name("scenario.json")),
                        "hook_gates": [{"event": "SessionStart", "command": registration,
                                        "until_gone": str(gate)}]})
    branch, role, origin = current["branch"], "early-resume", "early-resume-reentry"
    target = fresh_shell(branch, current.get("workspace", current["project"]),
                         str(Path(os.environ["HOME"]) / "work" / current["project"]))
    metadata = scenario(target, branch, origin, str(fixture), current["project"], current.get("workspace"))
    previous = before(target)
    evidence.update(registration_command=registration, settings=str(settings), target=target,
                    launch_argv=["claude", "--resume", initial["initial"]["session_id"]])
    resumed = None
    try:
        with temporary_text(settings, faulted):
            try:
                type_command(target, evidence["launch_argv"], str(directory / "early-resume-launch"))
                # Managed tmux launches create another pane; locate it from native scenario births.
                def launched():
                    targets = [target]
                    if target["backend"] == "tmux":
                        socket = metadata["launch_socket"]
                        if not Path(socket).exists():
                            return None
                        panes = report(["tmux", "-S", socket, "list-panes", "-a", "-F", "#{pane_id}"]).splitlines()
                        targets = [{"backend": "tmux", "socket": socket, "pane": pane} for pane in panes]
                    for candidate in targets:
                        observed = observed_birth(candidate)
                        if observed and observed["birth"]["environment"].get("MOCK_ENGINE_SCENARIO") == metadata["scenario"]:
                            return {**observed, "target": candidate}
                    return None
                observed = wait("native rendered resume engine and intended terminal", launched, 25)
                resume_target = observed["target"]
                evidence.update(resumed_birth=observed["birth"], resumed_terminal=observed["terminal"])
                records = metadata["records"]
                entered = wait("native registration gate entered", lambda: gate_records(records, registration, gate), 15)
                evidence["gate_entered"] = entered
                evidence["hook_payload"] = entered[0]["input"]
                wait("native resumed composer before registration release", lambda:
                     any(line.strip() == "❯" for line in terminal(resume_target)["text"].splitlines()), 15)
                evidence["held_before_rename"] = gate.exists() and len(gate_records(records, registration, gate)) == 1 and entered[0]["phase"] == "entered"
                text(resume_target, "/rename " + NAME)
                keys(resume_target, ["enter"])
                wait("native rename before registration release", lambda: native_title(Path(initial["transcript"])) == NAME, 15)
                evidence["renamed_native_name"] = native_title(Path(initial["transcript"]))
                evidence["held_after_rename"] = gate.exists() and len(gate_records(records, registration, gate)) == 1
                evidence["resumed_process"] = process_snapshot()[observed["birth"]["pid"]]
                births = native_records(Path(records) / "birth.json")
                evidence["resumed_native"] = next(row for row in births if row["pid"] == observed["birth"]["pid"])
            finally:
                evidence["released_at_ms"] = now_ms()
                command(["rm", "--", str(owned_path(gate))])
                if "resumed_birth" in evidence:
                    evidence["gate_completed"] = wait("native registration completed after gate release", lambda:
                        rows if len(rows := gate_records(metadata["records"], registration, gate)) == 2 else None, 20)
                    evidence["registration_completed"] = registration_completed(evidence["gate_completed"])
                def captured():
                    nonlocal resumed
                    resumed = adopt_continuation(target, branch, role, origin, previous,
                                                 current["project"], current.get("workspace")) if resumed is None else refresh(branch, role)
                    return resumed if resumed and resumed["name"] == NAME else None
                if "renamed_native_name" in evidence:
                    evidence["resumed"] = wait("same resumed native session retains early name", captured, 25)
                    evidence["after_registration"] = wait("resumed engine remains in captured terminal", lambda:
                        observed_birth(evidence["resumed"]["terminal"]), 10)
    finally:
        evidence["settings_restored"] = settings.read_bytes() == original_bytes and stat.S_IMODE(settings.stat().st_mode) == original_mode and (
            (os.readlink(settings) if settings.is_symlink() else None) == original_link)
        now = read_json(os.environ["JOURNEY_CARRY"])["chats"]
        evidence["carried_origins_preserved"] = all(row in now for row in carried)
        evidence["completed_at_ms"] = now_ms()
        write_json(directory / "early-evidence.json", evidence)
    actual = assert_early(factors, directory)
    write_json(directory / "early-projection.json", actual)
    return actual


def projection(evidence):
    initial, resumed = evidence["initial_birth"], evidence["resumed_birth"]
    argv = resumed["argv"]
    native = evidence["resumed_native"]
    return {"name": evidence["resumed"]["name"], "prior_native_name": evidence["prior_native_name"],
            "same_native_session": evidence["initial"]["session_id"] == evidence["resumed"]["session_id"] == evidence["hook_payload"]["session_id"] and
                evidence["hook_payload"]["hook_event_name"] == "SessionStart" and evidence["hook_payload"]["source"] == "resume",
            "new_native_birth": (initial["pid"], initial["start"]) != (resumed["pid"], resumed["start"]) and
                (evidence["initial_native"]["pid"], evidence["initial_native"]["start"]) != (native["pid"], native["start"]),
            "native_birth_bound": native["pid"] == resumed["pid"] and type(native["start"]) is int and native["start"] > 0 and
                native["parent_pid"] == evidence["resumed_process"]["parent"] and
                resumed["terminal"] == evidence["resumed_terminal"]["identity"] and
                all(evidence["after_registration"]["birth"][key] == resumed[key] for key in ("pid", "start", "terminal")),
            "actual_resume_argv": any(argv[index:index + 2] == ["--resume", evidence["initial"]["session_id"]]
                                      for index in range(len(argv))) and "--fork-session" not in argv and "--settings" in argv,
            "no_name_argument": not any(word == "--name" or word.startswith("--name=") for word in argv),
            "registration_held_during_rename": evidence["held_before_rename"] and evidence["held_after_rename"],
            "native_rename_recorded": evidence["renamed_native_name"] == NAME,
            "registration_completed": evidence["registration_completed"],
            "settings_restored": evidence["settings_restored"],
            "carried_origins_preserved": evidence["carried_origins_preserved"]}


def assert_early(factors, directory):
    evidence = read_json(staging_path(directory) / "early-evidence.json")
    if evidence["factors"] != factors:
        raise ValueError("BIRTH-EARLY-EVIDENCE: factors changed")
    actual = projection(evidence)
    if actual != expected(factors):
        raise ValueError(f"BIRTH-EARLY-PROJECTION: expected={expected(factors)!r} actual={actual!r}")
    return actual


if __name__ == "__main__":
    try:
        operation, *arguments = sys.argv[1:]
        value = {"supported": supported, "expected": expected,
                 "assert": assert_early, "run": run}[operation](*arguments)
        print(json.dumps(value, sort_keys=True))
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print("BIRTH-EARLY-ERROR: " + str(error), file=sys.stderr)
        sys.exit(2)
