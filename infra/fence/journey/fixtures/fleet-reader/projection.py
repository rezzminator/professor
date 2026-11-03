#!/usr/bin/env python3
"""Lane assertions over public reports and carried identities."""
import json
import csv
import io
import re
import shlex
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from runtime import read_json
from observation_process import engine_births
from faults import herdr_methods
from chat_carry import ENGINES


def selected(bundle, chat, required=True):
    if chat.get("session_id"):
        rows = [row for row in bundle["chats"] if isinstance(row.get("status"), dict)
                and row["status"].get("session_id") == chat["session_id"]]
    elif chat.get("seat_id"):
        rows = [row for row in bundle["chats"] if any(seat.get("seat_id") == chat["seat_id"] for seat in row.get("stored", {}).get("seat", []))]
    else:
        rows = [row for row in bundle["chats"] if row.get("name") == chat["name"]]
    if not rows and not required:
        return None
    if len(rows) != 1:
        raise ValueError(f"READER-IDENTITY: {chat.get('key', chat['name'])}: expected one row, got {len(rows)}")
    return rows[0]


def engine_birth(bundle, chat):
    matches = [{"pid": birth["pid"], "start": birth["start"]}
               for birth in engine_births(selected(bundle, chat), chat["engine"])]
    if len(matches) != 1:
        raise ValueError(f"READER-ENGINE-BIRTH: {chat['key']}: {matches!r}")
    return matches[0]


def supervisor_birth(bundle, chat):
    current = selected(bundle, chat)
    seats = [row for row in current["stored"]["seat"] if row["seat_id"] == chat["seat_id"]]
    if len(seats) != 1 or type(seats[0].get("seat_host_pid")) is not int or seats[0]["seat_host_pid"] <= 0:
        raise ValueError(f"READER-SEAT-HOST: {chat['key']}: one persisted supervisor required")
    pid = seats[0]["seat_host_pid"]
    matches = {(node["pid"], node["start"]) for birth in current["process_births"] for node in birth["chain"]
               if node["pid"] == pid and shlex.split(node["argv"])[1:] == ["internal", "seat-host", chat["seat_id"]]}
    if len(matches) != 1:
        raise ValueError(f"READER-SEAT-HOST-BIRTH: {chat['key']}: {sorted(matches)!r}")
    actual_pid, start = matches.pop()
    return {"pid": actual_pid, "start": start}


def equal(rows, field, expected, actual):
    rows.append({"field": field, "expected": expected, "actual": actual})


def moved_seats(rows, chat, expected=False):
    result = []
    for seat in rows:
        if seat["seat_id"] != chat["seat_id"]:
            result.append(seat)
            continue
        stable = {key: value for key, value in seat.items() if key != "updated_at"}
        if expected:
            stable.update(herdr_pane=chat["terminal"]["pane"], herdr_workspace=chat["workspace"])
        result.append(stable)
    return result


def changed_actors(values, chats):
    result = {}
    for value in values:
        kind, target = "changed", value
        for prefix in ("moved:", "replaced:"):
            if value.startswith(prefix):
                kind, target = prefix[:-1], value[len(prefix):]
                break
        if target.startswith("key:"):
            matches = [chat for chat in chats if chat.get("key") == target[4:]]
        else:
            matches = [chat for chat in chats if target and chat["name"] == target]
        if len(matches) != 1:
            raise ValueError(f"READER-CHANGED-IDENTITY: {value!r}: expected one carried actor, got {len(matches)}")
        chat = matches[0]
        result.setdefault(chat.get("key") or chat.get("session_id") or chat["name"], set()).add(kind)
    return result


def bundle_checks(bundle, carry, previous=None, changed=()):
    checks = []
    changes = changed_actors(changed, carry["chats"])
    equal(checks, "observation-errors", [], bundle["errors"])
    if previous is not None:
        equal(checks, "foreign-sentinel", previous["foreign"], bundle["foreign"])
    for chat in carry["chats"]:
        current = selected(bundle, chat)
        old = selected(previous, chat, required=False) if previous else None
        prefix = chat.get("key", chat["name"]) + ":"
        actor_changes = changes.get(chat.get("key") or chat.get("session_id") or chat["name"], set())
        if old and not actor_changes:
            # IdleSeconds is an elapsed clock, not part of a chat's identity.
            before = {key: value for key, value in old.items() if key not in ("status", "picker")}
            after = {key: value for key, value in current.items() if key not in ("status", "picker")}
            equal(checks, prefix + "untouched", before, after)
        expected_engine = ENGINES.get(chat.get("engine"))
        if expected_engine:
            equal(checks, prefix + "engine", expected_engine, current["status"].get("engine"))
        equal(checks, prefix + "session", chat["session_id"], current["status"].get("session_id"))
        equal(checks, prefix + "pane", chat["terminal"]["pane"], current["terminal"]["identity"]["pane"])
        equal(checks, prefix + "backend", chat["terminal"]["backend"], current["terminal"]["backend"])
        if chat["terminal"]["backend"] == "tmux":
            equal(checks, prefix + "socket", chat["terminal"]["socket"], current["terminal"]["identity"]["socket"])
            equal(checks, prefix + "workspace", "n/a:tmux", current["terminal"]["workspace"])
        for field, expected in (("account", chat["account"]), ("project", chat["project"]), ("name", chat["name"])):
            equal(checks, prefix + field, [expected], [row[field] for row in current["picker"]])
        if old and actor_changes:
            moved = "moved" in actor_changes
            if moved:
                equal(checks, prefix + "terminal-identity", {key: value for key, value in old["terminal"]["identity"].items() if key != "pane"},
                      {key: value for key, value in current["terminal"]["identity"].items() if key != "pane"})
            elif "replaced" not in actor_changes:
                equal(checks, prefix + "terminal-identity", old["terminal"]["identity"], current["terminal"]["identity"])
            for field in ("seat", "launch", "recipe", "hidden", "lineage"):
                if moved and field == "seat":
                    equal(checks, prefix + field, moved_seats(old["stored"][field], chat, expected=True), moved_seats(current["stored"][field], chat))
                else:
                    equal(checks, prefix + field, old["stored"][field], current["stored"][field])
    return checks


