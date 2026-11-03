#!/usr/bin/env python3
"""Native Claude callback receipts, reversible statusline inputs and exact checks.

seed PREFIX; resources CHAT PREFIX; run CHAT TARGET RESOURCES PREFIX; verify PREFIX BUNDLE.
RESOURCES names effort_state, usage_cache, config_dir and refresh inputs.
The mock's statusline-callbacks.jsonl is instrumentation of completed callbacks.
"""
from datetime import datetime, timezone
from pathlib import Path
import json, os, re, shlex, sys, uuid

from forecast import check
import pyte
from bootstrap import wait
from chat import inline
import faults
from observation import account_stats_cache, bundle, process_snapshot
from observation_process import environment
from runtime import command, now_ms, read_json, write_json, write_text

TOKENS = {"input": 120000, "output": 0, "cache_read": 0, "cache_creation": 0, "context_window": 200000}
MODEL = {"id": "claude-sonnet-4-5", "display_name": "Sonnet"}
MODEL_CELL = "🥈 ◇ Sonnet·effort unknown"
QUOTA = {"five_hour": {"used_percentage": 89, "resets_at": 4102444800},
         "seven_day": {"used_percentage": 34, "resets_at": 4102531200}}
WEEKLY = {"seven_day": QUOTA["seven_day"]}


def plain(value):
    # The existing terminal dependency owns ANSI/control decoding here too.
    display = pyte.Screen(max(160, len(value)), max(8, value.count("\n") + 2))
    pyte.Stream(display).feed(value.replace("\n", "\r\n"))
    return "\n".join(row.rstrip() for row in display.display).rstrip()


def cells(output):
    text = plain(output)
    models = [line.split("│", 1)[0].strip() for line in text.splitlines() if line.startswith("🥈 ")]
    costs = re.findall(r"💰(\$[^/\s]+)", text)
    windows = dict(re.findall(r"\b(5h|7d)-used:([^\s│]+)", text))
    return {"model": models, "cost": costs, "windows": windows}


def callback_frames(path, offset=0):
    source = Path(path)
    if not source.exists():
        raise ValueError(f"STATUSLINE-RECEIPTS-ABSENT: {source}")
    contents = source.read_bytes()
    if len(contents) < offset:
        raise ValueError(f"STATUSLINE-RECEIPTS-TRUNCATED: {source}")
    complete, separator, _ = contents[offset:].rpartition(b"\n")
    rows = [json.loads(line) for line in complete.splitlines() if line]
    for row in rows:
        if not isinstance(row.get("input"), dict) or type(row.get("pid")) is not int or row["pid"] <= 0:
            raise ValueError(f"STATUSLINE-RECEIPT-SHAPE: {source}: {row!r}")
        if type(row.get("exit_code")) is not int or not all(isinstance(row.get(key), str) for key in ("stdout", "stderr", "config_dir")):
            raise ValueError(f"STATUSLINE-RECEIPT-SHAPE: {source}: {row!r}")
        if not isinstance(row.get("argv"), list) or not row["argv"] or not all(isinstance(word, str) for word in row["argv"]):
            raise ValueError(f"STATUSLINE-RECEIPT-SHAPE: {source}: {row!r}")
    return rows, offset + len(complete) + len(separator)


def callbacks(path, offset=0):
    return callback_frames(path, offset)[0]


def callback_cursor(path):
    return callback_frames(path)[1] if Path(path).exists() else 0


def receipt_checks(row, chat, resources, windows, model=MODEL_CELL, rates=QUOTA):
    value = row["input"]
    actual = cells(row["stdout"])
    return [check("native-session", chat["session_id"], value.get("session_id")),
            check("native-account-directory", resources["config_dir"], row["config_dir"]),
            check("native-model", MODEL, value.get("model")),
            check("native-effort", {"level": ""}, value.get("effort")),
            # The mock tariff is one micro-dollar/token plus $0.0001, independent of pfm.
            check("native-turn-cost", 0.1201, value.get("cost", {}).get("total_cost_usd")),
            check("native-context", 60, value.get("context_window", {}).get("used_percentage")),
            check("native-rate-limits", rates, value.get("rate_limits")),
            check("callback-shell", "sh", Path(row["argv"][0]).name),
            check("callback-command", ["-c", resources.get("statusline_command", str(Path(os.environ["HOME"]) / ".local/bin/pfm-statusline"))], row["argv"][1:]),
            check("callback-exit", 0, row["exit_code"]),
            check("account-model-effort-cell", [model], actual["model"]),
            check("turn-cost-cell", ["$0.12"], actual["cost"]),
            check("quota-windows", windows, actual["windows"])]


