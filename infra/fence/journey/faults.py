#!/usr/bin/env python3
"""Reversible journey faults, bounded by run ownership and process birth.

Python: unreadable(path), deleted_cwd(path), stopped_tmux(socket), dead_tmux(socket, restore),
signalled_process(target, birth, signal_name, restore=None), moved_pane(target,
workspace_id, move), unit_state(unit, active), stopped_herdr(socket),
locked_activity(log), locked_reporter(chat, scope), released_agent(chat, restore).
preserved_file(path), temporary_text(path, contents, mode=None).
temporarily_missing_file(path).
Each is a context manager.
CLI: faults.py unreadable|deleted-cwd PATH -- COMMAND...
     faults.py stopped-tmux SOCKET -- COMMAND...
     faults.py killed-process CHAT-JSON BIRTH-JSON OWNER-TARGET -- COMMAND...
     faults.py signalled-process STOP|TERM|KILL CHAT-JSON BIRTH-JSON OWNER-TARGET -- COMMAND...
Only real operations are performed; unsupported kernel failures are not simulated.
"""
from contextlib import contextmanager, nullcontext
import ctypes
import errno
import os
import json
import shlex
from pathlib import Path
import signal
import sys

from observation import process_snapshot, terminal
from observation_process import environment
from runtime import command, now_ms, read_json, write_text, write_json
from herdr import request
from faults_files import (preserved_file, temporary_text, temporarily_missing_file, unreadable,
                          deleted_cwd, locked_activity, locked_file, locked_reporter, aged_files, file_read_error, file_symlink,
                          unlistable_directory)
from faults_waiter import delayed_tmux_waiter
from faults_tmux import tmux_birth, stopped_tmux, dead_tmux, empty_tmux, refusing_socket
from faults_indicator import retired_indicator
from faults_process import validate_target_birth, stopped_for_move
from faults_targets import owned_target, fresh_target, restoration_owner


def owned_path(value, *, preserve=False):
    root = Path(os.environ["JOURNEY_ROOT"]).resolve(strict=True)
    path = Path(value)
    if not path.is_absolute() or (path.is_symlink() and not preserve):
        raise ValueError(f"FAULT-PATH: absolute non-symlink required: {value}")
    actual = path.resolve(strict=not preserve)
    actual.relative_to(root)
    if preserve:
        actual = path.parent.resolve(strict=True) / path.name
        actual.relative_to(root)
    if actual == root or actual == Path(os.environ["HOME"]).resolve():
        raise ValueError(f"FAULT-PATH: cannot fault a run/home root: {value}")
    return actual


def herdr_methods(schema):
    methods = [row["properties"]["method"]["const"] for row in schema["schemas"]["request"]["oneOf"]]
    if not methods or not all(isinstance(method, str) for method in methods) or len(set(methods)) != len(methods):
        raise ValueError("READER-HERDR-SCHEMA: distinct advertised method strings required")
    return sorted(methods)


def herdr_capabilities(prefix):
    prefix = Path(prefix)
    owned_path(prefix.parent)
    artifacts = {suffix: Path(str(prefix) + suffix) for suffix in (".version.txt", ".pane-help.txt", ".schema.json")}
    if any(path.exists() or path.is_symlink() for path in artifacts.values()):
        raise ValueError(f"FAULT-HERDR-ARTIFACT-EXISTS: {prefix}")
    for suffix, arguments in ((".version.txt", ["--version"]), (".pane-help.txt", ["pane", "--help"]), (".schema.json", ["api", "schema", "--json"])):
        write_text(artifacts[suffix], command(["herdr", *arguments]))
    version = artifacts[".version.txt"].read_text().strip()
    if version != "herdr 0.9.5":
        raise ValueError(f"FAULT-HERDR-PIN: expected herdr 0.9.5, actual={version!r}; prefix={prefix}")
    methods = herdr_methods(read_json(artifacts[".schema.json"]))
    if "pane.get" not in methods:
        raise ValueError(f"FAULT-HERDR-SCHEMA: pane.get missing; prefix={prefix}")
    available = {name: "pane." + name in methods for name in ("recycle", "respawn", "restart")}
    return {"version": version, "methods": methods, "isolated_recycle": any(available.values()), **available,
            "artifacts": {suffix: str(path) for suffix, path in artifacts.items()}}






def checked_birth(birth):
    pid = birth["pid"]
    if not isinstance(pid, int) or pid <= 1 or pid == os.getpid():
        raise ValueError(f"FAULT-PID: {pid}")
    process = process_snapshot().get(pid)
    if process is None:
        raise ValueError(f"FAULT-PROCESS-ABSENT: {pid}")
    if process["start"] != birth["start"]:
        raise ValueError(f"FAULT-BIRTH-CHANGED: {pid}: expected={birth['start']} actual={process['start']}")
    if environment(pid).get("PFM_RUN") != os.environ["PFM_RUN"]:
        raise ValueError(f"FAULT-PROCESS-FOREIGN: {pid}")
    return process


