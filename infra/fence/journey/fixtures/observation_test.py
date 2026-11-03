#!/usr/bin/env python3
"""Observation contracts over real SQLite, tmux and a private native Herdr."""
import hashlib
import json, os, sqlite3, subprocess, sys, tempfile, time, unittest
from pathlib import Path
from unittest.mock import patch

import journey_path
import observation
import observation_process
import observation_picker
import terminal_state
from runtime import command, read_json, write_json


class Observation(unittest.TestCase):
    def test_reporter_activity_projects_product_owner_children_and_sequence(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            session = "carried-session"
            digest = hashlib.sha256(session.encode()).hexdigest()
            path = root / ".local/state/pfm/herdr" / ("activity-" + digest + ".json")
            write_json(path, {
                "stored": {"session_id": session, "state": "blocked",
                            "tool": "agent child-a Write 0123456789abcdef",
                            "agents": {"child-a": True, "child-b": False, "child-c": True}, "seq": 19, "main_stopped_at": 123456789}})
            chat = {"engine": "claude", "session_id": session, "terminal": {"backend": "herdr"}}
            with patch.dict(os.environ, {"HOME": scratch}):
                self.assertEqual({"session_id": session, "state": "blocked",
                                  "blocked_owner": {"tool": "Write", "input_fingerprint": "0123456789abcdef", "agent_id": "child-a"},
                                  "active_child_ids": ["child-a", "child-c"], "active_child_count": 2,
                                  "event_sequence": 19, "main_stopped_at": 123456789}, observation.reporter_activity(chat))
                for stopped, accepted in ((0, True), (True, False), (-1, False), ("123", False)):
                    payload = read_json(path)
                    payload["stored"]["main_stopped_at"] = stopped
                    write_json(path, payload)
                    if accepted:
                        self.assertEqual(0, observation.reporter_activity(chat)["main_stopped_at"])
                    else:
                        with self.assertRaisesRegex(ValueError, "OBSERVE-REPORTER-MAIN-STOPPED-AT"):
                            observation.reporter_activity(chat)


    def test_account_stats_cache_reads_owned_current_and_prior_identity(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            account_one = root / ".cc/1/stats-cache.json"
            account_two = root / ".cc/2/stats-cache.json"
            write_json(account_one, {"version": 5, "lastComputedDate": "fixture", "dailyActivity": [], "modelUsage": {}})
            shared = root / "legacy/stats-cache.json"
            write_json(shared, {"version": 5, "dailyActivity": [{"date": "fixture"}], "modelUsage": {"model": {}}})
            account_two.parent.mkdir(parents=True, exist_ok=True)
            account_two.symlink_to(shared)
            chat = {"engine": "claude", "session_id": "carried-session", "account": 2, "prior_account": 1}
            with patch.dict(os.environ, {"HOME": scratch}):
                actual = observation.account_stats_cache(chat)
            self.assertEqual("regular-file", actual["1"]["state"])
            self.assertEqual("symlink", actual["2"]["state"])
            self.assertEqual(account_one.stat().st_ino, actual["1"]["identity"]["inode"])
            self.assertEqual(shared.stat().st_ino, actual["2"]["identity"]["inode"])
            self.assertEqual(1, actual["2"]["parsed"]["daily_activity_count"])
            self.assertEqual({"version": 5, "dailyActivity": [{"date": "fixture"}], "modelUsage": {"model": {}}}, actual["2"]["document"])

    def test_actual_sidechains_read_engine_path_and_agent_id(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            transcript = root / "projects/atlas/main.jsonl"
            sidechain = transcript.with_suffix("") / "subagents/agent-native.jsonl"
            transcript.parent.mkdir(parents=True)
            transcript.write_text('{"type":"user"}\n')
            sidechain.parent.mkdir(parents=True)
            sidechain.write_text(
                '\n'.join(json.dumps(record) for record in (
                    {"type": "user", "isSidechain": True, "agentId": "agent-native"},
                    {"type": "assistant", "isSidechain": True, "agentId": "agent-native"},
                )) + '\n'
            )
            chat = {"engine": "claude", "session_id": "main-session"}
            with patch.object(observation, "report", return_value=json.dumps({"path": str(transcript)})):
                actual = observation.read_actual_sidechains(chat)
            self.assertEqual({"path": str(transcript), "subagents": [{"path": str(sidechain), "agent_id": "agent-native", "records": 2}]}, actual)
            sidechain.rename(sidechain.with_name("synthetic-id.jsonl"))
            with patch.object(observation, "report", return_value=json.dumps({"path": str(transcript)})), \
                 self.assertRaisesRegex(ValueError, "OBSERVE-SIDECHAIN-IDENTITY"):
                observation.read_actual_sidechains(chat)

    def test_actual_codex_children_read_native_rollouts_and_parent_binding(self):
        with tempfile.TemporaryDirectory() as scratch:
            home = Path(scratch) / "codex"
            day = home / "sessions/2026/01/01"
            day.mkdir(parents=True)
            parent = day / "rollout-2026-01-01T00-00-00-parent.jsonl"
            child = day / "rollout-2026-01-01T00-00-01-child.jsonl"
            foreign = day / "rollout-2026-01-01T00-00-02-foreign.jsonl"
            parent.write_text(json.dumps({"type": "session_meta", "payload": {"id": "parent", "thread_source": "user"}}) + "\n")
            child.write_text(json.dumps({"type": "session_meta", "payload": {
                "id": "child", "thread_source": "subagent", "parent_thread_id": "parent",
                "source": {"subagent": {"thread_spawn": {"parent_thread_id": "parent"}}}}}) + "\n")
            foreign.write_text(json.dumps({"type": "session_meta", "payload": {
                "id": "foreign", "thread_source": "subagent", "parent_thread_id": "other",
                "source": {"subagent": {"thread_spawn": {"parent_thread_id": "other"}}}}}) + "\n")
            chat = {"engine": "codex", "session_id": "parent"}
            with patch.object(observation, "report", return_value=json.dumps({"path": str(parent)})):
                self.assertEqual({"path": str(parent), "children": [{"id": "child", "path": str(child),
                                 "parent_thread_id": "parent"}]}, observation.read_actual_codex_children(chat))
            self.assertEqual("n/a:non-codex-children", observation.read_actual_codex_children({"engine": "claude", "session_id": "parent"}))
            child.unlink()
            with patch.object(observation, "report", return_value=json.dumps({"path": str(parent)})):
                self.assertEqual({"path": str(parent), "children": []}, observation.read_actual_codex_children(chat))
            with patch.object(observation, "report", return_value=json.dumps({"path": str(parent)})):
                self.assertEqual({"path": str(parent), "id": "parent"}, observation.read_actual_codex_session(chat))
            child.write_text("not json\n")
            with patch.object(observation, "report", return_value=json.dumps({"path": str(parent)})), \
                 self.assertRaisesRegex(ValueError, "OBSERVE-CODEX-CHILD-IDENTITY"):
                observation.read_actual_codex_children(chat)

    def test_picker_requires_rendered_project_groups(self):
        target = {"backend": "tmux", "pane": "%1"}
        display = " ◆ pfm   ● 3 Claude\n│╭─ cobalt ───\n│╭─ atlas ───\n│╭─ beacon ───\n│› ✦ New chat → home\n"
        with patch.object(terminal_state, "screen", return_value=display):
            self.assertEqual({"projects": ["atlas", "beacon", "cobalt"], "header": "pfm", "new_chat": "New chat", "pane": "%1", "backend": "tmux"}, terminal_state.picker(target))
        with patch.object(terminal_state, "screen", return_value="atlas beacon cobalt New chat"):
            self.assertEqual({"projects": [], "header": "missing", "new_chat": "missing", "pane": "%1", "backend": "tmux"}, terminal_state.picker(target))

    def wait_for(self, label, read, expected, bound=5):
        deadline = time.monotonic() + bound
        while True:
            actual = read()
            if actual == expected:
                return
            if time.monotonic() >= deadline:
                self.fail(f"{label}: expected={expected!r} actual={actual!r}")
            time.sleep(0.02)

    def test_report_clears_only_beat_attribution(self):
        with patch.dict(os.environ, {"PFM_BEAT": "terminal-open/open-picker", "PFM_BEAT_FILE": "/fixture/beat.json", "PFM_RUN": "synthetic"}):
            actual = observation.report(["python3", "-c", 'import json,os;print(json.dumps([os.environ["PFM_BEAT"],os.environ.get("PFM_BEAT_FILE","missing"),os.environ["PFM_RUN"]]))'])
        self.assertEqual('["", "missing", "synthetic"]\n', actual)

    def test_pane_text_cannot_come_from_a_foreign_pane(self):
        response = {"read": {"pane_id": "w1:p1", "text": "fixture-output"}}
        self.assertEqual("fixture-output", observation.pane_text(response, "w1:p1"))
        with self.assertRaisesRegex(ValueError, "OBSERVE-PANE-TEXT"):
            observation.pane_text(response, "w2:p1")

    def test_shield_holder_is_bound_to_this_socket_and_pane(self):
        pane = {"pane_id": "w1:p1", "workspace_id": "w1", "tab_id": "w1:t1"}
        processes = {"foreground_processes": [{"name": "herdr-shield"}]}
        holder = {"binding": {"pane": "w1:p1", "socket": "own.sock", "workspace": "w1", "tab": "w1:t1"},
                  "holder_live": True, "state": "running", "child": {"pid": 42}}
        foreign = {**holder, "binding": {**holder["binding"], "socket": "foreign.sock"}}
        listing = {"enumeration": "OK", "error": None, "holders": [foreign, holder]}
        with tempfile.TemporaryDirectory() as scratch, patch.dict(os.environ, {"HOME": scratch}):
            config = {"shell": "/fixture/pfm-herdr-shell", "args": ["-l"]}
            write_json(Path(scratch) / ".config/herdr-shield/config.json", config)
            with patch.object(observation, "report", return_value=json.dumps(listing)), patch.object(observation, "process_tree", return_value=["actual-birth"]) as tree:
                self.assertEqual({"holder": holder, "births": ["actual-birth"], "config": config}, observation.shield(pane, processes, "own.sock"))
                tree.assert_called_once_with([42], descendants=True)
            for changed, message in (([foreign], "OBSERVE-SHIELD-HOLDER"), ([holder, holder], "OBSERVE-SHIELD-HOLDER"),
                                     ([{**holder, "binding": {**holder["binding"], "tab": "w1:t2"}}], "OBSERVE-SHIELD-BINDING")):
                with self.subTest(holders=changed), patch.object(observation, "report", return_value=json.dumps({**listing, "holders": changed})):
                    with self.assertRaisesRegex(ValueError, message):
                        observation.shield(pane, processes, "own.sock")
        self.assertEqual("n/a:native-pane", observation.shield(pane, {"foreground_processes": [{"name": "zsh"}]}, "own.sock"))

    def test_terminal_entities_require_identity_label_and_focus(self):
        for kind in ("workspace", "tab"):
            valid = {kind + "_id": "fixture-id", "label": "fixture-label", "focused": False}
            self.assertEqual(valid, observation.entity(kind, {kind: valid}, "fixture-id"))
            for changed in ({kind + "_id": "foreign-id"}, {"label": None}, {"focused": None}, {"focused": "false"}):
                with self.subTest(kind=kind, changed=changed):
                    with self.assertRaisesRegex(ValueError, "OBSERVE-" + kind.upper() + "-IDENTITY"):
                        observation.entity(kind, {kind: {**valid, **changed}}, "fixture-id")

    def test_badge_is_read_from_the_rendered_row(self):
        cases = [("› ● carried-chat 🥇 ⚡ │       fixture-model", "", "idle"),
                 ("› ● carried-chat 🥇 ⚡ │ ●     fixture-model", "\n  working  ●      mid-turn", "working"),
                 ("› ◉ carried-chat 🥇 ⚡ │ ◉     fixture-model", "\n  working  ◉      mid-turn", "working"),
                 ("› ◉ carried-chat 🥇 ⚡ │ ◌◐◓   fixture-model", "\n  working  ◌◐◓    2 agents working", "idle"),
                 ("› ● [B] carried-chat 🥇 │       fixture-model", "", "idle")]
        for line, dossier, expected in cases:
            with self.subTest(line=line):
                self.assertEqual({"state": expected, "line": line}, observation.badge("carried-chat", line + dossier))
        with self.assertRaisesRegex(ValueError, "OBSERVE-PICKER-BADGE-ABSENT: carried-chat"):
            observation.badge("carried-chat", "New chat\n")

    def test_picker_badge_must_agree_with_observed_state(self):
        chat = {"name": "carried-chat", "session_id": "carried-session", "seat_id": "carried-seat", "terminal": {"backend": "herdr", "pane": "w1:p1"}}
        row = {"kind": "live-claude", "id": "carried-session", "project": "atlas", "cwd": "/fixture/atlas", "name": "carried-chat", "account": "1", "killed": "false", "socket": "fixture.sock"}
        storage = {"seat": [{"seat_id": "carried-seat", "backend": "herdr", "herdr_socket": "fixture.sock", "herdr_pane": "w1:p1"}], "hidden": []}
        display = {"identity": {"pane": "w1:p1"}}
        line = "› ● carried-chat 🥇 │       fixture-model"
        with patch.object(observation, "stored", return_value=storage), patch.object(observation, "terminal", return_value=display):
            with patch.object(observation, "report", return_value='{"engine":"cc","state":"working"}'):
                with self.assertRaisesRegex(ValueError, "OBSERVE-BADGE-MISMATCH: carried-chat badge=idle status=working"):
                    observation.chat_projection(chat, [row], line)
            with patch.object(observation, "report", return_value='{"engine":"cc","state":"idle"}'):
                actual = observation.chat_projection(chat, [row], line)
                with self.assertRaisesRegex(ValueError, "OBSERVE-BADGE-MISMATCH: carried-chat badge=working status=idle"):
                    observation.chat_projection(chat, [row], line + "\n  working  ●      mid-turn")
            with patch.object(observation, "report", return_value='{"engine":"cc","state":"dead"}'):
                with self.assertRaisesRegex(ValueError, "OBSERVE-LIVE-STATE"):
                    observation.chat_projection(chat, [row], line)
            with patch.object(observation, "report", return_value='{"engine":"cc","state":"blocked"}'):
                for rendered, expected in ((line, "idle"), (line + "\n  working  ●      mid-turn", "working")):
                    blocked = observation.chat_projection(chat, [row], rendered)
                    self.assertEqual("blocked", blocked["status"]["state"])
                    self.assertEqual({"state": expected, "line": line}, blocked["picker"][0]["badge"])
                    self.assertEqual("n/a:picker-has-no-blocked-state", blocked["picker_status_agreement"])
        self.assertEqual([{"engine": "cc", "project": "atlas", "name": "carried-chat", "account": 1, "kind": "live-claude", "state": "idle", "killed": False, "hidden": False,
                           "backend": "herdr", "address": {"socket": "fixture.sock", "pane": "w1:p1"}, "badge": {"state": "idle", "line": line}}], actual["picker"])

    def test_bundle_retains_terminal_evidence_when_a_report_is_unreadable(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            write_json(root / "carry.json", {"chats": [], "foreign": {"name": "foreign-sentinel", "session_id": "foreign-session"}})
            write_json(root / "target.json", {"backend": "tmux", "socket": "fixture.sock", "pane": "%0"})
            display = {"identity": {"pane": "%0"}, "text": "fixture output"}
            with patch.dict(os.environ, {"JOURNEY_CARRY": str(root / "carry.json")}), patch.object(observation, "terminal", return_value=display), patch.object(observation, "fleet_rows", side_effect=ValueError("fixture-refusal")):
                actual = observation.bundle(root / "target.json")
            self.assertEqual(display, actual["terminal"])
            self.assertEqual({"error": "picker.rows: ValueError: fixture-refusal"}, actual["picker"]["rows"])
            self.assertEqual(["picker.rows: ValueError: fixture-refusal", "foreign: ValueError: OBSERVE-PICKER-ROW-ABSENT: foreign-sentinel foreign-session"], actual["errors"])
            carried = [{"key": "typed", "name": "atlas-review", "session_id": "one"},
                       {"name": "atlas-review", "session_id": "two"}]
            write_json(root / "carry.json", {"chats": carried, "foreign": {"session_id": "foreign"},
                "picker": {"backend": "tmux", "pane": "%0", "socket": "fixture.sock"},
                "observer_picker": {"backend": "tmux", "pane": "%9", "socket": "fixture.sock"}})
            state = {"text": "action-render"}
            def display_read(_):
                return {"text": state["text"]}
            def navigate(target, *_):
                self.assertEqual({"backend": "tmux", "pane": "%9", "socket": "fixture.sock"}, target)
                state["text"] = "observer-navigation"
                return {identity: {"state": "idle", "line": "selected " + identity} for identity in ("one", "two", "foreign")}
            def projection(chat, *_):
                return {"stored": {"session": chat["session_id"]}}
            with patch.dict(os.environ, {"JOURNEY_CARRY": str(root / "carry.json")}), patch.object(observation, "terminal", side_effect=display_read), \
                 patch.object(observation, "fleet_rows", return_value=[]), patch.object(observation_picker, "badges", side_effect=navigate), \
                 patch.object(observation, "chat_projection", side_effect=projection), patch.object(observation, "foreign_projection", return_value={}):
                actual = observation.bundle(root / "target.json")
            self.assertEqual({"typed": {"session": "one"}, "two": {"session": "two"}}, actual["stored"])
            self.assertEqual({"text": "action-render"}, actual["terminal"])
            self.assertEqual([], actual["errors"])

    def test_bundle_keeps_independent_chat_evidence_when_picker_badges_fail(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            target = root / "target.json"
            carry = root / "carry.json"
            write_json(target, {"backend": "tmux", "socket": "fixture.sock", "pane": "%0"})
            chat = {"key": "typed", "name": "carried-chat", "session_id": "carried-session", "seat_id": "carried-seat",
                    "engine": "claude", "terminal": {"backend": "tmux", "socket": "fixture.sock", "pane": "%0"}}
            foreign = {"name": "foreign-sentinel", "session_id": "foreign-session", "seat_id": "foreign-seat",
                       "engine": "claude", "terminal": {"backend": "tmux", "socket": "fixture.sock", "pane": "%0"}}
            write_json(carry, {"chats": [chat], "foreign": foreign,
                               "picker": {"backend": "tmux", "socket": "fixture.sock", "pane": "%0"}})
            rows = [{"kind": "live-claude", "id": item["session_id"], "project": "atlas", "cwd": "/fixture/atlas",
                     "name": item["name"], "account": "1", "killed": "false", "socket": "fixture.sock"}
                    for item in (chat, foreign)]
            status = {"state": "idle", "engine": "cc", "socket": "fixture.sock"}
            display = {"identity": {"socket": "fixture.sock", "pane": "%0"}, "text": "picker frame", "births": []}

            def storage(item, *_):
                return {"seat": [{"seat_id": item["seat_id"], "state": "live", "backend": "tmux",
                                  "tmux_socket": "fixture.sock", "tmux_pane": "%0"}], "hidden": []}

            with patch.dict(os.environ, {"JOURNEY_CARRY": str(carry)}), \
                 patch.object(observation, "fleet_rows", return_value=rows), patch.object(observation, "terminal", return_value=display), \
                 patch.object(observation, "report", return_value=json.dumps(status)), patch.object(observation, "stored", side_effect=storage), \
                 patch.object(observation_picker, "badges", side_effect=ValueError("fleet stale: HERDR ERROR full detail")), \
                 patch.object(observation, "foreign_projection", return_value={}), \
                 patch.object(observation, "account_stats_cache", return_value={}), \
                 patch.object(observation, "reporter_activity", return_value={}), \
                 patch.object(observation, "indicator_daemon", return_value="n/a:indicator-daemon-absent"):
                actual = observation.bundle(target)
                with patch.object(observation_picker, "badges", side_effect=None, return_value={
                        item["session_id"]: {"state": "working", "line": "rendered working row"} for item in (chat, foreign)}):
                    mismatch = observation.bundle(target)

            self.assertEqual("idle", actual["chats"][0]["status"]["state"])
            self.assertEqual(storage(chat), actual["chats"][0]["stored"])
            self.assertEqual({"state": "n/a:picker-badges-unreadable", "line": "n/a:picker-badges-unreadable"},
                             actual["chats"][0]["picker"][0]["badge"])
            self.assertEqual(["picker.badges: ValueError: fleet stale: HERDR ERROR full detail"], actual["errors"])
            self.assertEqual(storage(chat), mismatch["chats"][0].get("stored"))
            self.assertEqual(status, mismatch["chats"][0]["status"])
            self.assertEqual([f"chat:{chat['session_id']}: ProjectionMismatch: OBSERVE-BADGE-MISMATCH: {chat['name']} badge=working status=idle",
                              f"foreign: ProjectionMismatch: OBSERVE-BADGE-MISMATCH: {foreign['name']} badge=working status=idle"], mismatch["errors"])

    def test_foreign_projection_keeps_identity_focus_and_own_birth(self):
        terminal = {"backend": "herdr", "workspace": {"workspace": {"label": "cobalt", "focused": False, "revision": 7}},
                    "tab": {"tab": {"label": "foreign-sentinel", "focused": False}}, "pane": {"focused": False},
                    "identity": {"socket": "fixture.sock", "workspace": "w2", "tab": "w2:t1", "pane": "w2:p1"}, "text": "FOREIGN-READY",
                    "births": [{"pid": 123, "start": "fixture-start", "chain": [{"pid": 123, "argv": "/fixture/claude"}, {"pid": 1, "argv": "/fixture/init"}]}]}
        row = {"kind": "live-claude", "id": "foreign-session", "project": "cobalt", "cwd": "/fixture/cobalt", "name": "foreign-sentinel", "account": "3", "killed": "false", "socket": "fixture.sock"}
        actual = observation.foreign_projection({"terminal": terminal, "rows": [row], "stored": {"seat": "fixture-seat"}, "status": {"state": "idle", "idle_seconds": 20}})
        self.assertEqual({"rows": [row], "stored": {"seat": "fixture-seat"}, "state": "idle", "terminal": {"identity": terminal["identity"],
                          "labels": {"workspace": "cobalt", "tab": "foreign-sentinel"}, "focus": {"workspace": False, "tab": False, "pane": False},
                          "text": "FOREIGN-READY", "births": [{"pid": 123, "start": "fixture-start", "argv": "/fixture/claude"}]}}, actual)

    def test_private_terminal_reports_identity_focus_text_and_birth(self):
        with tempfile.TemporaryDirectory(prefix="jo-") as scratch:
            root = Path(scratch)
            env = dict(os.environ, HOME=str(root), XDG_CONFIG_HOME=str(root / "config"),
                       XDG_DATA_HOME=str(root / "data"), XDG_STATE_HOME=str(root / "state"), XDG_CACHE_HOME=str(root / "cache"))
            socket_path = root / "config/herdr/sessions/observation/herdr.sock"
            env["HERDR_SOCKET_PATH"] = str(socket_path)
            config = root / "config/herdr"
            config.mkdir(parents=True)
            (config / "config.toml").write_text('onboarding = false\n[terminal]\ndefault_shell = "/bin/sh"\n')
            with (root / "server.log").open("w") as log:
                server = subprocess.Popen(["herdr", "--session", "observation", "server"], env=env, stdout=log, stderr=log)
                try:
                    self.wait_for("private Herdr socket " + str(socket_path), socket_path.is_socket, True, 10)
                    with patch.dict(os.environ, env):
                        created = json.loads(command(["herdr", "--session", "observation", "workspace", "create", "--label", "atlas", "--cwd", str(root)]))["result"]
                        pane = created["root_pane"]["pane_id"]
                        visible = lambda: observation.herdr.request(str(socket_path), "pane.read", {"pane_id": pane, "source": "visible"})["result"]["read"]["text"]
                        self.wait_for("fixture shell ready", visible, "#\n")
                        observation.herdr.request(str(socket_path), "pane.send_text", {"pane_id": pane, "text": "printf 'OBSERVATION-FIXTURE\\n'"})
                        observation.herdr.request(str(socket_path), "pane.send_keys", {"pane_id": pane, "keys": ["enter"]})
                        expected_screen = "# printf 'OBSERVATION-FIXTURE\\n'\nOBSERVATION-FIXTURE\n#\n"
                        self.wait_for("fixture shell output", visible, expected_screen)
                        actual = observation.terminal({"backend": "herdr", "pane": pane})
                        self.assertEqual("atlas", actual["workspace"]["workspace"]["label"])
                        self.assertEqual(pane, actual["pane"]["pane_id"])
                        self.assertIsInstance(actual["pane"]["focused"], bool)
                        self.assertEqual(expected_screen, actual["text"])
                        self.assertEqual({"pane": "unknown", "tab": "unknown", "workspace": "unknown"}, actual["agent_status"])
                        self.assertEqual({"pane": {}, "workspace": {}}, actual["indicators"])
                        observation.herdr.request(str(socket_path), "pane.report_agent", {"pane_id": pane, "source": "observation-fixture", "agent": "claude", "state": "working", "seq": 1})
                        observation.herdr.request(str(socket_path), "pane.report_metadata", {"pane_id": pane, "source": "observation-fixture", "tokens": {"pfm_working": "●"}})
                        active = observation.terminal({"backend": "herdr", "pane": pane})
                        self.assertEqual("working", active["agent_status"]["pane"])
                        self.assertEqual({"pfm_working": "●"}, active["indicators"]["pane"])
                        self.assertEqual({"socket": str(socket_path), "workspace": created["workspace"]["workspace_id"],
                                          "tab": created["tab"]["tab_id"], "pane": pane}, actual["identity"])
                        self.assertEqual("/bin/sh", actual["births"][0]["chain"][0]["argv"])
                        self.assertEqual(1, actual["births"][0]["chain"][-1]["pid"])
                        self.assertEqual([pane], [item["pane_id"] for item in terminal_state.panes(created["workspace"]["workspace_id"])])
                finally:
                    server.terminate()
                    try:
                        server.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        server.kill()
                        server.wait(timeout=5)

    def test_private_tmux_reports_the_owned_pane(self):
        with tempfile.TemporaryDirectory(prefix="jt-") as scratch:
            socket_path = str(Path(scratch) / "tmux.sock")
            arguments = ["tmux", "-S", socket_path]
            command(arguments + ["new-session", "-d", "-s", "owner", "-c", scratch, "/bin/sh"])
            try:
                actual = observation.terminal({"backend": "tmux", "socket": socket_path, "pane": "owner:0.0"})
                self.assertEqual({"socket": socket_path, "session": "owner", "window": "@0", "pane": "%0"}, actual["identity"])
                self.assertEqual("n/a:tmux", actual["workspace"])
                self.assertEqual("n/a:tmux", actual["tab"])
                self.assertEqual(scratch, actual["pane"]["pane_current_path"])
                self.assertEqual(True, actual["pane"]["focused"])
                self.assertEqual("/bin/sh", actual["births"][0]["chain"][0]["argv"])
                command(arguments + ["send-keys", "-t", "owner:0.0", "bash --login", "Enter"])
                target = {"backend": "tmux", "socket": socket_path, "pane": "owner:0.0"}
                self.wait_for("nested login bash birth", lambda: any(birth["chain"][0]["argv"] == "bash --login" for birth in observation.terminal(target)["births"]), True)
            finally:
                command(arguments + ["kill-server"])


if __name__ == "__main__":
    unittest.main()
