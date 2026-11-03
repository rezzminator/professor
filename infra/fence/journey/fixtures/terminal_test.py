#!/usr/bin/env python3
"""Owner key names reach the selected backend's actual input door."""
import os
import base64
import json
from pathlib import Path
import shlex
import signal
import sys
import tempfile
import unittest
from unittest.mock import patch

import journey_path
import terminal
import terminal_pty
from bootstrap import wait
import faults
from observation import terminal as observe_terminal
from runtime import command


class TerminalInput(unittest.TestCase):
    def test_native_cli_input_scopes_its_door_without_changing_parent(self):
        self.assertEqual("env PFM_DOOR=cli pfm chat reload 'session id'",
                         terminal.cli_command(["pfm", "chat", "reload", "session id"]))
        native = terminal.cli_command(["sh", "-c", 'printf %s "$PFM_DOOR"'])
        with patch.dict(os.environ, {"PFM_DOOR": "typed"}):
            self.assertEqual("cli|typed", command(["sh", "-c", native + '; printf "|%s" "$PFM_DOOR"']))

    def test_picker_keys_use_backend_key_names(self):
        keys = ["right", "left", "up", "down", "tab", "backspace", "enter", "escape", "ctrl+b", "ctrl+o", "ctrl+u", "c"]
        with patch.object(terminal, "command") as command:
            terminal.keys({"backend": "tmux", "socket": "/fixture/owned.sock", "pane": "%7"}, keys)
            command.assert_called_once_with(["tmux", "-S", "/fixture/owned.sock", "send-keys", "-t", "%7",
                "Right", "Left", "Up", "Down", "Tab", "BSpace", "Enter", "Escape", "C-b", "C-o", "C-u", "c"])
        with patch.dict(os.environ, {"HERDR_SOCKET_PATH": "/fixture/owned.sock"}), patch.object(terminal.herdr, "request") as request:
            terminal.keys({"backend": "herdr", "pane": "w1:p7"}, keys)
            request.assert_called_once_with("/fixture/owned.sock", "pane.send_keys", {"pane_id": "w1:p7", "keys": keys})
        with patch.object(terminal, "text") as text:
            terminal.keys({"backend": "pty"}, keys)
            text.assert_called_once_with({"backend": "pty"}, "\x1b[C\x1b[D\x1b[A\x1b[B\t\x7f\r\x1b\x02\x0f\x15c")


