#!/usr/bin/env python3
"""Prepare a recipient through its own composer, preserving its carried identity."""
import json
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from runtime import read_json, write_json
from terminal import text


def prepare(state, chat_file, directory):
    chat = read_json(chat_file)
    root = Path(directory)
    root.mkdir(parents=True, exist_ok=True)
    if state == "draft":
        text(chat["terminal"], "owner-unsent-draft")
        return
    if state == "idle":
        return
    if state == "working":
        gate = root / "recipient-working"
        gate.write_text("release after delivery receipt\n")
        steps = [{"type": "hold", "until_gone": str(gate)}, {"type": "turn", "reply": "work-finished", "busy_ms": 200}]
    elif state in {"modal", "blocked"}:
        steps = read_json(Path(__file__).parent / (state + ".json"))
    else:
        raise ValueError(f"MESH-RECIPIENT-STATE: {state}")
    write_json(root / "recipient-steps.json", steps)


if __name__ == "__main__":
    try:
        prepare(*sys.argv[1:])
    except (OSError, ValueError, KeyError) as error:
        print(f"MESH-RECIPIENT-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
