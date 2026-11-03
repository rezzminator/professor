#!/usr/bin/env python3
"""Exercise a lost seat within the one public process-fault scope."""
import os
from pathlib import Path
import sys

from bootstrap import wait
from chat_carry import temporary_expectation
from faults import signalled_process
from lifecycle import expect, state
from observation_process import engine_births
from projection import selected
from runtime import command, read_json
from terminal import type_command


def run(chat_path, owner_path, before_path):
    chat, owner, before = (read_json(path) for path in (chat_path, owner_path, before_path))
    births = engine_births(selected(before, chat), chat["engine"])
    if len(births) != 1:
        raise ValueError(f"ENDING-LOST-BIRTH: expected one engine, got {births!r}")
    root = Path(os.environ["JOURNEY_ROOT"])
    restoration = root / "lost-restore" / chat["key"]
    restoration.mkdir(parents=True, exist_ok=True)
    here = Path(__file__).resolve().parents[2]
    def restore():
        expect(chat, "live")
        for operation in ("unkill", "open"):
            receipt = str(restoration / operation)
            type_command(owner, ["pfm", "chat", operation, chat["session_id"]], receipt)
            wait("lost-seat restore " + operation, lambda: Path(receipt + ".status").exists(), 30)
            if Path(receipt + ".status").read_text().strip() != "0":
                raise ValueError(f"ENDING-LOST-RESTORE: {receipt}")
        wait("lost-seat restored idle", lambda: state(chat, "idle", str(root / "lost-restored.json")) == 0, 30)
        command([sys.executable, str(here / "chat.py"), "refresh", chat["branch"], chat.get("role", chat["origin"])])
    with temporary_expectation(chat["branch"], chat.get("role", chat["origin"]), {"lifecycle": "transient", "terminal": "live", "visibility": "visible"}, before=before):
        with signalled_process(chat["terminal"], births[0], "KILL", restore=restore):
            wait("lost engine dead", lambda: state(chat, "lost", str(root / "lost-state.json")) == 0, 20)
            command(["bash", str(Path(__file__).with_suffix(".sh")), chat_path, owner_path, before_path])


if __name__ == "__main__":
    try:
        run(*sys.argv[1:])
    except (OSError, ValueError, KeyError, RuntimeError) as error:
        print(f"ENDING-LOST: {error}", file=sys.stderr)
        sys.exit(2)
