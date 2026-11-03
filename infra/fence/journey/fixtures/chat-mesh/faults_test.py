#!/usr/bin/env python3
"""Fault boundaries use real filesystem and process identities."""
import os
import errno
from pathlib import Path
from contextlib import nullcontext
import stat
import subprocess
import socket as sockets
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
import faults
from bootstrap import tmux_server, wait
import terminal
import terminal_pty
from runtime import command, read_json, start, write_json
from observation import process_snapshot


class FaultTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="journey-fault-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.variables = patch.dict(os.environ, {"JOURNEY_ROOT": str(self.root), "HOME": str(self.root / "home"), "PFM_RUN": "synthetic"})
        self.variables.start()
        self.addCleanup(self.variables.stop)
        (self.root / "home").mkdir()

    def test_paths_refuse_foreign_symlink_and_roots(self):
        inside = self.root / "owned"
        inside.write_text("owned")
        link = self.root / "link"
        link.symlink_to(inside)
        for value in (str(self.root), str(self.root / "home"), str(link), "relative"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                faults.owned_path(value)
        self.assertEqual(inside, faults.owned_path(inside))

    def test_deleted_cwd_restores_bytes_and_mode_after_failure(self):
        cwd = self.root / "work"
        cwd.mkdir(mode=0o750)
        (cwd / "turn").write_bytes(b"fixture-turn\n")
        with self.assertRaisesRegex(RuntimeError, "^owner action failed$"):
            with faults.deleted_cwd(cwd):
                self.assertEqual(False, cwd.exists())
                raise RuntimeError("owner action failed")
        self.assertEqual(b"fixture-turn\n", (cwd / "turn").read_bytes())
        self.assertEqual(0o750, stat.S_IMODE(cwd.stat().st_mode))

    def test_unreadable_restores_mode_including_privileged_refusal(self):
        path = self.root / "transcript"
        path.write_text("answer")
        path.chmod(0o640)
        if os.geteuid() == 0:
            with self.assertRaisesRegex(ValueError, "^FAULT-PRIVILEGED-READER:"):
                with faults.unreadable(path):
                    self.fail("privileged uid must not claim unreadable")
        else:
            with faults.unreadable(path):
                with self.assertRaises(PermissionError):
                    path.read_text()
        self.assertEqual(0o640, stat.S_IMODE(path.stat().st_mode))
        self.assertEqual("answer", path.read_text())

    def test_birth_refuses_wrong_start_and_foreign_run(self):
        child = start([sys.executable, "-c", "import time;time.sleep(30)"], env={**os.environ, "PFM_RUN": "foreign"})
        self.addCleanup(child.wait)
        self.addCleanup(child.terminate)
        row = process_snapshot()[child.pid]
        with self.assertRaisesRegex(ValueError, "^FAULT-BIRTH-CHANGED:"):
            faults.checked_birth({"pid": child.pid, "start": "wrong-birth"})
        with self.assertRaisesRegex(ValueError, "^FAULT-PROCESS-FOREIGN:"):
            faults.checked_birth({"pid": child.pid, "start": row["start"]})
        self.assertEqual(None, child.poll())

    def test_cli_restores_cwd_when_terminated(self):
        cwd = self.root / "interrupt-cwd"
        cwd.mkdir()
        (cwd / "answer").write_text("retained")
        output = command([sys.executable, faults.__file__, "deleted-cwd", str(cwd), "--", sys.executable,
                          "-c", "import os,signal;os.kill(os.getppid(),signal.SIGTERM)"], allowed_codes=(2,))
        self.assertEqual("", output)
        self.assertEqual("retained", (cwd / "answer").read_text())

    def test_activity_lock_blocks_another_process_then_releases(self):
        log = self.root / "activity"
        log.write_text("")
        lock = self.root / "activity.lock"
        lock.write_text("")
        probe = "import fcntl,sys;f=open(sys.argv[1],'r+');\ntry: fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB);print('available')\nexcept BlockingIOError: print('locked')"
        with faults.locked_activity(log):
            self.assertEqual("locked\n", command([sys.executable, "-c", probe, str(lock)]))
        self.assertEqual("available\n", command([sys.executable, "-c", probe, str(lock)]))

    def test_cli_refuses_bad_resource_arity_and_missing_action(self):
        for arguments in (("unreadable", "missing"), ("killed-process", "chat", "--", "true"), ("unknown", "x", "--", "true")):
            with self.subTest(arguments=arguments), self.assertRaisesRegex(ValueError, "^FAULT-USAGE:"):
                faults.main(*arguments)

    def test_herdr_capabilities_keep_native_artifacts_and_refuse_unknown_pin(self):
        directory = self.root / "capabilities"
        directory.mkdir()
        prefix = directory / "pinned"
        actual = faults.herdr_capabilities(prefix)
        self.assertEqual({"version": "herdr 0.9.5", "isolated_recycle": False, "recycle": False, "respawn": False, "restart": False},
                         {key: actual[key] for key in ("version", "isolated_recycle", "recycle", "respawn", "restart")})
        self.assertEqual(["pane.get"], [name for name in actual["methods"] if name == "pane.get"])
        self.assertEqual("herdr 0.9.5\n", Path(actual["artifacts"][".version.txt"]).read_text())
        self.assertEqual("Usage: herdr pane [COMMAND]", next(line for line in Path(actual["artifacts"][".pane-help.txt"]).read_text().splitlines() if line.startswith("Usage:")))
        with self.assertRaisesRegex(ValueError, "^FAULT-HERDR-ARTIFACT-EXISTS:"):
            faults.herdr_capabilities(prefix)
        unknown = directory / "unknown"
        with patch.object(faults, "command", side_effect=["herdr 9.9.9\n", *[Path(actual["artifacts"][suffix]).read_text() for suffix in (".pane-help.txt", ".schema.json")]]):
            with self.assertRaisesRegex(ValueError, "^FAULT-HERDR-PIN:"):
                faults.herdr_capabilities(unknown)
        self.assertEqual("herdr 9.9.9\n", Path(str(unknown) + ".version.txt").read_text())

    def test_agent_release_restores_registration_after_action_or_transport_failure(self):
        target = {"backend": "herdr", "pane": "owned-pane"}
        chat = {"terminal": target, "engine": "claude"}
        for transport_error in (False, True):
            restored = []
            with self.subTest(transport_error=transport_error), patch.dict(os.environ, {"HERDR_SOCKET_PATH": "owned-socket"}), \
                    patch.object(faults, "owned_target", return_value={}) as owned, \
                    patch.object(faults, "now_ms", return_value=1234), \
                    patch.object(faults, "request", side_effect=RuntimeError("transport failed") if transport_error else None) as request:
                with self.assertRaisesRegex(RuntimeError, "^(transport failed|owner action failed)$"):
                    with faults.released_agent(chat, lambda: restored.append("registered")) as actual:
                        self.assertEqual(target, actual)
                        raise RuntimeError("owner action failed")
                owned.assert_called_once_with(target)
                request.assert_called_once_with("owned-socket", "pane.release_agent", {
                    "pane_id": "owned-pane", "source": "pfm", "agent": "claude", "seq": 1234000000})
                self.assertEqual(["registered"], restored)
                request.reset_mock()
                with self.assertRaisesRegex(ValueError, "^FAULT-AGENT-RESTORE:"):
                    with faults.released_agent(chat, None):
                        self.fail("missing restoration accepted")
                request.assert_not_called()

    def test_nondumpable_proves_native_denial_and_restores_on_owner_failure(self):
        if sys.platform == "linux" and not os.environ.get("JOURNEY_PERMISSION_TEST_CHILD"):
            command(["setpriv", "--reuid=65534", "--regid=65534", "--clear-groups", "--bounding-set=-all", sys.executable, __file__,
                     "FaultTests.test_nondumpable_proves_native_denial_and_restores_on_owner_failure"],
                    env={**os.environ, "JOURNEY_PERMISSION_TEST_CHILD": "1"})
            return
        (self.root / "artifacts").mkdir()
        prefix = self.root / "artifacts" / "permission"
        if sys.platform != "linux":
            with self.assertRaisesRegex(ValueError, "^FAULT-NONDUMPABLE-PLATFORM:"):
                with faults.nondumpable(prefix):
                    self.fail("Linux fault accepted on another OS")
            return
        probe = 'import os; open("/proc/"+str(os.getppid())+"/environ","rb").close(); print("readable")'
        self.assertEqual("readable\n", command([sys.executable, "-c", probe]))
        with self.assertRaisesRegex(RuntimeError, "^owner action failed$"):
            with faults.nondumpable(prefix) as evidence:
                self.assertEqual(0, evidence["before"]["errno"])
                self.assertEqual(errno.EACCES, evidence["denied"]["errno"])
                self.assertEqual(os.getpid(), evidence["denied"]["parent_pid"])
                self.assertEqual(evidence, read_json(str(prefix) + ".environ.json"))
                with self.assertRaisesRegex(ValueError, "^FAULT-NONDUMPABLE-PRECONDITION:"):
                    with faults.nondumpable(prefix.with_name("nested")):
                        self.fail("already denied caller accepted")
                raise RuntimeError("owner action failed")
        self.assertEqual("readable\n", command([sys.executable, "-c", probe]))
        with self.assertRaisesRegex(ValueError, "^FAULT-NONDUMPABLE-RECEIPT-EXISTS:"):
            with faults.nondumpable(prefix):
                self.fail("existing evidence overwritten")

    def test_tmux_stop_restores_same_server_after_owner_error(self):
        socket = self.root / "tmux"
        command(["tmux", "-S", str(socket), "new-session", "-d", "-s", "fixture"])
        self.addCleanup(lambda: command(["tmux", "-S", str(socket), "kill-server"]))
        prior = command(["tmux", "-S", str(socket), "display-message", "-p", "#{pid}"]).strip()
        with self.assertRaisesRegex(RuntimeError, "^owner action failed$"):
            with faults.stopped_tmux(socket) as birth:
                self.assertEqual(int(prior), birth["pid"])
                self.assertEqual("T", command(["ps", "-o", "stat=", "-p", prior]).strip()[0])
                raise RuntimeError("owner action failed")
        self.assertEqual(prior, command(["tmux", "-S", str(socket), "display-message", "-p", "#{pid}"]).strip())

    def test_unit_state_accepts_only_native_run_namespace_and_restores(self):
        rows = [
            ("linux", "journey-atlas-mcp.service", True),
            ("linux", "journey-atlas-theme.timer", True),
            ("linux", "journey-atlas2-mcp.service", False),
            ("linux", "journey-atlas-", False),
            ("linux", "com.professor.journey-atlas.mcp", False),
            ("darwin", "com.professor.journey-atlas.mcp", True),
            ("darwin", "com.professor.journey-atlas.schedule", True),
            ("darwin", "com.professor.journey-atlas2.mcp", False),
            ("darwin", "com.professor.journey-atlas.", False),
            ("darwin", "journey-atlas-mcp.service", False),
        ]
        for platform, unit, accepted in rows:
            with self.subTest(platform=platform, unit=unit), patch.dict(os.environ, {"PFM_UNIT_PREFIX": "journey-atlas"}):
                plist = self.root / "home/Library/LaunchAgents" / (unit + ".plist")
                plist.parent.mkdir(parents=True, exist_ok=True)
                plist.write_text("fixture-plist")
                calls = []
                def execute(arguments, **options):
                    calls.append(arguments)
                    if arguments[0] == "bash":
                        return platform + "\n"
                    return "active\n" if arguments[0] == "systemctl" else "state = running\n"
                with patch.object(faults, "command", side_effect=execute):
                    if not accepted:
                        with self.assertRaisesRegex(ValueError, "^FAULT-UNIT-OWNERSHIP:"):
                            with faults.unit_state(unit, False):
                                self.fail("foreign unit accepted")
                        self.assertEqual([], [row for row in calls if row[0] != "bash"])
                        continue
                    with self.assertRaisesRegex(RuntimeError, "^owner action failed$"):
                        with faults.unit_state(unit, False) as actual:
                            self.assertEqual(unit, actual)
                            raise RuntimeError("owner action failed")
                expected = ([["systemctl", "--user", "is-active", unit], ["systemctl", "--user", "stop", unit],
                             ["systemctl", "--user", "start", unit]] if platform == "linux" else
                            [["launchctl", "print", "gui/" + str(os.getuid()) + "/" + unit],
                             ["launchctl", "bootout", "gui/" + str(os.getuid()) + "/" + unit],
                             ["launchctl", "bootstrap", "gui/" + str(os.getuid()), str(plist)]])
                self.assertEqual(expected, [row for row in calls if row[0] != "bash"])

    def test_waiter_connect_delay_records_real_tmux_boundaries_and_restores_path(self):
        with patch.dict(os.environ, {"PFM_BEAT": "fault/waiter", "SHELL": "/bin/bash"}):
            target = terminal_pty.create("/bin/bash", str(self.root / "home"))
            self.addCleanup(lambda: terminal_pty.request(target, "close"))
            wait("owner shell ready", lambda: terminal_pty.read(target)["text"].rstrip().endswith(("$", "#")))
            def receipt(name, script):
                prefix = str(self.root / name)
                terminal.type_command(target, ["eval", script], prefix)
                status = Path(prefix + ".status")
                wait("shell receipt " + name, lambda: status.is_file() and status.read_text().endswith("\n"))
                self.assertEqual("0\n", Path(prefix + ".status").read_text())
                return Path(prefix + ".out").read_text()
            prior = receipt("prior", 'printf %s "$PATH"')
            def waiter_reap(child):
                if child.poll() is None:
                    child.kill()
                child.stdout.close()
                child.stderr.close()
                child.wait(timeout=3)
            def waiter_output(child):
                try:
                    return child.communicate(timeout=8)
                except subprocess.TimeoutExpired:
                    waiter_reap(child)
                    self.fail(f"expected bounded waiter completion; actual pid={child.pid} still running")
            invalid = self.root / "invalid-bound"
            invalid.mkdir()
            for bound in (0, -0.1, True, float("nan"), float("inf"), float("-inf")):
                with self.subTest(bound=bound), self.assertRaisesRegex(ValueError, "^FAULT-WAITER-BOUND:"):
                    with faults.delayed_tmux_waiter(target, invalid, bound):
                        self.fail("invalid waiter bound accepted")
            self.assertEqual([], list(invalid.iterdir()))
            accepted = self.root / "finite-bound"
            accepted.mkdir()
            with faults.delayed_tmux_waiter(target, accepted, 31):
                self.assertEqual(str(accepted) + ":" + prior, receipt("finite-path", 'printf %s "$PATH"'))
            self.assertEqual(prior, receipt("finite-restored", 'printf %s "$PATH"'))
            sockets_root = self.root / "bad-sockets"
            sockets_root.mkdir()
            for kind in ("file", "directory", "symlink", "datagram"):
                directory = self.root / (kind + "-wrapper")
                directory.mkdir()
                socket_path = sockets_root / (kind + "-socket")
                with self.subTest(kind=kind), sockets.socket(sockets.AF_UNIX, sockets.SOCK_DGRAM) as endpoint:
                    if kind == "file":
                        socket_path.write_bytes(b"not a socket\n")
                    elif kind == "directory":
                        socket_path.mkdir()
                    elif kind == "symlink":
                        socket_path.symlink_to("absent-socket")
                    else:
                        endpoint.bind(str(socket_path))
                    with faults.delayed_tmux_waiter(target, directory) as scope:
                        with start([scope["tmux"], "-S", str(socket_path), "wait-for", "pfm-launch-done-owned"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) as child:
                            stdout, stderr = waiter_output(child)
                        self.assertEqual("", stdout)
                        self.assertEqual(2, child.returncode)
                        error = f"FAULT-WAITER-SOCKET-PROBE: errno={errno.EPROTOTYPE}: {socket_path}" if kind == "datagram" else f"FAULT-WAITER-SOCKET-NOT-SOCKET: {socket_path}"
                        if kind == "symlink":
                            error = f"FAULT-PATH: absolute non-symlink required: {socket_path}"
                        self.assertEqual("FAULT-WAITER-ERROR: " + error + "\n", stderr)
                        self.assertEqual(["error"] if kind == "datagram" else [], [read_json(p)["outcome"] for p in directory.glob("waiter-*/result.json")])
                self.assertEqual(prior, receipt(kind + "-restored", 'printf %s "$PATH"'))
            for outcome in ("server-gone", "timeout", "empty-server", "cancelled", "unresponsive"):
                with self.subTest(outcome=outcome):
                    directory = self.root / outcome
                    directory.mkdir()
                    servers = self.root / (outcome + "-servers")
                    servers.mkdir()
                    socket = servers / "tmux"
                    command(["tmux", "-S", str(socket), "new-session", "-d", "-s", "owned"])
                    server_pid = int(command(["tmux", "-S", str(socket), "display-message", "-p", "#{pid}"]).strip())
                    def cleanup(socket=socket):
                        command(["tmux", "-S", str(socket), "kill-server"], allowed_codes=(0, 1))
                    self.addCleanup(cleanup)
                    arguments = ["-S", str(socket), "wait-for", "pfm-launch-done-owned"]
                    try:
                        with faults.delayed_tmux_waiter(target, directory, bound=0.4 if outcome in ("timeout", "empty-server", "unresponsive") else 5) as scope:
                            self.assertEqual(str(directory) + ":" + prior, receipt(outcome + "-path", 'printf %s "$PATH"'))
                            self.assertEqual("owned\n", command([scope["tmux"], "-S", str(socket), "display-message", "-p", "#{session_name}"]))
                            self.assertEqual("", command([scope["tmux"], "-S", str(socket), "wait-for", "-S", "pfm-launch-done-other"]))
                            self.assertEqual([], list(directory.glob("waiter-*")))
                            if outcome == "empty-server":
                                command(["tmux", "-S", str(socket), "set-option", "-s", "exit-empty", "off"])
                                command(["tmux", "-S", str(socket), "kill-session", "-t", "owned"])
                            with faults.stopped_tmux(socket) if outcome == "unresponsive" else nullcontext():
                                with start([scope["tmux"], *arguments], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) as child:
                                    try:
                                        reached = wait("real launcher waiter entered", lambda: list(directory.glob("waiter-*/entered.json")))
                                        entered = read_json(reached[0])
                                        self.assertEqual(arguments, entered["argv"])
                                        self.assertEqual(child.pid, entered["pid"])
                                        if outcome == "server-gone":
                                            live = reached[0].parent / "live-probe.json"
                                            try:
                                                wait("actual live has-session probe before server exit", live.is_file, 5)
                                            except ValueError:
                                                self.fail(f"expected actual live has-session probe; actual receipt absent; artifacts={directory}")
                                            probe = read_json(live)
                                            self.assertEqual(["-S", str(socket), "has-session"], probe["argv"][1:])
                                            self.assertEqual(0, probe["returncode"])
                                            self.assertEqual(server_pid, probe["server"]["pid"])
                                            cleanup()
                                        elif outcome == "cancelled":
                                            child.terminate()
                                        stdout, stderr = waiter_output(child)
                                        self.assertEqual("", stdout)
                                        self.assertEqual({"server-gone": 1, "timeout": 99, "empty-server": 99, "cancelled": 143, "unresponsive": 2}[outcome], child.returncode, stderr)
                                    finally:
                                        waiter_reap(child)
                            result = read_json(reached[0].parent / "result.json")
                            self.assertEqual({"unresponsive": "error", "empty-server": "timeout"}.get(outcome, outcome), result["outcome"])
                            self.assertEqual(arguments, result["argv"])
                            if outcome == "cancelled":
                                self.assertEqual(15, result["signal"])
                            if outcome in ("server-gone", "timeout", "empty-server"):
                                self.assertEqual(server_pid, result["server"]["pid"])
                            raise RuntimeError("owner action failed")
                    except RuntimeError as error:
                        self.assertEqual("owner action failed", str(error))
                    self.assertEqual(prior, receipt(outcome + "-restored", 'printf %s "$PATH"'))
if __name__ == "__main__":
    unittest.main()
