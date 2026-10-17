#!/usr/bin/env bash
# Flight prompt contracts: no-actor continuation and complete change inventory.
# BROKEN STATE: a missing template or a failed contract assertion exits nonzero.
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - <<'PY'
from pathlib import Path
import re
import unittest

class FlightContracts(unittest.TestCase):
    def test_no_actor_continuation(self):
        foreman = Path('templates/global/agents/flights-foreman.md').read_text()
        step = re.search(r'^(\d+)\. (Wait only .+)$', foreman, re.M)
        self.assertIsNotNone(step, 'the run has a numbered "Wait only" step')
        n, wait = int(step.group(1)), step.group(2)
        self.assertTrue('outstanding' in wait and f'step {n + 2}' in wait and f'step {n + 3}' in wait,
                        'no-actor roots must continue to landing and children to return')
        self.assertRegex(wait, rf'returns? to verify.*step {n + 1}\b',
                         'children already returned during the build must be verified before landing')
        for name, k in (('Verify', 1), ('Land', 2), ('Return once', 3)):
            self.assertRegex(foreman, rf'(?m)^{n + k}\. {name}\b', f'step {n + k} is {name}')

    def test_complete_inventory(self):
        foreman = Path('templates/global/agents/flights-foreman.md').read_text()
        self.assertIn('## Change inventory\n', foreman, 'one shared complete inventory')
        inventory = foreman.split('## Change inventory\n', 1)[1].split('\n## ', 1)[0]
        self.assertIn('git ls-files --others --exclude-standard -z', inventory)
        self.assertTrue('wc -l' in inventory and 'symlink' in inventory and 'stray' in inventory,
                        'inventory counts untracked source and names aliases and strays')
        self.assertIn('nonzero', inventory, 'inventory failure must be visible')
        for file in ('templates/global/agents/flights-lander.md',
                     'templates/global/commands/flights/doctor.md',
                     'templates/global/commands/flights/orchestrate-cross-harness.md'):
            self.assertIn('Change inventory', Path(file).read_text(), f'{file}: canonical inventory')
        lander = Path('templates/global/agents/flights-lander.md').read_text()
        reviewers = [Path(f'templates/global/agents/{name}.md').read_text() for name in ('reviewer', 'reviewer-pro')]
        self.assertTrue('UNTRACKED the owned untracked files' in lander
                        and all('untracked line count' in r and 'WORKTREE' in r for r in reviewers),
                        'review sizing and input include untracked source, whichever tier the lander picks')

unittest.main()
PY
