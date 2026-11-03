#!/usr/bin/env python3
"""Managed Claude births with real launch-time reader inputs."""
import errno
import os
from pathlib import Path
import shlex
import stat

from forecast import (action, bounded_check, check, checked_owner, outside_checks,
                      responsive_owner, stopped_checks)
from bootstrap import wait
from chat import before, capture
from chat_carry import carry
import faults
from herdr import request
from observation import bundle, process_snapshot
from observation_process import environment
from projection import bundle_checks, roster_receipt, selected, text_fleet_checks
from runtime import now_ms, read_json, write_text
from terminal import type_command

PLANS = {"settings_json=unreadable,engine=claude_code,telemetry_env=user_otlp_set": ("launch-unreadable-settings", "launch-reader"),
         "tmux_server=sigstopped,caller=claude_launch_outside_pane": ("outside-launcher-version", "launcher")}
ARGV = ["pfm", "ls", "--all", "--tsv"]


def describe(name):
    if name == "outside-launcher-version":
        return {"plan": name, "entry": "launcher", "engine": "claude", "argv": ["claude", "--version"],
                "resources": ["tmux_socket", "native_version", "records", "scenario"]}
    if name != "launch-unreadable-settings":
        raise ValueError(f"NATIVE-LAUNCH-PLAN: {name}")
    return {"plan": name, "entry": "launch-reader", "engine": "claude", "argv": ARGV,
            "resources": ["branch", "role", "launch_name", "cwd", "scenario", "records", "ready_hold", "settings", "user_otlp", "native_binary"]}


def launch_arguments(inputs):
    return ["pfm", "chat", "new", "--engine", "cc", "--backend", "herdr", "--account", "1",
            "--cwd", inputs["cwd"], "--name", inputs["launch_name"]]


def log_cursor(path):
    try:
        observed = Path(path).stat()
    except FileNotFoundError:
        return None
    return {"device": observed.st_dev, "inode": observed.st_ino, "offset": observed.st_size}


def cost_messages(path, start):
    # cost-sink.log has a product-specific timestamp + writer + message format.
    actual = Path(path).stat()
    if start is not None and (actual.st_dev, actual.st_ino, actual.st_size < start["offset"]) != (start["device"], start["inode"], False):
        raise ValueError(f"NATIVE-COST-LOG-CHANGED: {path}")
    with Path(path).open("rb") as stream:
        stream.seek(start["offset"] if start else 0)
        content = stream.read().decode()
    return [line.partition(" cost-launch: ")[2] for line in content.splitlines() if " cost-launch: " in line]


def launch_checks(inputs, birth, process, env, observed_errno, messages, gate_exists, startup_exists):
    settings, pid = inputs["settings"], birth["pid"]
    expected_log = [f"settings.json unreadable, so the user's telemetry settings cannot be ruled out: read {settings}: read {settings}: is a directory",
                    f"pre-empted, no cost ledger for pid {pid}: the launch environment already sets OTEL_EXPORTER_OTLP_ENDPOINT"]
    relevant = [line for line in messages if settings in line or f" for pid {pid}:" in line]
    executable = shlex.split(process["argv"])[0]
    return [check("launch-settings-native-errno", errno.EISDIR, observed_errno),
            check("launch-native-pid", pid, process["pid"]),
            check("launch-native-executable", str(Path(inputs["native_binary"]).resolve(strict=True)), str(Path(executable).resolve(strict=True))),
            check("launch-native-scenario", inputs["scenario"], env.get("MOCK_ENGINE_SCENARIO")),
            check("launch-native-account", str(Path(settings).parent), env.get("CLAUDE_CONFIG_DIR")),
            check("launch-user-otlp-retained", inputs["user_otlp"], env.get("OTEL_EXPORTER_OTLP_ENDPOINT")),
            check("launch-cost-preempted", "preempted", env.get("PFM_COST_LEDGER")),
            check("launch-no-owned-cost-sink", None, env.get("PFM_COST_LEDGER_PID")),
            check("launch-native-diagnostic", expected_log, relevant),
            check("launch-ready-gate-closed", False, gate_exists),
            check("launch-before-native-hooks", False, startup_exists)]


