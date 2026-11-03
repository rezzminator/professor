#!/usr/bin/env python3
"""Prepare the approved archive policy through public unkill, then read its proof."""
import os
from pathlib import Path
import sys

from bootstrap import wait
from lifecycle import expect
from observation_process import engine_births
from projection import selected
from runtime import read_json, write_json
from terminal import type_command


def prepare(candidate, owner, before):
    carried = read_json(os.environ["JOURNEY_CARRY"])
    owned = {sid for chat in carried["chats"] for sid in [chat["session_id"], *chat.get("lineage_ids", [])]}
    killed = {row["uuid"] for chat in before["chats"] for row in chat["stored"]["hidden"]}
    if killed - owned:
        raise ValueError(f"ARCHIVE-POLICY-UNOWNED: {sorted(killed - owned)}")
    cleared = []
    directory = Path(os.environ["JOURNEY_ROOT"]) / "archive-prelude" / candidate["key"]
    directory.mkdir(parents=True, exist_ok=True)
    for session in sorted(killed - {candidate["session_id"]}):
        receipt = str(directory / session)
        type_command(owner, ["pfm", "chat", "unkill", session], receipt)
        wait("archive prelude unkill receipt", lambda: Path(receipt + ".status").exists(), 20)
        if Path(receipt + ".status").read_text().strip() != "0":
            raise ValueError(f"ARCHIVE-POLICY-UNKILL: {receipt}")
        for chat in carried["chats"]:
            if chat["session_id"] == session:
                expect(chat, chat.get("expect", {}).get("lifecycle", "live"))
        cleared.append(session)
    return {"unkilled": cleared, "selected": candidate["session_id"]}


def checks(before, after, candidate, transcript):
    rows = []
    def equal(field, expected, actual):
        rows.append({"field": field, "expected": expected, "actual": actual})
    archived = selected(after, candidate)["lifecycle"]["ended"]["archive"]
    equal("archive-session", candidate["session_id"], archived["row"]["uuid"])
    equal("archive-bytes", transcript["sha256"], archived["sha256"])
    equal("original-path", "absent", archived["original"])
    return rows + peer_checks(before, after, candidate["key"])


def peer_checks(before, after, excluded=None):
    rows = [{"field": "foreign", "expected": before["foreign"], "actual": after["foreign"]}]
    def equal(field, expected, actual):
        rows.append({"field": field, "expected": expected, "actual": actual})
    carried = read_json(os.environ["JOURNEY_CARRY"])
    for chat in carried["chats"]:
        if chat["key"] == excluded:
            continue
        old, current = selected(before, chat), selected(after, chat)
        for field in ("picker", "stored"):
            equal(chat["key"] + ":" + field, old[field], current[field])
        if isinstance(old["status"], dict):
            equal(chat["key"] + ":status", {key: value for key, value in old["status"].items() if key != "idle_seconds"}, {key: value for key, value in current["status"].items() if key != "idle_seconds"})
        else:
            equal(chat["key"] + ":status", old["status"], current["status"])
        if isinstance(old["terminal"], dict) and old["status"]["state"] != "dead":
            equal(chat["key"] + ":birth", engine_births(old, chat["engine"]), engine_births(current, chat["engine"]))
    return rows


if __name__ == "__main__":
    try:
        operation, *args = sys.argv[1:]
        if operation == "prepare":
            value = prepare(*(read_json(path) for path in args[:-1]))
        elif operation == "checks":
            value = checks(*(read_json(path) for path in args[:-1]))
        elif operation == "kept":
            value = peer_checks(*(read_json(path) for path in args[:-1]))
        else:
            raise ValueError(f"ARCHIVE-OPERATION: {operation}")
        write_json(args[-1], value)
    except (OSError, ValueError, KeyError, RuntimeError, TypeError) as error:
        print(f"ENDING-ARCHIVE: {error}", file=sys.stderr)
        sys.exit(2)
