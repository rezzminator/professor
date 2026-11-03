"""Observe indicator phases through the public terminal reader."""
from pathlib import Path
import hashlib
import json
import os
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from bootstrap import wait
from observation import (bundle, indicator_daemon, read_actual_codex_children, read_actual_sidechains,
                         process_cwd, process_snapshot, reporter_activity, terminal, terminal_births)
from observation_process import engine_births
from runtime import now_ms, read_json, write_json
from chat_carry import temporary_expectation
from herdr import Refused, request


def phases(samples):
    if not samples:
        raise ValueError("ATTENTION-PHASES: no terminal samples")
    return {"states": sorted({sample["state"] for sample in samples}),
            "phases": sorted({sample["tokens"] for sample in samples})}


def mark(window_name, name):
    return {"window_name": window_name,
            "mark": "absent" if window_name == name[:24] else "unclassified:" + window_name}


def new_agents(before, after):
    previous = {row["agent_id"] for row in before["subagents"]}
    return sorted({row["agent_id"] for row in after["subagents"]} - previous)


def codex_hook(child, parent, cwd, question):
    if child["parent_thread_id"] != parent:
        raise ValueError("ATTENTION-CODEX-CHILD: actual parent differs from carried session")
    return [{"type": "hook", "event": "SessionStart", "payload": {
        "hook_event_name": "SessionStart", "session_id": child["id"],
        "transcript_path": child["path"], "cwd": cwd, "source": "startup"}}, *question]


def tool_output(record_file, expected_command):
    path = Path(record_file)
    if not path.exists():
        return None
    content = path.read_text()
    if not content.endswith("\n"):
        return None
    matches = [row for line in content.splitlines() if (row := json.loads(line))["command"] == expected_command]
    if not matches:
        return None
    if len(matches) != 1:
        raise ValueError(f"ATTENTION-TOOL-RECEIPT: expected one command, got {len(matches)}: {path}")
    row = matches[0]
    return {"command": row["command"], "cwd": row["cwd"], "exit_code": row["exit_code"],
            "stderr": row["stderr"], "stdout_sha256": hashlib.sha256(row["stdout"].encode()).hexdigest()}


def native_projection(value):
    result = {key: value[key] for key in ("agent", "manifest_source", "state", "matched_rule",
                                         "visible_idle", "screen_detection_skipped")}
    result["claude_working"] = value["agent"] == "claude" and value["state"] == "working"
    return result


def native_state(target, output):
    try:
        response = request(os.environ["HERDR_SOCKET_PATH"], "agent.explain", {"target": target["pane"]})
    except Refused as error:
        if error.code != "agent_not_found":
            raise
        write_json(output + ".response.json", {"error_code": error.code, "diagnostic": str(error)})
        write_json(output, {"claude_working": False, "error_code": error.code})
        return
    write_json(output + ".response.json", response)
    write_json(output, native_projection(response["result"]["explain"]))


def accounting(before, after, elapsed_ms):
    if elapsed_ms < 15000:
        raise ValueError(f"ATTENTION-ACCOUNTING-WINDOW: expected>=15000 actual={elapsed_ms}")
    if elapsed_ms > 16000:
        raise RuntimeError(f"CONTENTION: indicator accounting window expected<=16000 actual={elapsed_ms}")
    deltas = {}
    for key in ("cpu_seconds", "write_bytes"):
        if any(type(value[key]) not in (int, float) for value in (before, after)):
            raise ValueError(f"ATTENTION-ACCOUNTING-UNREADABLE: {key}: before={before!r} after={after!r}")
        deltas[key] = after[key] - before[key]
        if deltas[key] < 0:
            raise ValueError(f"ATTENTION-ACCOUNTING-COUNTER-RESET: {key}: {deltas[key]}")
    return {"same_birth": (before["pid"], before["start"]) == (after["pid"], after["start"]),
            "cpu_under_limit": deltas["cpu_seconds"] < 1.5,
            "write_under_limit": deltas["write_bytes"] < 5000000}


def cadence(samples):
    # Herdr's blinkHalfPeriod is500ms; absence needs two phases without250ms sampling gaps.
    span = samples[-1]["observed_ms"] - samples[0]["started_ms"]
    gaps = [right["observed_ms"] - left["observed_ms"] for left, right in zip(samples, samples[1:])]
    reads = [sample["observed_ms"] - sample["started_ms"] for sample in samples]
    if span < 1000 or max(gaps, default=1000) > 250 or max(reads) > 250:
        raise RuntimeError(f"CONTENTION: indicator sampling span={span} gaps={gaps} reads={reads}")
    return {"span_ms": span, "max_gap_ms": max(gaps), "max_read_ms": max(reads)}


def sample(target, state, output):
    samples = []

    def both_phases():
        started = now_ms()
        observed = terminal(target)
        pane = observed["pane"]
        tokens = sorted(key for key in pane.get("tokens", {}) if key.startswith("pfm_"))
        samples.append({"state": pane["agent_status"], "tokens": ",".join(tokens) or "none",
                        "started_ms": started, "observed_ms": now_ms()})
        return phases(samples)["phases"] == ["none", "pfm_" + state]

    try:
        wait("attention indicator on/off phases", both_phases, 2)
    except ValueError as error:
        if str(error) != "ROOT-TIMEOUT: attention indicator on/off phases":
            raise
        cadence(samples)
    finally:
        write_json(output + ".samples.json", samples)
    write_json(output, phases(samples))


