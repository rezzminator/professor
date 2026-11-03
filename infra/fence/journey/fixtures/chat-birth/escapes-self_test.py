#!/usr/bin/env python3
"""Pin escape projections and restoration at the shared input fault seam."""
from pathlib import Path
import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
import escapes
from runtime import read_json, write_json, write_text

import copy
import json
import os
import shlex
import tempfile
import unittest
from unittest.mock import patch


class BirthEscapes(unittest.TestCase):
    def test_restoration_requires_actual_reply_and_actual_backend_registration(self):
        target = {"backend": "herdr", "pane": "w1:p2"}
        response = {"result": {"agent": {"pane_id": "w1:p2", "name": "atlas-1"}}}
        with patch.dict(os.environ, {"HERDR_SOCKET_PATH": "/fixture/owned.sock"}), \
                patch.object(escapes.observation, "terminal") as display, patch.object(escapes, "request", return_value=response) as queried:
            display.return_value = {"text": '❯ rename-authority-restored mock-engine: [{"reply":"rename-authority-restored"}]'}
            self.assertIsNone(escapes.restored_agent(target))
            queried.assert_not_called()
            display.return_value = {"text": "⏺ rename-authority-restored\n❯\n"}
            self.assertEqual(response, escapes.restored_agent(target))
            queried.assert_called_once_with("/fixture/owned.sock", "agent.get", {"target": "w1:p2"})
            for code in ("agent_not_found", "permission_denied"):
                queried.side_effect = escapes.Refused("agent.get", {"error": {"code": code}})
                if code == "agent_not_found":
                    self.assertIsNone(escapes.restored_agent(target))
                else:
                    with self.assertRaises(escapes.Refused) as refused:
                        escapes.restored_agent(target)
                    self.assertEqual("permission_denied", refused.exception.code)

    def test_projections_keep_each_refusal_and_native_identity(self):
        alias = {"receipt": {"status": "2"}, "stdout": "", "stderr": "exact refusal\n",
                 "before": {"panes": ["own"]}, "after": {"panes": ["own"]},
                 "catalogue_before": None, "catalogue_after": None}
        title = {"native_titles": [{"thread_name": "atlas-thread-title"}], "before": {"session_id": "native"},
                 "after": {"session_id": "native", "name": "atlas"},
                 "terminal": {"tab": {"tab": {"label": "atlas"}}},
                 "birth_before": {"pid": 7, "start": "original"},
                 "birth_after": {"pid": 7, "start": "original"}}
        agentless = {**title, "agent_get_refusal": "agent_not_found", "receipt": {"status": "0"},
                     "after": {"name": "atlas-1"}, "rename_count": "n/a:public-backend-request-log-unavailable"}
        cases = [
            (escapes.alias_projection, alias,
             {"exit_status": "2", "stdout": "", "stderr": "exact refusal\n", "topology_preserved": True, "catalogue_restored": True},
             [("receipt", "status", "0"), (None, "stdout", "unexpected\n"), (None, "stderr", "wrong\n"),
              ("after", "panes", ["own", "leak"]), (None, "catalogue_after", "changed")]),
            (escapes.title_projection, title,
             {"native_titles": ["atlas-thread-title"], "name": "atlas", "tab": "atlas", "birth_preserved": True, "session_preserved": True},
             [(None, "native_titles", [{"thread_name": "old"}]), ("after", "name", "atlas-thread-title"),
              ("after", "session_id", "replacement"), ("birth_after", "start", "replacement"),
              (None, "terminal", {"tab": {"tab": {"label": "WRONG"}}})]),
            (escapes.agentless_projection, agentless,
             {"agent_get_refusal": "agent_not_found", "name": "atlas-1", "exit_status": "0",
              "rename_count": "n/a:public-backend-request-log-unavailable", "birth_preserved": True},
             [(None, "agent_get_refusal", "permission_denied"), ("after", "name", "lost"),
              ("receipt", "status", "2"), (None, "rename_count", 1), ("birth_after", "start", "replacement")])]
        for project, evidence, expected, mutations in cases:
            self.assertEqual(expected, project(evidence))
            for section, key, value in mutations:
                changed = copy.deepcopy(evidence)
                (changed if section is None else changed[section])[key] = value
                with self.subTest(project=project.__name__, section=section, key=key):
                    self.assertNotEqual(expected, project(changed))

    def test_permission_edit_preserves_every_other_config_source(self):
        original = {"accounts": [{"id": 1, "claude": {"theme": "aurora"}}],
                    "claude": {"permissionMode": "bypass", "cache1h": True}, "codex": {"homes": [1]}}
        changed = escapes.permission_config(original)
        self.assertEqual({"accounts": [{"id": 1, "claude": {"theme": "aurora"}}],
                          "claude": {"permissionMode": "auto", "cache1h": True}, "codex": {"homes": [1]}}, changed)
        self.assertEqual("bypass", original["claude"]["permissionMode"])
        raw = json.dumps(original)
        evidence = {"original": raw, "restored": raw, "configured": changed,
                    "birth": {"argv": ["claude", "--permission-mode", "auto", "--model", "sonnet"]}}
        expected = {"permission_arguments": [["--permission-mode", "auto"]], "configured_mode": "auto",
                    "config_restored": True, "other_sources_preserved": True}
        self.assertEqual(expected, escapes.permission_projection(evidence))
        for section, key, value in (("birth", "argv", ["claude", "--permission-mode", "bypass"]),
                                    ("birth", "argv", ["claude", "--permission-mode"]),
                                    ("birth", "argv", ["claude", "--permission-mode", "auto", "--permission-mode", "bypass"]),
                                    (None, "restored", "{}"), (None, "configured", {**changed, "codex": {}})):
            mutated = copy.deepcopy(evidence)
            (mutated if section is None else mutated[section])[key] = value
            with self.subTest(section=section, key=key, value=value):
                self.assertNotEqual(expected, escapes.permission_projection(mutated))
        override = copy.deepcopy(original)
        override["accounts"][0]["claude"]["permissionMode"] = "bypass"
        with self.assertRaisesRegex(ValueError, "^BIRTH-ESCAPE-PERMISSION-OVERRIDE:"):
            escapes.permission_config(override)

    def test_cli_permission_scope_restores_config_on_success_and_capture_failure(self):
        with tempfile.TemporaryDirectory(prefix="birth-escapes-") as temporary:
            root = Path(temporary)
            directory = root / "birth"
            directory.mkdir()
            socket = root / "herdr.sock"
            socket.touch()
            config = root / "config.json"
            write_json(config, {"accounts": [{"id": 1}], "claude": {"permissionMode": "bypass", "cache1h": True}})
            config.chmod(0o640)
            original = config.read_text()
            target = {"backend": "herdr", "pane": "w1:p2"}
            target_file = root / "target.json"
            write_json(target_file, target)
            write_json(directory / "before.json", {"target": target, "seats": []})
            environment = {"JOURNEY_ROOT": temporary, "HOME": str(root / "home"), "PFM_CONFIG": str(config),
                           "HERDR_SOCKET_PATH": str(socket), "PFM_RUN": "owned", "PFM_DEV_FENCE": "1",
                           "JOURNEY_BRANCH": "linux+herdr-shield+project+claude"}
            current = {"session_id": "actual", "engine": "claude"}
            def captured(*arguments):
                self.assertEqual("auto", read_json(config)["claude"]["permissionMode"])
                return current
            with patch.dict(os.environ, environment), patch.object(escapes, "owner_action", return_value={"status": "0"}) as typed, \
                    patch.object(escapes, "capture", side_effect=captured), \
                    patch.object(escapes, "native_birth", return_value={"argv": ["claude", "--permission-mode", "auto"]}):
                self.assertEqual({"permission_arguments": [["--permission-mode", "auto"]], "configured_mode": "auto",
                                  "config_restored": True, "other_sources_preserved": True},
                                 escapes.permission_birth(str(directory), str(target_file), "new-from-cli", "atlas-birth"))
                typed.assert_called_once_with(target, directory, "permission-owner",
                                              ["pfm", "chat", "new", "--engine", "claude", "--name", "atlas-birth"])
                with patch.object(escapes, "capture", side_effect=RuntimeError("capture refused")):
                    with self.assertRaisesRegex(RuntimeError, "^capture refused$"):
                        escapes.permission_birth(str(directory), str(target_file), "new-from-cli", "atlas-birth")
                self.assertEqual(original, config.read_text())
                self.assertEqual(0o640, config.stat().st_mode & 0o777)
                self.assertEqual(original, read_json(directory / "permission-evidence.json")["restored"])
                with patch.dict(os.environ, {"PFM_DEV_FENCE": "0"}):
                    with self.assertRaisesRegex(ValueError, "^BIRTH-ESCAPE-SCOPE:"):
                        escapes.permission_birth(str(directory), str(target_file), "new-from-cli", "atlas-birth")
                account = root / "home/.codex"
                account.mkdir(parents=True)
                catalogue = account / "models_cache.json"
                branch = {"JOURNEY_BRANCH": "linux+herdr-shield+project+codex"}
                for existing in (None, '{"models":[{"slug":"prior-model"}]}\n'):
                    if existing is not None:
                        catalogue.write_text(existing)
                        catalogue.chmod(0o640)
                    stage = directory / ("missing-catalogue" if existing is None else "existing-catalogue")
                    stage.mkdir()
                    expected_error = ('pfm chat new: codex model "bogus" is not offered by Codex (catalogue ' +
                                      str(catalogue) + '); offered: gpt-6-astra\n')
                    def refused(caller, output, name, arguments):
                        self.assertEqual({"models": [{"slug": "gpt-6-astra"}]}, read_json(catalogue))
                        self.assertEqual(target, caller)
                        self.assertEqual("alias-owner", name)
                        line = "pfm chat new --engine codex --model bogus --name atlas-owned >" + shlex.quote(str(stage / "alias.stdout")) + " 2>" + shlex.quote(str(stage / "alias.stderr"))
                        self.assertEqual(["sh", "-c", line], arguments)
                        write_text(stage / "alias.stdout", "")
                        write_text(stage / "alias.stderr", expected_error)
                        return {"status": "2"}
                    with patch.dict(os.environ, branch), patch.object(escapes, "owner_action", side_effect=refused), \
                            patch.object(escapes, "topology", return_value={"panes": ["actual-pane"]}):
                        self.assertEqual({"exit_status": "2", "stdout": "", "stderr": expected_error,
                                          "topology_preserved": True, "catalogue_restored": True},
                                         escapes.unknown_alias(str(stage), str(target_file), "atlas-owned"))
                        self.assertEqual(existing, catalogue.read_text() if catalogue.exists() else None)
                        if existing is not None:
                            self.assertEqual(0o640, catalogue.stat().st_mode & 0o777)


if __name__ == "__main__":
    unittest.main()
