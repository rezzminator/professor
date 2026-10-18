#!/usr/bin/env bash
# Flight prompt contracts: the cast's tiers, the one long-command wait, review sizing and input, critical decisions to main.
# BROKEN STATE: a missing template or a failed contract assertion exits nonzero.
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - <<'PY'
from pathlib import Path
import re
import unittest

AGENTS = Path('templates/global/agents')
GENERATED = Path('templates/generated/agents')


def resolved(name):
    # The file that holds the agent's full text: a `from:` declaration is a thin overlay, its render in GENERATED the agent.
    declared = AGENTS / f'{name}.md'
    return GENERATED / f'{name}.md' if '\nfrom: ' in declared.read_text() else declared


def text(name):
    return resolved(name).read_text()


def body(name):
    return text(name).split('---\n', 2)[2]


def frontmatter(path):
    head = path.read_text().split('---\n', 2)[1]
    return dict(re.findall(r'(?m)^(model|effort): (\S+)$', head))


class FlightContracts(unittest.TestCase):
    def test_cast_tiers(self):
        cast = {
            'flights-foreman': ('opus', 'high'), 'flights-foreman-sonnet': ('sonnet', 'high'),
            'flights-lander-mini': ('sonnet', 'high'), 'flights-lander': ('opus', 'medium'),
            'flights-lander-pro': ('opus', 'high'),
        }
        for name, (model, effort) in cast.items():
            path = resolved(name)
            self.assertEqual(frontmatter(path), {'model': model, 'effort': effort}, f'{path}: tier')

    def test_the_two_foremen_share_one_manual(self):
        self.assertIn('\nfrom: flights-foreman\n', (AGENTS / 'flights-foreman-sonnet.md').read_text(), 'the Sonnet foreman derives from flights-foreman')
        self.assertEqual(body('flights-foreman-sonnet'), body('flights-foreman'), 'the Sonnet foreman carries the foreman manual whole')
        for name in ('flights-foreman', 'flights-foreman-sonnet'):
            head = text(name).split('---\n', 2)[1]
            self.assertIn('effortControl:', head, f'{name}: steered by auto-effort-control')
            self.assertNotRegex(head, r'(?m)^tools:', f'{name}: no tools line, so it inherits every tool and the effort buttons')

    def test_one_blocking_wait_past_ten_minutes(self):
        for name in ('flights-foreman', 'flights-foreman-sonnet', 'flights-lander'):
            body = text(name)
            self.assertIn('longer than 10 minutes', body, f'{name}: the long-command threshold')
            self.assertIn("timeout {s} bash -c 'until [ -s \"$1\" ]", body, f'{name}: the one blocking wait')

    def test_review_sizing_and_input(self):
        lander = text('flights-lander')
        self.assertIn('`reviewer-pro` at 10,000+ code lines', lander, 'reviewer-pro only past 10,000 code lines')
        self.assertIn('diff --numstat', lander, 'code lines are counted, not guessed')
        self.assertIn('ls-files --others --exclude-standard', lander, 'the lander lists untracked source')
        self.assertIn('UNTRACKED the untracked files', lander, 'the reviewer brief carries the untracked files')
        for name in ('reviewer', 'reviewer-pro'):
            body = text(name)
            self.assertTrue('untracked line count' in body and 'WORKTREE' in body, f'{name}: untracked source sizes and joins the review')

    def test_critical_decisions_go_to_main(self):
        for name in ('flights-foreman', 'flights-lander'):
            body = text(name)
            self.assertIn('never ask the user', body, f'{name}: decides, never asks')
            self.assertIn('SendMessage(to: "main")', body, f'{name}: a critical decision goes to main')


unittest.main()
PY