def reaping_checks(rows, daemon, before, after, refreshes):
    if not rows:
        raise ValueError("STATUSLINE-CALLBACKS-EMPTY: no refresh can establish reaping")
    for snapshot in (before, after):
        if daemon["pid"] not in snapshot:
            raise ValueError(f"STATUSLINE-DAEMON-ABSENT: {daemon}")
    children = {pid for pid, row in after.items() if row["parent"] == daemon["pid"]}
    if any("state" not in after[pid] for pid in children | {daemon["pid"]}):
        raise ValueError("STATUSLINE-PROCESS-STATE-UNAVAILABLE: shared process snapshot lacks state")
    return [check("daemon-birth-before", daemon["start"], before[daemon["pid"]]["start"]),
            check("daemon-birth-after", daemon["start"], after[daemon["pid"]]["start"]),
            check("ten-completed-callbacks", 10, len(rows)),
            check("ten-actual-refresh-children", 10, len(refreshes)),
            check("refresh-child-parents", [daemon["pid"]] * 10, [row["parent"] for row in refreshes]),
            check("refresh-child-argv", [["statusline", "--refresh-gpt"]] * 10, [shlex.split(row["argv"])[-2:] for row in refreshes]),
            check("refresh-child-unique-births", 10, len({(row["pid"], row["start"]) for row in refreshes})),
            check("refresh-children-reaped", [], [row["pid"] for row in refreshes if row["pid"] in after and after[row["pid"]]["start"] == row["start"]]),
            check("completed-callback-children-reaped", [], sorted({row["pid"] for row in rows} & set(after))),
            check("daemon-zombie-children", [], sorted(pid for pid in children if after[pid]["state"].startswith("Z")))]


def reaped_snapshot(refreshes, bound=3):
    latest = {}
    label = "same-birth refresh children reaped"
    def ready():
        nonlocal latest
        latest = process_snapshot()
        return not any(row["pid"] in latest and latest[row["pid"]]["start"] == row["start"] for row in refreshes)
    try:
        # wait4 is local work: three seconds allow scheduler delay, not a provider request.
        wait(label, ready, bound)
    except ValueError as error:
        if str(error) != "ROOT-TIMEOUT: " + label:
            raise
    return latest


def warning_checks(records, path):
    wanted = f'statusline: auto-effort state {path} (effort shows ERR): parse "{{": unexpected end of JSON input'
    actual = sorted({(row.get("level"), row.get("msg"), row.get("err")) for row in records
                     if row.get("msg") == "statusline.warn" and path in str(row.get("err", ""))}, key=json.dumps)
    return [check("visible-ERR-cause", [["WARN", "statusline.warn", wanted]], [list(row) for row in actual])]


def paced_workers():
    candidates = []
    for row in process_snapshot().values():
        if " statusline --paced " not in row["argv"]:
            continue
        words = shlex.split(row["argv"])
        if len(words) < 4 or words[-3:-1] != ["statusline", "--paced"]:
            continue
        try:
            actual = environment(row["pid"])
        except FileNotFoundError:
            continue
        if actual.get("PFM_RUN") == os.environ["PFM_RUN"] and row["state"] != "Z":
            candidates.append({**row, "key": words[-1], "scenario": actual.get("MOCK_ENGINE_SCENARIO")})
    return candidates


def paced_worker(chat):
    candidates = [row for row in paced_workers() if row["scenario"] == chat["scenario"]]
    if len(candidates) > 1:
        raise ValueError(f"STATUSLINE-PACED-WORKER-AMBIGUOUS: {candidates!r}")
    return candidates[0] if candidates else False


def seed(prefix):
    faults.owned_path(prefix, preserve=True)
    write_text(prefix + ".refresh-release", "release\n")
    write_json(prefix + ".seed.json", {"model": MODEL["id"], "tokens": TOKENS, "steps": [],
               "idle_statusline_refresh": {"count": 1800, "interval_ms": 100, "stop_when_exists": prefix + ".idle-stop"},
               "app_server_stdout_hold": prefix + ".refresh-release"})


