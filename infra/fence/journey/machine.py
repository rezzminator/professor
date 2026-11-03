#!/usr/bin/env python3
"""Installed-machine projections for lifecycle assertions."""
import hashlib
import json
import os
from pathlib import Path
import re
import sys

from observation import fleet_rows, foreign_projection, report, screen
import observation_picker
from runtime import command, read_json, write_json


def install_idle():
    """Wait before the owner action, never retry a refused installation."""
    prefix = os.environ["PFM_UNIT_PREFIX"]
    states = []
    for job in ("name-sync", "schedule"):
        if sys.platform == "darwin":
            output = command(["launchctl", "print", f"gui/{os.getuid()}/com.professor.{prefix}.{job}"])
            state = _properties(output).get("state")
            if not state:
                raise ValueError(f"INSTALL-GATE-UNREADABLE: {job}: {output!r}")
            states.append(state != "running")
        else:
            state = command(["systemctl", "--user", "show", "--property=ActiveState", "--value", f"{prefix}-{job}.service"]).strip()
            if state not in {"active", "activating", "deactivating", "reloading", "refreshing", "inactive", "failed"}:
                raise ValueError(f"INSTALL-GATE-UNREADABLE: {job}: {state!r}")
            states.append(state in {"inactive", "failed"})
    return all(states)


def installed():
    managed = Path(os.environ["HOME"]) / ".local/share/pfm/install"
    if not managed.is_dir():
        raise ValueError(f"MANAGED-ABSENT: {managed}")
    hashes = {}
    for path in sorted(managed.rglob("*")):
        if path.is_symlink():
            hashes[str(path.relative_to(managed))] = "link:" + os.readlink(path)
        elif path.is_file():
            hashes[str(path.relative_to(managed))] = hashlib.sha256(path.read_bytes()).hexdigest()
    prefix = os.environ["PFM_UNIT_PREFIX"]
    if sys.platform == "darwin":
        return _installed_launchd(hashes, prefix)

    units = {}
    for name in ("mcp.service", "schedule.timer", "name-sync.path"):
        unit = prefix + "-" + name
        units[unit] = command(["systemctl", "--user", "show", unit, "--property=ActiveState,ExecStart,ExecMainStartTimestampMonotonic,FragmentPath"])
        if "ActiveState=active\n" not in units[unit]:
            raise ValueError(f"UNIT-NOT-ACTIVE: {unit}: {units[unit]}")
    pid = int(command(["systemctl", "--user", "show", prefix + "-mcp.service", "--property=MainPID", "--value"]).strip())
    manager = _properties(command(["systemctl", "--user", "show", prefix + "-mcp.service",
                                   "--property=InvocationID,ExecMainStartTimestampMonotonic,NRestarts"]))
    if not manager.get("InvocationID"):
        raise ValueError(f"MCP-MANAGER-RECEIPT-ABSENT: {prefix}-mcp.service")
    if not manager.get("ExecMainStartTimestampMonotonic"):
        raise ValueError(f"MCP-MANAGER-START-ABSENT: {prefix}-mcp.service")
    expected_binary = hashlib.sha256((Path(os.environ["HOME"]) / ".local/bin/pfm").read_bytes()).hexdigest()
    running_binary = hashlib.sha256(Path(f"/proc/{pid}/exe").read_bytes()).hexdigest()
    if running_binary != expected_binary:
        raise ValueError(f"UNIT-BINARY-MISMATCH: {prefix}-mcp.service pid={pid} expected={expected_binary} actual={running_binary}")
    return {"files": hashes, "units": units, "mcp": {"pid": pid,
            "start": manager["ExecMainStartTimestampMonotonic"],
            "receipt": manager["InvocationID"],
            "restarts": manager.get("NRestarts", "")}}


def _properties(text):
    return {key.strip(): value.strip() for line in text.splitlines() if "=" in line
            for key, value in [line.split("=", 1)]}


