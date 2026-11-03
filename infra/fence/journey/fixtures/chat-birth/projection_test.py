#!/usr/bin/env python3
"""Exercise birth evidence selection and preservation with synthetic bundles."""
import copy
from pathlib import Path
import sys
import unittest

class BirthProjection(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
        import projection
        cls.projection = projection

    def fixture(self):
        carried = {"key": "chat-birth:linux+tmux+project+claude:new-typed",
                   "session_id": "own-session", "seat_id": "own-seat", "engine": "claude"}
        observed = {"name": "same-name", "rows": [{"id": "own-session"}],
                    "status": {"state": "idle"}, "terminal": {
                        "backend": "tmux", "identity": {"socket": "own.sock", "pane": "%1"},
                        "pane": {"window_name": "same-name"}, "shield": "n/a:tmux",
                        "text": "────────────────────────────────────────\n❯ \n────────────────────────────────────────\n",
                        "births": [{"pid": 42, "start": "fixture-start"}]}}
        foreign = copy.deepcopy(observed)
        foreign["rows"] = [{"id": "foreign-session"}]
        foreign["terminal"]["identity"] = {"socket": "foreign.sock", "pane": "%2"}
        return carried, {"chats": [foreign, observed]}

    def test_selects_carried_identity_and_refuses_missing_or_ambiguous(self):
        carried, bundle = self.fixture()
        self.assertEqual({"socket": "own.sock", "pane": "%1"},
                         self.projection.chat(bundle, carried)["terminal"]["identity"])
        for rows, count in ((bundle["chats"][:1], 0), (bundle["chats"] * 2, 2)):
            with self.subTest(count=count), self.assertRaisesRegex(ValueError, f"matched {count} observations$"):
                self.projection.chat({"chats": rows}, carried)

    def test_composer_preserves_draft_and_wrong_shape(self):
        carried, bundle = self.fixture()
        observed = self.projection.chat(bundle, carried)
        cases = [("claude", "────────────────────────────────────────\n❯ \n────────────────────────────────────────\n",
                  "────────────────────────────────────────\n❯\n────────────────────────────────────────\n"),
                 ("claude", "❯ unsent draft\n", "❯ unsent draft\n"),
                 ("claude", " ❯ 1. Yes, I trust this folder\n   2. No, exit\n", "❯ 1. Yes, I trust this folder\n"),
                 ("codex", "› Ask Codex to do anything\n", "› Ask Codex to do anything\n"),
                 ("opencode", "FIXTURE-OPENCODE-PROMPT\n", "FIXTURE-OPENCODE-PROMPT\n"),
                 ("claude", "shell%\n", "\n")]
        for engine, screen, expected in cases:
            with self.subTest(engine=engine, screen=screen):
                observed["terminal"]["text"] = screen
                self.assertEqual(expected, self.projection.composer(observed, engine))

    def test_preservation_keeps_birth_terminal_screen_and_state(self):
        _, bundle = self.fixture()
        expected = [{"ids": ["own-session"], "identity": {"socket": "own.sock", "pane": "%1"},
                     "label": "same-name", "state": "idle",
                     "text": "────────────────────────────────────────\n❯ \n────────────────────────────────────────\n",
                     "births": {"terminal": {"socket": "own.sock", "pane": "%1"},
                                "processes": [(42, "fixture-start")]}}]
        self.assertEqual(expected, self.projection.preserved(bundle, ["own-session"]))
        with self.assertRaisesRegex(ValueError, "BIRTH-PRESERVATION-UNREADABLE: missing$"):
            self.projection.preserved(bundle, ["missing"])
        for field, value in (("text", "foreign output"), ("identity", {"socket": "recycled.sock", "pane": "%1"})):
            changed = copy.deepcopy(bundle)
            changed["chats"][1]["terminal"][field] = value
            self.assertNotEqual(expected, self.projection.preserved(changed, ["own-session"]))
        observed = bundle["chats"][1]
        observed["process_births"] = [
            {"pid": 43, "start": "engine-start", "chain": [{"argv": "/fixture/claude --model sonnet"}],
             "environment": {"CLAUDE_CONFIG_DIR": "/fixture/account"}},
            {"pid": 42, "start": "shell-start", "chain": [{"argv": "zsh -l"}], "environment": {}}]
        self.assertEqual([{"pid": 43, "start": "engine-start", "argv": ["/fixture/claude", "--model", "sonnet"],
                           "environment": {"CLAUDE_CONFIG_DIR": "/fixture/account"},
                           "terminal": {"socket": "own.sock", "pane": "%1"}}],
                         self.projection.engine_births(observed, "claude"))

    def test_foreign_workspace_keeps_caller_shell_and_requires_new_tab(self):
        identity = {"socket": "owned.sock", "workspace": "w2", "tab": "w2:t4", "pane": "w2:p4"}
        workspace = {"workspace_id": "w2", "label": "beacon", "active_tab_id": "w2:t1", "tab_count": 4, "pane_count": 4}
        before = {"identity": identity, "workspace": {"workspace": workspace}}
        expected = {"new_tab": True, "caller_identity": identity, "prompt": "journey%", "workspace": workspace}
        for tab, prompt, count, fresh in (("w1:t9", "journey%", 4, True), ("w2:t4", "journey%", 4, False),
                                          ("w1:t9", "❯", 4, True), ("w1:t9", "journey%", 3, True)):
            with self.subTest(tab=tab, prompt=prompt, count=count):
                caller = copy.deepcopy(before)
                caller["text"] = "command\n" + prompt + "\n"
                caller["workspace"]["workspace"]["tab_count"] = count
                actual = {"new_tab": fresh, "caller_identity": identity, "prompt": prompt,
                          "workspace": {**workspace, "tab_count": count}}
                self.assertEqual({"expected": expected, "actual": actual}, self.projection.foreign_workspace(
                    {"terminal": {"identity": {"tab": tab}}}, before, caller))

    def test_labels_keep_unnamed_launch_and_typed_labels_separate_from_named_chats(self):
        cases = [
            ("herdr", "claude", "", "new-from-picker", "5", "New Claude chat", "New Claude chat", "picker-launch-label"),
            ("herdr", "codex", "", "new-from-picker", "5", "New Codex chat", "New Codex chat", "picker-launch-label"),
            ("herdr", "opencode", "", "new-from-picker", "5", "New OpenCode chat", "New OpenCode chat", "picker-launch-label"),
            ("herdr", "claude", "", "new-from-picker", "5", "wrong-launch-label", "New Claude chat", "picker-launch-label"),
            ("herdr", "claude", "", "new-typed", "5", "atlas", "atlas", "typed-default-number-to-cwd-leaf"),
            ("herdr", "claude", "", "new-typed", "5", "5", "atlas", "typed-default-number-to-cwd-leaf"),
            ("herdr", "claude", "", "new-typed", "5", "", "atlas", "typed-default-number-to-cwd-leaf"),
            ("herdr", "claude", "", "new-typed", "5", "Owner Atlas", "Owner Atlas", "typed-owner-label"),
            ("herdr", "claude", "", "new-typed", "5", "atlas", "Owner Atlas", "typed-owner-label"),
            ("herdr", "claude", "", "new-typed", "Fresh Atlas", "atlas", "atlas", "typed-fresh-label-to-cwd-leaf"),
            ("herdr", "claude", "", "new-typed", "Fresh Atlas", "Fresh Atlas", "atlas", "typed-fresh-label-to-cwd-leaf"),
            ("herdr", "claude", "", "open-from-foreign-workspace", "5", "atlas", "atlas", "typed-foreign-workspace-new-tab"),
            ("tmux", "claude", "", "new-from-picker", "prior-shell", "chat", "chat", "unnamed-inner-window"),
            ("tmux", "claude", "", "new-typed", "prior-shell", "chat", "chat", "unnamed-inner-window"),
            ("herdr", "claude", "atlas-review", "rename-from-cli", "5", "atlas-review", "atlas-review", "named-label"),
            ("tmux", "claude", "atlas-review", "rename-from-cli", "prior-shell", "atlas-review", "atlas-review", "named-label"),
        ]
        for backend, engine, name, entry, previous, actual, expected, rule in cases:
            with self.subTest(backend=backend, engine=engine, name=name, entry=entry, actual=actual):
                before = {"backend": backend, "tab": {"tab": {"label": previous, "number": 5}},
                          "pane": {"window_name": previous}}
                after = {"backend": backend, "tab": {"tab": {"label": actual}},
                         "pane": {"window_name": actual}}
                wanted = {"rule": rule, "expected": expected, "actual": actual}
                caller = after if backend == "herdr" and entry != "open-from-foreign-workspace" else before
                if not name and entry != "new-from-picker":
                    wanted["caller"] = {"expected": expected if caller is after else previous,
                                        "actual": actual if caller is after else previous}
                origin = "new-typed-owner-label" if rule == "typed-owner-label" else entry
                self.assertEqual(wanted, self.projection.labels(
                    {"terminal": after}, {"name": "wrong-captured-name", "engine": engine, "origin": origin},
                    entry, before, caller, "/fixture/work/atlas", name))
        self.assertEqual({"rule": "unnamed-inner-window", "expected": "chat", "actual": "chat", "caller": {
            "expected": "n/a:outside-terminal", "actual": "n/a:outside-terminal"}}, self.projection.labels(
                {"terminal": {"backend": "tmux", "pane": {"window_name": "chat"}}},
                {"name": "", "engine": "claude"}, "new-typed", {"backend": "pty"}, {"backend": "pty"}, "/fixture/work/atlas", ""))


if __name__ == "__main__":
    unittest.main()