class PlainTerminal(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory(prefix="journey-pty-")
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.home = self.root / "home"
        self.home.mkdir()
        self.variables = patch.dict(os.environ, {
            "HOME": str(self.home), "JOURNEY_ROOT": str(self.root), "SHELL": "/bin/bash",
            "PFM_RUN": "synthetic", "PFM_BEAT": "terminal/plain", "PFM_CONFIG": str(self.home / "config.json"),
            "TMUX_TMPDIR": str(self.root), "TMUX": "foreign,1,0", "TMUX_PANE": "%99",
            "HERDR_PANE_ID": "foreign:p1", "HERDR_SOCKET_PATH": "foreign.sock",
            "CLAUDE_CODE_SESSION_ID": "foreign", "CODEX_THREAD_ID": "foreign",
        })
        self.variables.start()
        self.addCleanup(self.variables.stop)
        (self.home / ".bash_profile").write_text("export JOURNEY_LOGIN=owned\nPS1='owned$ '\n")
        (self.home / ".zprofile").write_text("export JOURNEY_LOGIN=owned\n")
        (self.home / ".zshrc").write_text("PS1='owned%% '\n")

    def create(self, shell=None):
        arguments = [sys.executable, terminal.__file__, "create"]
        if shell:
            arguments.append(shell)
        target = json.loads(command(arguments))
        self.addCleanup(self.close, target)
        wait("owned login prompt", lambda: terminal_pty.read(target)["text"].rstrip().endswith(("$", "%", "#")))
        return target

    def close(self, target):
        if Path(target["socket"]).exists():
            terminal_pty.request(target, "close")
            wait("PTY socket removed", lambda: not Path(target["socket"]).exists())
        self.assertEqual("", command(["ps", "-p", str(target["pid"]), "-o", "pid="], allowed_codes=(1,)))

    def receipt(self, target, arguments, name):
        prefix = self.root / name
        command([sys.executable, terminal.__file__, "receipt", self.write_target(target), str(prefix), *arguments])
        wait("typed shell receipt", lambda: prefix.with_suffix(".status").exists())
        self.assertEqual("0\n", prefix.with_suffix(".status").read_text())
        return prefix.with_suffix(".out").read_text()

    def test_configured_login_shell_environment_and_kernel_terminal(self):
        for shell in (None, "/bin/zsh"):
            with self.subTest(shell=shell):
                target = self.create(shell)
                script = (
                    "import json,os; print(json.dumps({"
                    "'env':{key:os.environ.get(key) for key in "
                    "['HOME','PFM_CONFIG','TMUX_TMPDIR','JOURNEY_LOGIN','TMUX','TMUX_PANE',"
                    "'HERDR_PANE_ID','HERDR_SOCKET_PATH','CLAUDE_CODE_SESSION_ID','CODEX_THREAD_ID']},"
                    "'tty':os.ttyname(0),'terminal':os.isatty(0),'foreground':os.tcgetpgrp(0)==os.getpgrp()}))"
                )
                actual = json.loads(self.receipt(target, [sys.executable, "-c", script], "environment-zsh" if shell else "environment-bash"))
                self.assertEqual({"env": {"HOME": str(self.home), "PFM_CONFIG": str(self.home / "config.json"),
                    "TMUX_TMPDIR": str(self.root), "JOURNEY_LOGIN": "owned", "TMUX": None, "TMUX_PANE": None,
                    "HERDR_PANE_ID": None, "HERDR_SOCKET_PATH": None, "CLAUDE_CODE_SESSION_ID": None,
                    "CODEX_THREAD_ID": None}, "tty": target["pty"], "terminal": True, "foreground": True}, actual)
                self.assertEqual(" ".join(command(["ps", "-p", str(target["pid"]), "-o", "lstart="]).split()), target["start"])
                observed = observe_terminal(target)
                self.assertEqual(target["pid"], observed["births"][0]["pid"])
                self.assertEqual(target["start"], observed["births"][0]["start"])
                self.assertEqual(target["pty"], observed["identity"]["pty"])
                self.assertEqual([shell or "/bin/bash", "-l", "-i"], shlex.split(observed["births"][0]["chain"][0]["argv"]))
                self.close(target)

    def test_visible_and_raw_reads_keys_and_wrong_birth(self):
        target = self.create()
        terminal.text(target, "this-command-must-be-cleared")
        terminal.keys(target, ["ctrl+u"])
        prefix = self.root / "render"
        terminal.main("type", self.write_target(target),
            "printf '\\033[2J\\033[Hstale\\r\\033[2Katlas\\nbeacon\\n'; printf '0\\n' >" + shlex.quote(str(prefix)))
        wait("render receipt", prefix.exists)
        self.assertEqual("0\n", prefix.read_text())
        reading = terminal_pty.read(target)
        self.assertEqual(["atlas", "beacon"], reading["text"].splitlines()[:2])
        self.assertEqual(b"\x1b[Hstale\r\x1b[2Katlas\r\nbeacon\r\n",
                         base64.b64decode(reading["raw_base64"]).rsplit(b"\x1b[2J", 1)[1].split(b"\x1b[?2004h", 1)[0].split(b"owned$ ", 1)[0])
        self.assertEqual(reading["text"], command([sys.executable, terminal.__file__, "screen", str(self.root / "target.json")]).rstrip("\n"))
        observed = json.loads(command([sys.executable, terminal.__file__, "read", str(self.root / "target.json")]))
        self.assertEqual(reading["raw_base64"], observed["raw_base64"])
        with self.assertRaisesRegex(ValueError, "^PTY-TARGET-MISMATCH:"):
            terminal_pty.read({**target, "start": "wrong-birth"})
        with self.assertRaisesRegex(ValueError, "^PTY-INPUT-LIMIT:"):
            terminal.text(target, "x" * 65537)
        broker = observe_terminal(target)["births"][0]["chain"][1]
        with faults.stopped_birth(broker):
            with self.assertRaisesRegex(TimeoutError, "^timed out$"):
                terminal_pty.read(target)
        try:
            responsive = terminal_pty.read(target)["pid"]
        except OSError:
            responsive = "PTY-UNAVAILABLE"
        self.assertEqual(target["pid"], responsive)
        query = ("import json,os,select,termios,tty\n"
                 "descriptor=os.open('/dev/tty',os.O_RDWR)\n"
                 "saved=termios.tcgetattr(descriptor)\n"
                 "try:\n"
                 " tty.setraw(descriptor)\n"
                 " os.write(descriptor,b'\\x1b[c')\n"
                 " print(json.dumps(os.read(descriptor,64).decode() if select.select([descriptor],[],[],2)[0] else 'no-reply'))\n"
                 "finally:\n"
                 " termios.tcsetattr(descriptor,termios.TCSANOW,saved)\n"
                 " os.close(descriptor)\n")
        self.assertEqual("\x1b[?6c", json.loads(self.receipt(target, [sys.executable, "-c", query], "device")))
        self.close(target)
        with self.assertRaises(FileNotFoundError):
            terminal_pty.read(target)

    def write_target(self, target):
        from runtime import write_json
        path = self.root / "target.json"
        write_json(path, target)
        return str(path)

    def test_public_receipt_remains_responsive_while_owned_tmux_is_stopped(self):
        target = self.create()
        socket = str(self.root / "owned.sock")
        command(["tmux", "-S", socket, "new-session", "-d", "-s", "owned", "/bin/bash"], env=terminal_pty.plain_environment())
        try:
            with faults.stopped_tmux(socket):
                output = self.receipt(target, ["printf", "independent\n"], "stopped")
                self.assertEqual("independent\n", output)
                self.assertEqual(target["pid"], terminal_pty.read(target)["pid"])
            self.assertEqual("owned\n", command(["tmux", "-S", socket, "list-sessions", "-F", "#{session_name}"]))
        finally:
            command(["tmux", "-S", socket, "kill-server"])
        background = int(self.receipt(target, ["/bin/sh", "-c", "sleep 60 & echo $!"], "background"))
        birth = {"pid": background, "start": faults.process_snapshot()[background]["start"]}
        try:
            self.assertEqual("sleep\n", command(["ps", "-p", str(background), "-o", "comm="]))
            self.close(target)
            # An orphan can briefly be a zombie until the fence init reaps it.
            try:
                wait("PTY background child exited", lambda: command(["ps", "-p", str(background), "-o", "stat="], allowed_codes=(0, 1)).strip()[:1] in ("", "Z"), 1)
            except ValueError as error:
                self.assertEqual("ROOT-TIMEOUT: PTY background child exited", str(error))
            state = command(["ps", "-p", str(background), "-o", "stat="], allowed_codes=(0, 1)).strip()
            self.assertEqual("exited", "exited" if not state or state.startswith("Z") else state)
        finally:
            state = command(["ps", "-p", str(background), "-o", "stat="], allowed_codes=(0, 1)).strip()
            if state and not state.startswith("Z"):
                faults.signal_birth(birth, signal.SIGKILL)


if __name__ == "__main__":
    unittest.main()
