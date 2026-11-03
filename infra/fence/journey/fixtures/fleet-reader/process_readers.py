#!/usr/bin/env python3
"""Real watch, reap preview and theme reads over run-owned process fixtures."""
from contextlib import contextmanager, ExitStack
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import sys

from forecast import action, check, checked_owner
from bootstrap import wait, tmux_server
from chat import inline
import faults
from observation import bundle, process_snapshot, read_actual_sidechains, report, reporter_activity, screen
from runtime import command, now_ms, read_json, write_json, write_text
from terminal import type_command

# Claude's recorded light fixture: userMessageBackground rgb(240,240,240), text rgb(0,0,0).
LIGHT_STATUS_STYLE = "bg=#f0f0f0,fg=#000000"


def owned_prefix(value):
    prefix = Path(value)
    prefix.parent.resolve(strict=True).relative_to(Path(os.environ["JOURNEY_ROOT"]).resolve(strict=True))
    if prefix.is_symlink():
        raise ValueError("PROCESS-READER-PREFIX-SYMLINK")
    return str(prefix)


def resources_for(operation, value):
    resources = dict(value)
    suffix = hashlib.sha256(os.environ["PFM_RUN"].encode()).hexdigest()[:12]
    roles = {"reap": ("dead", "foreign"), "theme": ("stopped",)}.get(operation, ())
    for role in roles:
        field = role + "_socket"
        if field not in resources:
            alias = "cc-reader-" + ("theme" if role == "stopped" else role) + "-" + suffix
            target = tmux_server(alias, session="reader-" + role)
            resources[field] = target["socket"]
            resources[role + "_target"] = target
    return resources


def status(chat):
    return json.loads(report(["pfm", "chat", "status", chat["session_id"], "--json"], allowed_codes=(0, 3)))


def child_after_main_stop(value, agent_id):
    return value if isinstance(value, dict) and value["active_child_ids"] == [agent_id] and \
        value["state"] == "working" and value.get("main_stopped_at", 0) > 0 else None


def child_completion_checks(rows, pid, shell):
    fields = ("pid", "command", "stdout", "stderr", "exit_code")
    return [check("native-child-completion", [{"pid": pid, "command": shell,
                   "stdout": "reader-child-finished", "stderr": "", "exit_code": 0}],
                  [{field: row[field] for field in fields} for row in rows if row.get("pid") == pid])]


def watch_events(text, name):
    events = []
    for line in text.splitlines():
        fields = shlex.split(line)
        if len(fields) < 2 or fields[1] != name:
            raise ValueError(f"PROCESS-WATCH-FOREIGN-OR-MALFORMED: {line!r}")
        if fields[0] == "SEEN" and len(fields) == 3:
            events.append({"event": "SEEN", "state": fields[2]})
        elif fields[0] == "IDLE" and len(fields) == 3 and re.fullmatch(r"idle_seconds=\d+", fields[2]):
            events.append({"event": "IDLE", "state": "idle"})
        else:
            raise ValueError(f"PROCESS-WATCH-UNEXPECTED: {line!r}")
    return events


def retained_checks(first, second, dead, busy, foreign):
    def projection(value):
        return {group: sorted([{key: row.get(key) for key in ("socket", "state", "reason", "label", "cwd")}
                              for row in value[group]], key=lambda row: row["socket"])
                for group in ("reap", "spared", "unknown")}
    checks = [check("preview-apply", False, first["apply"]), check("preview-killed", 0, first["killed"]),
              check("preview-failed", 0, first["failed"]),
              check("only-dead-owned-socket-removable", [dead], sorted(row["socket"] for row in first["reap"])),
              check("dead-socket-classification", ["dead"], [row["state"] for row in first["reap"] if row["socket"] == dead]),
              check("busy-child-retained", [{"state": "active", "reason": "transcript written within 1m0s"}],
                    [{key: row[key] for key in ("state", "reason")} for row in first["spared"] if row["socket"] == busy]),
              check("foreign-owner-retained", ["hosts"], [row["state"] for row in first["spared"] if row["socket"] == foreign]),
              check("repeat-preview-classifications", projection(first), projection(second)),
              check("repeat-preview-apply", False, second["apply"]), check("repeat-preview-killed", 0, second["killed"])]
    return checks


def probe_clients(snapshot, socket, server_pid):
    matches = []
    for row in snapshot.values():
        if row["pid"] == server_pid or socket not in row["argv"]:
            continue
        argv = shlex.split(row["argv"])
        if argv and Path(argv[0]).name == "tmux" and any(argv[index:index + 2] == ["-S", socket] for index in range(len(argv) - 1)):
            matches.append({"pid": row["pid"], "start": row["start"], "argv": argv})
    return sorted(matches, key=lambda row: row["pid"])


