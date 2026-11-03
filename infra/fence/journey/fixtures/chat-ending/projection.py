#!/usr/bin/env python3
"""Exact lifecycle assertions over the public observation bundle."""
import json
from pathlib import Path
import sys

from runtime import read_json, write_json
from observation_process import engine_births


def selected(bundle, chat):
    def owns(row):
        status = row.get("status")
        if isinstance(status, dict) and status.get("session_id") == chat["session_id"]:
            return True
        if any(item["uuid"] == chat["session_id"] for item in row.get("stored", {}).get("chat", [])):
            return True
        return row.get("lifecycle", {}).get("ended", {}).get("archive", {}).get("row", {}).get("uuid") == chat["session_id"]
    matches = [row for row in bundle["chats"] if owns(row)]
    if len(matches) != 1:
        raise ValueError(f"ENDING-CHAT: {chat['key']}: expected one observation, got {len(matches)}")
    result = matches[0]
    for key in ("status", "stored", "picker", "terminal"):
        if key not in result:
            raise ValueError(f"ENDING-OBSERVATION: {chat['key']}: missing {key}")
    return result


def resume_recipe(recipe):
    args = recipe["args"]
    return {"cwd": recipe["cwd"],
            "options": {flag: args[args.index(flag) + 1] if flag in args else "engine-default"
                        for flag in ("--model", "--effort")},
            "environment": sorted(value for value in recipe["env"] if value.split("=", 1)[0]
                                  in {"CLAUDE_CONFIG_DIR", "CODEX_HOME", "CLAUDE_CODE_EFFORT_LEVEL", "ANTHROPIC_MODEL"})}


def checks(before, after, chat, mode):
    old, current = selected(before, chat), selected(after, chat)
    rows = []

    def equal(field, expected, actual):
        rows.append({"field": field, "expected": expected, "actual": actual})

    equal("bundle-errors", [], after["errors"])
    equal("foreign", before["foreign"], after["foreign"])
    equal("engine", {"claude": "cc", "codex": "cx", "opencode": "ox"}[chat["engine"]], current["status"]["engine"])
    equal("name", chat["name"], current["name"])
    live = mode in ("resume", "clear", "refused")
    equal("status", "idle" if live else "dead", current["status"]["state"])
    killed = mode in ("kill", "lost")
    equal("picker-killed", [killed], [row["killed"] for row in current["picker"]])
    equal("stored-killed", [chat["session_id"]] if killed or mode == "clear" else [], [row["uuid"] for row in current["stored"]["hidden"] if row["uuid"] == chat["session_id"]])
    equal("picker-hidden", [killed], [row["hidden"] for row in current["picker"]])
    equal("picker-account", [chat["account"]], [row["account"] for row in current["picker"]])
    equal("picker-project", [row["project"] for row in old["picker"]], [row["project"] for row in current["picker"]])
    equal("picker-engine", [row["engine"] for row in old["picker"]], [row["engine"] for row in current["picker"]])
    equal("picker-backend", [chat["terminal"]["backend"]], [row["backend"] for row in current["picker"]])
    equal("picker-state", ["idle" if live else "dead"], [row["state"] for row in current["picker"]])
    if mode != "clear":
        equal("session", chat["session_id"], current["status"].get("session_id"))
        equal("stored-chat", [{key: row[key] for key in ("uuid", "cwd", "label")} for row in old["stored"]["chat"]], [{key: row[key] for key in ("uuid", "cwd", "label")} for row in current["stored"]["chat"]])
    else:
        new_session = current["status"].get("session_id")
        equal("continued-session", [], [new_session] if not new_session or new_session == chat["session_id"] else [])
        equal("hidden-predecessor", [chat["session_id"]], [row["uuid"] for row in current["stored"]["hidden"] if row["uuid"] == chat["session_id"]])
        equal("one-conversation", 1, len(current["picker"]))
        equal("detached-agents", [], [row for row in current["rows"] if "agent" in row["kind"].lower()])
    if chat["terminal"]["backend"] == "herdr":
        if mode == "resume":
            equal("live-seat-session", [chat["session_id"]], [row["session_id"] for row in current["stored"]["seat"] if row["state"] == "live"])
            equal("stored-resume-recipe", resume_recipe(old["stored"]["recipe"]), resume_recipe(current["stored"]["recipe"]))
        else:
            equal("seat-state", ["live" if live else "ended"], [row["state"] for row in current["stored"]["seat"] if row["seat_id"] == chat["seat_id"]])
        display = current["terminal"]
        equal("workspace", old["terminal"]["workspace"]["workspace"]["label"], display["workspace"]["workspace"]["label"])
        equal("cwd", old["terminal"]["pane"]["cwd"], display["pane"]["cwd"])
        if mode != "resume":
            equal("terminal-identity", old["terminal"]["identity"], display["identity"])
    elif not live:
        equal("ended-tmux", "n/a:ended-tmux-pane", current["terminal"])
    if not live:
        prior = old.get("lifecycle", {}).get("ended", {}).get("births")
        expected_births = [row["prior"] for row in prior] if prior else [{"pid": row["pid"], "start": row["start"]} for row in engine_births(old, chat["engine"])]
        equal("death-proof", expected_births, [row["prior"] for row in current["lifecycle"]["ended"]["births"]])
    if mode in ("refused", "clear"):
        equal("process-births", engine_births(old, chat["engine"]), engine_births(current, chat["engine"]) if current["status"]["state"] == "idle" else [])
    if mode == "resume":
        for field in ("model", "effort", "cwd"):
            equal("resumed-" + field, old["status"].get(field, "n/a:unreported"), current["status"].get(field, "n/a:unreported"))
        old_births = {(row["pid"], row["start"]) for row in engine_births(old, chat["engine"])}
        new_births = engine_births(current, chat["engine"])
        equal("old-processes-in-resumed-terminal", [], [row for row in new_births if (row["pid"], row["start"]) in old_births])
        equal("resumed-terminal-birth-count", 1, len(new_births))
        equal("continued-transcript", old["stored"]["transcript"], current["stored"]["transcript"])
    return rows




if __name__ == "__main__":
    try:
        before, after, chat, mode, output = sys.argv[1:]
        write_json(output, checks(read_json(before), read_json(after), read_json(chat), mode))
    except (KeyError, OSError, ValueError, TypeError) as error:
        print(f"ENDING-PROJECTION: {error}", file=sys.stderr)
        sys.exit(2)
