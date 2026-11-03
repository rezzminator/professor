#!/usr/bin/env python3
"""Linked project fixtures are real repositories inside the private fence."""
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import journey_path
import projects


class ProjectWorktrees(unittest.TestCase):
    def test_auxiliary_worktree_is_linked_and_confined(self):
        self.assertEqual("1", os.environ.get("PFM_DEV_FENCE"))
        with tempfile.TemporaryDirectory(prefix="journey-project-") as temporary:
            root = Path(temporary)
            home = root / "home"
            source = home / "work/atlas"
            with patch.dict(os.environ, {"HOME": str(home), "JOURNEY_ROOT": str(root)}):
                projects.repository(source)
                target = source / ".worktrees/journey-indicator"
                target.mkdir(parents=True)
                for _ in range(2):
                    self.assertEqual(str(target), projects.worktree("atlas", "indicator"))
                    self.assertEqual(str(source / ".git") + "\n", projects.git(target, "rev-parse", "--path-format=absolute", "--git-common-dir"))
                    self.assertEqual("journey-indicator\n", projects.git(target, "branch", "--show-current"))
                for project, name in (("foreign", "indicator"), ("atlas", "../other"), ("atlas", "")):
                    with self.subTest(project=project, name=name), self.assertRaisesRegex(ValueError, "plain worktree name"):
                        projects.worktree(project, name)
                occupied = source / ".worktrees/journey-other"
                projects.repository(occupied)
                with self.assertRaisesRegex(ValueError, "expected common dir"):
                    projects.worktree("atlas", "other")
                with patch.dict(os.environ, {"PFM_DEV_FENCE": "0"}), self.assertRaisesRegex(ValueError, "requires the fence"):
                    projects.worktree("atlas", "indicator")


if __name__ == "__main__":
    unittest.main()
