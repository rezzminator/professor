#!/usr/bin/env bash
# Flight prompt contracts: the cast's tiers, the one long-command wait, the lander's computed review spawn and its kit, critical decisions to main.
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
            'flights-lander': ('opus', 'medium'),
            'flights-hunter': ('opus', None), 'flights-spec-walker': ('sonnet', None), 'flights-sub-lander': ('haiku', None),
        }
        for name, (model, effort) in cast.items():
            path = resolved(name)
            want = {'model': model, **({'effort': effort} if effort else {})}
            self.assertEqual(frontmatter(path), want, f'{path}: tier')
        for retired in ('flights-lander-mini', 'flights-lander-pro'):
            for d in (AGENTS, GENERATED):
                self.assertFalse((d / f'{retired}.md').exists(), f'{d}/{retired}.md: a retired lander tier ships')

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

    def test_the_lander_spawns_its_computed_plan(self):
        lander = text('flights-lander')
        kit = '$HOME/.claude/skills/lander-inventory/lander-inventory.py'
        self.assertIn(f'python3 -I {kit} ', lander, 'the inventory runs from the installed kit')
        self.assertTrue(Path('templates/global/skills/lander-inventory/lander-inventory.py').is_file(), 'the inventory kit ships')
        self.assertTrue(Path('templates/global/skills/lander-inventory/SKILL.md').is_file(), 'pfm install links only a skill directory holding SKILL.md')
        self.assertNotRegex(lander, r'/(Users|home)/', 'no machine-absolute path')
        self.assertIn('ONE message spawning exactly the PLAN rows', lander, 'the spawn is the computed plan')
        self.assertIn('ls-files --others --exclude-standard', lander, 'the lander reads untracked source')
        for child in ('flights-hunter', 'flights-spec-walker', 'flights-sub-lander'):
            self.assertIn(f'subagent_type: {child}', lander, f'the lander spawns {child}')
            head = text(child).split('---\n', 2)[1]
            self.assertIn('include: [flights-lander]', head, f'{child}: visible to the lander alone')
            self.assertRegex(head, r'(?m)^tools: Read, Grep, Glob, Bash(, mcp__forward-compaction__(forward_compaction|look_back))*$', f'{child}: read-only tools, forward-compaction its only addition')

    def test_reviewers_size_untracked_source(self):
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
