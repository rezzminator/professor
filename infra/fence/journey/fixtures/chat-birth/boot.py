#!/usr/bin/env python3
"""Stage native startup faults and retain observations without answering trust."""
import json
import os
from pathlib import Path
import sys
import tempfile

from bootstrap import wait
from faults import owned_path
from observation import process_snapshot, report, terminal, terminal_births
from observation_process import engine_births
from runtime import now_ms, read_json, write_json


def supported(factors):
    rows = read_json(Path(__file__).with_name("boot-rows.json"))
    matches = [row for row in rows if row["factors"] == factors]
    if len(matches) != 1:
        raise ValueError("BIRTH-BOOT-FACTORS: " + factors)
    return matches[0]


def staging_path(directory):
    return owned_path(directory)


def prepare(factors, directory):
    row = supported(factors)
    directory = staging_path(directory)
    gates = [directory / "boot-ready.json", directory / "boot-stdout.json"]
    if any(path.exists() for path in gates) or (directory / "boot-input.json").exists():
        raise ValueError("BIRTH-BOOT-REUSED: staging directory already prepared")
    value = {**read_json(Path(__file__).with_name("scenario.json")), **row["scenario"],
             "ready_hold": str(gates[0]), "ready_timeout_ms": 30000}
    if row["kind"] == "stdout":
        value["app_server_stdout_hold"] = str(gates[1])
    path = directory / "boot-input.json"
    write_json(path, value)
    return str(path)


def native_records(path):
    return [json.loads(line) for line in path.read_text().splitlines() if line]


def actual_birth(path):
    values = wait("native mock birth " + str(path), lambda: native_records(path) if path.is_file() else None, 15)
    if len(values) != 1:
        raise ValueError(f"BIRTH-BOOT-AMBIGUOUS: {path}: {values!r}")
    value = values[0]
    parent = value.get("ppid", value.get("parent_pid"))
    if any(type(number) is not int or number <= 0 for number in
           (value["pid"], value["start"], parent, value["pgid"])) or not Path(value["cwd"]).is_absolute():
        raise ValueError(f"BIRTH-BOOT-RECORD: {path}: {value!r}")
    return value


def after_entry(factors, directory, target_file):
    row = supported(factors)
    if row["kind"] == "stdout":
        raise ValueError("BIRTH-BOOT-STDOUT-ENTRY: use stdout-after-entry after the public app-server helper")
    directory = staging_path(directory)
    metadata = read_json(directory / "scenario.json")
    scenario = read_json(metadata["scenario"])
    expected = read_json(directory / "boot-input.json")
    if any(scenario.get(key) != value for key, value in expected.items()):
        raise ValueError("BIRTH-BOOT-SCENARIO: normal scenario staging dropped boot input")
    records = Path(metadata["records"])
    birth = actual_birth(records / "birth.json")
    evidence = {"factors": factors, "birth": birth, "owner": read_json(target_file), "held_at_ms": now_ms()}
    gate = directory / "boot-ready.json"
    if gate.exists():
        raise ValueError("BIRTH-BOOT-READY: startup gate opened before birth observation")
    try:
        if row.get("remain_on_exit"):
            def retained():
                value = report(["tmux", "-S", metadata["launch_socket"], "display-message", "-p",
                                "-t", Path(metadata["launch_socket"]).name + ":", "#{remain-on-exit}"]).strip()
                return value if value == "on" else None
            evidence["held_remain_on_exit"] = wait("launch watcher retains held native pane", retained, 5)
    finally:
        write_json(gate, {"release": True})
    evidence["released_at_ms"] = now_ms()
    write_json(directory / "boot-evidence.json", evidence)
    return evidence


def stdout_after_entry(factors, directory, prefix):
    if supported(factors)["kind"] != "stdout":
        raise ValueError("BIRTH-BOOT-STDOUT-FACTORS: app-server stdout row required")
    directory = staging_path(directory)
    metadata = read_json(directory / "scenario.json")
    scenario = read_json(metadata["scenario"])
    if scenario.get("app_server_stdout_hold") != str(directory / "boot-stdout.json"):
        raise ValueError("BIRTH-BOOT-STDOUT-SCENARIO: app-server stdout gate missing")
    records = Path(metadata["records"])
    try:
        # This helper has no interactive SessionStart; release only its startup gate.
        write_json(directory / "boot-ready.json", {"release": True})
        child = actual_birth(records / "app-server-stdout-child.json")
        parent = actual_birth(records / "birth.json")
        wait("public helper completed with retained stdout", lambda: Path(prefix + ".status").is_file(), 20)
        reaped = wait("public helper stdout descendant reaped", lambda: child["pid"] not in process_snapshot(), 5)
        projection = {"private_pgid": child["pgid"] == parent["pgid"] == parent["pid"],
                      "actual_stdout_descendant": child.get("ppid", child.get("parent_pid")) == parent["pid"],
                      "stdout_gate_closed_at_completion": not (directory / "boot-stdout.json").exists(),
                      "stdout_child_reaped": reaped}
        evidence = {"birth": parent, "stdout_child": child, "projection": projection,
                    "status": Path(prefix + ".status").read_text().strip(),
                    "output": Path(prefix + ".out").read_text(), "completed_at_ms": now_ms()}
        write_json(directory / "boot-evidence.json", evidence)
        if not all(projection.values()):
            raise ValueError("BIRTH-BOOT-STDOUT: public helper did not reap its private stdout descendant: " + json.dumps(projection, sort_keys=True))
        return projection
    finally:
        write_json(directory / "boot-stdout.json", {"release": True})


