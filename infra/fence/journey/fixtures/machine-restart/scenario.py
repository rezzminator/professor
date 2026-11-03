#!/usr/bin/env python3
"""Stage ordinary launch inputs and scoped faults for the restart walk."""
from contextlib import ExitStack
import os
from pathlib import Path
import sys

from bootstrap import wait
from chat import ensure_role
from claude_versions import prior_version
from faults import preserved_file, temporarily_missing_file, unit_state
from observation import bundle, indicator_daemon
from runtime import command, read_json, write_json
from terminal import type_command
from control import unit_name


def run(kind, slug):
    here = Path(__file__).resolve().parents[2]
    root = Path(os.environ["RESTART_ROOT"])
    branch = os.environ["JOURNEY_OS"] + "+" + os.environ["HERDR_KIND"] + "+project+claude"
    factor = {"kind": kind}
    with ExitStack() as scope:
        if kind == "pruned":
            factor.update(scope.enter_context(prior_version()))
        if kind == "preference":
            config_path = scope.enter_context(preserved_file(os.environ["PFM_CONFIG"]))
            config = read_json(config_path)
            account = next(row for row in config["accounts"] if row["id"] == 1)
            account.setdefault("claude", {})["remoteControl"] = True
            write_json(config_path, config)
        if kind in {"pruned", "preference"}:
            chat = ensure_role(branch, "restart-" + kind, "restart-" + kind, here / "fixtures/chat-birth/scenario.json")
            factor["chat"] = chat
            before = bundle(os.environ["OWNER"])
            if before["errors"]:
                raise ValueError(f"RESTART-FACTOR-OBSERVATION: {before['errors']!r}")
            write_json(root / (kind + "-born.json"), before)
        if kind == "pruned":
            scope.enter_context(temporarily_missing_file(factor["old"]))
        elif kind == "preference":
            account["claude"]["remoteControl"] = False
            write_json(config_path, config)
        elif kind == "indicators":
            if not isinstance(indicator_daemon(), dict):
                receipt = str(root / "indicator-prelude")
                type_command(read_json(os.environ["OWNER"]), ["sh", "-c", 'nohup pfm internal herdr-indicators >"$JOURNEY_ROOT/indicator-prelude.log" 2>&1 </dev/null &'], receipt)
                wait("indicator prelude command", lambda: Path(receipt + ".status").exists(), 10)
                if Path(receipt + ".status").read_text() != "0\n":
                    raise ValueError(f"RESTART-INDICATOR-START: {receipt}")
            factor["birth"] = wait("owned indicator daemon", lambda: indicator_daemon() if isinstance(indicator_daemon(), dict) else None, 15)
        elif kind == "active-timer":
            scope.enter_context(unit_state(unit_name("schedule"), True))
        elif kind == "stopped-mcp":
            scope.enter_context(unit_state(unit_name("mcp"), False))
        elif kind != "pruned" and kind != "preference":
            raise ValueError(f"RESTART-FACTOR: {kind}")
        path = root / (kind + "-factor.json")
        write_json(path, factor)
        os.environ["RESTART_FACTOR"] = str(path)
        operation = "daemons" if kind in {"active-timer", "stopped-mcp"} else "herdr"
        sys.stdout.write(command(["bash", str(Path(__file__).with_name("walk.sh")), operation, slug], timeout=180))


if __name__ == "__main__":
    try:
        run(*sys.argv[1:])
    except (OSError, ValueError, KeyError, RuntimeError, StopIteration) as error:
        print(f"RESTART-SCENARIO: {error}", file=sys.stderr)
        sys.exit(2)