def captured_checks(inputs, chat, observed):
    return [check("launch-root-identity", {"origin": "new-from-cli", "role": inputs["role"], "branch": inputs["branch"], "name": inputs["launch_name"], "account": 1},
                  {key: chat.get(key) for key in ("origin", "role", "branch", "name", "account")}),
            check("launch-root-parent-edge", None, chat.get("parent_key")),
            check("launch-root-public-session", chat["session_id"], observed["status"].get("session_id")),
            check("launch-root-public-state", "idle", observed["status"]["state"])]


def cost_processes(birth, processes):
    expected = ["internal", "cost-sink", "--pid", str(birth["pid"]), "--start", str(birth["start"])]
    return [{key: row[key] for key in ("pid", "start", "argv")} for row in processes.values()
            if shlex.split(row["argv"])[1:] == expected]


def version_restored_checks(birth, actual_birth, previous, actual_inventory):
    return [check("launcher-restored-server-birth", birth, actual_birth),
            check("launcher-final-inventory", previous, actual_inventory)]


def inventory():
    panes = request(os.environ["HERDR_SOCKET_PATH"], "pane.list", {})["result"]["panes"]
    fields = ("pane_id", "terminal_id", "workspace_id", "tab_id")
    sockets = []
    for path in faults.owned_path(os.environ["PFM_TMUX_DIR"]).iterdir():
        metadata = path.lstat()
        if stat.S_ISSOCK(metadata.st_mode):
            sockets.append({"path": str(path), "device": metadata.st_dev, "inode": metadata.st_ino})
    return {"panes": sorted([{key: row[key] for key in fields} for row in panes], key=lambda row: row["pane_id"]),
            "sockets": sorted(sockets, key=lambda row: row["path"])}


def version_checks(inputs, receipt, argv, born, before, after):
    return [check("launcher-version-status", 0, receipt["exit_code"]),
            check("launcher-native-version", inputs["native_version"] + "\n", receipt["output"]),
            check("launcher-native-version-argv", "claude\n--version\n", argv),
            check("launcher-no-chat-birth", False, born),
            check("launcher-owned-pane-and-socket-inventory", before, after), bounded_check(receipt["elapsed_ms"], 10000)]


def run_version(owner_file, inputs, prefix, require):
    owner = read_json(owner_file)
    if owner.get("backend") != "pty":
        raise ValueError("NATIVE-LAUNCH-OWNER: independent plain PTY required")
    observed_owner = responsive_owner(owner, inputs["tmux_socket"])
    records = faults.owned_path(inputs["records"])
    declared = read_json(faults.owned_path(inputs["scenario"]))
    if declared.get("version") != inputs["native_version"] or declared["record_dir"] != str(records):
        raise ValueError("NATIVE-LAUNCH-VERSION: declared version and native recording directory required")
    evidence = {"plan": "outside-launcher-version", "checks": [], "before": bundle(owner_file)}
    require(prefix, evidence, outside_checks(owner, observed_owner))
    previous = inventory()
    with faults.stopped_tmux(inputs["tmux_socket"]) as birth:
        evidence["stopped_birth"] = birth
        require(prefix, evidence, stopped_checks(birth))
        started = now_ms()
        receipt = {**action(owner, ["claude", "--version"], str(prefix) + ".command"),
                   "prefix": str(prefix) + ".command", "door": "typed"}
        receipt["elapsed_ms"] = now_ms() - started
        evidence["receipt"], evidence["during"] = receipt, bundle(owner_file)
        require(prefix, evidence, version_checks(inputs, receipt, (records / "argv").read_text(),
                (records / "birth.json").exists(), previous, inventory())
                + bundle_checks(evidence["during"], carry(), evidence["before"]))
    evidence["after"] = bundle(owner_file)
    require(prefix, evidence, version_restored_checks(birth, faults.tmux_birth(inputs["tmux_socket"]), previous, inventory())
            + bundle_checks(evidence["after"], carry(), evidence["before"]))
    return evidence


