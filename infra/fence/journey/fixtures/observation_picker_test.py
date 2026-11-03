#!/usr/bin/env python3
"""Rendered picker identities, discovery and selection restoration."""
import unittest

from journey_path import JOURNEY
import observation_picker


class PickerObservation(unittest.TestCase):
    def test_picker_walk_binds_duplicate_names_and_restores_selection(self):
        target = {"backend": "tmux", "socket": "fixture.sock", "pane": "%1"}
        chats = [{"session_id": "carried-session", "name": "atlas-review"},
                 {"session_id": "offscreen-session", "name": "atlas-review"},
                 {"session_id": "foreign-session", "name": "atlas-review"}]
        rows = [{"id": chat["session_id"]} for chat in chats]
        state = {"index": 1}
        seen = []
        reads = []
        lines = ["› ● atlas-review 🥇       fixture-model", "› ● atlas-review 🥈 ●     fixture-model",
                 "› ● atlas-review 🥉       fixture-model"]
        def read(_):
            index = state["index"]
            reads.append(index)
            if index == -1:
                return "◆ pfm fixture\nfind ›     4/4 visible\n› ✦ New chat →"
            return "◆ pfm fixture\nfind ›     3/3 visible\n│ ● atlas-review 🥈 ●\n" + lines[index] + "\n  session " + chats[index]["session_id"] + ("\n  working  ●      mid-turn" if index == 1 else "")
        def keys(_, values):
            seen.append(values)
            self.assertEqual(["down"], values)
            state["index"] = (state["index"] + 2) % 4 - 1 if state.get("new_chat") else (state["index"] + 1) % 3
        actual = observation_picker.badges(target, chats, rows, read, keys)
        self.assertEqual({"carried-session": {"state": "idle", "line": lines[0]},
                          "offscreen-session": {"state": "working", "line": lines[1]},
                          "foreign-session": {"state": "idle", "line": lines[2]}}, actual)
        self.assertEqual({"index": 1}, state)
        self.assertEqual([["down"], ["down"], ["down"]], seen)
        self.assertEqual([1, 2, 0, 1], reads)
        with self.assertRaisesRegex(ValueError, "OBSERVE-PICKER-NOT-OPEN: carry.picker absent"):
            observation_picker.badges(None, chats, rows, read, keys)
        with self.assertRaisesRegex(ValueError, "OBSERVE-PICKER-NOT-OPEN: existing live picker required"):
            observation_picker.badges(target, chats, rows, lambda _: "journey$", keys)
        with self.assertRaisesRegex(ValueError, "OBSERVE-PICKER-FILTERED"):
            observation_picker.badges(target, chats, rows, lambda _: read(target).replace("3/3 visible", "atlas 3/3 match"), keys)
        with self.assertRaisesRegex(ValueError, "OBSERVE-PICKER-CAROUSEL"):
            observation_picker.badges(target, chats, rows, lambda _: read(target) + "\n◖ ⚡ reboot ◗", keys)
        self.assertEqual([["down"], ["down"], ["down"]], seen)
        self.assertEqual(actual, observation_picker.badges(target, chats, rows, lambda _: read(target).replace("find ›     ", "find › type to find · @ to go   "), keys))
        self.assertEqual([["down"], ["down"], ["down"], ["down"], ["down"], ["down"]], seen)
        for shown, identities, expected in [
                ("  session exact-session", ["exact-session", "other"], "exact-session"),
                ("│ › ● session notes    fixture-model\n  ┃ session caution\n  session exact-session", ["exact-session", "other"], "exact-session"),
                ("  session …123456789abcdef", ["prefix-123456789abcdef", "other"], "prefix-123456789abcdef")]:
            with self.subTest(shown=shown):
                self.assertEqual(expected, observation_picker.session(shown, identities))
        for identities in (["prefix-123456789abcdef", "foreign-123456789abcdef"], ["unrelated"]):
            with self.subTest(identities=identities), self.assertRaisesRegex(ValueError, "OBSERVE-PICKER-SESSION"):
                observation_picker.session("  session …123456789abcdef", identities)
        with self.assertRaisesRegex(ValueError, "OBSERVE-PICKER-IDENTITY"):
            observation_picker.select_session(target, {"session_id": "absent-session"}, [row["id"] for row in rows], read, keys)
        self.assertEqual({"index": 1}, state)
        with self.assertRaisesRegex(ValueError, "OBSERVE-PICKER-IDENTITY"):
            observation_picker.badges(target, [*chats, {"session_id": "absent-session", "name": "atlas-review"}], rows, read, keys)
        self.assertEqual({"index": 1}, state)
        self.assertEqual({}, observation_picker.badges(None, [{"expect": {"lifecycle": "ended"}}], [], read, keys))
        with self.assertRaisesRegex(ValueError, "OBSERVE-PICKER-COUNT"):
            observation_picker.select_session(target, chats[0], [row["id"] for row in rows], lambda _: read(target).replace("3/3 visible", "count unavailable"), keys)
        state.update(index=-1, new_chat=True)
        self.assertEqual(actual, observation_picker.badges(target, chats, rows, read, keys))
        self.assertEqual({"index": -1, "new_chat": True}, state)
        self.assertEqual([["down"], ["down"], ["down"], ["down"]], seen[-4:])
        state.clear()
        state.update(index=1)
        refresh_keys = []
        def delayed_keys(_, values):
            refresh_keys.append(values)
            state["index"] = (state["index"] + 1) % (2 if len(refresh_keys) <= 2 else 3)
        try:
            discovered = observation_picker.badges(target, chats, rows, read, delayed_keys)
        except ValueError as error:
            self.fail(f"expected refreshed sessions, actual={error}")
        self.assertEqual(actual, discovered)
        self.assertEqual({"index": 1}, state)
        self.assertEqual([["down"]] * 5, refresh_keys)


if __name__ == "__main__":
    unittest.main()
