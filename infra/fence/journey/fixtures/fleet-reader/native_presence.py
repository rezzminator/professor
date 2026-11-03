#!/usr/bin/env python3
"""A real quiet picker notices a raw native chat's dead auxiliary server."""
import json
import math
import os
from pathlib import Path
import shlex
import sys

from forecast import HERE, action, check, checked_owner
import activity
from bootstrap import tmux_server, wait
from chat_carry import carry, stored
import faults
from observation import bundle, process_metrics, process_snapshot, report, screen, terminal, terminal_births
from observation_picker import select_session, selected_line, session
from projection import bundle_checks, carried_rows, roster_receipt, text_fleet_checks, text_rows, transcript
from runtime import command, now_ms, read_json, write_json
from terminal import keys, text, type_command
import native_actor

PLANS = {"chat_row=resumable_or_bg,tmux_socket=gone_or_refusing,picker_state=parked": ("quiet-picker-dead-server", "presence-reader")}
ARGV = ["pfm", "ls", "--all", "--tsv"]
PROMPT, REPLY = "reader-presence-native", "reader-presence-answer"
QUIET_MS, MAX_OWN_CPU_FRACTION = 12000, .02


def describe(name):
    if name != "quiet-picker-dead-server":
        raise ValueError(f"NATIVE-PRESENCE-PLAN: {name}")
    return {"plan": name, "entry": "presence-reader", "engine": "claude", "argv": ARGV,
            "resources": ["subject", "socket_name", "session", "scenario", "records", "session_id", "native_binary", "cwd", "config_dir", "picker", "name"]}


def require(prefix, evidence, checks):
    from native_forecasts import require as require_conditions
    return require_conditions(prefix, evidence, checks)


def scans(rows, pid, run, *, successful=True):
    result = []
    for row in rows:
        if (row.get("run"), row.get("pid"), row.get("op"), row.get("subcmd")) != (run, pid, "tmux.exec", "list-panes"):
            continue
        if type(row.get("exit")) is not int:
            raise ValueError(f"NATIVE-PRESENCE-SCAN-SHAPE: {row!r}")
        if not successful or row["exit"] == 0:
            result.append(row)
    return result


def view_failures(shown):
    return [marker for marker in ("⟳ refreshing", "fleet stale", "⚠ row facts") if marker in shown]


def own_cpu(metrics):
    value = metrics.get("cpu_seconds")
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0:
        raise ValueError(f"NATIVE-PRESENCE-CPU-UNAVAILABLE: {metrics!r}")
    return value


def quiet_checks(birth, current, previous_cpu, current_cpu, elapsed, shown):
    if elapsed <= 0 or current_cpu < previous_cpu:
        raise ValueError("NATIVE-PRESENCE-COUNTERS: elapsed time and cumulative native CPU must advance")
    fraction = (current_cpu - previous_cpu) / (elapsed / 1000)
    return [check("quiet-picker-native-birth", birth, {key: current[key] for key in ("pid", "start")}),
            check("quiet-picker-native-state", "running", "running" if current["state"] not in {"T", "Z", "X"} else current["state"]),
            check("quiet-picker-view-failures", [], view_failures(shown)),
            check("quiet-picker-observed-interval", "at-least-12s", "at-least-12s" if elapsed >= QUIET_MS else elapsed),
            check("quiet-picker-own-cpu-fraction", "at-most-2%-of-one-core", "at-most-2%-of-one-core" if fraction <= MAX_OWN_CPU_FRACTION else fraction)]


def picker_birth(target):
    births = terminal_births(terminal(target))
    matches = []
    for birth in births:
        argv = shlex.split(birth["chain"][0]["argv"])
        if argv and Path(argv[0]).name == "pfm" and argv[1:] == []:
            matches.append({key: birth[key] for key in ("pid", "start")})
    if len(matches) != 1:
        return False
    faults.checked_birth(matches[0])
    return matches[0]


def visible_identity(shown, sid):
    line = selected_line(shown)
    # selected_line owns selection syntax; the first token after its arrow is its row marker.
    marker = line.partition("›")[2].strip().split()[0]
    return {"session_id": session(shown, [sid]), "marker": marker}


def automatic_update(shown, sid, frames):
    identity = visible_identity(shown, sid)
    if not frames or identity != {"session_id": sid, "marker": "↻"} or view_failures(shown):
        return False
    return {"screen": shown, "scans": frames, "identity": identity}