def theme_checks(value, socket, elapsed, clients):
    report_value = value["report"]
    rows = [row for row in report_value["surfaces"] if row["surface"] == "tmux"]
    detail = rows[0]["detail"] if len(rows) == 1 else ""
    failures = []
    patterns = (
        r"themesync: list tmux servers: read tmux server pid on ([^:]+): (tmux server did not answer): (signal: killed)",
        r"1 of \d+ servers? failed: tmux ([^:]+): read tmux option ([^ ]+) status-style: (tmux server did not answer): (signal: killed)",
    )
    lines = detail.splitlines()
    for index, line in enumerate(lines):
        match = re.fullmatch(patterns[index], line) if index < len(patterns) else None
        failures.append(list(match.groups()) if match else [line])
    alias = Path(socket).name
    expected = [[alias, "tmux server did not answer", "signal: killed"],
                [alias, alias, "tmux server did not answer", "signal: killed"]]
    return [check("chosen-theme", "light", value["use"]["value"]),
            check("theme-account-refusals", [], value["use"]["refused"]),
            check("theme-enabled", True, report_value["enabled"]),
            check("theme-source", "light", report_value["source"]["value"]),
            check("stopped-theme-surface", ["ERROR"], [row["verdict"] for row in rows]),
            check("stopped-theme-failure", expected, failures),
            check("theme-probe-bounded", "within-15s", "within-15s" if elapsed <= 15000 else elapsed),
            check("blocked-probe-clients", [], clients)]


def theme_style_checks(before, after):
    return [check("healthy-status-style", LIGHT_STATUS_STYLE, after),
            check("healthy-status-style-changed", True, before != after)]


def theme_source_checks(paths, values):
    return [check("account-settings-source-theme", {path: "light" for path in paths}, values)]


@contextmanager
def background(chat, prefix, evidence):
    gate, child_pid = prefix + ".hold", prefix + ".child.pid"
    agent_id = "reader-child-" + os.environ["PFM_RUN"]
    shell = f"printf '%s\\n' $$ > {shlex.quote(child_pid)}; bash -c " + shlex.quote(
        'deadline=$((SECONDS+50)); while test -f "$1" && (( SECONDS<deadline )); do sleep .02; done; '
        'if test -f "$1"; then exit 124; fi; printf reader-child-finished') + " reader-child " + shlex.quote(gate)
    reply = "reader-main-finished:" + os.environ["PFM_BEAT"]
    steps = [{"type": "background_agent", "name": "reader-child", "agent_id": agent_id,
              "agent_type": "general-purpose", "input": {"command": shell}},
             {"type": "turn", "reply": reply, "busy_ms": 20}]
    step_file = prefix + ".steps.json"
    write_json(step_file, steps)
    evidence["expected_input"] = "reader-process mock-engine: " + json.dumps(steps, separators=(",", ":"))
    prompts = Path(chat["records"]) / "prompts"
    offset = prompts.stat().st_size if prompts.exists() else 0
    pid = None
    with faults.temporary_text(gate, "reader child remains active\n"):
        try:
            inline(chat["terminal"], step_file, "reader-process")
            wait("actual background child shell pid receipt", lambda: Path(child_pid).is_file(), 15)
            pid = int(Path(child_pid).read_text().strip())
            birth = process_snapshot().get(pid)
            if birth is None:
                raise ValueError("PROCESS-CHILD-ABSENT: actual shell pid exited before observation")
            evidence["child_birth"] = {"pid": pid, "start": birth["start"]}
            wait("real main reply completes while child gate stays held", lambda: reply in screen(chat["terminal"]), 15)
            activity = "n/a:tmux-reporter"
            if chat["terminal"]["backend"] == "herdr":
                activity = wait("native background child holds working after actual main Stop", lambda:
                                child_after_main_stop(reporter_activity(chat), agent_id), 15)
            evidence["child_activity"] = activity
            evidence["sidechains"] = read_actual_sidechains(chat)
            evidence["checks"].append(check("actual-sidechain-identity", [agent_id],
                [row["agent_id"] for row in evidence["sidechains"]["subagents"] if row["agent_id"] == agent_id]))
            yield activity
        finally:
            with faults.temporarily_missing_file(gate):
                if pid is not None:
                    wait("background child shell completed and reaped", lambda: pid not in process_snapshot(), 15)
                if pid is not None and chat["terminal"]["backend"] == "herdr":
                    wait("native SubagentStop clears active child", lambda: isinstance(value := reporter_activity(chat), dict) and not value["active_child_ids"], 15)
    evidence["actual_input"] = prompts.read_bytes()[offset:].decode().rstrip("\n")
    receipts = Path(chat["records"]) / "shell.jsonl"
    def completed():
        if not receipts.exists():
            return None
        rows = [json.loads(line) for line in receipts.read_text().splitlines(keepends=True) if line.endswith("\n")]
        return rows if any(row.get("pid") == pid for row in rows) else None
    rows = wait("completed native background shell receipt", completed, 5)
    evidence["checks"] += child_completion_checks(rows, pid, shell)