def _installed_launchd(hashes, prefix):
    labels = {name: f"gui/{os.getuid()}/com.professor.{prefix}.{name}"
              for name in ("mcp", "schedule", "name-sync")}
    units = {label: command(["launchctl", "print", label]) for label in labels.values()}
    for label, output in units.items():
        if "state = running\n" not in output:
            raise ValueError(f"UNIT-NOT-ACTIVE: {label}: {output}")
    mcp = _properties(units[labels["mcp"]])
    if not mcp.get("pid"):
        raise ValueError(f"MCP-MANAGER-PID-ABSENT: {labels['mcp']}")
    # launchd's `runs` is its activation counter; unlike a PID it is owned by
    # the manager and remains useful when a daemon is replaced quickly.
    if not mcp.get("runs"):
        raise ValueError(f"MCP-MANAGER-RECEIPT-ABSENT: {labels['mcp']}")
    return {"files": hashes, "units": units, "mcp": {"pid": int(mcp["pid"]),
            "start": mcp["runs"], "receipt": mcp["runs"]}}


def doctor(path):
    return doctor_text(Path(path).read_text())


def doctor_text(text):
    reports = [line for line in text.splitlines() if line.startswith("doctor:")]
    if not reports:
        raise ValueError("DOCTOR-REPORT-ABSENT")
    # Optional absent tools are informational; explicit warnings and failures are not.
    return {"errors": [line for line in text.splitlines()
                       if re.search(r"\b(?:warnings|failures)=[1-9]|\b(?:WARN|FAIL|UNREACHABLE|DRIFT|UNTRUSTED|CONFLICT|CHECK FAILED)\b", line)
                       or (" MISSING" in line and " MISSING optional " not in line)],
            "families": sorted({name for name in ("claude", "codex", "opencode", "herdr", "mcp")
                                if any(name in line.lower() for line in reports)}),
            "hook_accounts": sorted(set(re.findall(r"^doctor: hook (\S+) .+ ok$", text, re.MULTILINE))),
            "herdr_shell": re.findall(r'^doctor: herdr-shell: ok .*default_shell="([^"]+)"', text, re.MULTILINE),
            "mcp_endpoint": re.findall(r"^doctor: mcp daemon=running .* endpoint=(\S+)$", text, re.MULTILINE)}


def gap_only(path, source, *sources):
    state = read_json(path)
    failed = [row for row in state["assertions"] if not row["pass"]]
    return not state["errors"] and bool(failed) and all(row["source"] in (source, *sources) for row in failed)


def chat_state(session, wanted, receipt):
    actual = json.loads(report(["pfm", "chat", "status", session, "--json"]))
    if actual["state"] != wanted:
        return False
    if wanted == "idle":
        from terminal import keys
        declared = read_json(os.environ["JOURNEY_CARRY"])
        matches = [chat for chat in declared["chats"] if chat["session_id"] == session]
        if len(matches) != 1:
            raise ValueError(f"MACHINE-CHAT-IDENTITY: {session}: {matches!r}")
        actual["picker"] = observation_picker.badges(declared["observer_picker"], matches, fleet_rows(), screen, keys)[session]
        if actual["picker"]["state"] != wanted:
            return False
    write_json(receipt, actual)
    return True


if __name__ == "__main__":
    try:
        if sys.argv[1] == "install-idle":
            sys.exit(0 if install_idle() else 1)
        if sys.argv[1] == "release":
            print(report(["pfm", "version"]).strip())
            sys.exit(0)
        if sys.argv[1] == "chat-state":
            sys.exit(0 if chat_state(*sys.argv[2:5]) else 1)
        if sys.argv[1] == "gap-only":
            sys.exit(0 if gap_only(*sys.argv[2:]) else 1)
        if sys.argv[1] == "installed":
            value = installed()
        elif sys.argv[1] == "serving":
            value = doctor_text(report(["pfm", "doctor"], allowed_codes=(0, 1)))
        elif sys.argv[1] == "carried":
            value = foreign_projection(next(chat for chat in read_json(sys.argv[2])["chats"] if chat["name"] == "carried-chat"))
        else:
            value = doctor(sys.argv[2])
        print(json.dumps(value, sort_keys=True, separators=(",", ":")))
    except (OSError, ValueError, RuntimeError) as error:
        print(f"MACHINE-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
