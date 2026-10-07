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
        wait = re.search(r'^10\. (.+)$', foreman, re.M).group(1)
        self.assertTrue('outstanding' in wait and 'step 12' in wait and 'step 13' in wait,
                        'no-actor roots must continue to landing and children to return')
        self.assertRegex(wait, r'returns? to verify.*step 11',
                         'children already returned during the build must be verified before landing')

    def test_complete_inventory(self):
        foreman = Path('templates/global/agents/flights-foreman.md').read_text()
        self.assertIn('## Change inventory\n', foreman, 'one shared complete inventory')
        inventory = foreman.split('## Change inventory\n', 1)[1].split('\n## ', 1)[0]
        self.assertIn('git ls-files --others --exclude-standard -z', inventory)
        self.assertTrue('wc -l' in inventory and 'symlink' in inventory and 'stray' in inventory,
                        'inventory counts untracked source and names aliases and strays')
        self.assertIn('nonzero', inventory, 'inventory failure must be visible')
        for file in ('templates/global/agents/flights-lander.md',
                     'templates/global/commands/flights/audit.md',
                     'templates/global/commands/flights/orchestrate-cross-harness.md'):
            self.assertIn('Change inventory', Path(file).read_text(), f'{file}: canonical inventory')
        lander = Path('templates/global/agents/flights-lander.md').read_text()
        self.assertTrue('untracked line counts' in lander and 'review input' in lander,
                        'review sizing and input include untracked source')

unittest.main()
PY