@contextmanager
def released_agent(chat, restore):
    target = chat["terminal"]
    owned_target(target)
    if target["backend"] != "herdr" or chat["engine"] != "claude":
        raise ValueError("FAULT-AGENT: carried Herdr Claude recipient required")
    if not callable(restore):
        raise ValueError("FAULT-AGENT-RESTORE: registration callback required")
    try:
        request(os.environ["HERDR_SOCKET_PATH"], "pane.release_agent", {
            "pane_id": target["pane"], "source": "pfm", "agent": "claude",
            "seq": now_ms() * 1_000_000,
        })
        yield target
    finally:
        restore()




@contextmanager
def signalled_process(target, birth, signal_name, restore=None):
    observed, process = validate_target_birth(target, birth)
    if signal_name not in {"STOP", "TERM", "KILL"}:
        raise ValueError(f"FAULT-SIGNAL: {signal_name}")
    if signal_name != "STOP" and not callable(restore):
        raise ValueError("FAULT-DEATH-RESTORE: reopen callback required")
    if signal_name == "STOP":
        with stopped_birth(birth):
            yield birth
    else:
        signal_birth(birth, getattr(signal, "SIG" + signal_name))
        try:
            yield birth
        finally:
            restore()




def signal_birth(birth, number):
    checked_birth(birth)
    os.kill(birth["pid"], number)


def birth_exited(birth):
    process = process_snapshot().get(birth["pid"])
    return process is None or process["start"] != birth["start"] or process["state"] == "Z"


@contextmanager
def stopped_birth(birth):
    signal_birth(birth, signal.SIGSTOP)
    try:
        yield birth
    finally:
        signal_birth(birth, signal.SIGCONT)


@contextmanager
def stopped_herdr(socket):
    socket = owned_path(socket)
    if str(socket) != os.environ["HERDR_SOCKET_PATH"]:
        raise ValueError("FAULT-HERDR-SOCKET: not this run's server")
    matches = []
    for process in process_snapshot().values():
        argv = shlex.split(process["argv"])
        if argv and Path(argv[0]).name == "herdr" and argv[1:] == ["--session", os.environ["JOURNEY_SESSION"], "server"]:
            if environment(process["pid"]).get("PFM_RUN") == os.environ["PFM_RUN"]:
                matches.append(process)
    if len(matches) != 1:
        raise ValueError(f"FAULT-HERDR-SERVER: expected one owned server, got {len(matches)}")
    with stopped_birth({"pid": matches[0]["pid"], "start": matches[0]["start"]}) as birth:
        yield birth







@contextmanager
def nondumpable(prefix):
    """Keep this owned caller alive as a genuinely unreadable Linux ancestor."""
    prefix = Path(prefix)
    owned_path(prefix.parent)
    if sys.platform != "linux" or not os.environ.get("PFM_RUN"):
        raise ValueError("FAULT-NONDUMPABLE-PLATFORM: owned Linux caller required")
    receipt = prefix.with_name(prefix.name + ".environ.json")
    if receipt.exists() or receipt.is_symlink():
        raise ValueError(f"FAULT-NONDUMPABLE-RECEIPT-EXISTS: {receipt}")
    libc = ctypes.CDLL(None, use_errno=True)
    libc.prctl.argtypes = [ctypes.c_int, ctypes.c_ulong, ctypes.c_ulong, ctypes.c_ulong, ctypes.c_ulong]
    libc.prctl.restype = ctypes.c_int
    def setting(operation, value=0):
        result = libc.prctl(operation, value, 0, 0, 0)
        if result == -1:
            code = ctypes.get_errno()
            raise OSError(code, os.strerror(code))
        return result
    probe = ('import os,json; result={"pid":os.getpid(),"parent_pid":os.getppid(),"errno":0}\n'
             'try:\n with open("/proc/"+str(os.getppid())+"/environ","rb") as stream: stream.read()\n'
             'except OSError as error: result["errno"]=error.errno\n'
             'result["capabilities"]=[line.strip() for line in open("/proc/self/status") if line.startswith("Cap")]\nprint(json.dumps(result))')
    before = json.loads(command([sys.executable, "-c", probe]))
    prior = setting(3)  # Linux PR_GET_DUMPABLE / PR_SET_DUMPABLE operate on this caller only.
    if prior != 1 or before["errno"] != 0:
        raise ValueError(f"FAULT-NONDUMPABLE-PRECONDITION: dumpable={prior}, probe={before}")
    birth = process_snapshot()[os.getpid()]
    try:
        setting(4, 0)
        denied = json.loads(command([sys.executable, "-c", probe]))
        if denied["parent_pid"] != os.getpid() or denied["errno"] != errno.EACCES:
            raise ValueError(f"FAULT-NONDUMPABLE-NOT-DENIED: dumpable={setting(3)}: {denied}")
        evidence = {"birth": {"pid": birth["pid"], "start": birth["start"]}, "before": before, "denied": denied}
        write_json(receipt, evidence)
        yield evidence
    finally:
        setting(4, prior)