def assert_boot(factors, directory, target_file):
    row = supported(factors)
    directory = staging_path(directory)
    evidence = read_json(directory / "boot-evidence.json")
    if evidence["factors"] != factors:
        raise ValueError("BIRTH-BOOT-EVIDENCE: factors changed")
    target = read_json(target_file)
    carried = read_json(os.environ["JOURNEY_CARRY"])
    metadata = read_json(directory / "scenario.json")
    matches = [chat for chat in carried["chats"] if chat["terminal"] == target and chat.get("scenario") == metadata["scenario"]]
    if len(matches) != 1:
        raise ValueError("BIRTH-BOOT-CAPTURE: one actual carried native session required")
    current = matches[0]
    display = terminal(target)
    births = engine_births({"terminal": display, "process_births": terminal_births(display)}, current["engine"])
    if not any(birth["pid"] == evidence["birth"]["pid"] for birth in births):
        raise ValueError("BIRTH-BOOT-TARGET: recorded native birth is not in captured terminal")
    projection = {"native_session_captured": True}
    evidence.update(target=target, session_id=current["session_id"])
    if row["kind"] == "trust":
        records = Path(metadata["records"])
        order = records / "startup.jsonl"
        wait("native SessionStart and emitted trust frame", lambda: order.is_file() and
             any(row["event"] == "trust-frame-written" for row in native_records(order)), 5)
        events = native_records(order)
        expected = [{"pid": evidence["birth"]["pid"], "event": event}
                    for event in ("SessionStart-completed", "trust-frame-written")]
        if events != expected:
            raise ValueError(f"BIRTH-BOOT-ORDER: {order}: expected={expected!r} actual={events!r}")
        frame = (records / "trust-frame.txt").read_bytes().decode()
        frame_rows = [line.strip() for line in frame.splitlines() if line.strip()]
        current_rows = [line.strip() for line in display["text"].splitlines() if line.strip()]
        if row["confirm"] not in frame_rows or row["cancel"] not in frame_rows:
            raise ValueError("BIRTH-BOOT-FRAME: emitted trust rows differ from literal forecast")
        untouched = row["confirm"] in current_rows and row["cancel"] in current_rows
        evidence.update(trust_frame=frame, startup_events=events, modal_after=display)
        projection.update(confirm=row["confirm"], cancel=row["cancel"], modal_unchanged=untouched,
                          native_readiness_before_trust=True)
        if row.get("remain_on_exit"):
            if target["backend"] != "tmux":
                raise ValueError("BIRTH-BOOT-TMUX: actual tmux trust target required")
            remain = report(["tmux", "-S", target["socket"], "display-message", "-p", "-t", target["pane"], "#{remain-on-exit}"]).strip()
            projection.update(held_remain_on_exit=evidence["held_remain_on_exit"], remain_on_exit=remain)
    evidence["projection"] = projection
    write_json(directory / "boot-evidence.json", evidence)
    return projection


def self_test():
    with tempfile.TemporaryDirectory(prefix="birth-boot-") as temporary:
        previous = os.environ.get("JOURNEY_ROOT")
        os.environ["JOURNEY_ROOT"] = temporary
        try:
            rows = read_json(Path(__file__).with_name("boot-rows.json"))
            for index, row in enumerate(rows):
                directory = str(Path(temporary) / str(index))
                Path(directory).mkdir()
                actual = read_json(prepare(row["factors"], directory))
                assert actual["ready_hold"] == directory + "/boot-ready.json", actual
                assert actual["ready_timeout_ms"] == 30000, actual
                assert all(actual[key] == value for key, value in row["scenario"].items()), actual
                assert actual.get("app_server_stdout_hold") == (directory + "/boot-stdout.json" if row["kind"] == "stdout" else None), actual
                try:
                    prepare(row["factors"], directory)
                except ValueError as error:
                    assert str(error) == "BIRTH-BOOT-REUSED: staging directory already prepared", str(error)
                else:
                    raise AssertionError("reused startup gate accepted")
            print("BIRTH-BOOT-SELFTEST PASS: native inputs and reuse guards")
        finally:
            if previous is None:
                os.environ.pop("JOURNEY_ROOT", None)
            else:
                os.environ["JOURNEY_ROOT"] = previous


if __name__ == "__main__":
    try:
        operation, *arguments = sys.argv[1:]
        if operation == "self-test":
            self_test()
        else:
            value = {"prepare": prepare, "after-entry": after_entry, "assert": assert_boot,
                     "stdout-after-entry": stdout_after_entry, "supported": supported}[operation](*arguments)
            print(value if isinstance(value, str) else json.dumps(value, sort_keys=True))
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print("BIRTH-BOOT-ERROR: " + str(error), file=sys.stderr)
        sys.exit(2)
