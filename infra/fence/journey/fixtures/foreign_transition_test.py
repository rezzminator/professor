#!/usr/bin/env python3
"""Foreign sentinel baseline transitions preserve real identity and death proof."""
import copy
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import journey_path
import foreign_transition
import observation
from runtime import read_json, write_json


class ForeignTransition(unittest.TestCase):
    def foreign_bundle(self, pid, start, pane="foreign:p1", *, state="idle", machine=None, ended=None):
        session = "foreign-session"
        birth = {"pid": pid, "start": start, "chain": [{"argv": "mock-engine --resume " + session}],
                 "environment": {"MOCK_ENGINE_SCENARIO": "/fixture/foreign/scenario.json"}}
        identity = {"socket": "/fixture/herdr.sock", "workspace": "w-cobalt", "tab": "w-cobalt:t1", "pane": pane}
        stored = {"launch": [{"session_id": session, "engine": "cc", "account": 3}],
                  "seat": [{"session_id": session, "state": "live"}],
                  "chat": [{"uuid": session, "cwd": "/fixture/work/cobalt"}],
                  "transcript": {"entries": [{"role": "user", "text": "foreign-proof"}]},
                  "recipe": {"binary": "/fixture/mock-engine"}}
        status = {"state": state, "session_id": session, "engine": "cc", "account": 3,
                  "cwd": "/fixture/work/cobalt", "model": "fixture-model"}
        rows = [{"kind": "live-claude", "id": session, "project": "cobalt", "cwd": "/fixture/work/cobalt",
                 "name": "foreign-sentinel", "account": "3", "killed": "false", "socket": "/fixture/herdr.sock",
                 "activity_ns": "1", "prompts": "1", "size": "1"}]
        picker = [{"id": session, "engine": "cc", "project": "cobalt", "name": "foreign-sentinel", "account": 3,
                   "kind": "live-claude", "state": state, "killed": False, "hidden": False,
                   "backend": "herdr", "address": {"pane": pane}, "badge": {"state": state}}]
        full = {"name": "foreign-sentinel", "status": status, "rows": rows, "picker": picker, "stored": stored,
                "terminal": {"backend": "herdr", "identity": identity,
                             "workspace": {"workspace": {"label": "cobalt", "focused": False}},
                             "tab": {"tab": {"label": "FOREIGN-SENTINEL", "focused": False}},
                             "pane": {"focused": False}, "text": "foreign-sentinel\n",
                             "births": [birth]}, "process_births": [birth]}
        if ended is not None:
            full["lifecycle"] = {"ended": {"births": ended}}
        projection = observation.foreign_projection(full)
        bundle = {"errors": [], "activity": {"unexpected": [], "missing_expected": []},
                  "foreign": projection, "foreign_full": full}
        if machine is not None:
            bundle["machine"] = machine
        return bundle

    def transition_paths(self, root, before, after, receipt, suffix):
        before_path, after_path = root / ("before-" + suffix + ".json"), root / ("after-" + suffix + ".json")
        receipt_path, baseline_path = root / ("receipt-" + suffix + ".json"), root / "out/foreign.json"
        write_json(before_path, before)
        write_json(after_path, after)
        write_json(receipt_path, receipt)
        write_json(baseline_path, before["foreign"])
        write_json(root / "out/fleet-reader.state.json", {
            "closed": False, "assertions": [{"id": "transition", "pass": True}], "errors": []})
        return before_path, after_path, receipt_path, baseline_path

    def test_foreign_restart_transition_requires_actual_rebind_and_preserves_baseline_on_error(self):
        with tempfile.TemporaryDirectory(prefix="foreign-transition-") as scratch, patch.dict(os.environ, {
                "JOURNEY_ROOT": scratch, "JOURNEY_OUT": scratch + "/out", "JOURNEY_LANE": "fleet-reader"}), \
                patch.object(foreign_transition, "process_snapshot", return_value={}):
            root = Path(scratch)
            before = self.foreign_bundle(101, "old-birth")
            after = self.foreign_bundle(202, "new-birth", "foreign:p2")
            self.assertNotEqual(before["foreign_full"]["rows"], before["foreign"]["rows"])
            receipt = {"kind": "restart", "session_id": "foreign-session", "rebound": True,
                       "old_births_gone": True, "before_terminal": before["foreign_full"]["terminal"]["identity"],
                       "after_terminal": after["foreign_full"]["terminal"]["identity"],
                       "old_births": [{"pid": 101, "start": "old-birth"}],
                       "new_births": [{"pid": 202, "start": "new-birth"}]}
            before_path, after_path, receipt_path, baseline = self.transition_paths(root, before, after, receipt, "valid")
            result = foreign_transition.adopt_transition("restart", before_path, after_path, receipt_path, baseline,
                                                         root / "beat/foreign-transition.json")
            self.assertEqual(str(baseline), result["baseline"])
            self.assertEqual(after["foreign"], read_json(baseline))
            evidence = read_json(root / "beat/foreign-transition.json")
            self.assertEqual(before["foreign"], evidence["old_baseline"])
            self.assertEqual(after["foreign"], evidence["after_foreign"])

            failures = []
            wrong_identity = copy.deepcopy(after)
            wrong_identity["foreign_full"]["status"]["session_id"] = "other-session"
            failures.append(("wrong-identity", wrong_identity, receipt))
            wrong_birth = copy.deepcopy(after)
            wrong_birth["foreign_full"]["process_births"][0]["start"] = "unrecorded-birth"
            wrong_birth["foreign_full"]["terminal"]["births"][0]["start"] = "unrecorded-birth"
            failures.append(("wrong-birth", wrong_birth, receipt))
            live_old = copy.deepcopy(after)
            live_old["foreign_full"]["process_births"][0] = copy.deepcopy(before["foreign_full"]["process_births"][0])
            live_old["foreign_full"]["terminal"]["births"][0] = copy.deepcopy(before["foreign_full"]["terminal"]["births"][0])
            failures.append(("live-old", live_old, receipt))
            failed_assertion = copy.deepcopy(after)
            failures.append(("failed-assertion", failed_assertion, receipt))
            for suffix, changed, changed_receipt in failures:
                with self.subTest(suffix=suffix):
                    changed = copy.deepcopy(changed)
                    if suffix != "failed-assertion":
                        changed["foreign"]["state"] = "wrong-" + suffix
                    _, candidate, candidate_receipt, _ = self.transition_paths(root, before, changed, changed_receipt, suffix)
                    write_json(baseline, after["foreign"])
                    write_json(root / "out/fleet-reader.state.json", {
                        "closed": False, "assertions": [{"id": "transition", "pass": True}], "errors": []})
                    if suffix == "failed-assertion":
                        write_json(root / "out/fleet-reader.state.json", {
                            "closed": False, "assertions": [{"id": "transition", "pass": False}], "errors": []})
                    with self.assertRaisesRegex(ValueError, "FOREIGN-TRANSITION"):
                        foreign_transition.adopt_transition("restart", before_path, candidate, candidate_receipt, baseline,
                                                             root / ("beat/" + suffix + ".json"))
                    self.assertEqual(after["foreign"], read_json(baseline))
                    self.assertFalse((root / ("beat/" + suffix + ".json")).exists())

            preserved_evidence = root / "beat/preserved.json"
            write_json(preserved_evidence, {"old": "evidence"})
            write_json(root / "out/fleet-reader.state.json", {
                "closed": False, "assertions": [{"id": "transition", "pass": True}], "errors": []})
            with self.assertRaisesRegex(ValueError, "FOREIGN-TRANSITION"):
                foreign_transition.adopt_transition("restart", before_path, after_path, receipt_path, baseline,
                                                     preserved_evidence)
            self.assertEqual({"old": "evidence"}, read_json(preserved_evidence))
            self.assertEqual(after["foreign"], read_json(baseline))

    def test_foreign_uninstall_transition_requires_installed_uninstalled_and_ended_proof(self):
        with tempfile.TemporaryDirectory(prefix="foreign-uninstall-") as scratch, patch.dict(os.environ, {
                "JOURNEY_ROOT": scratch, "JOURNEY_OUT": scratch + "/out", "JOURNEY_LANE": "fleet-reader"}), \
                patch.object(foreign_transition, "process_snapshot", return_value={}):
            root = Path(scratch)
            before = self.foreign_bundle(101, "old-birth", machine={"phase": "installed"})
            death = [{"prior": {"pid": 101, "start": "old-birth"}, "current": "n/a:process-absent"}]
            after = self.foreign_bundle(101, "old-birth", state="n/a:uninstalled", machine={"phase": "uninstalled", "binary": "absent", "managed_files": {}, "units": []}, ended=death)
            after["foreign_full"].update(status="n/a:uninstalled", rows="n/a:uninstalled", picker="n/a:uninstalled", terminal="n/a:ended-tmux-pane", process_births=[])
            after["foreign"] = observation.foreign_projection(after["foreign_full"])
            receipt = {"kind": "uninstall", "session_id": "foreign-session", "ended": True,
                       "ended_proof": {"session_id": "foreign-session", "state": "n/a:uninstalled", "births": death},
                       "inapplicable": {"picker": "uninstalled", "rows": "uninstalled", "status": "uninstalled", "terminal": "ended"}}
            before_path, after_path, receipt_path, baseline = self.transition_paths(root, before, after, receipt, "uninstall")
            foreign_transition.adopt_transition("uninstall", before_path, after_path, receipt_path, baseline,
                                                root / "beat/uninstall-transition.json")
            self.assertEqual(after["foreign"], read_json(baseline))
            self.assertEqual(before["foreign"], read_json(root / "beat/uninstall-transition.json")["old_baseline"])

    def test_foreign_uninstall_keeps_shell_and_retained_binary_but_rejects_fake_na_reports(self):
        with tempfile.TemporaryDirectory(prefix="foreign-uninstall-retained-") as scratch, patch.dict(os.environ, {
                "JOURNEY_ROOT": scratch, "JOURNEY_OUT": scratch + "/out", "JOURNEY_LANE": "fleet-reader"}), \
                patch.object(foreign_transition, "process_snapshot", return_value={}):
            root = Path(scratch)
            before = self.foreign_bundle(101, "old-birth", machine={"phase": "installed"})
            death = [{"prior": {"pid": 101, "start": "old-birth"}, "current": "n/a:process-absent"}]
            shell = {"pid": 303, "start": "shell-birth", "chain": [{"argv": "bash --noprofile"}],
                     "environment": {}}
            after = self.foreign_bundle(101, "old-birth", state="dead", machine={
                "phase": "uninstalled", "binary": "/fixture/bin/pfm", "managed_files": {}, "units": []}, ended=death)
            after["foreign_full"]["picker"][0]["state"] = "dead"
            after["foreign_full"]["terminal"]["births"] = [shell]
            after["foreign_full"]["process_births"] = [shell]
            after["foreign"] = observation.foreign_projection(after["foreign_full"])
            receipt = {"kind": "uninstall", "session_id": "foreign-session", "ended": True,
                       "ended_proof": {"session_id": "foreign-session", "state": "dead", "births": death},
                       "inapplicable": {}}
            before_path, after_path, receipt_path, baseline = self.transition_paths(root, before, after, receipt, "retained")
            foreign_transition.adopt_transition("uninstall", before_path, after_path, receipt_path, baseline,
                                                root / "beat/retained-transition.json")
            self.assertEqual(after["foreign"], read_json(baseline))
            self.assertEqual([shell], after["foreign_full"]["process_births"])

            fake_na = copy.deepcopy(after)
            fake_na["foreign_full"].update(status="n/a:uninstalled", rows="n/a:uninstalled", picker="n/a:uninstalled")
            fake_na["foreign"] = observation.foreign_projection(fake_na["foreign_full"])
            _, candidate, candidate_receipt, _ = self.transition_paths(root, before, fake_na, receipt, "retained-na")
            write_json(baseline, before["foreign"])
            with self.assertRaisesRegex(ValueError, "reports may be n/a"):
                foreign_transition.adopt_transition("uninstall", before_path, candidate, candidate_receipt, baseline,
                                                    root / "beat/retained-na-transition.json")
            self.assertEqual(before["foreign"], read_json(baseline))

            write_json(root / "out/fleet-reader.state.json", {
                "closed": False, "assertions": [{"id": "transition", "pass": True}], "errors": []})
            write_json(baseline, before["foreign"])
            with patch.object(foreign_transition, "process_snapshot", return_value={101: {"start": "old-birth"}}), \
                    self.assertRaisesRegex(ValueError, "prior engine birth is not proven dead"):
                foreign_transition.adopt_transition("uninstall", before_path, after_path, receipt_path, baseline,
                                                    root / "beat/live-birth-transition.json")
            self.assertEqual(before["foreign"], read_json(baseline))

            already_ended = copy.deepcopy(before)
            already_ended["foreign_full"]["status"]["state"] = "dead"
            already_ended["foreign_full"]["lifecycle"] = {"ended": {"births": death}}
            already_ended["foreign_full"]["process_births"] = [shell]
            already_ended["foreign_full"]["terminal"]["births"] = [shell]
            already_ended["foreign"] = observation.foreign_projection(already_ended["foreign_full"])
            ended_before, ended_after, ended_receipt, ended_baseline = self.transition_paths(
                root, already_ended, after, receipt, "already-ended")
            foreign_transition.adopt_transition("uninstall", ended_before, ended_after, ended_receipt,
                                                ended_baseline, root / "beat/already-ended-transition.json")
            self.assertEqual(after["foreign"], read_json(ended_baseline))

    def test_foreign_install_transition_requires_typed_public_receipt_and_ended_continuity(self):
        with tempfile.TemporaryDirectory(prefix="foreign-install-") as scratch, patch.dict(os.environ, {
                "JOURNEY_ROOT": scratch, "JOURNEY_OUT": scratch + "/out", "JOURNEY_LANE": "fleet-reader"}), \
                patch.object(foreign_transition, "process_snapshot", return_value={}):
            root = Path(scratch)
            death = [{"prior": {"pid": 101, "start": "old-birth"}, "current": "n/a:process-absent"}]
            shell = {"pid": 303, "start": "shell-birth", "chain": [{"argv": "bash --noprofile"}],
                     "environment": {}}
            before = self.foreign_bundle(101, "old-birth", state="n/a:uninstalled", machine={
                "phase": "uninstalled", "binary": "absent", "managed_files": {}, "units": []}, ended=death)
            before["foreign_full"].update(status="n/a:uninstalled", rows="n/a:uninstalled", picker="n/a:uninstalled",
                                           process_births=[shell])
            before["foreign_full"]["terminal"]["births"] = [shell]
            before["foreign"] = observation.foreign_projection(before["foreign_full"])
            after = self.foreign_bundle(101, "old-birth", state="dead", machine={"phase": "installed"}, ended=death)
            after["foreign_full"]["picker"][0]["state"] = "dead"
            after["foreign_full"]["terminal"]["births"] = [shell]
            after["foreign_full"]["process_births"] = [shell]
            after["foreign"] = observation.foreign_projection(after["foreign_full"])
            receipt = {"kind": "install", "argv": ["pfm", "install", "--yes"], "status": 0,
                       "out": "pfm install --yes\n", "session_id": "foreign-session",
                       "ended_proof": {"session_id": "foreign-session", "state": "n/a:uninstalled", "births": death},
                       "inapplicable": {"picker": "uninstalled", "rows": "uninstalled", "status": "uninstalled"}}
            before_path, after_path, receipt_path, baseline = self.transition_paths(root, before, after, receipt, "install")
            write_json(root / "out/fleet-reader.state.json", {
                "closed": True, "verdict": "pass", "assertions": [{"id": "uninstall", "pass": True}], "errors": []})
            result = foreign_transition.adopt_transition("install", before_path, after_path, receipt_path, baseline,
                                                         root / "beat/install-transition.json")
            self.assertEqual("foreign-session", result["session_id"])
            self.assertEqual(after["foreign"], read_json(baseline))
            retained = copy.deepcopy(after)
            retained["machine"] = {"phase": "uninstalled", "binary": str(root / "pfm"), "managed_files": {}, "units": []}
            retained_receipt = {**receipt, "inapplicable": {}}
            retained_paths = self.transition_paths(root, retained, after, retained_receipt, "install-retained")
            foreign_transition.adopt_transition("install", *retained_paths, root / "beat/install-retained-transition.json")
            self.assertEqual(after["foreign"], read_json(retained_paths[3]))
            bad = copy.deepcopy(receipt)
            bad["argv"] = ["pfm", "internal", "install"]
            _, _, bad_receipt, _ = self.transition_paths(root, before, after, bad, "install-bad")
            write_json(baseline, before["foreign"])
            with self.assertRaisesRegex(ValueError, "typed public install argv"):
                foreign_transition.adopt_transition("install", before_path, after_path, bad_receipt, baseline,
                                                    root / "beat/install-bad-transition.json")
            self.assertEqual(before["foreign"], read_json(baseline))


if __name__ == "__main__":
    unittest.main()
