#!/usr/bin/env python3
"""MCP receipt parsing and run-owned process/login boundary checks."""
import json
import copy
import importlib.util
import os
from pathlib import Path
import shlex
import sys
import tempfile
import unittest
from unittest.mock import patch

FIXTURES = Path(__file__).resolve().parents[1]
sys.path.extend(str(path) for path in (FIXTURES.parent, FIXTURES / "machine-restart"))
import mcp
import ssh_session
import control
import restore as restart_restore
import lifecycle
import projection
import history
import archive
from runtime import command

TEARDOWN_SPEC = importlib.util.spec_from_file_location("teardown_projection", FIXTURES / "machine-lifecycle/projection.py")
teardown = importlib.util.module_from_spec(TEARDOWN_SPEC)
TEARDOWN_SPEC.loader.exec_module(teardown)


class EndingProtocols(unittest.TestCase):
    def test_restart_role_selection_preserves_absence_and_unreadable(self):
        script = ". " + shlex.quote(str(FIXTURES / "machine-restart/call.sh")) + """
mesh_select() {
  printf '%s\\n' "$2"
  case "$2" in new-from-picker) return "$1" ;; restart-new-from-picker) return "$3" ;; esac
}
_restart_select "$1" new-from-picker "$2"
result=$?
printf 'result=%s role=%s\\n' "$result" "$RESTART_ROLE"
"""
        for original, fallback, expected in (
            (0, 0, "new-from-picker\nresult=0 role=new-from-picker\n"),
            (1, 0, "new-from-picker\nrestart-new-from-picker\nresult=0 role=restart-new-from-picker\n"),
            (1, 1, "new-from-picker\nrestart-new-from-picker\nresult=1 role=restart-new-from-picker\n"),
            (2, 0, "new-from-picker\nresult=2 role=new-from-picker\n"),
        ):
            with self.subTest(original=original, fallback=fallback):
                self.assertEqual(expected, command(["bash", "-c", script, "restart-selection", str(original), str(fallback)]))

    def test_branch_receipt_and_recovery_files_keep_actual_session(self):
        with tempfile.TemporaryDirectory() as temporary, patch.dict(os.environ, {"JOURNEY_ROOT": temporary}):
            root = Path(temporary)
            receipt = root / "receipt"
            output = Path(str(receipt) + ".out")
            output.write_text('Branched parent… as "child" into detached socket owned-socket — waiting in pfm ls; open later.\n')
            with patch.object(history, "report", return_value=json.dumps({"session_id": "actual-child", "state": "idle"})):
                self.assertEqual("actual-child", history.branch_session(str(receipt)))
            with patch.object(history, "report", return_value=json.dumps({"state": "not-found"})):
                self.assertEqual(None, history.branch_session(str(receipt)))
            output.write_text("unrelated output\n")
            with self.assertRaisesRegex(ValueError, "ENDING-BRANCH-RECEIPT"):
                history.branch_session(str(receipt))
            files = [root / name for name in ("brief.md", "compaction-memory.md", "transcript.md")]
            for path in files:
                path.write_text("abc")
            output.write_text("messages=1 carried=1 malformed=0\nrecovered thread actual-child\n" + "".join("  " + str(path) + "\n" for path in files))
            self.assertEqual({"session_id": "actual-child", "files": {path.name: {"bytes": 3, "sha256": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"} for path in files}}, history.recovery(str(receipt)))
            files[0].unlink()
            with self.assertRaises(FileNotFoundError):
                history.recovery(str(receipt))

    def test_lifecycle_projection_separates_death_visibility_and_new_birth(self):
        recipe = {"cwd": "/owned/project", "args": ["--model", "sonnet", "--effort", "high", "--resume", "session"],
                  "env": ["PFM_BEAT=old", "CLAUDE_CONFIG_DIR=/owned/account/2", "CLAUDE_CODE_EFFORT_LEVEL=high"]}
        self.assertEqual({"cwd": "/owned/project", "options": {"--model": "sonnet", "--effort": "high"},
                          "environment": ["CLAUDE_CODE_EFFORT_LEVEL=high", "CLAUDE_CONFIG_DIR=/owned/account/2"]}, projection.resume_recipe(recipe))
        chat = {"key": "owned", "name": "desk", "engine": "codex", "account": 2, "session_id": "thread", "seat_id": "seat", "terminal": {"backend": "tmux"}}
        birth = {"pid": 42, "start": "old", "chain": [{"argv": "/run/mock-engine"}], "environment": {"PFM_RUN": "run", "CODEX_HOME": "/run/account/2"}}
        before = {"errors": [], "foreign": {"id": "foreign", "state": "idle"}, "chats": [{
            "name": "desk", "status": {"state": "idle", "engine": "cx", "session_id": "thread", "model": "gpt-5", "effort": "high", "cwd": "/run/work/atlas"},
            "picker": [{"killed": False, "hidden": False, "account": 2, "project": "atlas", "engine": "cx", "backend": "tmux", "state": "idle"}],
            "stored": {"chat": [{"uuid": "thread", "cwd": "/run/work/atlas", "label": "desk"}], "hidden": [], "transcript": {"entries": [{"text": "nonce"}]}},
            "terminal": {"backend": "tmux", "identity": {"pane": "%1"}, "births": [birth]}, "process_births": [birth]}]}
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / "parent"
            source.write_text("abc")
            transcript = {"path": str(source), "sha256": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", "entries": [{"role": "user", "text": "nonce"}]}
            child = {**chat, "session_id": "child", "parent": "thread", "parent_key": "owned"}
            after = copy.deepcopy(before)
            child_observed = copy.deepcopy(before["chats"][0])
            child_observed["status"]["session_id"] = "child"
            child_observed["stored"]["chat"][0]["uuid"] = "child"
            child_observed["process_births"][0].update(pid=43, start="new")
            after["chats"].append(child_observed)
            rows = history.branch_checks(before, after, chat, child, transcript, transcript)
            self.assertEqual("thread", next(row["actual"] for row in rows if row["field"] == "branch-parent"))
            self.assertEqual([], [row for row in rows if row["expected"] != row["actual"]])
            child["parent"] = "foreign"
            rows = history.branch_checks(before, after, chat, child, transcript, transcript)
            self.assertEqual(["branch-parent"], [row["field"] for row in rows if row["expected"] != row["actual"]])
        for mode in ("end", "kill", "unkill", "resume", "refused"):
            with self.subTest(mode=mode):
                after = copy.deepcopy(before)
                current = after["chats"][0]
                dead = mode in ("end", "kill", "unkill")
                current["status"]["state"] = "dead" if dead else "idle"
                current["picker"][0].update(state="dead" if dead else "idle", killed=mode == "kill", hidden=mode == "kill")
                if mode == "kill":
                    current["stored"]["hidden"] = [{"uuid": "thread", "hidden_at": 1, "at_payload": None}]
                if dead:
                    current["terminal"] = "n/a:ended-tmux-pane"
                    current["process_births"] = []
                    current["lifecycle"] = {"ended": {"births": [{"prior": {"pid": 42, "start": "old"}, "current": "n/a:process-absent"}]}}
                elif mode == "resume":
                    current["process_births"][0].update(pid=43, start="new")
                if not dead:
                    self.assertEqual(mode == "resume", history.recovery_birth(before, after, chat))
                rows = projection.checks(before, after, chat, mode)
                values = {row["field"]: row["actual"] for row in rows}
                self.assertEqual("dead" if dead else "idle", values["status"])
                self.assertEqual([mode == "kill"], values["picker-killed"])
                self.assertEqual([], [row for row in rows if row["expected"] != row["actual"]])
                if mode == "resume":
                    current["status"]["model"] = "different-model"
                    self.assertEqual(["resumed-model"], [row["field"] for row in projection.checks(before, after, chat, mode) if row["expected"] != row["actual"]])
                    current["status"]["model"] = "gpt-5"
                current["status"]["state"] = "working"
                failures = [row["field"] for row in projection.checks(before, after, chat, mode) if row["expected"] != row["actual"]]
                self.assertEqual(["status", "process-births"] if mode == "refused" else ["status"], failures)
        clear_before = copy.deepcopy(before)
        clear_before["chats"][0]["status"]["engine"] = "cc"
        clear_before["chats"][0]["picker"][0]["engine"] = "cc"
        clear_after = copy.deepcopy(clear_before)
        cleared = clear_after["chats"][0]
        cleared["status"]["session_id"] = "continued-session"
        cleared["stored"]["hidden"] = [{"uuid": "thread"}]
        cleared["rows"] = [{"kind": "claude"}]
        clear_chat = {**chat, "engine": "claude"}
        self.assertEqual([], [row for row in projection.checks(clear_before, clear_after, clear_chat, "clear") if row["expected"] != row["actual"]])
        cleared["status"]["session_id"] = "thread"
        cleared["rows"].append({"kind": "agent"})
        self.assertEqual(["continued-session", "detached-agents"], [row["field"] for row in projection.checks(clear_before, clear_after, clear_chat, "clear") if row["expected"] != row["actual"]])
        native = copy.deepcopy(before["chats"][0])
        native["picker"][0]["name"] = "desk"
        native["terminal"].update(backend="herdr", workspace={"workspace": {"label": "atlas"}}, tab={"tab": {"label": "DESK"}})
        native["stored"]["recipe"] = recipe
        restored = copy.deepcopy(native)
        restored["process_births"][0].update(pid=43, start="new")
        previous = {"errors": [], "chats": [native], "foreign_full": native}
        following = {"errors": [], "chats": [restored], "foreign_full": restored}
        rows = restart_restore.checks(previous, following, {43: {"start": "new"}})
        self.assertEqual([], [row for row in rows if row["expected"] != row["actual"]])
        rows = restart_restore.checks(previous, following, {42: {"start": "old"}, 43: {"start": "new"}})
        self.assertEqual(["desk:previous-births-still-running"], [row["field"] for row in rows if row["expected"] != row["actual"]])

    def test_mcp_result_uses_structured_receipt_and_rejects_ambiguous_text(self):
        wanted = {"code": 0, "status": "delivered", "typed": True}
        for receipt in ({"structuredContent": wanted}, {"content": [{"type": "text", "text": json.dumps(wanted)}]}):
            self.assertEqual(wanted, mcp.result(receipt))
        for content in ([], [{"type": "text", "text": "[]"}], [{"type": "text", "text": "{}"}] * 2):
            with self.subTest(content=content), self.assertRaisesRegex(ValueError, "READER-MCP-(CONTENT|SHAPE)"):
                mcp.result({"content": content})
        with tempfile.TemporaryDirectory() as temporary:
            receipt = Path(temporary) / "mcp-call.json"
            receipt.write_text(json.dumps({"structuredContent": {"code": 3}}) + "\n" + json.dumps({"structuredContent": wanted}) + "\n")
            self.assertEqual(wanted, mcp.record(receipt))

    def test_archive_checks_original_bytes_and_isolates_approved_prelude(self):
        chat = {"key": "selected", "session_id": "selected-session", "name": "selected", "engine": "claude"}
        observed = {"name": "selected", "status": "n/a:archived-chat", "stored": {"chat": []}, "picker": "n/a:archived", "terminal": "n/a:ended-tmux-pane", "lifecycle": {"ended": {"archive": {"row": {"uuid": "selected-session"}, "sha256": "original-bytes", "original": "absent"}}}}
        before = {"foreign": {"session": "foreign", "state": "idle"}, "chats": []}
        after = {**before, "chats": [observed]}
        with patch.object(archive, "read_json", return_value={"chats": [chat]}), patch.dict(os.environ, {"JOURNEY_CARRY": "owned-carry"}):
            rows = archive.checks(before, after, chat, {"sha256": "original-bytes"})
            self.assertEqual(["selected-session", "original-bytes", "absent", before["foreign"]], [row["actual"] for row in rows])
            self.assertEqual([], [row for row in rows if row["expected"] != row["actual"]])
            unowned = {"chats": [{"stored": {"hidden": [{"uuid": "foreign-session"}]}}]}
            with self.assertRaisesRegex(ValueError, "ARCHIVE-POLICY-UNOWNED"), patch.object(archive, "type_command") as send:
                archive.prepare(chat, {}, unowned)
            self.assertEqual([], send.call_args_list)

    def test_ssh_is_loopback_and_uses_only_run_keys(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            key = root / "key"
            known = root / "known-hosts"
            key.write_text("synthetic-key")
            known.write_text("synthetic-host")
            config = {"host": "127.0.0.1", "port": 2222, "user": "journey", "identity": str(key), "known_hosts": str(known)}
            argv = ssh_session.arguments(root, config, "carried-session")
            self.assertEqual(["ssh", "-tt", "-p", "2222", "-i", str(key), "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + str(known), "journey@127.0.0.1"], argv[:-1])
            self.assertEqual(". " + str(root / "env.sh") + "; exec env -u HERDR_PANE_ID -u TMUX -u TMUX_PANE pfm chat open carried-session", argv[-1])
            with self.assertRaisesRegex(ValueError, "ENDING-SSH-ENDPOINT"):
                ssh_session.arguments(root, {**config, "host": "example.invalid"}, "carried-session")
            with self.assertRaises(ValueError):
                ssh_session.arguments(root, {**config, "identity": str(root.parent / "outside-key")}, "carried-session")

    def test_service_control_keeps_the_run_namespace_on_both_operating_systems(self):
        with tempfile.TemporaryDirectory() as home, patch.dict(os.environ, {"PFM_UNIT_PREFIX": "journey-synthetic", "HOME": home}):
            with patch.object(sys, "platform", "linux"):
                self.assertEqual("journey-synthetic-mcp.service", control.unit_name("mcp"))
                self.assertEqual("journey-synthetic-schedule.timer", control.unit_name("schedule"))
                for state, loaded in (("loaded", True), ("not-found", False)):
                    with patch.object(control, "report", return_value=state + "\n"):
                        self.assertEqual({"name": "journey-synthetic-theme.service", "loaded": loaded, "state": state}, control.unit_loaded("theme"))
                with patch.object(control, "report", return_value=""), self.assertRaisesRegex(ValueError, "RESTART-UNIT-UNREADABLE"):
                    control.unit_loaded("theme")
            with patch.object(sys, "platform", "darwin"), patch.object(os, "getuid", return_value=1000):
                self.assertEqual("com.professor.journey-synthetic.mcp", control.unit_name("mcp"))
                self.assertEqual("com.professor.journey-synthetic.schedule", control.unit_name("schedule"))
                for code, text, loaded, state in ((113, "", False, "absent"), (0, "state = running\n", True, "running"), (0, "state = not running\n", True, "not running")):
                    def report(*args, **kwargs):
                        kwargs["capture"]["status"] = code
                        return text
                    with patch.object(control, "report", side_effect=report):
                        self.assertEqual({"name": "com.professor.journey-synthetic.theme", "loaded": loaded, "state": state}, control.unit_loaded("theme"))
                code, text = 0, ""
                with patch.object(control, "report", side_effect=report), self.assertRaisesRegex(ValueError, "RESTART-UNIT-UNREADABLE"):
                    control.unit_loaded("theme")
            with patch.dict(os.environ, {"PFM_UNIT_PREFIX": ""}), self.assertRaisesRegex(ValueError, "RESTART-UNIT-PREFIX"):
                control.unit_name("mcp")

    def test_death_proof_retains_exact_observed_engine_birth(self):
        birth = {"pid": 42, "start": "Tue Nov 3 01:00:00 2026", "chain": [{"argv": "/fixture/mock-engine"}], "environment": {"PFM_RUN": "synthetic"}}
        shell = {**birth, "pid": 41, "chain": [{"argv": "/bin/zsh"}]}
        chat = {"name": "atlas-desk", "key": "owned-chat", "session_id": "owned-session", "engine": "claude", "terminal": {"backend": "herdr"}}
        observed = {"name": "atlas-desk", "status": {"session_id": "owned-session"}, "stored": {"chat": []}, "picker": [], "terminal": {"births": [shell], "identity": {"pane": "owned-pane"}}, "process_births": [shell, birth]}
        with tempfile.TemporaryDirectory() as temporary, patch.dict(os.environ, {"JOURNEY_CARRY": str(Path(temporary) / "carry.json")}):
            carried = {"chats": [chat], "foreign": {"key": "foreign", "session_id": "foreign-session"}}
            Path(os.environ["JOURNEY_CARRY"]).write_text(json.dumps(carried))
            lifecycle.expect(chat, "ended", {"chats": [observed], "foreign_full": {"name": "foreign"}})
            actual = json.loads(Path(os.environ["JOURNEY_CARRY"]).read_text())["chats"][0]
            self.assertEqual([{"pid": 42, "start": "Tue Nov 3 01:00:00 2026"}], actual["expected_prior_births"])
            self.assertEqual({"lifecycle": "ended", "terminal": "shell", "visibility": "visible"}, actual["expect"])
            with self.assertRaisesRegex(ValueError, "CHAT-ENGINE-BIRTH-MISSING"):
                lifecycle.expect(chat, "ended", {"chats": [{**observed, "process_births": [shell]}], "foreign_full": {"name": "foreign"}})

    def test_crumb_audit_requires_exact_report(self):
        for text, expected in (
            ("doctor: crumbs entries=3 invalid=0\n", {"invalid": 0}),
            ("doctor: crumbs entries=3 invalid=1\n", {"invalid": 1}),
        ):
            with self.subTest(text=text):
                self.assertEqual(expected, teardown.crumbs(text))
        for text in ("", "doctor: warning crumb_dir=unreadable\n", "doctor: crumbs entries=3 invalid=0\ndoctor: crumbs entries=4 invalid=0\n"):
            with self.subTest(text=text), self.assertRaisesRegex(ValueError, "DOCTOR-CRUMBS"):
                teardown.crumbs(text)

    def test_foreign_bytes_and_directory_entries_are_independent(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            (root / "owner.json").write_text("{}\n")
            (root / "owner.json").chmod(0o640)
            (root / "owned-link").symlink_to("owner.json")
            self.assertEqual(["owned-link", "owner.json"], teardown.entries(root))
            baseline = {"version": 1, "files": {"owner.json": {}, "owned-link": {}}, "projects": {}}
            actual = teardown.foreign(root, baseline)
            self.assertEqual({"kind": "file", "mode": 416, "sha256": "ca3d163bab055381827226140568f3bef7eaac187cebd76878e0b63e9e442356", "base64": "e30K"}, actual["files"]["owner.json"])
            self.assertEqual({"kind": "symlink", "mode": 511, "target": "owner.json"}, actual["files"]["owned-link"])
            self.assertEqual({}, actual["projects"])
            original = {"root": "/original", "files": {"link": {"kind": "symlink", "mode": 511, "target": "/original/fixture/file"}, "file": actual["files"]["owner.json"]}, "projects": {}}
            relocated = teardown.relocated_baseline(original, Path("/restored"))
            self.assertEqual("/restored/fixture/file", relocated["files"]["link"]["target"])
            self.assertEqual(actual["files"]["owner.json"], relocated["files"]["file"])
            original["files"]["file"] = {**original["files"]["file"], "sha256": "corrupt"}
            with self.assertRaisesRegex(ValueError, "UNINSTALL-BASELINE-HASH"):
                teardown.relocated_baseline(original, Path("/restored"))
            self.assertEqual({"type": "object", "sha256": "ca3d163bab055381827226140568f3bef7eaac187cebd76878e0b63e9e442356"}, teardown.settings(root / "owner.json"))
            (root / "owner.json").write_text("{,")
            with self.assertRaises(ValueError):
                teardown.settings(root / "owner.json")
            with self.assertRaisesRegex(ValueError, "UNINSTALL-FOREIGN-MISSING"):
                teardown.foreign(root, {**baseline, "files": {"absent.json": {}}})

    def test_install_transition_uses_typed_receipt_and_observed_death(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            prior = {"pid": 42, "start": "prior"}
            births = [{"prior": prior, "current": "n/a:process-absent"}]
            before = {"foreign_full": {"status": "n/a:uninstalled", "rows": "n/a:uninstalled",
                      "picker": "n/a:uninstalled", "terminal": "n/a:ended"}}
            after = {"foreign_full": {"status": {"session_id": "foreign-session", "state": "dead"},
                     "lifecycle": {"ended": {"births": births}}}}
            for name, value in (("before", before), ("after", after),
                                ("install.dispatch.json", {"argv": ["pfm", "install", "--yes"]})):
                (root / name).write_text(json.dumps(value))
            (root / "install.status").write_text("0\n")
            (root / "install.out").write_text("actual installation receipt\n")
            with patch.object(teardown, "adopt_transition", return_value={"kind": "install"}) as adopt:
                self.assertEqual({"kind": "install"}, teardown.adopt_lifecycle("install", root / "before", root / "after", str(root / "install")))
                receipt = json.loads((root / "install-transition-receipt.json").read_text())
                self.assertEqual({"kind": "install", "session_id": "foreign-session", "ended": True,
                    "ended_proof": {"session_id": "foreign-session", "state": "dead", "births": births},
                    "inapplicable": {"status": "uninstalled", "rows": "uninstalled", "picker": "uninstalled", "terminal": "ended"},
                    "argv": ["pfm", "install", "--yes"], "status": "0\n", "out": "actual installation receipt\n"}, receipt)
                adopt.assert_called_once_with("install", root / "before", root / "after",
                    str(root / "install-transition-receipt.json"), evidence_path=str(root / "install-foreign-transition.json"))


if __name__ == "__main__":
    unittest.main()
