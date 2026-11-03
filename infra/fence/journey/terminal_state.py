#!/usr/bin/env python3
"""Read terminal outcomes used by terminal-open; never synthesizes a success."""
import json
import os
from pathlib import Path
import re
import sys

import herdr
from observation import screen
from runtime import read_json, write_json


def panes(workspace):
    result = herdr.request(os.environ["HERDR_SOCKET_PATH"], "pane.list", {})["result"]
    return [pane for pane in result["panes"] if pane["workspace_id"] == workspace]


def picker(target):
    text = screen(target)
    # Full raw pane text is retained in observe; this pins the required rendered structure.
    projects = sorted(re.findall(r"^[│ ]*╭─ (atlas|beacon|cobalt) ─", text, re.MULTILINE))
    return {"projects": projects, "header": "pfm" if text.lstrip().startswith("◆ pfm ") else "missing",
            "new_chat": "New chat" if "✦ New chat →" in text else "missing",
            "pane": target["pane"], "backend": target["backend"]}


def main(operation, *args):
    if operation == "panes":
        write_json(args[1], panes(args[0]))
    elif operation == "new-pane":
        before = {pane["pane_id"] for pane in read_json(args[1])}
        current = [pane for pane in panes(args[0]) if pane["pane_id"] not in before]
        if not current:
            return 1
        if len(current) != 1:
            raise ValueError(f"NEW-PANE-AMBIGUOUS: {[pane['pane_id'] for pane in current]}")
        write_json(args[2], {"backend": "herdr", "pane": current[0]["pane_id"]})
    elif operation == "picker-ready":
        return int("New chat" not in screen(read_json(args[0])))
    elif operation == "shell-ready":
        lines = [line for line in screen(read_json(args[0])).splitlines() if line.strip()]
        return int(not lines or not lines[-1].rstrip().endswith(("%", "$", "#")))
    elif operation == "picker":
        print(json.dumps(picker(read_json(args[0])), sort_keys=True))
    else:
        raise ValueError(f"TERMINAL-STATE-OPERATION: {operation}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(*sys.argv[1:]))
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print(f"TERMINAL-STATE-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
