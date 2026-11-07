#!/usr/bin/env bash
# Reviewer prompt contracts: reviewer and reviewer-pro size a worktree's untracked source and review it with the diff.
# BROKEN STATE: a missing template or a failed contract assertion exits nonzero.
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - <<'PY'
from pathlib import Path
import unittest

AGENTS = Path('templates/global/agents')


class ReviewerContracts(unittest.TestCase):
    def test_reviewers_size_untracked_source(self):
        for name in ('reviewer', 'reviewer-pro'):
            path = AGENTS / f'{name}.md'
            body = path.read_text()
            self.assertTrue('untracked line count' in body and 'WORKTREE' in body, f'{path}: untracked source sizes and joins the review')


unittest.main()
PY
