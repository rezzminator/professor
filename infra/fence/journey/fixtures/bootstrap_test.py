#!/usr/bin/env python3
"""The root and auxiliary terminals share one run-owned tmux factory."""
import os
from pathlib import Path
import socket
import shlex
import sys
import tempfile
import unittest
from unittest.mock import patch

import journey_path
import bootstrap
from faults import tmux_birth
from runtime import command, read_json, write_json, write_text
from observation import process_snapshot


class TmuxServers(unittest.TestCase):
    def test_root_script_entry_runs_its_native_target_self_test(self):
        self.assertEqual('BOOTSTRAP TARGET PASS: moved durable seat followed; refused old pane read once; input reaches actual new pane\n',
                         command([sys.executable, str(Path(bootstrap.__file__)), '--self-test']))

    def test_upgrade_rearms_only_the_dedicated_observer(self):
        path = self.root / "carry.json"
        observer = {"backend": "herdr", "pane": "observer"}
        declared = {"observer_picker": observer, "picker": {"backend": "herdr", "pane": "actor"}, "foreign": {"session_id": "sentinel"}}
        write_json(path, declared)
        shown = ["◆ pfm fixture\nNew chat"]
        events = []
        def keys(target, values):
            events.append((target, values))
            shown[0] = "journey%" if values == ["escape"] else "◆ pfm fixture\nNew chat"
        def wait(label, predicate, bound=15):
            actual = predicate()
            self.assertEqual(True, actual, f"{label}: frame={shown[0]!r}")
            return actual
        with patch.dict(os.environ, {"JOURNEY_CARRY": str(path)}), \
                patch.object(bootstrap, "wait", side_effect=wait), \
                patch.object(bootstrap.observation, "screen", side_effect=lambda target: shown[0]), \
                patch("chat.shell_ready", side_effect=lambda target: shown[0] == "journey%"), \
                patch.object(bootstrap, "keys", side_effect=keys), \
                patch.object(bootstrap, "text", side_effect=lambda target, value: events.append((target, value))):
            self.assertEqual(observer, bootstrap.refresh_observer())
            shown[0] = "◆ pfm fixture\nfind › carried-chat\n1/13 match"
            self.assertEqual(declared["picker"], bootstrap.rearm_picker(declared["picker"]))
        self.assertEqual([(observer, ["escape"]), (observer, "env -u PFM_BEAT_FILE -u PFM_BEAT pfm"), (observer, ["enter"]),
                          (declared["picker"], ["escape"]), (declared["picker"], "pfm"), (declared["picker"], ["enter"])], events)
        self.assertEqual(declared, read_json(path))

    def test_missing_composer_retains_native_seat_and_server_evidence(self):
        seat = {"seat_id": "owned", "session_id": "native", "herdr_pane": "w2:p2", "herdr_socket": str(self.root / "herdr")}
        write_text(self.root / "herdr.log", "native server diagnostic\n")
        write_text(self.root / "herdr-server.log", "native seat-host stderr\n")
        log = self.root / "pfm.jsonl"
        write_text(log, "")
        write_text(Path(str(log) + ".lock"), "")
        def wait(label, predicate, bound=15):
            if label == "live chat beacon-chat":
                return seat
            if label == "engine composer beacon-chat":
                with log.open("a") as stream:
                    stream.write('{"time":"2026-01-01T00:00:00Z","msg":"rewrite seat"}\n')
                raise ValueError("ROOT-TIMEOUT: " + label)
        def request(socket, method, params):
            return {"result": {"read": {"text": "journey%"}}} if method == "pane.read" else {"result": {"workspaces": []}}
        with patch.dict(os.environ, {"JOURNEY_OUT": str(self.root), "JOURNEY_ACTIVITY": str(log)}), \
                patch.object(bootstrap, "wait", side_effect=wait), patch.object(bootstrap, "text"), \
                patch.object(bootstrap, "keys"), patch.object(bootstrap.herdr, "request", side_effect=request), \
                patch("chat.seats", return_value=[seat]):
            with self.assertRaisesRegex(ValueError, "ROOT-TIMEOUT: engine composer beacon-chat"):
                bootstrap.live_chat("beacon-chat", "w2:p1", 2, "beacon")
        self.assertEqual({"seat": seat, "missing": None, "bindings": [], "seats": [seat],
                          "workspaces": {"result": {"workspaces": []}}, "panes": {"result": {"workspaces": []}},
                          "pfm_log": [{"time": "2026-01-01T00:00:00Z", "msg": "rewrite seat"}], "server_log": "native server diagnostic\n", "native_server_log": "native seat-host stderr\n"},
                         read_json(self.root / "bootstrap/owned.json"))

    def setUp(self):
        directory = tempfile.TemporaryDirectory(prefix="journey-boot-")
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        (self.root / "home").mkdir()
        self.sockets = self.root / "tmux"
        variables = patch.dict(os.environ, {
            "JOURNEY_ROOT": str(self.root), "HOME": str(self.root / "home"),
            "PFM_RUN": self.root.name, "PFM_TMUX_DIR": str(self.sockets),
            "HERDR_SOCKET_PATH": str(self.root / "herdr"),
        })
        variables.start()
        self.addCleanup(variables.stop)

    def server(self, *arguments):
        target = bootstrap.tmux_server(*arguments)
        self.addCleanup(lambda: command(["tmux", "-S", target["socket"], "kill-server"], allowed_codes=(0, 1)))
        return target

    def test_root_and_auxiliary_servers_have_distinct_owned_births_and_reuse(self):
        owner = self.server()
        auxiliary = self.server("reader", "probe")
        self.assertEqual({"backend": "tmux", "socket": str(self.sockets / "default"), "pane": "owner:0.0"}, owner)
        self.assertEqual({"backend": "tmux", "socket": str(self.sockets / "reader"), "pane": "probe:0.0"}, auxiliary)
        births = [tmux_birth(target["socket"]) for target in (owner, auxiliary)]
        self.assertNotEqual(births[0]["pid"], births[1]["pid"])
        for target in (owner, auxiliary):
            self.assertEqual("%0\n", command(["tmux", "-S", target["socket"], "display-message", "-p", "-t", target["pane"], "#{pane_id}"]))
        self.assertEqual(auxiliary, bootstrap.tmux_server("reader", "probe"))
        self.assertEqual(births, [tmux_birth(target["socket"]) for target in (owner, auxiliary)])
        with socket.socket(socket.AF_UNIX) as endpoint:
            endpoint.bind(os.environ["HERDR_SOCKET_PATH"])
            with patch.object(bootstrap.herdr, "request", return_value={}):
                self.assertEqual(owner, bootstrap.start_servers())
        self.assertEqual(births[0], tmux_birth(owner["socket"]))

    def test_namespace_names_and_foreign_symlinks_are_refused(self):
        for name, session in (("../escape", "owner"), ("/escape", "owner"), ("reader", "owner:0"), ("reader", "../owner")):
            with self.subTest(name=name, session=session), self.assertRaisesRegex(ValueError, "^ROOT-TMUX-NAME:"):
                bootstrap.tmux_server(name, session)
        outside = tempfile.TemporaryDirectory(prefix="journey-foreign-")
        self.addCleanup(outside.cleanup)
        for directory in ("relative", outside.name):
            with self.subTest(directory=directory), patch.dict(os.environ, {"PFM_TMUX_DIR": directory}), self.assertRaises(ValueError):
                bootstrap.tmux_server()
        foreign_directory = self.root / "foreign"
        with patch.dict(os.environ, {"PFM_TMUX_DIR": str(foreign_directory), "PFM_RUN": "foreign"}):
            foreign = self.server("sentinel")
            birth = tmux_birth(foreign["socket"])
        self.sockets.mkdir()
        (self.sockets / "reader").symlink_to(foreign["socket"])
        with self.assertRaisesRegex(ValueError, "^ROOT-SOCKET-OCCUPIED:"):
            bootstrap.tmux_server("reader")
        link = self.root / "linked"
        link.symlink_to(foreign_directory, target_is_directory=True)
        with patch.dict(os.environ, {"PFM_TMUX_DIR": str(link)}), self.assertRaisesRegex(ValueError, "^ROOT-TMUX-DIRECTORY:"):
            bootstrap.tmux_server("sentinel")
        with patch.dict(os.environ, {"PFM_RUN": "foreign"}):
            self.assertEqual(birth, tmux_birth(foreign["socket"]))
        self.assertEqual("%0\n", command(["tmux", "-S", foreign["socket"], "display-message", "-p", "#{pane_id}"]))


    def test_server_uses_the_native_zsh_found_on_the_run_path(self):
        native = bootstrap.zsh_path()
        import isolation
        with patch.object(isolation, "zsh_path", return_value="/bin/zsh"):
            self.assertEqual("/bin/zsh", isolation.environment("/j/shell", "synthetic", 20001)["SHELL"])
        directory = self.root / "bin"
        directory.mkdir()
        marker = self.root / "selected-shell"
        write_text(directory / "zsh", '#!/bin/sh\nprintf "selected-zsh\\n" > ' + shlex.quote(str(marker)) +
                   '\nexec ' + shlex.quote(native) + ' "$@"\n', mode=0o700)
        with patch.dict(os.environ, {"PATH": str(directory) + os.pathsep + os.environ["PATH"]}):
            target = self.server("path-selected")
        pid = int(command(["tmux", "-S", target["socket"], "display-message", "-p", "#{pane_pid}"]).strip())
        bootstrap.wait("native login shell after wrapper", lambda: process_snapshot().get(pid, {}).get("argv") == native + " -l")
        self.assertEqual("selected-zsh\n", marker.read_text() if marker.exists() else "shell wrapper was not executed")


if __name__ == "__main__":
    unittest.main()