if __name__ == "__main__":
    try:
        if sys.argv[1] == "sample":
            sample(read_json(sys.argv[2]), sys.argv[3], sys.argv[4])
        elif sys.argv[1] == "mark":
            observed = terminal(read_json(sys.argv[2]))
            write_json(sys.argv[4], mark(observed["pane"]["window_name"], sys.argv[3]))
        elif sys.argv[1] == "activity":
            value = reporter_activity(read_json(sys.argv[2]))
            write_json(sys.argv[4], value)
            if value == "n/a:reporter-activity-absent":
                sys.exit(1)
            if not isinstance(value, dict):
                raise ValueError(f"ATTENTION-ACTIVITY: unexpected applicability {value!r}")
            sys.exit(0 if value["active_child_count"] == int(sys.argv[3]) else 1)
        elif sys.argv[1] == "sidechains":
            write_json(sys.argv[3], read_actual_sidechains(read_json(sys.argv[2])))
        elif sys.argv[1] == "new-agents":
            write_json(sys.argv[4], new_agents(read_json(sys.argv[2]), read_json(sys.argv[3])))
        elif sys.argv[1] == "engine-births":
            observed = read_json(sys.argv[2])
            write_json(sys.argv[3], engine_births({"terminal": observed, "process_births": terminal_births(observed)}, "claude"))
        elif sys.argv[1] == "codex-children":
            write_json(sys.argv[3], read_actual_codex_children(read_json(sys.argv[2])))
        elif sys.argv[1] == "codex-hook":
            value = read_json(sys.argv[2])
            write_json(sys.argv[6], codex_hook(value, sys.argv[3], sys.argv[4], read_json(sys.argv[5])))
        elif sys.argv[1] == "tool-output":
            steps = read_json(sys.argv[3])
            value = tool_output(sys.argv[2], steps[0]["input"]["command"])
            if value is None:
                sys.exit(1)
            write_json(sys.argv[4], value)
        elif sys.argv[1] == "native":
            native_state(read_json(sys.argv[2]), sys.argv[3])
        elif sys.argv[1] == "previous-settings":
            from previous_settings import prepare
            prepare(*sys.argv[2:])
        elif sys.argv[1] == "indicator-setup":
            from indicator_setup import prepare
            prepare(*sys.argv[2:])
        elif sys.argv[1] == "indicator-live":
            from indicator_live import run
            run(sys.argv[2])
        elif sys.argv[1] == "indicator":
            selected = None
            if len(sys.argv) > 3:
                pid = int(sys.argv[3])
                observed = process_snapshot()
                selected = {pid: observed[pid]} if pid in observed else {}
            value = indicator_daemon(snapshot=selected)
            write_json(sys.argv[2], {"observed_ms": now_ms(), "daemon": value})
            if value == "n/a:indicator-daemon-absent":
                sys.exit(1)
            if not isinstance(value, dict):
                raise ValueError(f"ATTENTION-INDICATOR-UNREADABLE: {value!r}")
        elif sys.argv[1] == "process-cwd":
            write_json(sys.argv[3], {"cwd": process_cwd(int(sys.argv[2]))})
        elif sys.argv[1] == "window":
            deadline = read_json(sys.argv[2])["observed_ms"] + 15000
            wait("indicator 15 second observation window", lambda: now_ms() >= deadline, 18)
        elif sys.argv[1] == "accounting":
            before, after = read_json(sys.argv[2]), read_json(sys.argv[3])
            elapsed = after["observed_ms"] - before["observed_ms"]
            write_json(sys.argv[4], {"checks": accounting(before["daemon"], after["daemon"], elapsed),
                                     "before": before, "after": after, "elapsed_ms": elapsed})
        elif sys.argv[1] == "peer-idle":
            from chat import select
            from observation import report
            chat = select(sys.argv[2], "new-typed")
            if chat is None:
                raise ValueError(f"ATTENTION-PEER-ABSENT: {sys.argv[2]}")
            write_json(sys.argv[3], json.loads(report(["pfm", "chat", "status", chat["session_id"], "--json"],
                                                      allowed_codes=(0, 3))))
        elif sys.argv[1] == "ended-bundle":
            chat = read_json(sys.argv[2])
            expected = {"lifecycle": "ended", "terminal": "shell", "visibility": "visible"}
            with temporary_expectation(chat["branch"], chat.get("role") or chat["origin"], expected,
                                       before=read_json(sys.argv[3])):
                write_json(sys.argv[5], bundle(sys.argv[4]))
        else:
            raise ValueError("ATTENTION-STATE: expected sample or mark")
    except (OSError, ValueError, KeyError, TypeError, RuntimeError) as error:
        print(f"ATTENTION-PHASES-UNREADABLE: {error}; artifact={sys.argv[-1]}", file=sys.stderr)
        sys.exit(2)
