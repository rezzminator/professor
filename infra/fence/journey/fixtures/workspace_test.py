#!/usr/bin/env python3
"""Placement NEED states against a private real Herdr server in the fence."""
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import journey_path
from bootstrap import herdr_command, wait, workspace as create_workspace
import herdr
from projects import repository
from runtime import command, read_json, start, write_json

SOURCE = Path(os.environ.get("JOURNEY_WORKSPACE_MODULE", journey_path.JOURNEY / "workspace.py"))
SPEC = importlib.util.spec_from_file_location("journey_workspace", SOURCE)
workspace = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(workspace)


class WorkspaceStates(unittest.TestCase):
    def setUp(self):
        if os.environ.get("PFM_DEV_FENCE") != "1":
            self.fail("WORKSPACE-FENCE: real Herdr fixtures require the fence")
        self.directory = tempfile.TemporaryDirectory(prefix="ws-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.home = self.root / "home"
        self.home.mkdir()
        config = self.home / ".config/herdr"
        config.mkdir(parents=True)
        (config / "config.toml").write_text('default_shell = "/bin/sh"\n')
        self.session = "workspace-fixture"
        socket = config / "sessions" / self.session / "herdr.sock"
        self.environment = patch.dict(os.environ, {"HOME": str(self.home), "XDG_CONFIG_HOME": str(self.home / ".config"),
            "XDG_DATA_HOME": str(self.home / ".local/share"), "XDG_STATE_HOME": str(self.home / ".local/state"),
            "XDG_CACHE_HOME": str(self.home / ".cache"), "JOURNEY_ROOT": str(self.root),
            "JOURNEY_CARRY": str(self.root / "carry.json"), "JOURNEY_SESSION": self.session,
            "HERDR_SOCKET_PATH": str(socket), "SHELL": "/bin/sh"})
        self.environment.start()
        self.addCleanup(self.environment.stop)
        self.log = open(self.root / "herdr.log", "w+")
        self.addCleanup(self.log.close)
        self.server = start(herdr_command("server"), stdout=self.log, stderr=self.log)
        self.addCleanup(self.stop_server)
        wait("private real Herdr socket", socket.is_socket, 10)
        for name in ("atlas", "beacon", "cobalt"):
            repository(self.home / "work" / name)
        self.base = create_workspace("atlas", str(self.home / "work/atlas"))
        self.foreign = create_workspace("foreign-sentinel", str(self.home / "work/cobalt"))
        wait("sentinel cwd", lambda: self.pane(self.foreign["pane"])["foreground_cwd"] == str(self.home / "work/cobalt"))
        self.carry = {"workspaces": {"atlas": self.base}, "chats": [],
            "foreign": {"name": "foreign-sentinel", "project": "cobalt", "terminal": {
                "backend": "herdr", "pane": self.foreign["pane"]}}}
        write_json(os.environ["JOURNEY_CARRY"], self.carry)

    def stop_server(self):
        if self.server.poll() is None:
            try:
                command(herdr_command("server", "stop"), timeout=5)
                self.server.wait(timeout=5)
            finally:
                if self.server.poll() is None:
                    self.server.kill()
                    self.server.wait(timeout=5)

    def pane(self, pane_id):
        return herdr.request(os.environ["HERDR_SOCKET_PATH"], "pane.get", {"pane_id": pane_id})["result"]["pane"]

    def test_states_are_real_disjoint_and_never_repaired(self):
        preserved = {pane: self.pane(pane) for pane in (self.base["pane"], self.foreign["pane"])}
        expected = {"named": "atlas-journey-8032c5cb260f", "misnamed-exclusive": "atlas-journey-7f2f008e4876",
                    "mixed": "atlas-journey-160f13d23bc7"}
        observations = {}
        for state, physical in expected.items():
            with self.subTest(state=state):
                result = workspace.need("atlas", "branch-a", state)
                observations[state] = result
                label = physical if state == "named" else physical + "-" + state
                self.assertEqual(("atlas", physical, label, str(self.home / "work" / physical)),
                    (result["source_project"], result["project"], result["label"], result["cwd"]), self.root)
                paths = [str(self.home / "work" / physical)]
                if state == "mixed":
                    paths.append(str(self.home / "work/beacon"))
                self.assertEqual(sorted(paths), sorted(pane["foreground_cwd"] for pane in result["panes"]), self.root)
                self.assertEqual({result["id"]}, {pane["workspace_id"] for pane in result["panes"]}, self.root)
                self.assertEqual({"backend": "herdr", "pane": result["pane"]}, result["target"], self.root)
        other = workspace.need("atlas", "branch-b", "mixed", str(self.home))
        self.assertEqual(("atlas-journey-89820fe55765", str(self.home)), (other["project"], other["start_cwd"]), self.root)
        self.assertEqual(sorted([str(self.home), other["cwd"], str(self.home / "work/beacon")]),
                         sorted(pane["foreground_cwd"] for pane in other["panes"]), self.root)
        ids = [item["id"] for item in [*observations.values(), other]]
        self.assertEqual(4, len(set(ids)), (self.root, ids))
        renamed = observations["misnamed-exclusive"]
        command(herdr_command("workspace", "rename", renamed["id"], renamed["project"]))
        self.assertEqual(renamed["project"], workspace.need("atlas", "branch-a", "misnamed-exclusive")["label"], self.root)
        self.assertEqual(other, workspace.need("atlas", "branch-b", "mixed", str(self.home)), self.root)
        with self.assertRaises(ValueError) as failure:
            workspace.need("atlas", "branch-b", "mixed")
        self.assertEqual("WORKSPACE-CWD-CONFLICT: atlas:branch-b:mixed", str(failure.exception), self.root)
        self.assertEqual(observations["mixed"], workspace.read("atlas", "branch-a", "mixed"), self.root)
        command(herdr_command("workspace", "close", observations["named"]["id"]))
        closed = workspace.need("atlas", "branch-a", "named")
        self.assertEqual((False, "n/a:closed", "n/a:closed", [], []),
            (closed["present"], closed["label"], closed["workspace"], closed["panes"], closed["tabs"]), self.root)
        self.assertEqual(self.carry, {key: value for key, value in read_json(os.environ["JOURNEY_CARRY"]).items()
                                     if key != "workspace_branches"}, self.root)
        self.assertEqual(preserved, {pane: self.pane(pane) for pane in preserved}, self.root)

    def test_missing_and_invalid_inputs_are_not_empty_workspaces(self):
        self.assertIsNone(workspace.read("atlas", "absent", "named"))
        for args, expected in [
            (("atlas", "a", "invented"), "WORKSPACE-INPUT: expected fixture project, nonempty branch and named|misnamed-exclusive|mixed"),
            (("atlas", "a", "named", str(self.root)), "WORKSPACE-CWD: expected HOME or a directory in the source project"),
            (("atlas", "a", "named", str(self.home / "work/atlas/missing")), "WORKSPACE-CWD-ABSENT: " + str(self.home / "work/atlas/missing")),
        ]:
            with self.subTest(args=args):
                with self.assertRaises(ValueError) as failure:
                    workspace.need(*args)
                self.assertEqual(expected, str(failure.exception), self.root)
        command_line = [sys.executable, str(SOURCE), "read", "atlas", "absent", "named"]
        with start(command_line, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) as child:
            stdout, stderr = child.communicate(timeout=10)
        self.assertEqual((1, "", ""), (child.returncode, stdout, stderr), self.root)
        with patch.dict(os.environ, {"PFM_DEV_FENCE": "0"}):
            with self.assertRaises(ValueError) as failure:
                workspace.need("atlas", "a", "named")
        self.assertEqual("WORKSPACE-FENCE: requires the private journey HOME inside the fence", str(failure.exception), self.root)
        with patch.dict(os.environ, {"HOME": str(self.root)}):
            with self.assertRaises(ValueError) as failure:
                workspace.need("atlas", "a", "named")
        self.assertEqual("WORKSPACE-FENCE: requires the private journey HOME inside the fence", str(failure.exception), self.root)
        (self.home / "work/cobalt/.git").rename(self.home / "work/cobalt/git-receipt")
        with self.assertRaises(ValueError) as failure:
            workspace.need("cobalt", "a", "named")
        self.assertEqual("WORKSPACE-PROJECT-ABSENT: cobalt", str(failure.exception), self.root)

    def test_private_mutations_are_assertion_failures(self):
        replacements = [
            ('"label": matches[0]["label"] if matches else "n/a:closed"', '"label": "wrong-label"'),
            ('if state == "mixed":', 'if state == "unreachable":'),
            ('if pane["workspace_id"] == saved["id"]', 'if pane["workspace_id"]'),
            ('write_json(os.environ["JOURNEY_CARRY"], declared)',
             'declared["foreign"]["name"] = "changed"\n    write_json(os.environ["JOURNEY_CARRY"], declared)'),
            ('return current', 'return {**current, "label": "repaired"}'),
            ('"present": bool(matches)', '"present": True'),
            ('if current["source_cwd"] != requested:', 'if False:'),
            ('or state not in STATES', '', "test_missing_and_invalid_inputs_are_not_empty_workspaces"),
            ('os.environ.get("PFM_DEV_FENCE") != "1" or ', '', "test_missing_and_invalid_inputs_are_not_empty_workspaces"),
        ]
        original = SOURCE.read_text()
        for index, (old, new, *test) in enumerate(replacements):
            with self.subTest(mutation=index), tempfile.TemporaryDirectory(prefix="ws-mutation-") as directory:
                mutated = Path(directory) / "workspace.py"
                self.assertIn(old, original)
                mutated.write_text(original.replace(old, new))
                with patch.dict(os.environ, {"JOURNEY_WORKSPACE_MODULE": str(mutated)}):
                    with start([sys.executable, str(Path(__file__).resolve()),
                        "WorkspaceStates." + (test[0] if test else "test_states_are_real_disjoint_and_never_repaired")],
                        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) as child:
                        output, errors = child.communicate(timeout=45)
                self.assertEqual((1, ""), (child.returncode, output), errors)
                self.assertRegex(errors, r"FAILED \(failures=[1-9][0-9]*\)\n$", errors)
                logs = Path(os.environ.get("PFM_TEST_TIMING_DIR", directory)) / "workspace-mutations"
                logs.mkdir(parents=True, exist_ok=True)
                log = logs / (str(index + 1) + ".log")
                log.write_text(errors)
                print(f"WATCHED-FAIL workspace mutation {index + 1} PASS log={log}")


if __name__ == "__main__":
    unittest.main()