def watch(chat, owner, resources, prefix, evidence):
    receipt = prefix + ".watch"
    arguments = ["pfm", "chat", "watch", chat["name"], "--transitions", "--once", "--poll", "10"]
    def first_snapshot():
        output = Path(receipt + ".out")
        if not output.exists():
            return False
        complete, _, _ = output.read_text().rpartition("\n")
        return watch_events(complete, chat["name"]) == [{"event": "SEEN", "state": "working"}]
    with background(chat, prefix, evidence):
        type_command(owner, arguments, receipt=receipt)
        wait("watch's first real working snapshot", first_snapshot, 15)
        evidence["checks"].append(check("watch-did-not-finish-before-child", False, Path(receipt + ".status").exists()))
        evidence["working"] = status(chat)
        evidence["checks"].append(check("child-still-working", "working", evidence["working"]["state"]))
        evidence["working_bundle"] = bundle(evidence["owner_file"])
    wait("same watch process emits idle after real child exit", lambda: Path(receipt + ".status").is_file(), 25)
    evidence["checks"] += [check("watch-exit", 0, int(Path(receipt + ".status").read_text())),
        check("same-target-watch-transitions", [{"event": "SEEN", "state": "working"}, {"event": "IDLE", "state": "idle"}],
              watch_events(Path(receipt + ".out").read_text(), chat["name"]))]
    evidence["receipts"].append({"prefix": receipt, "argv": arguments})


def reap(chat, owner, resources, prefix, evidence):
    dead, foreign = (str(faults.owned_path(resources[key])) for key in ("dead_socket", "foreign_socket"))
    for value in (dead, foreign):
        if Path(value).parent.resolve() != Path(os.environ["PFM_TMUX_DIR"]).resolve() or not Path(value).is_socket():
            raise ValueError(f"PROCESS-REAP-SOCKET: real owned socket under PFM_TMUX_DIR required: {value}")
    foreign_birth = faults.tmux_birth(foreign)
    with background(chat, prefix, evidence):
        target = resources.get("dead_target", {"pane": "reader-dead:0.0"})
        restore = lambda: tmux_server(Path(dead).name, session=target["pane"].partition(":")[0])
        # Start the engine's real child before aging: setup readers may sweep old socket files.
        with faults.dead_tmux(dead, restore) as birth, faults.aged_files([dead], 3601, kind="socket"):
            resources["dead_birth"] = birth
            evidence["checks"].append(check("stale-socket-age", "older-than-one-hour",
                "older-than-one-hour" if now_ms() - Path(dead).stat().st_mtime_ns / 1_000_000 > 3_600_000 else "too-young"))
            arguments = ["pfm", "reap", "--json"]
            first = action(owner, arguments, prefix + ".reap-first")
            second = action(owner, arguments, prefix + ".reap-second")
            for label, receipt in (("first", first), ("second", second)):
                evidence["checks"].append(check("preview-" + label + "-exit", 0, receipt["exit_code"]))
                evidence["receipts"].append({"prefix": prefix + ".reap-" + label, "argv": arguments})
            first_value, second_value = json.loads(first["output"]), json.loads(second["output"])
            evidence["checks"] += retained_checks(first_value, second_value, Path(dead).name,
                                                    Path(chat["terminal"]["socket"]).name, Path(foreign).name)
        restored = faults.tmux_birth(dead)
        evidence["checks"].append(check("dead-auxiliary-server-restored", "new-birth", "new-birth" if restored != birth else "old-birth"))
        child = evidence["child_birth"]
        evidence["checks"].append(check("busy-child-birth-survives-preview", child["start"],
                                        process_snapshot().get(child["pid"], {}).get("start")))
        evidence["checks"].append(check("foreign-server-birth-survives-preview", foreign_birth, faults.tmux_birth(foreign)))


