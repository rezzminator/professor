#!/usr/bin/env python3
"""Ended and archived carry needs independent lifecycle evidence."""
import json
import os
from pathlib import Path
import shutil
import socket
import sqlite3
import sys
import tempfile
import unittest
from unittest.mock import patch

import journey_path
import observation
import observation_lifecycle as lifecycle
import observation_test
from observation_process import engine_births
from runtime import command, write_json


class LifecycleObservation(unittest.TestCase):
    def test_engine_classifier_retains_its_own_argv_environment_and_terminal(self):
        target = {"socket": "owned.sock", "pane": "%1"}
        shell = {"pid": 40, "start": "shell", "chain": [{"argv": "zsh -l"}], "environment": {}}
        engine = {"pid": 41, "start": "engine", "chain": [{"argv": "claude --model 'fixture model'"}],
                  "environment": {"MOCK_ENGINE_SCENARIO": "/j/fixture/scenario.json"}}
        foreign = {"pid": 42, "start": "foreign", "chain": [{"argv": "codex --model fixture"}], "environment": {}}
        observed = {"terminal": {"identity": target, "births": [shell]}, "process_births": [shell, engine, engine, foreign]}
        self.assertEqual([{"pid": 41, "start": "engine", "argv": ["claude", "--model", "fixture model"],
                           "environment": {"MOCK_ENGINE_SCENARIO": "/j/fixture/scenario.json"}, "terminal": target}],
                         engine_births(observed, "claude"))
        self.assertEqual([], engine_births({"terminal": {"identity": target, "births": [shell]}}, "claude"))

    def test_reload_records_read_only_current_and_prior_owned_seats(self):
        with tempfile.TemporaryDirectory() as scratch, patch.dict(os.environ, {"PFM_SID_DIR": scratch}):
            root = Path(scratch)
            (root / "reload-herdr-carried-seat.pid").write_text("123 456\n")
            (root / "reload-herdr-prior-seat.log").write_text("retired worker output\n")
            (root / "reload-herdr-foreign-seat.pid").write_text("FOREIGN-UNTOUCHED\n")
            chat = {**self.chat(), "terminal": {"backend": "herdr", "pane": "w1:p1"}, "reload_records": True, "prior_seat_ids": ["prior-seat"]}
            self.assertEqual({"reload-herdr-carried-seat.pid": "123 456\n", "reload-herdr-carried-seat.log": "absent",
                "reload-herdr-prior-seat.pid": "absent", "reload-herdr-prior-seat.log": "retired worker output\n"}, lifecycle.reload_records(chat))
            self.assertEqual("FOREIGN-UNTOUCHED\n", (root / "reload-herdr-foreign-seat.pid").read_text())
            self.assertEqual("n/a:not-touched", lifecycle.reload_records(self.chat()))
            self.assertEqual({"reload-owned.sock.pid": "absent", "reload-owned.sock.log": "absent"},
                             lifecycle.reload_records({**chat, "terminal": {"backend": "tmux", "socket": str(root / "owned.sock")}}))
            with self.assertRaisesRegex(ValueError, "OBSERVE-RELOAD-IDENTITY"):
                lifecycle.reload_records({**chat, "prior_seat_ids": ["../foreign"]})

    def test_expectations_refuse_undeclared_lifecycle_or_inapplicable_terminal(self):
        self.assertEqual({"lifecycle": "live", "terminal": "live", "visibility": "visible"}, lifecycle.expectations({}))
        self.assertEqual({"lifecycle": "transient", "terminal": "live", "visibility": "visible"},
                         lifecycle.expectations({"expect": {"lifecycle": "transient"}}))
        for expected in ({"lifecycle": "missing"}, {"terminal": "absent"}, {"visibility": "missing"}, {"undeclared": "value"}):
            with self.subTest(expected=expected), self.assertRaisesRegex(ValueError, "OBSERVE-LIFECYCLE-EXPECTATION"):
                lifecycle.expectations({"expect": expected})
        for expected in ({"lifecycle": "transient", "terminal": "shell"},
                         {"lifecycle": "transient", "visibility": "killed"}):
            with self.subTest(expected=expected), self.assertRaisesRegex(ValueError, "OBSERVE-LIFECYCLE-EXPECTATION"):
                lifecycle.expectations({"expect": expected})

    def chat(self):
        return {"name": "carried-chat", "session_id": "carried-session", "seat_id": "carried-seat", "engine": "claude",
                "terminal": {"backend": "tmux", "socket": "fixture.sock", "pane": "%1"},
                "expect": {"lifecycle": "ended", "terminal": "absent", "visibility": "visible"},
                "expected_prior_births": [{"pid": 123, "start": "fixture-start"}]}

    def test_ended_terminal_needs_dead_status_and_its_own_process_birth_gone(self):
        chat = self.chat()
        row = {"id": "carried-session", "kind": "resume-claude", "project": "atlas", "name": "carried-chat",
               "account": "1", "killed": "false", "socket": "fixture.sock"}
        storage = {"seat": [{"seat_id": "carried-seat", "state": "ended"}], "hidden": []}
        status = '{"state":"dead","engine":"cc"}'
        with patch.object(observation, "stored", return_value=storage), patch.object(observation, "report", return_value=status), \
             patch.object(observation, "process_snapshot", return_value={}), patch.object(lifecycle, "terminal_absent", return_value="n/a:ended-tmux-pane"):
            actual = observation.chat_projection(chat, [row])
            self.assertEqual("n/a:ended-tmux-pane", actual["terminal"])
            self.assertEqual({"births": [{"prior": {"pid": 123, "start": "fixture-start"}, "current": "n/a:process-absent"}]}, actual["lifecycle"]["ended"])
            with patch.object(observation, "process_snapshot", return_value={123: {"start": "different-birth"}}):
                self.assertEqual({"start": "different-birth"}, observation.chat_projection(chat, [row])["lifecycle"]["ended"]["births"][0]["current"])
            shell = {"births": [{"pid": 124, "start": "shell-start", "chain": [{"argv": "/bin/zsh -l"}]}], "identity": {"pane": "%1"}, "shield": "n/a:native-pane"}
            shell_chat = {**chat, "expect": {**chat["expect"], "terminal": "shell"}}
            with patch.object(observation, "terminal", return_value=shell):
                self.assertEqual(shell, observation.chat_projection(shell_chat, [row])["terminal"])
            with patch.object(observation, "terminal", return_value={**shell, "births": [{"chain": [{"argv": "/fixture/claude"}]}]}):
                with self.assertRaisesRegex(ValueError, "OBSERVE-SHELL-ABSENT"):
                    observation.chat_projection(shell_chat, [row])
            for processes, report, seats, message in [
                ({123: {"start": "fixture-start"}}, status, storage["seat"], "OBSERVE-ENGINE-STILL-LIVE"),
                ({}, '{"state":"idle","engine":"cc"}', storage["seat"], "OBSERVE-END-STATE"),
                ({}, status, [{"state": "live"}], "OBSERVE-SEAT-STILL-LIVE"),
            ]:
                with self.subTest(message=message), patch.object(observation, "process_snapshot", return_value=processes), \
                     patch.object(observation, "report", return_value=report), patch.object(observation, "stored", return_value={**storage, "seat": seats}):
                    with self.assertRaisesRegex(ValueError, message):
                        observation.chat_projection(chat, [row])
            with self.assertRaisesRegex(ValueError, "OBSERVE-DEATH-PROOF-ABSENT"):
                observation.chat_projection({**chat, "expected_prior_births": []}, [row])

    def test_transient_dead_status_preserves_actual_terminal_rows_and_status(self):
        chat = {**self.chat(), "expect": {"lifecycle": "transient", "terminal": "live", "visibility": "visible"}}
        row = {"kind": "live-claude", "id": "carried-session", "project": "atlas", "cwd": "/fixture/atlas",
               "name": "carried-chat", "account": "1", "killed": "false", "socket": "fixture.sock"}
        storage = {"seat": [{"seat_id": "carried-seat", "state": "lost", "backend": "tmux",
                              "tmux_socket": "fixture.sock", "tmux_pane": "%1"}], "hidden": []}
        status = {"state": "dead", "engine": "cc", "session_id": "carried-session", "socket": "fixture.sock"}
        display = {"identity": {"socket": "fixture.sock", "pane": "%1"}, "text": "lost shell", "births": []}
        with patch.object(observation, "stored", return_value=storage), patch.object(observation, "report", return_value=json.dumps(status)), \
             patch.object(observation, "process_snapshot", return_value={}), patch.object(observation, "terminal", return_value=display):
            actual = observation.chat_projection(chat, [row])
        self.assertEqual(display, actual["terminal"])
        self.assertEqual(status, actual["status"])
        self.assertEqual([row], actual["rows"])
        self.assertEqual({"births": [{"prior": {"pid": 123, "start": "fixture-start"}, "current": "n/a:process-absent"}]},
                         actual["lifecycle"]["transient"])
        self.assertEqual("dead", actual["picker"][0]["state"])
        self.assertEqual({"state": "n/a:transient", "line": "n/a:transient"}, actual["picker"][0]["badge"])

        lost_status = {**status, "state": "lost"}
        with patch.object(observation, "stored", return_value=storage), patch.object(observation, "report", return_value=json.dumps(lost_status)), \
             patch.object(observation, "process_snapshot", return_value={}), patch.object(observation, "terminal", return_value=display):
            self.assertEqual(lost_status, observation.chat_projection(chat, [row])["status"])

        with patch.object(observation, "stored", return_value=storage), patch.object(observation, "report", return_value=json.dumps({**status, "state": "idle"})), \
             patch.object(observation, "process_snapshot", return_value={}), patch.object(observation, "terminal", return_value=display):
            with self.assertRaisesRegex(ValueError, "OBSERVE-TRANSIENT-STATE"):
                observation.chat_projection(chat, [row])
        with patch.object(observation, "stored", return_value=storage), patch.object(observation, "report", return_value=json.dumps(status)), \
             patch.object(observation, "process_snapshot", return_value={123: {"start": "fixture-start"}}), patch.object(observation, "terminal", return_value=display):
            with self.assertRaisesRegex(ValueError, "OBSERVE-ENGINE-STILL-LIVE"):
                observation.chat_projection(chat, [row])
        with patch.object(observation, "stored", return_value={**storage, "seat": [{**storage["seat"][0], "state": "live"}]}), \
             patch.object(observation, "report", return_value=json.dumps(status)), patch.object(observation, "process_snapshot", return_value={}), \
             patch.object(observation, "terminal", return_value=display):
            with self.assertRaisesRegex(ValueError, "OBSERVE-TRANSIENT-SEAT"):
                observation.chat_projection(chat, [row])
        with patch.object(observation, "stored", return_value=storage), patch.object(observation, "report", return_value=json.dumps(status)), \
             patch.object(observation, "process_snapshot", return_value={}), patch.object(observation, "terminal", side_effect=ValueError("terminal-refusal")):
            with self.assertRaisesRegex(ValueError, "terminal-refusal"):
                observation.chat_projection(chat, [row])

    def test_absent_tmux_does_not_turn_unreadable_or_present_into_absence(self):
        with tempfile.TemporaryDirectory() as scratch:
            target = {"backend": "tmux", "socket": str(Path(scratch) / "tmux.sock"), "pane": "%1"}
            report = lambda arguments: "%1\n"
            self.assertEqual("n/a:ended-tmux-pane", lifecycle.terminal_absent(target, report, None))
            Path(target["socket"]).touch()
            with self.assertRaisesRegex(ValueError, "OBSERVE-TERMINAL-STILL-PRESENT"):
                lifecycle.terminal_absent(target, report, None)
            def unavailable(arguments):
                raise RuntimeError("fixture-refusal")
            with self.assertRaisesRegex(RuntimeError, "fixture-refusal"):
                lifecycle.terminal_absent(target, unavailable, None)
            Path(target["socket"]).unlink()
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as server:
                server.bind(target["socket"])
                self.assertEqual("n/a:ended-tmux-pane", lifecycle.terminal_absent(target, unavailable, None))
                server.listen()
                with self.assertRaisesRegex(RuntimeError, "fixture-refusal"):
                    lifecycle.terminal_absent(target, unavailable, None)

    def test_archive_requires_exact_publication_not_an_intent_or_foreign_row(self):
        with tempfile.TemporaryDirectory() as scratch, patch.dict(os.environ, {"HOME": scratch}):
            root = Path(scratch)
            archive = root / ".claude-archive"
            archive.mkdir()
            target = archive / "carried-session.jsonl"
            target.write_text('{"sessionId":"carried-session"}\n')
            metadata = target.stat()
            manifest = archive / "MANIFEST.tsv"
            fields = "uuid\tengine\torig_path\tbytes\tarchived_path\tarchived_at\tstate\tfile_id\n"
            row = f'carried-session\tcc\t{root / "original.jsonl"}\t32\t{target}\tfixture-time\tintent\t{metadata.st_dev}:{metadata.st_ino}\n'
            manifest.write_text(fields + row)
            self.assertEqual({"manifest": str(manifest), "row": {"uuid": "carried-session", "engine": "cc", "orig_path": str(root / "original.jsonl"),
                "bytes": "32", "archived_path": str(target), "archived_at": "fixture-time", "state": "intent", "file_id": f"{metadata.st_dev}:{metadata.st_ino}"},
                "sha256": "6ffd12872a4a09a98bce9080d4fdd4ea4b8e72740743e594a5c79d6484d8241f", "bytes": 32, "original": "absent"}, lifecycle.archive(self.chat()))
            manifest.write_text(fields + row.replace("carried-session\t", "foreign-session\t"))
            with self.assertRaisesRegex(ValueError, "OBSERVE-ARCHIVE-ROW"):
                lifecycle.archive(self.chat())
            manifest.write_text(fields + row.replace(f"\t{metadata.st_dev}:{metadata.st_ino}\n", "\t0:0\n"))
            with self.assertRaisesRegex(ValueError, "OBSERVE-ARCHIVE-PUBLICATION"):
                lifecycle.archive(self.chat())
            manifest.write_text(fields + row.replace("\tcc\t", "\tcx\t"))
            with self.assertRaisesRegex(ValueError, "OBSERVE-ARCHIVE-ENGINE"):
                lifecycle.archive(self.chat())
            manifest.write_text(fields + row)
            (root / "original.jsonl").write_text("still original")
            with self.assertRaisesRegex(ValueError, "OBSERVE-ARCHIVE-PUBLICATION"):
                lifecycle.archive(self.chat())

    def test_uninstalled_reports_are_inapplicable_only_after_actual_binary_absence(self):
        with tempfile.TemporaryDirectory() as scratch, patch.dict(os.environ, {"HOME": scratch, "PFM_UNIT_PREFIX": "pfm-fixture"}):
            root = Path(scratch)
            state, available = lifecycle.machine({"phase": "uninstalled"}, lambda arguments: "")
            self.assertEqual({"phase": "uninstalled", "binary": "absent", "managed_files": {}, "units": []}, state)
            self.assertEqual(False, available)
            binary = root / ".local/bin/pfm"
            binary.parent.mkdir(parents=True)
            binary.write_bytes(b"public CLI remains after uninstall")
            state, available = lifecycle.machine({"phase": "uninstalled"}, lambda arguments: "")
            self.assertEqual(str(binary), state["binary"])
            self.assertEqual(True, available)
            self.assertEqual(({"phase": "installed"}, True), lifecycle.machine({}, lambda arguments: ""))
            def unavailable(arguments):
                raise RuntimeError("unit-read-refused")
            with self.assertRaisesRegex(RuntimeError, "unit-read-refused"):
                lifecycle.machine({"phase": "uninstalled"}, unavailable)

    def test_uninstalled_macos_reports_only_exact_launchd_prefix(self):
        with tempfile.TemporaryDirectory() as scratch, patch.dict(os.environ, {"HOME": scratch, "PFM_UNIT_PREFIX": "pfm-fixture"}), \
             patch.object(lifecycle.sys, "platform", "darwin"):
            output = "\n".join(("41\t0\tcom.professor.pfm-fixture.mcp",
                                   "42\t0\tcom.professor.pfm-fixture.schedule",
                                   "43\t0\tcom.professor.pfm-fixture-other.mcp",
                                   "44\t0\tcom.professor.other.mcp"))
            state, available = lifecycle.machine({"phase": "uninstalled"}, lambda arguments: output)
            self.assertEqual(["41\t0\tcom.professor.pfm-fixture.mcp", "42\t0\tcom.professor.pfm-fixture.schedule"], state["units"])
            self.assertFalse(available)

    def test_uninstalled_bundle_retains_readonly_store_foreign_and_terminal(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            database = observation_test.Observation().database(root)
            with sqlite3.connect(database) as connection:
                connection.execute("UPDATE seat SET state='ended'")
            transcript = root / "transcript.jsonl"
            transcript.write_text('{"sessionId":"carried-session"}\n')
            chat = {**self.chat(), "transcript_path": str(transcript)}
            foreign = {**chat, "name": "foreign-sentinel", "session_id": "foreign-session", "seat_id": "foreign-seat"}
            write_json(root / "carry.json", {"phase": "uninstalled", "chats": [chat], "foreign": foreign})
            write_json(root / "target.json", {"backend": "tmux", "socket": "owner.sock", "pane": "%0"})
            display = {"identity": {"pane": "%0"}, "text": "owner shell"}
            with patch.dict(os.environ, {"HOME": scratch, "JOURNEY_ROOT": scratch, "JOURNEY_CARRY": str(root / "carry.json"), "PFM_STATE_DB": str(database), "PFM_UNIT_PREFIX": "pfm-fixture"}), \
                 patch.object(observation, "report", return_value=""), patch.object(observation, "process_snapshot", return_value={}), \
                 patch.object(observation, "terminal", return_value=display), patch.object(lifecycle, "terminal_absent", return_value="n/a:ended-tmux-pane"):
                actual = observation.bundle(root / "target.json")
            self.assertEqual([], actual["errors"])
            self.assertEqual("n/a:uninstalled", actual["picker"]["rows"])
            self.assertEqual("n/a:uninstalled", actual["chats"][0]["status"])
            self.assertEqual("n/a:ended-tmux-pane", actual["foreign"]["terminal"])
            self.assertEqual("ended", actual["stored"]["carried-session"]["seat"][0]["state"])
            self.assertEqual("owner shell", actual["terminal"]["text"])

    def test_reopen_uses_exact_carried_seat_and_never_revives_ended_intent(self):
        with tempfile.TemporaryDirectory() as scratch:
            database = observation_test.Observation().database(Path(scratch))
            chat = {**self.chat(), "expect": {"lifecycle": "live", "terminal": "live"}}
            with patch.dict(os.environ, {"PFM_STATE_DB": str(database)}):
                self.assertEqual(True, observation.reopenable(chat))
                self.assertEqual(False, observation.reopenable(self.chat()))
                with sqlite3.connect(database) as connection:
                    connection.execute("UPDATE seat SET state='ended' WHERE seat_id='carried-seat'")
                self.assertEqual(False, observation.reopenable(chat))
                with self.assertRaisesRegex(ValueError, "OBSERVE-REOPEN-SEAT-ABSENT"):
                    observation.reopenable({**chat, "seat_id": "foreign-seat"})
                legacy = {key: value for key, value in chat.items() if key != "seat_id"}
                receipt = {"status": {"session_id": "carried-session", "state": "idle"}, "terminal": legacy["terminal"], "births": [{"pid": 123, "start": "fixture-start"}]}
                write_json(Path(scratch) / "checkpoint-births.json", {"carried-session": receipt})
                with patch.dict(os.environ, {"JOURNEY_ROOT": scratch}):
                    self.assertEqual(True, observation.reopenable(legacy))
                    moved = {**legacy, "terminal": {**legacy["terminal"], "socket": "relocated.sock"}}
                    write_json(Path(scratch) / "checkpoint-terminals.json", {"carried-session": {"source": receipt["terminal"], "current": moved["terminal"]}})
                    self.assertEqual(True, observation.reopenable(moved))
                    write_json(Path(scratch) / "checkpoint-terminals.json", {"carried-session": {"source": {**receipt["terminal"], "socket": "foreign.sock"}, "current": moved["terminal"]}})
                    with self.assertRaisesRegex(ValueError, "OBSERVE-REOPEN-CHECKPOINT-MISMATCH"):
                        observation.reopenable(moved)
                    write_json(Path(scratch) / "checkpoint-births.json", {"carried-session": {**receipt, "status": {"session_id": "foreign-session", "state": "idle"}}})
                    with self.assertRaisesRegex(ValueError, "OBSERVE-REOPEN-CHECKPOINT-MISMATCH"):
                        observation.reopenable(legacy)

    def test_observer_guards_fail_in_a_private_fence_copy(self):
        if os.environ.get("PFM_DEV_FENCE") != "1":
            self.fail("observer mutations require the fence")
        source = Path(__file__).resolve().parents[1]
        mutations = [
            ("observation_process.py", 'Path(command[0]).name in {engine, "mock-engine"}',
             'Path(command[0]).name in {"codex"}', "observation_lifecycle_test.py", "LifecycleObservation.test_engine_classifier_retains_its_own_argv_environment_and_terminal"),
            ("observation_lifecycle.py", 'if current and current["start"] == birth["start"]:', 'if False:', "observation_lifecycle_test.py", "LifecycleObservation.test_ended_terminal_needs_dead_status_and_its_own_process_birth_gone"),
            ("observation_lifecycle.py", 'if present:', 'if False:', "observation_lifecycle_test.py", "LifecycleObservation.test_absent_tmux_does_not_turn_unreadable_or_present_into_absence"),
            ("observation_lifecycle.py", 'row.get("file_id") != f"{metadata.st_dev}:{metadata.st_ino}"', 'False', "observation_lifecycle_test.py", "LifecycleObservation.test_archive_requires_exact_publication_not_an_intent_or_foreign_row"),
            ("observation_lifecycle.py", 'if row["engine"] != engine_id(chat["engine"]):', 'if False:', "observation_lifecycle_test.py", "LifecycleObservation.test_archive_requires_exact_publication_not_an_intent_or_foreign_row"),
            ("observation_lifecycle.py", 'available = not absent(binary)', 'available = False', "observation_lifecycle_test.py", "LifecycleObservation.test_uninstalled_reports_are_inapplicable_only_after_actual_binary_absence"),
            ("observation_lifecycle.py", 'if carry.get("phase") != "uninstalled":', 'if False:', "observation_lifecycle_test.py", "LifecycleObservation.test_uninstalled_reports_are_inapplicable_only_after_actual_binary_absence"),
            ("observation_lifecycle.py", 'if not isinstance(status, dict) or status.get("state") not in {"dead", "lost"}:', 'if False:', "observation_lifecycle_test.py", "LifecycleObservation.test_transient_dead_status_preserves_actual_terminal_rows_and_status"),
            ("observation.py", 'if expected["lifecycle"] != "live" or expected["terminal"] != "live":', 'if False:', "observation_lifecycle_test.py", "LifecycleObservation.test_reopen_uses_exact_carried_seat_and_never_revives_ended_intent"),
            ("observation.py", 'saved["status"]["session_id"] != chat["session_id"]', 'False', "observation_lifecycle_test.py", "LifecycleObservation.test_reopen_uses_exact_carried_seat_and_never_revives_ended_intent"),
            ("observation.py", 'relocated["source"] != terminal', 'False', "observation_lifecycle_test.py", "LifecycleObservation.test_reopen_uses_exact_carried_seat_and_never_revives_ended_intent"),
            ("observation_process.py", 'KEYS = {"CLAUDE_CONFIG_DIR",', 'KEYS = {"ANTHROPIC_API_KEY", "CLAUDE_CONFIG_DIR",', "observation_process_test.py", "Observation.test_process_environment_keeps_configuration_and_redacts_credentials"),
            ("observation.py", 'process_tree([int(pane["pane_pid"])], descendants=True)', 'process_tree([int(pane["pane_pid"])], descendants=False)', "observation_test.py", "Observation.test_private_tmux_reports_the_owned_pane"),
            ("observation_process.py", 'values[argc:]', 'values[argc + 1:]', "observation_process_test.py", "Observation.test_darwin_process_environment_reads_the_kernel_abi"),
            ("observation.py", '"pane": pane.get("tokens", {})', '"pane": {}', "observation_test.py", "Observation.test_private_terminal_reports_identity_focus_text_and_birth"),
            ("observation.py", 'if status["state"] in {"dead", "lost"}:', 'if False:', "observation_test.py", "Observation.test_picker_badge_must_agree_with_observed_state"),
            ("observation_lifecycle.py", 'if not chat.get("reload_records"):', 'if False:', "observation_lifecycle_test.py", "LifecycleObservation.test_reload_records_read_only_current_and_prior_owned_seats"),
            ("observation_lifecycle.py", 'if any(not re.fullmatch(r"[a-zA-Z0-9_.-]+", key) for key in keys):', 'if False:', "observation_lifecycle_test.py", "LifecycleObservation.test_reload_records_read_only_current_and_prior_owned_seats"),
        ]
        output = Path(os.environ.get("PFM_TEST_TIMING_DIR", "/tmp")) / "journey-observation-mutations"
        output.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix="journey-observation-mut-") as scratch:
            tree = Path(scratch) / "journey"
            shutil.copytree(source, tree, ignore=shutil.ignore_patterns("__pycache__"))
            for number, (module, old, new, test_file, test) in enumerate(mutations, 1):
                with self.subTest(module=module, test=test):
                    path = tree / module
                    original = path.read_text()
                    self.assertEqual(1, original.count(old), f"MUTATION-SOURCE-DRIFT: {module}: {old}")
                    path.write_text(original.replace(old, new))
                    try:
                        try:
                            result = command([sys.executable, str(tree / "fixtures" / test_file), test], timeout=20,
                                             env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"})
                        except RuntimeError as error:
                            result = str(error)
                        log = output / (str(number) + ".log")
                        log.write_text(result)
                        self.assertIn("AssertionError", result, str(log))
                        self.assertIn("FAILED (failures=", result, str(log))
                        print(f"WATCHED-FAIL {module}/{test} PASS log={log}")
                    finally:
                        path.write_text(original)


if __name__ == "__main__":
    unittest.main()
