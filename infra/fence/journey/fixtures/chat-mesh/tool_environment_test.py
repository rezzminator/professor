#!/usr/bin/env python3
"""Environment proofs require a real marker and retain an unmarked user's colour."""
import sys
from pathlib import Path
import unittest

sys.path.insert(0, str(Path(__file__).parent))
from tool_environment import CODEX_NAMES, HERDR_NAMES, checks


class ToolEnvironment(unittest.TestCase):
    def test_marked_and_user_environments_are_distinct(self):
        for engine in ("codex", "claude"):
            with self.subTest(engine=engine):
                caller = {"engine": engine, "session_id": "parent-session",
                          "terminal": {"backend": "herdr", "pane": "w1:p7"}}
                parent = {"HERDR_PANE_ID": "w1:p7", "NO_COLOR": "journey-user-colour", "PAGER": "less"}
                child = {"NO_COLOR": "journey-user-colour", "PAGER": "less", "TMUX_PANE": "%3"}
                if engine == "codex":
                    parent.update({name: "native-tool" for name in CODEX_NAMES})
                    parent.update(NO_COLOR="1", CODEX_SESSION_ID="parent-session", PAGER="cat")
                    child = {"TMUX_PANE": "%3"}
                birth = {"environment": child, "observed_environment_keys": list(CODEX_NAMES + HERDR_NAMES)}
                carried = {"terminal": {"pane": "%3"}}
                self.assertEqual([], [row for row in checks(caller, {"environment": parent}, birth, carried)
                                      if row["actual"] != row["expected"]])
                broken = dict(child)
                if engine == "codex":
                    broken["NO_COLOR"] = "1"
                    expected = {"field": "child-codex-environment", "expected": [], "actual": ["NO_COLOR"]}
                else:
                    del broken["NO_COLOR"]
                    expected = {"field": "user-colour-preserved", "expected": "journey-user-colour", "actual": None}
                actual = checks(caller, {"environment": parent}, {**birth, "environment": broken}, carried)
                self.assertEqual([expected], [row for row in actual if row["actual"] != row["expected"]])


if __name__ == "__main__":
    unittest.main()