def mcp_result(receipt):
    if receipt.get("isError"):
        raise ValueError("READER-MCP-ERROR: " + json.dumps(receipt["content"]))
    result = receipt.get("structuredContent")
    if result is None:
        texts = [item["text"] for item in receipt["content"] if item["type"] == "text"]
        if len(texts) != 1:
            raise ValueError(f"READER-MCP-CONTENT: expected one text result, got {len(texts)}")
        result = json.loads(texts[0])
    if not isinstance(result, dict):
        raise ValueError("READER-MCP-SHAPE: object required")
    return result


def fleet_checks(result, carry):
    checks = []
    for chat in carry["chats"]:
        rows = carried_rows(result["rows"], chat)
        actual = [{field: row.get(field) for field in ("engine", "project", "name", "account", "backend", "pane")} for row in rows]
        expected = {"engine": ENGINES[chat.get("engine", "claude")],
                    "project": chat["project"], "name": chat["name"], "account": chat["account"],
                    "backend": chat["terminal"]["backend"], "pane": chat["terminal"]["pane"]}
        equal(checks, chat.get("key", chat["name"]), [expected], actual)
    return checks


def carried_rows(rows, chat):
    return [row for row in rows if row.get("id") in (chat["session_id"], chat.get("seat_id"))]


def text_rows(text):
    reader = csv.DictReader(io.StringIO(text), delimiter="\t")
    required = {"id", "name", "account", "project", "cwd", "kind", "socket", "killed"}
    if not reader.fieldnames or not required.issubset(reader.fieldnames):
        raise ValueError(f"READER-TEXT-SCHEMA: {reader.fieldnames!r}")
    return list(reader)


def text_fleet_checks(text, carry):
    rows = text_rows(text)
    checks = []
    for chat in carry["chats"]:
        matched = carried_rows(rows, chat)
        fields = ("name", "account", "project")
        equal(checks, chat.get("key", chat["name"]) + ":text-row",
              [{"name": chat["name"], "account": str(chat["account"]), "project": chat["project"]}],
              [{key: row[key] for key in fields} for row in matched])
    return checks


def fleet_drift_checks(result, text, carry):
    rows = text_rows(text)
    checks = []
    for chat in carry["chats"]:
        fields = ("name", "account", "project")
        expected = [{**{key: str(row[key]) for key in fields}, "cwd": row["cwd"]} for row in carried_rows(rows, chat)]
        actual = [{**{key: str(row[key]) for key in fields}, "cwd": row["dir"]} for row in carried_rows(result["rows"], chat)]
        equal(checks, chat.get("key", chat["name"]) + ":mcp-text", expected, actual)
    return checks


def roster_receipt(text):
    # Terminal receipts join stdout/stderr; keep named warnings separate from the TSV.
    lines = text.splitlines(keepends=True)
    headers = [index for index, line in enumerate(lines) if line.startswith("kind\tid\t")]
    if len(headers) != 1:
        raise ValueError(f"READER-STOPPED-TABLE: expected one header, got {len(headers)}")
    start = headers[0]
    diagnostics = [line.strip() for line in lines[:start] if line.strip()]
    return diagnostics, "".join(lines[start:])


def stopped_fleet_checks(text, carry, socket):
    diagnostics, table = roster_receipt(text)
    checks = text_fleet_checks(table, carry)
    named, unexpected = set(), []
    for line in diagnostics:
        paths = re.findall(r"/[^\s:]+", line)
        if socket in paths and "tmux server did not answer" in line:
            named.add((socket, "tmux server did not answer"))
        else:
            unexpected.append(line)
    equal(checks, "stopped-server-warning", [[socket, "tmux server did not answer"]], [list(item) for item in sorted(named)])
    equal(checks, "unrelated-warning", [], unexpected)
    return checks


def transcript(entries):
    return [{"role": entry["role"], "text": entry["text"]} for entry in entries
            if entry["role"] in ("user", "assistant")]