@contextmanager
def moved_pane(target, workspace_id, move):
    before = owned_target(target)
    carry = read_json(os.environ["JOURNEY_CARRY"])
    allowed = {row["id"] for row in carry["workspaces"].values()}
    if workspace_id not in allowed or target["backend"] != "herdr":
        raise ValueError(f"FAULT-WORKSPACE: {workspace_id}")
    original = before["identity"]["workspace"]
    current = target
    try:
        current = move(target, workspace_id) or target
        after = terminal(current)
        if after["identity"]["workspace"] != workspace_id or after["pane"]["terminal_id"] != before["pane"]["terminal_id"]:
            raise ValueError("FAULT-MOVE-IDENTITY: key moved another terminal or workspace")
        yield current
    finally:
        move(current, original)


@contextmanager
def unit_state(unit, active):
    prefix = os.environ["PFM_UNIT_PREFIX"]
    facade = Path(__file__).resolve().parents[3] / "scripts/platform.sh"
    platform = command(["bash", "-c", 'source "$1"; platform_os', "fault-platform", str(facade)]).strip()
    namespace = "com.professor." + prefix + "." if platform == "darwin" else prefix + "-"
    if not prefix or not unit.startswith(namespace) or len(unit) == len(namespace) or any(c not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_." for c in unit):
        raise ValueError(f"FAULT-UNIT-OWNERSHIP: {unit}")
    if platform == "linux":
        before = command(["systemctl", "--user", "is-active", unit], allowed_codes=(0, 3)).strip()
        if before not in {"active", "inactive"}:
            raise ValueError(f"FAULT-UNIT-STATE: {unit}: {before}")
        wanted = "start" if active else "stop"
        command(["systemctl", "--user", wanted, unit])
        try:
            yield unit
        finally:
            command(["systemctl", "--user", "start" if before == "active" else "stop", unit])
    elif platform == "darwin":
        plist = owned_path(Path(os.environ["HOME"]) / "Library/LaunchAgents" / (unit + ".plist"))
        domain = "gui/" + str(os.getuid())
        target = domain + "/" + unit
        before = command(["launchctl", "print", target], allowed_codes=(0, 113))
        loaded = "state =" in before
        if active != loaded:
            command(["launchctl", "bootstrap", domain, str(plist)] if active else ["launchctl", "bootout", target])
        try:
            yield unit
        finally:
            if active != loaded:
                command(["launchctl", "bootstrap", domain, str(plist)] if loaded else ["launchctl", "bootout", target])
    else:
        raise ValueError(f"FAULT-PLATFORM: {platform}")


def interrupted(number, frame):
    raise RuntimeError(f"FAULT-INTERRUPTED: signal={number}")


@contextmanager
def interruptible():
    previous = signal.signal(signal.SIGTERM, interrupted)
    try:
        yield
    finally:
        signal.signal(signal.SIGTERM, previous)


def main(operation, *arguments):
    if operation == "herdr-capabilities" and len(arguments) == 1:
        print(json.dumps(herdr_capabilities(arguments[0]), sort_keys=True))
        return
    if "--" not in arguments:
        raise ValueError("FAULT-USAGE: OPERATION RESOURCE... -- COMMAND...")
    boundary = arguments.index("--")
    resources, action = arguments[:boundary], arguments[boundary + 1:]
    if not action:
        raise ValueError("FAULT-USAGE: action required")
    owner_scope = nullcontext()
    operations = {"unreadable": unreadable, "deleted-cwd": deleted_cwd, "stopped-tmux": stopped_tmux, "stopped-herdr": stopped_herdr, "locked-activity": locked_activity}
    if (operation == "killed-process" and len(resources) == 3) or (operation == "signalled-process" and len(resources) == 4):
        from terminal import type_command
        signal_name = resources[0] if operation == "signalled-process" else "KILL"
        chat, birth, owner = [read_json(value) for value in (resources[1:] if operation == "signalled-process" else resources)]
        owner_scope = restoration_owner(owner)
        scope = signalled_process(chat["terminal"], birth, signal_name,
            restore=lambda: type_command(owner, ["pfm", "chat", "open", chat["session_id"]]))
    elif operation == "locked-reporter" and len(resources) == 2:
        scope = locked_reporter(read_json(resources[0]), resources[1])
    elif operation in operations and len(resources) == 1:
        scope = operations[operation](resources[0])
    else:
        raise ValueError(f"FAULT-USAGE: unknown operation/resources: {operation} {resources}")
    with interruptible(), owner_scope, scope:
        sys.stdout.write(command(list(action), timeout=120))


if __name__ == "__main__":
    try:
        main(*sys.argv[1:])
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print(f"FAULT-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
