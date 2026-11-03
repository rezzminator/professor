#!/usr/bin/env python3
"""Run-owned kernel PTYs, independent of every multiplexer and its ancestry."""
import base64
import codecs
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import termios

from runtime import command, now_ms, start

LIMIT = 16 * 1024 * 1024
IDENTITY = ("backend", "socket", "pane", "pid", "start", "pty")


def owned_path(value):
    root = Path(os.environ["JOURNEY_ROOT"]).resolve(strict=True)
    path = Path(value)
    if not path.is_absolute() or path.is_symlink():
        raise ValueError(f"PTY-PATH: absolute non-symlink required: {value}")
    path.resolve(strict=True).relative_to(root)
    return path


def plain_environment():
    environment = dict(os.environ)
    for key in list(environment):
        if key.startswith(("ZELLIJ", "WEZTERM", "KITTY_")) or key in {
                "TMUX", "TMUX_PANE", "HERDR_SOCKET_PATH", "HERDR_PANE_ID", "HERDR_ENV",
                "HERDR_WORKSPACE_ID", "HERDR_TAB_ID", "HERDR_TERMINAL_ID",
                "STY", "WINDOW", "WINDOWID", "TERM_SESSION_ID", "CLAUDE_CODE_SESSION_ID",
                "CLAUDECODE", "CODEX_THREAD_ID", "PFM_SEAT_ID"}:
            environment.pop(key)
    environment["TERM"] = "xterm-256color"
    return environment


def receive(connection):
    with connection.makefile("rb") as stream:
        value = stream.readline(LIMIT + 1)
    if len(value) > LIMIT or not value.endswith(b"\n"):
        raise ValueError("PTY-PROTOCOL: incomplete or oversized frame")
    return json.loads(value)


def request(target, operation, value=None):
    owned_path(target["socket"])
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(3)
        connection.connect(target["socket"])
        connection.sendall(json.dumps({"target": target, "operation": operation, "value": value}).encode() + b"\n")
        response = receive(connection)
    if "error" in response:
        raise ValueError(response["error"])
    return response["result"]


def read(target):
    return request(target, "read")


def write_input(master, payload):
    deadline = now_ms() + 1500
    while payload:
        remaining = (deadline - now_ms()) / 1000
        if remaining <= 0 or not select.select([], [master], [], remaining)[1]:
            raise ValueError("PTY-INPUT-TIMEOUT")
        payload = payload[os.write(master, payload):]


def create(shell=None, cwd=None):
    home = owned_path(os.environ["HOME"])
    folder = owned_path(cwd or home)
    executable = shell or os.environ["SHELL"]
    if not Path(executable).is_absolute() or not os.access(executable, os.X_OK):
        raise ValueError(f"PTY-SHELL: executable absolute login shell required: {executable}")
    directory = Path(tempfile.mkdtemp(prefix="pty-", dir=os.environ["JOURNEY_ROOT"]))
    with (directory / "broker.log").open("wb") as log:
        with start([sys.executable, str(Path(__file__).resolve()), "serve", str(directory), executable, str(folder)],
                   env=plain_environment(), stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                   stderr=log, start_new_session=True) as broker:
            try:
                output, _ = broker.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                broker.kill()
                broker.wait()
                raise ValueError(f"PTY-BOOT-TIMEOUT: {log.name}")
            if broker.returncode != 0 or not output:
                raise ValueError(f"PTY-BOOT: {log.name}")
    target = json.loads(output)
    read(target)
    return target


def controlling_terminal():
    os.setsid()
    fcntl.ioctl(0, termios.TIOCSCTTY, 0)


def terminate(child, master):
    from observation import process_snapshot
    groups = {child.pid}
    for pid in process_snapshot():
        try:
            if os.getsid(pid) == child.pid:
                groups.add(os.getpgid(pid))
        except ProcessLookupError:
            pass
    try:
        groups.add(os.tcgetpgrp(master))
    except OSError:
        pass
    for group in groups:
        if group > 1:
            try:
                os.killpg(group, signal.SIGKILL)
            except ProcessLookupError:
                pass
    child.wait(timeout=3)


def serve(directory, shell, cwd):
    import pyte
    if os.fork():
        os._exit(0)
    # Orphan the broker before launching the shell: identity lookup walks ancestors.
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 160, 0, 0))
    child = start([shell, "-l", "-i"], stdin=slave, stdout=slave, stderr=slave, cwd=cwd,
                  env=plain_environment(), preexec_fn=controlling_terminal)
    target = {"backend": "pty", "socket": str(Path(directory) / "io.sock"),
              "pane": os.ttyname(slave), "pty": os.ttyname(slave), "pid": child.pid,
              "start": " ".join(command(["ps", "-p", str(child.pid), "-o", "lstart="]).split())}
    os.close(slave)
    os.set_blocking(master, False)
    display = pyte.Screen(160, 40)
    display.write_process_input = lambda value: write_input(master, value.encode())
    stream = pyte.Stream(display)
    decoder = codecs.getincrementaldecoder("utf-8")("replace")
    raw = bytearray()
    running = True

    def stopped(number, frame):
        nonlocal running
        running = False

    for number in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(number, stopped)

    def drain():
        while select.select([master], [], [], 0)[0]:
            try:
                chunk = os.read(master, 65536)
            except OSError as error:
                if error.errno == errno.EIO:
                    return
                raise
            if not chunk:
                return
            raw.extend(chunk)
            if len(raw) > LIMIT // 8:
                raise ValueError("PTY-OUTPUT-LIMIT: raw output exceeds 2 MiB")
            stream.feed(decoder.decode(chunk))

    try:
        with socket.socket(socket.AF_UNIX) as listener:
            listener.bind(target["socket"])
            os.chmod(target["socket"], 0o600)
            listener.listen(8)
            print(json.dumps(target), flush=True)
            os.close(1)
            while running and Path(directory).is_dir() and child.poll() is None:
                ready, _, _ = select.select([listener, master], [], [], 0.1)
                if master in ready:
                    drain()
                if listener not in ready:
                    continue
                with listener.accept()[0] as connection:
                    connection.settimeout(2)
                    try:
                        message = receive(connection)
                        if any(message["target"].get(key) != target[key] for key in IDENTITY):
                            raise ValueError("PTY-TARGET-MISMATCH: birth or terminal changed")
                        operation = message["operation"]
                        if operation == "write":
                            payload = base64.b64decode(message["value"], validate=True)
                            if len(payload) > 65536:
                                raise ValueError("PTY-INPUT-LIMIT: maximum 65536 bytes")
                            write_input(master, payload)
                            result = None
                        elif operation == "read":
                            drain()
                            result = {**target, "text": "\n".join(row.rstrip() for row in display.display).rstrip(),
                                      "raw": raw.decode("utf-8", "replace"), "raw_base64": base64.b64encode(raw).decode(),
                                      "foreground_pid": os.tcgetpgrp(master)}
                        elif operation == "close":
                            running, result = False, None
                        else:
                            raise ValueError(f"PTY-OPERATION: {operation}")
                        response = {"result": result}
                    except (OSError, ValueError, KeyError) as error:
                        response = {"error": str(error)}
                    try:
                        connection.sendall(json.dumps(response).encode() + b"\n")
                    except (BrokenPipeError, ConnectionResetError, TimeoutError):
                        pass
    finally:
        terminate(child, master)
        os.close(master)
        Path(target["socket"]).unlink(missing_ok=True)


if __name__ == "__main__":
    serve(*sys.argv[2:])
