#!/usr/bin/env python3
"""Private delayed-connect mechanism; use faults.delayed_tmux_waiter."""
from contextlib import contextmanager
import errno
import os
import math
from pathlib import Path
import shlex
import shutil
import signal
import socket as sockets
import subprocess
import sys
import tempfile

from bootstrap import wait
from observation import process_snapshot, terminal
from runtime import command, now_ms, read_json, start, write_json, write_text


@contextmanager
def delayed_tmux_waiter(target, directory, bound=5):
    """Delay launcher done clients until their real owned server stops.

    Yields {tmux, directory}; per-invocation entered.json/result.json record
    actual wrapper entry and server-gone, timeout or catchable cancellation.
    The owner shell must finish its command before leaving this context.
    """
    from faults import checked_birth, owned_path, owned_target
    from terminal import type_command
    directory = owned_path(directory)
    if not directory.is_dir() or any(directory.iterdir()):
        raise ValueError("FAULT-WAITER-DIRECTORY: empty owned directory required")
    if isinstance(bound, bool) or not isinstance(bound, (int, float)) or not math.isfinite(bound) or bound <= 0:
        raise ValueError("FAULT-WAITER-BOUND: finite positive seconds required")
    if target["backend"] == "pty":
        owned_path(target["socket"])
        checked_birth({"pid": target["pid"], "start": target["start"]})
        observed = terminal(target)
        if observed["identity"] != {key: target[key] for key in ("socket", "pane", "pty")}:
            raise ValueError("FAULT-WAITER-TARGET: PTY identity changed")
    else:
        owned_target(target)
    real_tmux = shutil.which("tmux")
    if not real_tmux:
        raise ValueError("FAULT-WAITER-TMUX: real executable unavailable")
    real_tmux = str(Path(real_tmux).resolve(strict=True))
    config = directory / "config.json"
    write_json(config, {"run": os.environ["PFM_RUN"], "tmux": real_tmux, "bound": bound})
    wrapper = directory / "tmux"
    write_text(wrapper, "#!/bin/sh\nexec " + shlex.join([sys.executable, str(Path(__file__).resolve()), str(config)]) +
               ' "$@"\n', mode=0o700)
    original = directory / "original-path"
    def shell(label, script):
        prefix = str(directory / label)
        type_command(target, ["eval", script], prefix)
        status = Path(prefix + ".status")
        wait("waiter PATH " + label, lambda: status.is_file() and status.read_text().endswith("\n"))
        if status.read_text() != "0\n":
            raise ValueError("FAULT-WAITER-PATH: " + Path(prefix + ".out").read_text())
    try:
        shell("install", 'printf %s "$PATH" > ' + shlex.quote(str(original)) +
              " && export PATH=" + shlex.quote(str(directory)) + ':"$PATH"')
        yield {"tmux": str(wrapper), "directory": str(directory)}
    finally:
        if original.is_file():
            shell("restore", "export PATH=" + shlex.quote(original.read_text()))


def connect(config_path, arguments):
    from faults import checked_birth, owned_path
    config_path = owned_path(config_path)
    config = read_json(config_path)
    if config["run"] != os.environ["PFM_RUN"]:
        raise ValueError("FAULT-WAITER-RUN: wrapper belongs to another run")
    real_tmux = config["tmux"]
    if len(arguments) >= 4 and arguments[2] == "wait-for" and arguments[3].startswith("pfm-launch-done-"):
        if arguments[0] != "-S":
            raise ValueError("FAULT-WAITER-SOCKET: explicit -S required")
        socket = Path(arguments[1])
        owned_path(socket if socket.exists() or socket.is_symlink() else socket.parent)
        if socket.exists() and not socket.is_socket():
            raise ValueError(f"FAULT-WAITER-SOCKET-NOT-SOCKET: {socket}")
        directory = Path(tempfile.mkdtemp(prefix="waiter-", dir=config_path.parent))
        row = process_snapshot()[os.getpid()]
        entered = {"argv": arguments, "pid": row["pid"], "start": row["start"], "at_ms": now_ms()}
        def result(outcome, **extra):
            write_json(directory / "result.json", {**entered, "outcome": outcome, "at_ms": now_ms(), **extra})
        def cancelled(number, frame):
            result("cancelled", signal=number)
            raise SystemExit(128 + number)
        for number in (signal.SIGTERM, signal.SIGINT):
            signal.signal(number, cancelled)
        write_json(directory / "entered.json", entered)
        try:
            pid = command([real_tmux, "-S", str(socket), "display-message", "-p", "#{pid}"], allowed_codes=(0, 1), timeout=config["bound"]).strip()
            birth = None
            if pid:
                server = process_snapshot().get(int(pid))
                if server:
                    birth = {"pid": int(pid), "start": server["start"]}
                    try:
                        checked_birth(birth)
                    except (OSError, ValueError):
                        if process_snapshot().get(int(pid)) is not None:
                            raise
                        birth = None
            def gone():
                probe_arguments = [real_tmux, "-S", str(socket), "has-session"]
                with start(probe_arguments, stdout=subprocess.PIPE, stderr=subprocess.PIPE) as probe:
                    try:
                        stdout, stderr = probe.communicate(timeout=config["bound"])
                    except subprocess.TimeoutExpired as error:
                        probe.kill()
                        probe.stdout.close()
                        probe.stderr.close()
                        probe.wait(timeout=3)
                        raise RuntimeError("FAULT-WAITER-PROBE: owned server did not answer") from error
                if probe.returncode not in (0, 1):
                    raise ValueError(f"FAULT-WAITER-PROBE: exit={probe.returncode}: {stderr!r}{stdout!r}")
                if probe.returncode == 0:
                    write_json(directory / "live-probe.json", {"argv": probe_arguments, "returncode": probe.returncode,
                               "server": birth, "at_ms": now_ms()})
                    return False
                if birth:
                    state = command(["ps", "-p", str(birth["pid"]), "-o", "stat="], allowed_codes=(0, 1), timeout=config["bound"]).strip()
                    if state and not state.startswith("Z"):
                        return False
                return tmux_socket_gone(socket, config["bound"])
            try:
                wait("owned tmux server gone before waiter connect", gone, config["bound"])
            except ValueError as error:
                if str(error).startswith("ROOT-TIMEOUT:"):
                    result("timeout", server=birth)
                    print("FAULT-WAITER-TIMEOUT: owned server still running", file=sys.stderr)
                    return 99
                raise
            result("server-gone", server=birth)
        except (OSError, ValueError, RuntimeError) as error:
            result("error", error=str(error))
            raise
    # This is the sole process replacement door; all argv reach real tmux unchanged.
    os.execv(real_tmux, [real_tmux, *arguments])


def socket_connect_errno(path, bound):
    with sockets.socket(sockets.AF_UNIX, sockets.SOCK_STREAM) as connection:
        connection.settimeout(bound)
        try:
            connection.connect(str(path))
        except OSError as error:
            if error.errno in (errno.ENOENT, errno.ECONNREFUSED):
                return error.errno
            raise ValueError(f"FAULT-WAITER-SOCKET-PROBE: errno={error.errno}: {path}") from error
    return 0


def tmux_socket_gone(path, bound):
    return socket_connect_errno(path, bound) != 0


if __name__ == "__main__":
    try:
        sys.exit(connect(sys.argv[1], sys.argv[2:]))
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print(f"FAULT-WAITER-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