def row_checks(rows, inputs, *, resumed):
    matches = carried_rows(rows, {"session_id": inputs["session_id"]})
    fields = ("id", "name", "account", "cwd", "project")
    expected = {"id": inputs["session_id"], "name": inputs["name"], "account": "2", "cwd": inputs["cwd"], "project": inputs.get("project", "atlas")}
    kinds = [row["kind"] for row in matches]
    actual_kind = kinds if resumed else ["live-native" if kind in {"live-claude", "agent"} else kind for kind in kinds]
    return [check("raw-native-row-identity", [expected], [{key: row[key] for key in fields} for row in matches]),
            check("raw-native-row-kind", ["resume-claude"] if resumed else ["live-native"], actual_kind)]


def actual_transcript(inputs):
    document = json.loads(report(["pfm", "chat", "read", inputs["session_id"], "--tail", "2", "--json"]))
    paths = stored("SELECT path FROM transcripts WHERE uuid = ?", (inputs["session_id"],))
    if len(paths) != 1:
        raise ValueError(f"NATIVE-PRESENCE-TRANSCRIPT: expected actual indexed native path, actual={paths!r}")
    path = faults.owned_path(paths[0]["path"])
    path.relative_to(Path(inputs["config_dir"]) / "projects")
    return {"path": str(path), "document": document, "entries": transcript(document["entries"])}


def launch_raw(inputs, prefix, evidence):
    subject = inputs["subject"]
    checked_owner(subject)
    setup = {**inputs, "prefix": str(prefix) + ".raw"}
    setup_file = str(prefix) + ".raw-setup.json"
    write_json(setup_file, setup)
    records = faults.owned_path(inputs["records"])
    if (records / "birth.json").exists():
        raise ValueError("NATIVE-PRESENCE-REUSED-RECORDS: actual raw native birth must be new")
    require(prefix, evidence, [check("raw-native-turn-input", [{"type": "turn", "reply": REPLY, "busy_ms": 0}], read_json(inputs["scenario"])["steps"])])
    type_command(subject, [sys.executable, str(Path(native_actor.__file__).resolve()), "native-wrapper", setup_file])
    wait("actual raw native Claude birth", lambda: (records / "birth.json").exists(), 20)
    born = read_json(records / "birth.json")
    process = process_snapshot()[born["pid"]]
    birth = {key: process[key] for key in ("pid", "start")}
    evidence["raw"] = {"birth": birth, "native_birth": born}
    faults.checked_birth(birth)
    observed = checked_owner(subject)
    native = [row for row in terminal_births(observed) if row["pid"] == birth["pid"]]
    require(prefix, evidence, [check("raw-native-owned-pid", [birth["pid"]], [row["pid"] for row in native]),
            check("raw-native-input-scenario", [inputs["scenario"]], [row["environment"].get("MOCK_ENGINE_SCENARIO") for row in native]),
            check("raw-native-recorded-parent", born["parent_pid"], process["parent"])])
    native_actor.composer(subject, "claude")
    text(subject, PROMPT)
    keys(subject, ["enter"])
    wait("actual raw native answer painted after transcript append", lambda: REPLY in screen(subject), 20)
    def answered():
        value = actual_transcript(inputs)
        return value if value["entries"] == [{"role": "user", "text": PROMPT}, {"role": "assistant", "text": REPLY}] else False
    result = wait("actual raw native prompt and answer indexed", answered, 20)
    native_actor.composer(subject, "claude")
    text(subject, "/rename " + inputs["name"])
    keys(subject, ["enter"])
    def named():
        rows = text_rows(report(ARGV))
        return rows if not any(row["expected"] != row["actual"] for row in row_checks(rows, inputs, resumed=False)) else False
    rows = wait("actual raw native title and live public row", named, 20)
    evidence["raw"] = {"birth": birth, "native_birth": born, "transcript": result, "rows": rows, "terminal": terminal(subject)}
    require(prefix, evidence, row_checks(rows, inputs, resumed=False))
    return birth


def quiet_picker(inputs, prefix, evidence, log_start):
    target, sid = inputs["picker"], inputs["session_id"]
    type_command(target, ["export", "PFM_LOG_LEVEL=debug"])
    type_command(target, ["pfm"])
    birth = wait("actual independent picker process birth", lambda: picker_birth(target), 20)
    rows = text_rows(report(ARGV))
    selected = select_session(target, {"session_id": sid}, [row["id"] for row in rows], screen, keys)
    require(prefix, evidence, [check("quiet-picker-initial-session", sid, session(selected, [sid]))])
    log = os.environ["JOURNEY_ACTIVITY"]
    def completed():
        shown = screen(target)
        records = scans(activity.records(log, log_start), birth["pid"], os.environ["PFM_RUN"])
        return {"screen": shown, "records": records} if records and not view_failures(shown) else False
    first = wait("first native full-pass group and settled picker view", completed, 30)
    def another():
        value = completed()
        return value if value and len(value["records"]) > len(first["records"]) else False
    second = wait("second native full-pass group without another key", another, 30)
    previous = {"count": len(second["records"]), "at": now_ms(), "cpu": own_cpu(process_metrics(birth["pid"]))}
    def quiet():
        current = faults.checked_birth(birth)
        shown, at = screen(target), now_ms()
        records = scans(activity.records(log, log_start), birth["pid"], os.environ["PFM_RUN"])
        cpu = own_cpu(process_metrics(birth["pid"]))
        if len(records) != previous["count"] or view_failures(shown):
            previous.update(count=len(records), at=at, cpu=cpu)
            return False
        if at - previous["at"] < QUIET_MS:
            return False
        checks = quiet_checks(birth, current, previous["cpu"], cpu, at - previous["at"], shown)
        require(prefix, evidence, checks)
        return {"birth": birth, "first": first, "second": second, "records": records,
                "started_cpu_seconds": previous["cpu"], "ended_cpu_seconds": cpu, "elapsed_ms": at - previous["at"], "screen": shown}
    evidence["quiet"] = wait("observed quiet full-pass stream and <=2% own picker CPU for 12s", quiet, 60)
    return birth


