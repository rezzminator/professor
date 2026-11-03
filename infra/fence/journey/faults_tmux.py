"""Private tmux fault mechanisms; callers use the faults public door."""
from contextlib import contextmanager
import os
import errno
from pathlib import Path
import signal

from observation import process_snapshot
from runtime import command, read_json


def refusing_socket(socket):
    """Read an owned retained Unix socket's actual refused connection."""
    from faults import owned_path
    from faults_waiter import socket_connect_errno
    socket = owned_path(socket)
    if not socket.is_socket():
        raise ValueError(f"FAULT-SOCKET-NOT-SOCKET: {socket}")
    before = socket.stat()
    actual = socket_connect_errno(socket, 2)
    after = socket.stat()
    if (before.st_dev, before.st_ino) != (after.st_dev, after.st_ino):
        raise ValueError(f"FAULT-SOCKET-REPLACED: {socket}")
    if actual != errno.ECONNREFUSED:
        raise ValueError(f"FAULT-SOCKET-NOT-REFUSING: expected={errno.ECONNREFUSED} actual={actual}: {socket}")
    return {"socket": str(socket), "errno": actual}


def tmux_birth(socket):
    from faults import owned_path, checked_birth
    socket = owned_path(socket)
    # The server answers its own pid before STOP; no process-name or global socket lookup.
    pid = int(command(["tmux", "-S", str(socket), "display-message", "-p", "#{pid}"]).strip())
    process = process_snapshot().get(pid)
    if process is None:
        raise ValueError(f"FAULT-TMUX-OWNERSHIP: {socket}: pid={pid}")
    birth = {"pid": pid, "start": process["start"]}
    checked_birth(birth)
    return birth


@contextmanager
def stopped_tmux(socket):
    from faults import stopped_birth
    birth = tmux_birth(socket)
    with stopped_birth(birth):
        yield birth


def auxiliary(socket, restore):
    from faults import owned_path
    socket = owned_path(socket)
    if not callable(restore):
        raise ValueError("FAULT-TMUX-RESTORE: server factory callback required")
    if os.environ.get("JOURNEY_CARRY"):
        carry = read_json(os.environ["JOURNEY_CARRY"])
        targets = [chat["terminal"] for chat in [*carry["chats"], carry["foreign"]]]
        targets.extend(carry[key] for key in ("owner", "client", "picker", "observer_picker") if carry.get(key))
        if any(target["backend"] == "tmux" and Path(target["socket"]).resolve() == socket for target in targets):
            raise ValueError(f"FAULT-TMUX-CARRIED: {socket}")
    return socket, tmux_birth(socket)


@contextmanager
def dead_tmux(socket, restore):
    from faults import birth_exited, signal_birth
    from bootstrap import wait
    socket, birth = auxiliary(socket, restore)
    signal_birth(birth, signal.SIGKILL)
    try:
        wait("killed tmux server " + str(socket), lambda: birth_exited(birth))
        yield birth
    finally:
        restore()


@contextmanager
def empty_tmux(socket, session, restore):
    from faults import checked_birth
    socket, birth = auxiliary(socket, restore)
    arguments = ["tmux", "-S", str(socket)]
    sessions = command([*arguments, "list-sessions", "-F", "#{session_name}"])
    if not isinstance(session, str) or not session or sessions.splitlines() != [session]:
        raise ValueError(f"FAULT-TMUX-AUX-SESSION: expected only {session!r}, actual={sessions!r}")
    before = socket.stat()
    prior = command([*arguments, "show-options", "-sv", "exit-empty"]).strip()
    if prior not in {"on", "off"}:
        raise ValueError(f"FAULT-TMUX-EXIT-EMPTY: {prior!r}")
    try:
        command([*arguments, "set-option", "-s", "exit-empty", "off"])
        command([*arguments, "kill-session", "-t", "=" + session])
        checked_birth(birth)
        after = socket.stat()
        listing = command([*arguments, "list-sessions", "-F", "#{session_name}"])
        option = command([*arguments, "show-options", "-sv", "exit-empty"]).strip()
        if (before.st_dev, before.st_ino) != (after.st_dev, after.st_ino) or listing != "" or option != "off":
            raise ValueError(f"FAULT-TMUX-NOT-EMPTY: birth={birth}, sessions={listing!r}, exit-empty={option!r}")
        yield {"birth": birth, "socket": str(socket), "list_sessions": listing, "exit_empty": option}
    finally:
        try:
            restore()
        finally:
            command([*arguments, "set-option", "-s", "exit-empty", prior])
