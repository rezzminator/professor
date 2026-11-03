#!/usr/bin/env python3
"""Open the real picker and retain the selected scratch chat during a fault."""
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from bootstrap import wait
from observation import fleet_rows, screen
from observation_picker import select_session, selection
from runtime import now_ms, read_json, write_json
from terminal import keys, type_command


def open_picker(owner_file, chat_file, prefix, mode):
    owner, chat = read_json(owner_file), read_json(chat_file)
    type_command(owner, ["pfm"])
    wait("native reader picker", lambda: "◆ pfm " in screen(owner), 10)
    identities = [row["id"] for row in fleet_rows()]
    shown = select_session(owner, chat, identities, screen, keys)
    started = now_ms()
    samples = []
    if mode == "dead":
        def retained():
            current = screen(owner)
            identity = selection(current, identities)
            if identity != ("session", chat["session_id"]):
                raise ValueError(f"READER-PICKER-SELECTION-CHANGED: {identity!r}")
            elapsed = now_ms() - started
            samples.append({"elapsed_ms": elapsed, "identity": identity})
            return current if elapsed >= 10_000 else None
        shown = wait("ten seconds of actual picker refresh with selected lost seat", retained, 13)
    write_json(str(prefix) + ".picker.json", {"screen": shown, "selected": selection(shown, identities),
               "elapsed_ms": now_ms() - started, "refresh_samples": samples})


if __name__ == "__main__":
    try:
        open_picker(*sys.argv[1:])
    except (OSError, ValueError, RuntimeError, KeyError, TypeError) as error:
        print(f"READER-PICKER-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
