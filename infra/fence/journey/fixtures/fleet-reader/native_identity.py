#!/usr/bin/env python3
"""Unnamed callers resolve the durable address to their native transcript id."""
import os
import sys

from forecast import check
from chat_carry import ENGINES
from projection import engine_birth, identity_checks, selected
from runtime import read_json, write_json


def unnamed_checks(chat, before, after, cli, mcp):
    wanted = {"status": "ok", "session": "seat:" + chat["seat_id"],
              "id": chat["session_id"], "engine": ENGINES[chat["engine"]],
              "pane": chat["terminal"]["pane"], "socket_path": os.environ["HERDR_SOCKET_PATH"]}
    current = selected(after, chat)
    seats = [row for row in current["stored"]["seat"] if row["seat_id"] == chat["seat_id"]]
    return [check("unnamed-native-caller", "", chat["name"]),
            check("unnamed-durable-seat-name", [""], [row["name"] for row in seats]),
            check("unnamed-mcp-native-identity", wanted, {key: mcp.get(key) for key in wanted}),
            check("unnamed-same-native-engine-birth", engine_birth(before, chat), engine_birth(after, chat)),
            *identity_checks(cli, chat)]


if __name__ == "__main__":
    chat, before, after, cli, mcp, output = sys.argv[1:]
    write_json(output, unnamed_checks(*(read_json(path) for path in (chat, before, after, cli, mcp))))