def theme(chat, owner, resources, prefix, evidence):
    socket = str(faults.owned_path(resources["stopped_socket"]))
    healthy = str(faults.owned_path(chat["terminal"]["socket"]))
    carry = read_json(os.environ["JOURNEY_CARRY"])
    if socket in {healthy, owner.get("socket"), carry["foreign"]["terminal"].get("socket")}:
        raise ValueError("PROCESS-THEME-STOPPED-OWNER-OR-SENTINEL")
    with ExitStack() as scopes:
        for path in resources["account_settings"]:
            scopes.enter_context(faults.preserved_file(path))
        previous = action(owner, ["pfm", "theme", "status", "--json"], prefix + ".theme-before")
        evidence["receipts"].append({"prefix": prefix + ".theme-before", "argv": ["pfm", "theme", "status", "--json"]})
        evidence["checks"].append(check("theme-before-exit", 0, previous["exit_code"]))
        if json.loads(previous["output"])["enabled"]:
            raise ValueError("PROCESS-THEME-PRECONDITION: predecessor must leave theme sync off")
        try:
            enabled = action(owner, ["pfm", "theme", "on", "--json"], prefix + ".theme-on")
            evidence["receipts"].append({"prefix": prefix + ".theme-on", "argv": ["pfm", "theme", "on", "--json"]})
            evidence["checks"].append(check("theme-on-exit", 0, enabled["exit_code"]))
            previous_style = command(["tmux", "-S", healthy, "show-options", "-gqv", "status-style"]).strip()
            with faults.stopped_tmux(socket) as birth:
                started = now_ms()
                arguments = ["pfm", "theme", "use", "light", "--json"]
                receipt = action(owner, arguments, prefix + ".theme-use", 25)
                elapsed = now_ms() - started
                # use emits its summary diagnostic before its JSON report on the combined receipt.
                raw = receipt["output"]
                start = raw.find("{\n")
                if start < 0:
                    raise ValueError("PROCESS-THEME-REPORT-ABSENT")
                value = json.loads(raw[start:])
                evidence["checks"] += theme_checks(value, socket, elapsed, probe_clients(process_snapshot(), socket, birth["pid"]))
                evidence["checks"].append(check("stopped-theme-exit", 1, receipt["exit_code"]))
                evidence["checks"].append(check("healthy-server-theme", "light", command(
                    ["tmux", "-S", healthy, "show-options", "-gqv", "@pfm-theme"]).strip()))
                current_style = command(["tmux", "-S", healthy, "show-options", "-gqv", "status-style"]).strip()
                evidence["checks"] += theme_style_checks(previous_style, current_style)
                evidence["settings_theme"] = {path: read_json(path).get("theme") for path in resources["account_settings"]}
                evidence["checks"] += theme_source_checks(resources["account_settings"], evidence["settings_theme"])
                evidence["stopped_birth"] = birth
                evidence["receipts"].append({"prefix": prefix + ".theme-use", "argv": arguments})
                evidence["themed_bundle"] = bundle(evidence["owner_file"])
        finally:
            restored = action(owner, ["pfm", "theme", "off", "--json"], prefix + ".theme-off")
            evidence["receipts"].append({"prefix": prefix + ".theme-off", "argv": ["pfm", "theme", "off", "--json"]})
            evidence["checks"].append(check("theme-off-exit", 0, restored["exit_code"]))
            evidence["checks"].append(check("theme-off-disabled", False, json.loads(restored["output"])["enabled"]))


def run(operation, chat_file, owner_file, resources_file, prefix):
    prefix = owned_prefix(prefix)
    chat, owner = read_json(chat_file), read_json(owner_file)
    faults.owned_target(chat["terminal"])
    checked_owner(owner)
    resources = resources_for(operation, read_json(resources_file))
    evidence = {"checks": [], "receipts": [], "resources": resources, "owner_file": owner_file, "before": bundle(owner_file)}
    functions = {"watch": watch, "reap": reap, "theme": theme}
    if operation not in functions:
        raise ValueError(f"PROCESS-READER-ENTRY-UNAVAILABLE: {operation}")
    functions[operation](chat, owner, resources, prefix, evidence)
    evidence["after"] = bundle(owner_file)
    write_json(prefix + ".checks.json", evidence["checks"])
    write_json(prefix + ".evidence.json", evidence)
    for label in ("expected_input", "actual_input"):
        if label in evidence:
            write_text(prefix + "." + label + ".txt", evidence[label] + "\n")


if __name__ == "__main__":
    try:
        if sys.argv[1] != "run":
            raise ValueError("usage: process_readers.py run watch|reap|theme CHAT OWNER RESOURCES PREFIX")
        run(*sys.argv[2:])
    except (OSError, ValueError, KeyError, TypeError, RuntimeError) as error:
        print(f"PROCESS-READER-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