def resources(chat_path, prefix):
    chat = read_json(chat_path)
    faults.owned_path(prefix, preserve=True)
    if chat["engine"] != "claude" or chat["account"] != 2:
        raise ValueError("STATUSLINE-RESOURCES-ACTOR: Claude account2 required")
    session = chat["session_id"]
    # Journey Claude actors use canonical UUIDs; no second filename codec is needed.
    if str(uuid.UUID(session)) != session:
        raise ValueError(f"STATUSLINE-RESOURCES-SESSION: canonical UUID required: {session!r}")
    if any(os.environ.get(key, "").strip() for key in ("AUTO_EFFORT_STATE_FILE", "AUTO_EFFORT_CONTROL_STATE_FILE")):
        raise ValueError("STATUSLINE-RESOURCES-EFFORT-OVERRIDE: root default path contract changed")
    log = Path(chat["records"]) / "statusline-callbacks.jsonl"
    config_dir = str(Path(account_stats_cache(chat)["2"]["path"]).parent)
    def callback():
        if not log.exists():
            return False
        rows = [row for row in callbacks(log) if row["input"].get("session_id") == session]
        return rows[-1] if rows and rows[-1]["config_dir"] == config_dir else False
    native = wait("native account2 callback resource identity", callback, 15)
    home = Path(os.environ["HOME"])
    # Mirrors the input file contracts of paths.TempBase, UsageCacheDir and AutoEffortStateFile.
    cache = Path(os.environ["PFM_HOME"]) / "tmp" if os.environ.get("PFM_HOME") else Path(os.environ.get("TMPDIR") or "/tmp")
    scenario = read_json(chat["scenario"])
    value = {"config_dir": config_dir, "statusline_command": str(home / ".local/bin/pfm-statusline"),
             "effort_state": str(home / ".local/state/auto-effort" / ("state." + session + ".json")),
             "usage_cache": str(cache / ("cc-usage-" + str(os.getuid())) / "acct-2.json"),
             "codex_cache": str(cache / ("cc-gpt-usage-" + str(os.getuid()) + ".json")),
             "refresh_gate": scenario.get("app_server_stdout_hold"),
             "idle_stop": scenario.get("idle_statusline_refresh", {}).get("stop_when_exists")}
    faults.owned_path(home / ".local/state")
    faults.owned_path(cache)
    # Input directories carry no product result; all input file changes stay scoped below.
    command(["mkdir", "-p", str(Path(value["effort_state"]).parent), str(Path(value["usage_cache"]).parent)])
    for key in ("config_dir", "effort_state", "usage_cache", "codex_cache", "refresh_gate", "idle_stop"):
        if not isinstance(value[key], str):
            raise ValueError(f"STATUSLINE-RESOURCES-MISSING: {key}")
        faults.owned_path(value[key], preserve=True)
    write_json(prefix + ".resources.json", value)
    write_json(prefix + ".resources-checks.json", [check("native-account2-input", config_dir, native["config_dir"])])


def refresh_children(chat, inputs, worker, prefix):
    cache, gate = Path(inputs["codex_cache"]), inputs["refresh_gate"]
    attempt = Path(str(cache) + ".attempt")
    wait("initial native Codex cache and attempt", lambda: cache.is_file() and attempt.is_file(), 15)
    wait("other owned paced workers naturally idle out", lambda: [row["pid"] for row in paced_workers()] == [worker["pid"]], 45)
    births, seen = [], set()
    def child():
        snapshot = process_snapshot()
        current = snapshot.get(worker["pid"])
        if current is None or current["start"] != worker["start"]:
            raise ValueError("STATUSLINE-PACED-WORKER-REPLACED")
        found = []
        for row in snapshot.values():
            if row["parent"] != worker["pid"] or (row["pid"], row["start"]) in seen:
                continue
            if shlex.split(row["argv"])[-2:] == ["statusline", "--refresh-gpt"]:
                if environment(row["pid"]).get("PFM_RUN") != os.environ["PFM_RUN"]:
                    raise ValueError("STATUSLINE-REFRESH-CHILD-FOREIGN")
                found.append(row)
        if len(found) > 1:
            raise ValueError(f"STATUSLINE-REFRESH-CHILD-AMBIGUOUS: {found!r}")
        return found[0] if found else False
    for _ in range(10):
        with faults.temporarily_missing_file(gate), faults.aged_files([cache, attempt], age_seconds=301):
            born = wait("new native refresh child of paced worker", child, 15)
            seen.add((born["pid"], born["start"]))
            births.append(born)
            write_json(prefix + ".refresh-births.json", births)
        def completed():
            current = process_snapshot().get(born["pid"])
            return current is None or current["start"] != born["start"] or current["state"] == "Z"
        wait("native refresh child completed or zombie", completed, 15)
    return births


