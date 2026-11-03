#!/usr/bin/env python3
"""Prove a machine-wide restore before adopting its explicitly changed sentinel."""
import json
import os
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "chat-ending"))
from observation import process_snapshot, report
from observation_process import engine_births
from foreign_transition import adopt_transition
from projection import resume_recipe, selected
from runtime import read_json, write_json


def ready(carry):
    for chat in [*carry["chats"], carry["foreign"]]:
        lifecycle = chat.get("expect", {}).get("lifecycle", "live")
        if lifecycle == "archived":
            continue
        state = json.loads(report(["pfm", "chat", "status", chat["session_id"], "--json"], allowed_codes=(0, 3)))
        if state["state"] != ("idle" if lifecycle == "live" else "dead"):
            return False
    return True


def checks(before, after, processes):
    rows = []
    def equal(field, expected, actual):
        rows.append({"field": field, "expected": expected, "actual": actual})
    equal("bundle-errors", [], after["errors"])
    for old, current in zip(before["chats"], after["chats"]):
        name = old["name"]
        equal(name + ":identity", old["name"], current["name"])
        if not isinstance(old["status"], dict) or old["status"]["state"] == "dead":
            for field in ("status", "stored", "picker"):
                equal(name + ":ended:" + field, old[field], current[field])
            continue
        for field in ("session_id", "engine", "cwd", "model", "effort"):
            equal(name + ":" + field, old["status"].get(field, "n/a:unreported"), current["status"].get(field, "n/a:unreported"))
        equal(name + ":state", "idle", current["status"]["state"])
        for field in ("name", "account", "engine", "project", "killed", "hidden", "backend"):
            equal(name + ":picker:" + field, [item[field] for item in old["picker"]], [item[field] for item in current["picker"]])
        equal(name + ":transcript", old["stored"]["transcript"], current["stored"]["transcript"])
        if old["terminal"]["backend"] == "tmux":
            equal(name + ":untouched-terminal", old["terminal"], current["terminal"])
            equal(name + ":untouched-store", old["stored"], current["stored"])
            continue
        equal(name + ":terminal", old["terminal"]["identity"], current["terminal"]["identity"])
        equal(name + ":recipe", resume_recipe(old["stored"]["recipe"]), resume_recipe(current["stored"]["recipe"]))
        for entity in ("workspace", "tab"):
            equal(name + ":" + entity, old["terminal"][entity][entity]["label"], current["terminal"][entity][entity]["label"])
        engine = {"cc": "claude", "cx": "codex", "ox": "opencode"}[old["status"]["engine"]]
        previous, births = engine_births(old, engine), engine_births(current, engine)
        equal(name + ":new-birth-count", 1, len(births))
        equal(name + ":previous-births-still-running", [], [birth for birth in previous
              if processes.get(birth["pid"], {}).get("start") == birth["start"]])
        equal(name + ":old-birth-reused", [], [birth for birth in births if any(
              (birth["pid"], birth["start"]) == (prior["pid"], prior["start"]) for prior in previous)])
    equal("carried-count", len(before["chats"]), len(after["chats"]))
    return rows


def adopt(before_path, after_path, receipt_path):
    before, after = read_json(before_path)["foreign_full"], read_json(after_path)["foreign_full"]
    engine = {"cc": "claude", "cx": "codex", "ox": "opencode"}[before["status"]["engine"]]
    old, new = engine_births(before, engine), engine_births(after, engine)
    processes = process_snapshot()
    write_json(receipt_path, {"kind": "restart", "session_id": before["status"]["session_id"],
        "rebound": bool(new) and all((a["pid"], a["start"]) != (b["pid"], b["start"]) for a in old for b in new),
        "old_births_gone": all(processes.get(row["pid"], {}).get("start") != row["start"] for row in old),
        "before_terminal": before["terminal"]["identity"], "after_terminal": after["terminal"]["identity"],
        "old_births": old, "new_births": new})
    return adopt_transition("restart", before_path, after_path, receipt_path)


def factor_checks(before, after, factor):
    rows = []
    def equal(field, expected, actual):
        rows.append({"field": field, "expected": expected, "actual": actual})
    if factor["kind"] in {"pruned", "preference"}:
        chat = factor["chat"]
        old, current = selected(before, chat), selected(after, chat)
        if factor["kind"] == "pruned":
            equal("prior-recorded-binary", factor["old"], old["stored"]["recipe"]["binary"])
            equal("old-version-absent", False, Path(factor["old"]).exists())
            equal("successor-recorded-binary", factor["successor"], current["stored"]["recipe"]["binary"])
            equal("successor-process-image", [factor["successor"]], [row["argv"][0] for row in engine_births(current, "claude")])
        else:
            for label, observed, expected in (("previous", old, True), ("current", current, False)):
                equal(label + "-remote-control", expected, any(arg in {"--remote-control", "--rc"} for arg in observed["stored"]["recipe"]["args"]))
    elif factor["kind"] == "indicators":
        prior = factor["birth"]
        equal("stopped-indicator-birth", False, process_snapshot().get(prior["pid"], {}).get("start") == prior["start"])
    return rows


if __name__ == "__main__":
    try:
        operation, *args = sys.argv[1:]
        if operation == "ready":
            sys.exit(0 if ready(read_json(os.environ["JOURNEY_CARRY"])) else 1)
        elif operation == "adopt":
            print(json.dumps(adopt(*args)))
        elif operation == "checks":
            before, after = read_json(args[0]), read_json(args[1])
            rows = checks(before, after, process_snapshot())
            if os.environ.get("RESTART_FACTOR"):
                rows.extend(factor_checks(before, after, read_json(os.environ["RESTART_FACTOR"])))
            write_json(args[2], rows)
        else:
            raise ValueError(f"RESTART-RESTORE-OPERATION: {operation}")
    except (OSError, ValueError, KeyError, RuntimeError, TypeError) as error:
        print(f"RESTART-RESTORE: {error}", file=sys.stderr)
        sys.exit(2)
