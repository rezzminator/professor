"""Native auxiliary tmux faults preserve the run's carried servers."""
import os
import errno
import signal
from pathlib import Path
import unittest
from unittest.mock import patch

import faults_test
import faults
from bootstrap import tmux_server
from observation import process_snapshot
from runtime import command, write_json


class EmptyTmux(unittest.TestCase):
    setUp = faults_test.FaultTests.setUp

    def test_dead_tmux_restores_fresh_server_and_refuses_carried_or_foreign(self):
        with patch.dict(os.environ, {"PFM_TMUX_DIR": str(self.root / "tmux"), "JOURNEY_CARRY": str(self.root / "carry.json")}):
            targets = {}
            for name in ("owner", "auxiliary", "foreign"):
                with patch.dict(os.environ, {"PFM_RUN": "foreign" if name == "foreign" else "synthetic"}):
                    targets[name] = tmux_server(name)
                self.addCleanup(lambda target=targets[name]: command(["tmux", "-S", target["socket"], "kill-server"], allowed_codes=(0, 1)))
            write_json(os.environ["JOURNEY_CARRY"], {"chats": [], "owner": targets["owner"], "foreign": {"terminal": targets["foreign"]}})
            owner_birth = faults.tmux_birth(targets["owner"]["socket"])
            auxiliary = targets["auxiliary"]["socket"]
            birth = faults.tmux_birth(auxiliary)
            with self.assertRaisesRegex(ValueError, "^FAULT-SOCKET-NOT-REFUSING:"):
                faults.refusing_socket(auxiliary)
            with self.assertRaises(FileNotFoundError):
                faults.refusing_socket(self.root / "absent.sock")
            plain = self.root / "plain"
            plain.write_text("owned")
            with self.assertRaisesRegex(ValueError, "^FAULT-SOCKET-NOT-SOCKET:"):
                faults.refusing_socket(plain)
            with patch.dict(os.environ, {"PFM_RUN": "foreign"}):
                foreign_birth = faults.tmux_birth(targets["foreign"]["socket"])
            restores = []
            def restore():
                restores.append(tmux_server("auxiliary"))
            for socket, error in ((targets["owner"]["socket"], "CARRIED"), (targets["foreign"]["socket"], "CARRIED")):
                with self.subTest(socket=socket), self.assertRaisesRegex(ValueError, "^FAULT-TMUX-" + error + ":"):
                    with faults.dead_tmux(socket, restore):
                        self.fail("carried server accepted")
            with self.assertRaisesRegex(ValueError, "^FAULT-TMUX-RESTORE:"):
                with faults.dead_tmux(auxiliary, None):
                    self.fail("missing restore accepted")
            with patch.dict(os.environ, {"PFM_RUN": "another-run"}), self.assertRaisesRegex(ValueError, "^FAULT-PROCESS-FOREIGN:"):
                with faults.dead_tmux(auxiliary, restore):
                    self.fail("foreign birth accepted")
            self.assertEqual([], restores)
            with self.assertRaisesRegex(RuntimeError, "^owner action failed$"):
                with faults.dead_tmux(auxiliary, restore) as actual:
                    self.assertEqual(birth, actual)
                    self.assertEqual(True, Path(auxiliary).is_socket())
                    self.assertEqual({"socket": auxiliary, "errno": errno.ECONNREFUSED}, faults.refusing_socket(auxiliary))
                    from faults_waiter import socket_connect_errno
                    parked = self.root / "parked.sock"
                    def replaced(path, bound):
                        result = socket_connect_errno(path, bound)
                        Path(path).rename(parked)
                        Path(path).write_text("replacement")
                        return result
                    try:
                        with patch("faults_waiter.socket_connect_errno", side_effect=replaced):
                            with self.assertRaisesRegex(ValueError, "^FAULT-SOCKET-REPLACED:"):
                                faults.refusing_socket(auxiliary)
                    finally:
                        if parked.exists():
                            Path(auxiliary).unlink()
                            parked.rename(auxiliary)
                    old = process_snapshot().get(birth["pid"])
                    self.assertEqual("Z", old["state"] if old else "Z")
                    self.assertEqual(owner_birth, faults.tmux_birth(targets["owner"]["socket"]))
                    with patch.dict(os.environ, {"PFM_RUN": "foreign"}):
                        self.assertEqual(foreign_birth, faults.tmux_birth(targets["foreign"]["socket"]))
                    raise RuntimeError("owner action failed")
            self.assertEqual([targets["auxiliary"]], restores)
            fresh = faults.tmux_birth(auxiliary)
            self.assertNotEqual(birth["pid"], fresh["pid"])
            self.assertEqual("%0\n", command(["tmux", "-S", auxiliary, "display-message", "-p", "-t", targets["auxiliary"]["pane"], "#{pane_id}"]))
            self.assertEqual(owner_birth, faults.tmux_birth(targets["owner"]["socket"]))
            with patch.dict(os.environ, {"PFM_RUN": "foreign"}):
                self.assertEqual(foreign_birth, faults.tmux_birth(targets["foreign"]["socket"]))

    def test_empty_auxiliary_keeps_server_and_restores_session_and_option(self):
        with patch.dict(os.environ, {"PFM_TMUX_DIR": str(self.root / "sockets"), "JOURNEY_CARRY": str(self.root / "carry.json")}):
            owner = tmux_server()
            target = tmux_server("empty", "auxiliary")
            for value in (owner, target):
                self.addCleanup(lambda value=value: command(["tmux", "-S", value["socket"], "kill-server"], allowed_codes=(0, 1)))
            write_json(os.environ["JOURNEY_CARRY"], {"chats": [], "owner": owner, "foreign": {"terminal": owner}})
            birth, owner_birth = [faults.tmux_birth(value["socket"]) for value in (target, owner)]
            socket = Path(target["socket"])
            inode = socket.stat().st_ino
            args = ["tmux", "-S", str(socket)]
            restore = lambda: tmux_server("empty", "auxiliary")
            for prior in ("on", "off"):
                command([*args, "set-option", "-s", "exit-empty", prior])
                handler = signal.getsignal(signal.SIGTERM)
                failure = "FAULT-INTERRUPTED: signal=15" if prior == "on" else "owner action failed"
                with self.subTest(prior=prior), self.assertRaisesRegex(RuntimeError, "^" + failure + "$"):
                    with faults.interruptible(), faults.empty_tmux(socket, "auxiliary", restore) as observed:
                        self.assertEqual({"birth": birth, "socket": str(socket), "list_sessions": "", "exit_empty": "off"}, observed)
                        self.assertEqual(inode, socket.stat().st_ino)
                        self.assertEqual(birth["start"], process_snapshot()[birth["pid"]]["start"])
                        self.assertEqual(owner_birth, faults.tmux_birth(owner["socket"]))
                        if prior == "on":
                            os.kill(os.getpid(), signal.SIGTERM)
                        raise RuntimeError("owner action failed")
                self.assertEqual(handler, signal.getsignal(signal.SIGTERM))
                self.assertEqual("auxiliary\n", command([*args, "list-sessions", "-F", "#{session_name}"]))
                self.assertEqual(prior + "\n", command([*args, "show-options", "-sv", "exit-empty"]))
                self.assertEqual(birth, faults.tmux_birth(socket))
            with self.assertRaisesRegex(ValueError, "^FAULT-TMUX-CARRIED:"):
                with faults.empty_tmux(owner["socket"], "owner", restore):
                    self.fail("carried session accepted")
            tmux_server("empty", "second")
            with self.assertRaisesRegex(ValueError, "^FAULT-TMUX-AUX-SESSION:"):
                with faults.empty_tmux(socket, "auxiliary", restore):
                    self.fail("another session could be killed")
            self.assertEqual("auxiliary\nsecond\n", command([*args, "list-sessions", "-F", "#{session_name}"]))


if __name__ == "__main__":
    unittest.main()