def ready_rows(path, offset, chat, windows, model=MODEL_CELL, count=1, settle=True):
    latest = []
    def ready():
        nonlocal latest
        if not Path(path).exists():
            return False
        latest = callbacks(path, offset)
        relevant = [row for row in latest if row["input"].get("session_id") == chat["session_id"]]
        if len(relevant) < count:
            return False
        if not settle:
            return relevant[:count]
        tail = relevant[-count:]
        wanted = {"model": [model], "cost": ["$0.12"], "windows": windows}
        return tail if all(cells(row["stdout"]) == wanted for row in tail) else False
    try:
        return wait("native statusline callbacks " + json.dumps(windows, sort_keys=True), ready, 30)
    except ValueError as error:
        raise ValueError(f"{error}; source={path}; last={latest[-1:]!r}") from error


def render_checks(chat_path, target_path, resources_path, prefix):
    started = now_ms()
    chat, target, resources = read_json(chat_path), read_json(target_path), read_json(resources_path)
    if chat["engine"] != "claude" or chat["account"] != 2 or chat["terminal"] != target:
        raise ValueError("STATUSLINE-ACTOR: carried Claude account2 terminal required")
    faults.owned_target(target)
    faults.owned_path(prefix, preserve=True)
    for key in ("effort_state", "usage_cache", "config_dir"):
        faults.owned_path(resources[key], preserve=True)
    records = Path(chat["records"]) / "statusline-callbacks.jsonl"
    faults.owned_path(records, preserve=True)
    cursor = callback_cursor(records)
    prompts = Path(chat["records"]) / "prompts"
    prompt_offset = prompts.stat().st_size if prompts.exists() else 0
    evidence = {"checks": [], "callbacks": {}, "inputs": [], "resources": resources,
                "before": bundle(target_path), "before_processes": process_snapshot()}
    write_json(prefix + ".evidence.json", evidence)

    def turn(label, rates):
        step = [{"type": "turn", "reply": label, "busy_ms": 20, "tokens": TOKENS, "rate_limits": rates}]
        path = prefix + "." + label + ".steps.json"
        write_json(path, step)
        evidence["inputs"].append("statusline-" + label + " mock-engine: " + json.dumps(step, sort_keys=True, separators=(",", ":")))
        inline(target, path, "statusline-" + label)

    def capture(label, windows, model=MODEL_CELL, count=1, settle=True, rates=QUOTA):
        rows = ready_rows(records, cursor, chat, windows, model, count, settle)
        evidence["callbacks"][label] = rows
        for index, row in enumerate(rows):
            for item in receipt_checks(row, chat, resources, windows, model, rates):
                item["field"] = label + ":" + str(index) + ":" + item["field"]
                evidence["checks"].append(item)
        write_json(prefix + ".evidence.json", evidence)
        return rows

    # Every mutation uses the run-owned shared fault door and restores on exit.
    with faults.temporary_text(resources["effort_state"], '{"version":2,"mode":"auto","level":"high","keeper":0}\n'):
        evidence["checks"].append(check("retired-effort-input", {"version": 2, "mode": "auto", "level": "high", "keeper": 0},
                                        read_json(resources["effort_state"])))
        turn("retired-effort", QUOTA)
        capture("retired-effort", {"5h": "89%", "7d": "34%"})
        daemon = wait("owned paced statusline worker", lambda: paced_worker(chat), 15)
        resources["daemon"] = daemon
        evidence["before_processes"] = process_snapshot()
        cursor = callback_cursor(records)
        refreshes = refresh_children(chat, resources, daemon, prefix)
        evidence["refreshes"] = refreshes
        rows = capture("ten-refreshes", {"5h": "89%", "7d": "34%"}, count=10, settle=False)
        after = reaped_snapshot(refreshes)
        evidence["checks"] += reaping_checks(rows, daemon, evidence["before_processes"], after, refreshes)
        evidence["after_processes"] = after
    with faults.temporary_text(resources["effort_state"], "{"):
        evidence["checks"].append(check("malformed-effort-input", "{", Path(resources["effort_state"]).read_text()))
        cursor = callback_cursor(records)
        capture("malformed-effort", {"5h": "89%", "7d": "34%"}, MODEL_CELL + " · ERR")
    cursor = callback_cursor(records)
    capture("restored-effort", {"5h": "89%", "7d": "34%"})
    turn("weekly-only", WEEKLY)
    cursor = callback_cursor(records)
    capture("weekly-settled", {"7d": "34%"}, rates=WEEKLY)
    cursor = callback_cursor(records)
    capture("weekly-only", {"7d": "34%"}, count=10, settle=False, rates=WEEKLY)
    cache = {"config_dir": resources["config_dir"],
             "fetched_at": datetime.fromtimestamp(now_ms() / 1000, timezone.utc).isoformat(),
             "seven_day": {"utilization": 89, "resets_at": "2100-01-02T00:00:00Z"}}
    with faults.temporary_text(resources["usage_cache"], json.dumps(cache)):
        evidence["checks"].append(check("idle-account-cache-input", cache, read_json(resources["usage_cache"])))
        cursor = callback_cursor(records)
        capture("idle-settled", {"7d": "89%"}, rates=WEEKLY)
        cursor = callback_cursor(records)
        idle = capture("idle-cached-weekly", {"7d": "89%"}, count=10, settle=False, rates=WEEKLY)
        evidence["checks"] += [check("omitted-five-hour-payload-window", [False] * 10, ["five_hour" in row["input"]["rate_limits"] for row in idle]),
                               check("present-seven-day-payload-window", [WEEKLY["seven_day"]] * 10, [row["input"]["rate_limits"].get("seven_day") for row in idle]),
                               check("shared-cache-freshness", "fresh", "fresh" if now_ms() - datetime.fromisoformat(read_json(resources["usage_cache"])["fetched_at"]).timestamp() * 1000 <= 300000 else "stale")]
        evidence["checks"].append(check("idle-input-count", 2, len(evidence["inputs"])))
    cursor = callback_cursor(records)
    capture("restored-cache", {"7d": "34%"}, rates=WEEKLY)
    evidence["after"] = bundle(target_path)
    evidence["elapsed_ms"] = now_ms() - started
    write_text(prefix + ".input.txt", prompts.read_bytes()[prompt_offset:].decode())
    write_text(prefix + ".expected-input.txt", "\n".join(evidence["inputs"]) + "\n")
    write_json(prefix + ".evidence.json", evidence)
    write_json(prefix + ".checks.json", evidence["checks"])