def lost_checks(text, bundle, chat):
    checks = []
    lines = [line.strip().split("  ") for line in text.splitlines() if chat["name"] in line]
    equal(checks, "lost-public-row", ["↻ " + chat["name"]], [fields[0] for fields in lines])
    current = selected(bundle, chat)
    equal(checks, "lost-status", "dead", current["status"]["state"])
    equal(checks, "lost-session", chat["session_id"], current["status"].get("session_id"))
    seats = [seat for seat in current["stored"]["seat"] if seat["seat_id"] == chat["seat_id"]]
    equal(checks, "lost-stored-seat", ["lost"], [seat["state"] for seat in seats])
    equal(checks, "lost-row-cwd", [""], [row["cwd"] for row in carried_rows(current["rows"], chat)])
    return checks


def deleted_cwd_checks(bundle, chat):
    current = selected(bundle, chat)
    return [{"field": field, "expected": expected, "actual": actual} for field, expected, actual in (
        ("deleted-cwd-session", chat["session_id"], current["status"].get("session_id")),
        ("deleted-cwd-state", "idle", current["status"]["state"]),
        ("deleted-cwd-picker", [{"name": chat["name"], "project": chat["project"], "state": "idle"}],
         [{key: row[key] for key in ("name", "project", "state")} for row in current["picker"]]))]


def identity_checks(result, chat):
    return [{"field": field, "expected": expected, "actual": result.get(field)} for field, expected in (
        ("id", chat["session_id"]), ("backend", chat["terminal"]["backend"]),
        ("pane", chat["terminal"]["pane"]),
        ("engine", ENGINES[chat["engine"]]))]


def delivery(receipt):
    return {"code": receipt["code"], "status": receipt["status"], "typed": receipt.get("typed", False)}


def prompts(entries, nonce):
    # Transcript inputs prove one submission despite repeated terminal repaints.
    return [entry["text"] for entry in entries if entry["role"] == "user" and entry["text"].startswith(nonce)]


def sender(chat):
    name = chat["name"].strip()
    address = '"' + name + '"' if any(character.isspace() for character in name) else name
    sid = chat["session_id"][:8]
    return f"sid {sid} · to reply: call {address or sid} <message>"


def call_checks(bundle, caller, callee, nonce):
    target = selected(bundle, callee)
    entries = target["stored"]["transcript"]["entries"]
    received = prompts(entries, nonce)
    return [
        {"field": "callee-session", "expected": callee["session_id"], "actual": target["status"].get("session_id")},
        {"field": "callee-state", "expected": "idle", "actual": target["status"]["state"]},
        {"field": "reply", "expected": {"role": "assistant", "text": "pong"}, "actual": {key: entries[-1][key] for key in ("role", "text")}},
        {"field": "submitted-prompt", "expected": [nonce], "actual": [text.split(" — ", 1)[0].split("\n", 1)[0].rstrip() for text in received]},
        {"field": "sender", "expected": [sender(caller)],
         "actual": [text.rsplit(" — ", 1)[-1] for text in received]},
    ]


def main(operation, *args):
    if operation == "sender":
        print(sender(read_json(args[0])))
        return
    if operation == "selected":
        value = selected(read_json(args[0]), read_json(args[1]))
    elif operation == "bundle":
        previous = read_json(args[2]) if len(args) > 2 else None
        value = bundle_checks(read_json(args[0]), read_json(args[1]), previous, args[3:])
    elif operation == "mcp":
        value = mcp_result(read_json(args[0]))
    elif operation == "fleet":
        value = fleet_checks(read_json(args[0]), read_json(args[1]))
    elif operation == "text-fleet":
        value = text_fleet_checks(Path(args[0]).read_text(), read_json(args[1]))
    elif operation == "fleet-drift":
        value = fleet_drift_checks(read_json(args[0]), Path(args[1]).read_text(), read_json(args[2]))
    elif operation == "stopped-fleet":
        value = stopped_fleet_checks(Path(args[0]).read_text(), read_json(args[1]), args[2])
    elif operation == "transcript":
        value = transcript(read_json(args[0])["entries"])
    elif operation == "herdr-schema":
        value = herdr_methods(read_json(args[0]))
    elif operation == "lost":
        value = lost_checks(Path(args[0]).read_text(), read_json(args[1]), read_json(args[2]))
    elif operation == "deleted-cwd":
        value = deleted_cwd_checks(read_json(args[0]), read_json(args[1]))
    elif operation == "identity":
        value = identity_checks(read_json(args[0]), read_json(args[1]))
    elif operation == "engine-birth":
        value = engine_birth(read_json(args[0]), read_json(args[1]))
    elif operation == "supervisor-birth":
        value = supervisor_birth(read_json(args[0]), read_json(args[1]))
    elif operation == "delivery":
        value = delivery(read_json(args[0]))
    elif operation == "call":
        value = call_checks(read_json(args[0]), read_json(args[1]), read_json(args[2]), args[3])
    else:
        raise ValueError(f"READER-PROJECTION: {operation}")
    print(json.dumps(value, sort_keys=True, separators=(",", ":")))


if __name__ == "__main__":
    try:
        main(*sys.argv[1:])
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(f"READER-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
