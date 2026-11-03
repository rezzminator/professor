#!/usr/bin/env python3
"""Run-owned service and process faults, with the OS difference at one door."""
import json
import os
from pathlib import Path
import re
import sys

from observation import report, process_snapshot, screen
from bootstrap import wait
from faults import stopped_tmux, unit_state
from runtime import now_ms, command
from runtime import read_json, write_json
from terminal import type_command, keys


def process_birth(pid):
    return process_snapshot().get(pid)


def stopped_picker(target, socket):
    start = now_ms()
    with stopped_tmux(socket) as birth:
        type_command(target, ["pfm"])
        text = wait("picker bounded stopped-server warning", lambda: screen(target) if "kill -CONT" in screen(target) else None, 10)
        value = {"birth": birth, "elapsed_ms": now_ms() - start,
                 "warning": "kill -CONT" if "kill -CONT" in text else "missing", "text": text}
        keys(target, ["escape"])
    return value


def unit_name(kind):
    prefix = os.environ["PFM_UNIT_PREFIX"]
    if not re.fullmatch(r"[a-z][a-z0-9-]{0,62}", prefix):
        raise ValueError("RESTART-UNIT-PREFIX: invalid run namespace")
    if kind not in {"mcp", "schedule", "theme"}:
        raise ValueError(f"RESTART-UNIT-KIND: {kind}")
    return f"com.professor.{prefix}.{kind}" if sys.platform == "darwin" else prefix + "-" + kind + (".timer" if kind == "schedule" else ".service")


def services():
    if sys.platform == "darwin":
        label = f"gui/{os.getuid()}/" + unit_name("mcp")
        text = report(["launchctl", "print", label])
        pids = re.findall(r"^\s*pid = (\d+)\s*$", text, re.MULTILINE)
        if len(pids) != 1:
            raise ValueError("RESTART-LAUNCHD-PID: expected one live MCP process")
        schedule = report(["launchctl", "print", f"gui/{os.getuid()}/" + unit_name("schedule")])
        states = re.findall(r"^\s*state = (.+)\s*$", schedule, re.MULTILINE)
        if len(states) != 1 or states[0] not in {"running", "not running"}:
            raise ValueError(f"RESTART-LAUNCHD-SCHEDULE: {states!r}")
        pid, active = int(pids[0]), "active"
    else:
        pid = int(report(["systemctl", "--user", "show", unit_name("mcp"), "--property=MainPID", "--value"]).strip())
        active = report(["systemctl", "--user", "show", unit_name("schedule"), "--property=ActiveState", "--value"]).strip()
        mcp_state = report(["systemctl", "--user", "show", unit_name("mcp"), "--property=ActiveState", "--value"]).strip()
        if mcp_state == "inactive" and pid == 0:
            return {"mcp": "n/a:inactive", "schedule": active}
    birth = process_birth(pid)
    if birth is None:
        raise ValueError(f"RESTART-MCP-BIRTH-MISSING: {pid}")
    return {"mcp": birth, "schedule": active}


def unit_loaded(kind):
    name = unit_name(kind)
    if sys.platform == "darwin":
        receipt = {}
        value = report(["launchctl", "print", f"gui/{os.getuid()}/" + name], allowed_codes=(0, 113), capture=receipt)
        if receipt["status"] == 113:
            return {"name": name, "loaded": False, "state": "absent"}
        states = re.findall(r"^\s*state = (.+?)\s*$", value, re.MULTILINE)
        if len(states) != 1:
            raise ValueError(f"RESTART-UNIT-UNREADABLE: {name}: {value!r}")
        return {"name": name, "loaded": True, "state": states[0]}
    value = report(["systemctl", "--user", "show", name, "--property=LoadState", "--value"]).strip()
    if value not in {"loaded", "not-found"}:
        raise ValueError(f"RESTART-UNIT-UNREADABLE: {name}: {value!r}")
    return {"name": name, "loaded": value == "loaded", "state": value}


if __name__ == "__main__":
    try:
        operation, *arguments = sys.argv[1:]
        if operation == "stopped-picker":
            target, socket, output = arguments
            write_json(output, stopped_picker(read_json(target), socket))
        elif operation == "scoped-unit":
            kind, active, *action = arguments
            if active not in {"active", "inactive"} or not action:
                raise ValueError("RESTART-SCOPE: KIND active|inactive COMMAND required")
            with unit_state(unit_name(kind), active == "active"):
                sys.stdout.write(command(action, timeout=120))
        elif operation == "snapshot":
            write_json(arguments[0], services())
        elif operation == "loaded":
            write_json(arguments[1], unit_loaded(arguments[0]))
        elif operation == "gone":
            birth = read_json(arguments[0])["mcp"]
            current = process_birth(birth["pid"])
            sys.exit(1 if current and current["start"] == birth["start"] else 0)
        else:
            raise ValueError(f"RESTART-CONTROL: {operation}")
    except (OSError, ValueError, KeyError, RuntimeError) as error:
        print(f"RESTART-CONTROL: {error}", file=sys.stderr)
        sys.exit(2)