def run(chat_path, target_path, resources_path, prefix):
    inputs = read_json(resources_path)
    faults.owned_path(inputs["idle_stop"], preserve=True)
    neutral = {"config_dir": inputs["config_dir"],
               "fetched_at": datetime.fromtimestamp(now_ms() / 1000, timezone.utc).isoformat(),
               "five_hour": {"utilization": 89, "resets_at": "2100-01-01T00:00:00Z"},
               "seven_day": {"utilization": 34, "resets_at": "2100-01-02T00:00:00Z"}}
    try:
        with faults.temporary_text(inputs["usage_cache"], json.dumps(neutral)):
            render_checks(chat_path, target_path, resources_path, prefix)
    finally:
        write_text(inputs["idle_stop"], "complete\n")


def main(operation, *arguments):
    if operation == "seed":
        seed(*arguments)
    elif operation == "resources":
        resources(*arguments)
    elif operation == "run":
        run(*arguments)
    elif operation == "verify":
        prefix, observed = arguments
        evidence = read_json(prefix + ".evidence.json")
        checks = warning_checks(read_json(observed)["activity"]["records"], evidence["resources"]["effort_state"])
        write_json(prefix + ".warning-checks.json", checks)
    else:
        raise ValueError("usage: statusline.py seed PREFIX | resources CHAT PREFIX | run CHAT TARGET RESOURCES PREFIX | verify PREFIX BUNDLE")


if __name__ == "__main__":
    try:
        main(*sys.argv[1:])
    except (OSError, ValueError, KeyError, RuntimeError) as error:
        print(f"STATUSLINE-UNAVAILABLE: {error}", file=sys.stderr)
        sys.exit(2)