def run(name, chat_file, owner_file, inputs, prefix, require, public_state):
    describe(name)
    if name == "outside-launcher-version":
        return run_version(owner_file, inputs, prefix, require)
    owner = read_json(owner_file)
    if owner.get("backend") != "pty":
        raise ValueError("NATIVE-LAUNCH-OWNER: independent plain PTY required")
    checked_owner(owner)
    prior_exporter = environment(owner["pid"]).get("OTEL_EXPORTER_OTLP_ENDPOINT")
    for key in ("cwd", "scenario", "records", "settings"):
        faults.owned_path(inputs[key])
    records = Path(inputs["records"])
    scenario = read_json(inputs["scenario"])
    if scenario.get("ready_hold") != inputs["ready_hold"] or Path(inputs["ready_hold"]).exists():
        raise ValueError("NATIVE-LAUNCH-HOLD: actual unopened native ready gate required")
    log = Path.home() / ".local/state/pfm/log/cost-sink.log"
    start, previous = log_cursor(log), before(owner)
    evidence = {"plan": name, "checks": [], "before": bundle(owner_file), "receipts": []}
    type_command(owner, ["export", "OTEL_EXPORTER_OTLP_ENDPOINT=" + inputs["user_otlp"]])
    try:
        with faults.file_read_error(inputs["settings"]):
            try:
                read_json(inputs["settings"])
            except OSError as error:
                observed_errno = error.errno
            else:
                raise ValueError("NATIVE-LAUNCH-READ-SUCCEEDED: owned settings fault did not deny a native read")
            launched = action(owner, launch_arguments(inputs), str(prefix) + ".launch")
            evidence["launch_receipt"] = launched
            evidence["receipts"].append({"prefix": str(prefix) + ".launch", "argv": launch_arguments(inputs)})
            require(prefix, evidence, [check("launch-public-status", 0, launched["exit_code"])])
            wait("managed native child reached startup gate", lambda: (records / "birth.json").exists(), 20)
            born = read_json(records / "birth.json")
            process = process_snapshot()[born["pid"]]
            faults.checked_birth({key: process[key] for key in ("pid", "start")})
            native_env = environment(born["pid"])
            messages = cost_messages(log, start)
            evidence["native"] = {"birth": born, "process": process, "environment": native_env, "cost_log": messages, "settings_errno": observed_errno}
            require(prefix, evidence, launch_checks(inputs, born, process, native_env, observed_errno, messages,
                    Path(inputs["ready_hold"]).exists(), (records / "startup.jsonl").exists()))
            evidence["native"]["cost_sink_processes"] = cost_processes(born, process_snapshot())
            require(prefix, evidence, [check("launch-native-cost-sink-processes", [], evidence["native"]["cost_sink_processes"])])
            started = now_ms()
            receipt = {**action(owner, ARGV, str(prefix) + ".command"), "prefix": str(prefix) + ".command", "door": "typed"}
            evidence["receipt"] = receipt
            diagnostics, table = roster_receipt(receipt["output"])
            require(prefix, evidence, [check("launch-list-status", 0, receipt["exit_code"]), check("launch-list-diagnostics", [], diagnostics),
                                      bounded_check(now_ms() - started, 10000), *text_fleet_checks(table, carry())])
            evidence["during"] = bundle(owner_file)
            require(prefix, evidence, bundle_checks(evidence["during"], carry(), evidence["before"]))
    finally:
        type_command(owner, ["unset", "OTEL_EXPORTER_OTLP_ENDPOINT"] if prior_exporter is None else ["export", "OTEL_EXPORTER_OTLP_ENDPOINT=" + prior_exporter])
        write_text(inputs["ready_hold"], "reader-settings-restored\n")
        if (records / "birth.json").exists():
            child = wait("actual root CLI launch captured after settings restoration", lambda: capture(owner, inputs["branch"], inputs["role"], previous), 30)
            wait("actual captured root native session is idle", lambda: public_state(child).get("state") == "idle", 20)
            evidence["launched_chat"] = child
            evidence["after"] = bundle(owner_file)
            require(prefix, evidence, captured_checks(inputs, child, selected(evidence["after"], child))
                    + bundle_checks(evidence["after"], carry(), evidence["before"]))
    return evidence