def run(name, chat_file, owner_file, inputs, prefix):
    describe(name)
    owner, picker = read_json(owner_file), inputs["picker"]
    checked_owner(owner)
    if picker.get("backend") != "pty" or picker == owner or picker in [carry().get(key) for key in ("picker", "observer_picker")]:
        raise ValueError("NATIVE-PRESENCE-PICKER: independent plain PTY required")
    checked_owner(picker)
    faults.owned_path(Path(prefix).parent)
    picker_file = str(prefix) + ".picker.json"
    write_json(picker_file, picker)
    evidence = {"plan": name, "checks": [], "receipts": [], "before": bundle(owner_file)}
    start = activity.cursor(os.environ["JOURNEY_ACTIVITY"])
    raw = None
    try:
        with faults.fresh_target(inputs["subject"]):
            raw = launch_raw(inputs, prefix, evidence)
            picker_process = quiet_picker(inputs, prefix, evidence, start)
            after_quiet = activity.cursor(os.environ["JOURNEY_ACTIVITY"])
            def restored():
                evidence["restored_auxiliary"] = tmux_server(inputs["socket_name"], inputs["session"])
            with faults.dead_tmux(inputs["subject"]["socket"], restored) as dead:
                evidence["dead_server"] = dead
                evidence["socket_refusal"] = faults.refusing_socket(inputs["subject"]["socket"])
                wait("actual raw native Claude exited with auxiliary server", lambda: faults.birth_exited(raw), 20)
                def updated():
                    shown = screen(picker)
                    frames = scans(activity.records(os.environ["JOURNEY_ACTIVITY"], after_quiet), picker_process["pid"], os.environ["PFM_RUN"], successful=False)
                    return automatic_update(shown, inputs["session_id"], frames)
                evidence["automatic_picker"] = wait("untouched quiet picker automatically renders the actual resumable chat", updated, 30)
                receipt = action(owner, ARGV, str(prefix) + ".command")
                evidence["receipt"] = {**receipt, "prefix": str(prefix) + ".command", "door": "typed"}
                diagnostics, table = roster_receipt(receipt["output"])
                require(prefix, evidence, [check("presence-public-list-status", 0, receipt["exit_code"]), check("presence-public-list-diagnostics", [], diagnostics),
                        *row_checks(text_rows(table), inputs, resumed=True), *text_fleet_checks(table, carry())])
                evidence["during"] = bundle(owner_file)
                require(prefix, evidence, bundle_checks(evidence["during"], carry(), evidence["before"]))
            evidence["restored_server_birth"] = faults.tmux_birth(inputs["subject"]["socket"])
            require(prefix, evidence, [check("presence-restored-auxiliary-socket", inputs["subject"]["socket"], evidence["restored_auxiliary"]["socket"]),
                    check("presence-restored-server-birth", "fresh", "original" if evidence["restored_server_birth"] == evidence["dead_server"] else "fresh")])
    finally:
        try:
            raw = raw or evidence.get("raw", {}).get("birth")
            if raw is not None and not faults.birth_exited(raw):
                receipt = action(owner, ["pfm", "chat", "end", inputs["session_id"]], str(prefix) + ".end-raw")
                evidence["receipts"].append({"prefix": str(prefix) + ".end-raw", "argv": ["pfm", "chat", "end", inputs["session_id"]]})
                require(prefix, evidence, [check("raw-native-public-end", 0, receipt["exit_code"])])
                wait("raw native producer stopped by public end", lambda: faults.birth_exited(raw), 20)
        finally:
            command([sys.executable, str(HERE / "terminal.py"), "close", picker_file])
            evidence["after"] = bundle(owner_file)
            require(prefix, evidence, bundle_checks(evidence["after"], carry(), evidence["before"]))
    return evidence
