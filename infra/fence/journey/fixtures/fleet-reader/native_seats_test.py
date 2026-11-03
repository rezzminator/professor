#!/usr/bin/env python3
"""The hostless reader proves the native fault and restores by public reopen."""
from contextlib import contextmanager
import copy
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import native_seats as seats
from runtime import read_json, write_json


class NativeSeatTests(unittest.TestCase):
    def chat(self):
        return {"key": "owned", "session_id": "actual-session", "seat_id": "actual-seat", "engine": "claude",
                "name": "reader", "account": 1, "project": "atlas", "branch": "linux+herdr-shield+project+claude",
                "origin": "new-from-cli", "role": "native-stale", "terminal": {"backend": "herdr", "pane": "old-pane"}}

    def lost(self):
        chat = self.chat()
        current = {"lifecycle": {"hostless": {"supervisor": {"state": "dead"}, "stored_pane": "pane_not_found"}},
                   "status": {"state": "dead", "session_id": "actual-session"},
                   "stored": {"seat": [{"seat_id": "actual-seat", "state": "lost", "herdr_pane": "old-pane"}]},
                   "terminal": {"identity": {"pane": "actual-moved-pane"}}}
        header = "kind\tid\tproject\tcwd\tname\taccount\tkilled\tsocket\n"
        row = "resume-claude\tactual-session\tAtlas\t\treader\t1\tfalse\t\n"
        return chat, current, {"output": header + row, "exit_code": 0, "elapsed_ms": 10000}

    def test_hostless_list_requires_exact_fault_lost_row_and_bound(self):
        chat, good, receipt = self.lost()
        cases = [(None, None, None, []), ("lifecycle", {"hostless": {"supervisor": {"state": "alive"}, "stored_pane": "pane_not_found"}}, None, ["hostless-native-supervisor"]),
                 ("lifecycle", {"hostless": {"supervisor": {"state": "dead"}, "stored_pane": "present"}}, None, ["hostless-original-pane"]),
                 ("status", {"state": "idle", "session_id": "actual-session"}, None, ["hostless-public-state"]),
                 ("status", {"state": "dead", "session_id": "foreign-session"}, None, ["hostless-public-session"]),
                 ("stored", {"seat": [{"seat_id": "actual-seat", "state": "live", "herdr_pane": "old-pane"}]}, None, ["hostless-persisted-state"]),
                 ("stored", {"seat": [{"seat_id": "actual-seat", "state": "lost", "herdr_pane": "new-pane"}]}, None, ["hostless-persisted-old-pane"]),
                 ("terminal", {"identity": {"pane": "foreign-pane"}}, None, ["hostless-actual-moved-pane"]),
                 (None, None, {"exit_code": 1}, ["hostless-list-exit"]),
                 (None, None, {"elapsed_ms": 9999}, []), (None, None, {"elapsed_ms": 10001}, ["hostless-list-bound"]),
                 (None, None, {"output": "unexpected diagnostic\n" + receipt["output"]}, ["hostless-list-diagnostics"]),
                 (None, None, {"output": receipt["output"].replace("resume-claude", "live-claude")}, ["hostless-public-lost-row"]),
                 (None, None, {"output": receipt["output"].replace("Atlas\t\t", "Atlas\t/recycled/occupant\t")}, ["hostless-public-lost-row"])]
        with patch.object(seats, "carry", return_value={"chats": [chat]}):
            for field, value, override, expected in cases:
                with self.subTest(field=field, expected=expected):
                    current = copy.deepcopy(good)
                    if field:
                        current[field] = value
                    result = seats.fault_checks(chat, current, {**receipt, **(override or {})}, {"pane": "actual-moved-pane"})
                    self.assertEqual(expected, [row["field"] for row in result if row["expected"] != row["actual"]])

    def test_reopen_waits_for_own_idle_public_session_before_refresh(self):
        chat = self.chat()
        for state, sid, expected in (("dead", "actual-session", False), ("working", "actual-session", False),
                                     ("idle", "foreign", False), ("idle", "actual-session", chat)):
            with self.subTest(state=state, sid=sid), patch.object(seats, "report", return_value=json.dumps({"state": state, "session_id": sid})), \
                    patch.object(seats, "refresh", return_value=chat) as refreshed:
                self.assertEqual(expected, seats.live_again(chat))
                self.assertEqual(1 if expected else 0, refreshed.call_count)

    def test_restoration_requires_fresh_supervisor_same_session_recipe_and_placement(self):
        chat = self.chat()
        row = {"status": {"session_id": "actual-session", "state": "idle"}, "stored": {"recipe": [{"model": "owned"}]},
               "terminal": {"identity": {"tab": "original-tab", "workspace": "original-workspace"}, "pane": {"terminal_id": "actual-reopened-terminal"}}}
        before = {"chats": [copy.deepcopy(row)]}
        mover = SimpleNamespace(before={"identity": {"tab": "original-tab", "workspace": "original-workspace"}}, terminal_id="actual-reopened-terminal")
        host = {"pid": 51, "start": "old-host"}
        cases = [(None, None, None, []), ("status", {"session_id": "actual-session", "state": "dead"}, None, ["restored-seat-public-state"]),
                 ("stored", {"recipe": [{"model": "foreign"}]}, None, ["restored-seat-recipe"]),
                 ("terminal", {"identity": {"tab": "wrong", "workspace": "original-workspace"}, "pane": {"terminal_id": "actual-reopened-terminal"}}, None, ["restored-seat-tab"]),
                 ("terminal", {"identity": {"tab": "original-tab", "workspace": "wrong"}, "pane": {"terminal_id": "actual-reopened-terminal"}}, None, ["restored-seat-workspace"]),
                 ("terminal", {"identity": {"tab": "original-tab", "workspace": "original-workspace"}, "pane": {"terminal_id": "wrong"}}, None, ["restored-seat-physical-terminal"]),
                 (None, None, host, ["restored-seat-supervisor-birth"])]
        with patch.object(seats, "carry", return_value={"chats": [chat]}), patch.object(seats, "bundle_checks", return_value=[]), \
                patch.object(seats, "engine_birth", return_value={"pid": 72, "start": "new-engine"}):
            for field, value, birth, expected in cases:
                current = copy.deepcopy(row)
                if field:
                    current[field] = value
                with self.subTest(field=field), patch.object(seats, "supervisor_birth", return_value=birth or {"pid": 71, "start": "new-host"}):
                    result = seats.restored_checks(chat, before, chat, {"chats": [current]}, mover, host)
                    self.assertEqual(expected, [row["field"] for row in result if row["expected"] != row["actual"]])
            with patch.object(seats, "supervisor_birth", return_value={"pid": 71, "start": "new-host"}):
                for field in ("key", "engine", "name", "account", "project"):
                    with self.subTest(restored_field=field):
                        result = seats.restored_checks(chat, before, {**chat, field: "foreign"}, {"chats": [row]}, mover, host)
                        self.assertEqual(["restored-seat-session-fields"], [value["field"] for value in result if value["expected"] != value["actual"]])

    def test_full_observation_is_inside_fault_and_reopen_precedes_rebinding_after_failure(self):
        for fails, dead in ((False, True), (True, True), (True, False)):
            with self.subTest(fails=fails, dead=dead), tempfile.TemporaryDirectory() as folder:
                root, events = Path(folder), []
                chat = self.chat()
                owner = {"backend": "pty", "socket": "/owned/socket", "pane": "owner"}
                restored = {**chat, "terminal": {"backend": "herdr", "pane": "actual-restored"}}
                write_json(root / "chat", chat)
                write_json(root / "owner", owner)
                mover = SimpleNamespace(before={"identity": {"workspace": "original"}}, destination="other",
                    move=lambda target, workspace: events.append("move:" + workspace) or restored["terminal"],
                    rebind=lambda actor: events.append("rebind:" + actor["terminal"]["pane"]))
                @contextmanager
                def fault(target, host, move, restore, *, receipt):
                    events.append("fault-enter")
                    move(target)
                    try:
                        if not dead:
                            raise RuntimeError("move-refused-before-kill")
                        yield {"backend": "herdr", "pane": "actual-moved"}
                    finally:
                        restore()
                        events.append("fault-exit")
                @contextmanager
                def hostless(*args):
                    events.append("hostless-enter")
                    try:
                        yield
                    finally:
                        events.append("hostless-exit")
                def action(*args):
                    events.append("public-list")
                    if fails:
                        raise RuntimeError("owner-list-failed")
                    return {"exit_code": 0, "output": "actual public output"}
                def observe(*args):
                    events.append("full-bundle")
                    return {"chats": [{"status": {"session_id": "actual-session"}}]}
                with patch.object(seats.faults, "owned_target"), patch.object(seats.faults, "stopped_for_move", side_effect=fault), \
                        patch.object(seats.faults, "birth_exited", return_value=dead), patch.object(seats, "supervisor_birth", return_value={"pid": 51, "start": "actual"}), \
                        patch.object(seats, "temporary_hostless", side_effect=hostless), patch.object(seats, "action", side_effect=action), \
                        patch.object(seats, "bundle", side_effect=observe), patch.object(seats, "fault_checks", return_value=[]), \
                        patch.object(seats, "bundle_checks", return_value=[]), patch.object(seats, "restored_checks", return_value=[]), \
                        patch.object(seats, "carry", return_value={"chats": [chat]}), patch.object(seats, "live_again", return_value=restored), \
                        patch.object(seats, "terminal", return_value={"actual": "terminal"}), \
                        patch.object(seats, "reopen", side_effect=lambda *args: events.append("public-reopen")):
                    invoke = lambda: seats.run(root / "chat", root / "owner", {"restore_owner": owner}, root / "read", {"checks": [], "before": {}},
                                                lambda *args: mover, lambda *args: restored, lambda *args: None)
                    if not dead:
                        with self.assertRaisesRegex(RuntimeError, "^move-refused-before-kill$"):
                            invoke()
                    elif fails:
                        with self.assertRaisesRegex(RuntimeError, "^owner-list-failed$"):
                            invoke()
                    else:
                        invoke()
                if dead:
                    during = [] if fails else ["full-bundle"]
                    expected = ["fault-enter", "move:other", "hostless-enter", "public-list", *during,
                                "hostless-exit", "public-reopen", "rebind:actual-restored", "move:original", "fault-exit", "full-bundle"]
                else:
                    expected = ["fault-enter", "move:other", "move:original", "fault-exit", "full-bundle"]
                self.assertEqual(expected, events)
                self.assertEqual(restored, read_json(root / "chat"))
                self.assertEqual(restored["terminal"], read_json(str(root / "chat") + ".target"))


if __name__ == "__main__":
    unittest.main()
