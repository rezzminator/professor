#!/usr/bin/env python3
"""Exercise the verify workflow's leak-policy staging without repository secrets."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[1]
TRUSTED = "github.actor != 'dependabot[bot]' && (github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository)"


class VerifyLeakPolicyTests(unittest.TestCase):
    def setUp(self):
        path = Path(os.environ.get('VERIFY_WORKFLOW_FILE', ROOT / '.github/workflows/verify.yml'))
        workflow = yaml.safe_load(path.read_text())
        self.steps = workflow['jobs']['linked-worktree-fence']['steps']

    def step(self, name):
        matches = [step for step in self.steps if step.get('name') == name]
        self.assertEqual(len(matches), 1, f'missing or duplicate workflow step: {name}')
        return matches[0]

    def test_trusted_private_scan_and_visible_fork_gap(self):
        stage = self.step('Stage private leak-check policy (trusted events only)')
        scan = self.step('Scan private leak terms (trusted events only)')
        skipped = self.step('Report skipped private leak coverage')
        self.assertEqual(stage.get('if'), '${{ ' + TRUSTED + ' }}')
        self.assertEqual(scan.get('if'), stage['if'])
        self.assertEqual(skipped.get('if'), '${{ !(' + TRUSTED + ') }}')
        self.assertIn('PRIVATE COVERAGE SKIPPED', skipped['run'])
        self.assertIn('GITHUB_STEP_SUMMARY', skipped['run'])
        self.assertIn('leak-terms-private.txt', scan['run'])
        self.assertIn('ls-files -z', scan['run'])
        self.assertIn('||', scan['run'], 'enumeration failure must be handled, not read as empty')
        for event, head, actor, expected in [('push', None, 'contributor', True),
                                              ('workflow_dispatch', None, 'contributor', True),
                                              ('pull_request', 'public/repo', 'contributor', True),
                                              ('pull_request', 'fork/repo', 'contributor', False),
                                              ('pull_request', 'public/repo', 'dependabot[bot]', False)]:
            with self.subTest(event=event, head=head):
                # Evaluate the exact workflow expression in the same JS syntax.
                github = {'event_name': event, 'repository': 'public/repo', 'actor': actor,
                          'event': {'pull_request': {'head': {'repo': {'full_name': head}}}}}
                expression = stage['if'][3:-3].strip()
                result = subprocess.run(['node', '-e', 'const github=JSON.parse(process.argv[1]); console.log(' + expression + ')', json.dumps(github)],
                                        capture_output=True, text=True, check=True)
                self.assertEqual(result.stdout.strip(), str(expected).lower())

    def test_fork_staging_runs_without_secret(self):
        for actor, head in [('contributor', 'fork/repo'), ('dependabot[bot]', 'public/repo')]:
            with self.subTest(actor=actor, head=head):
                github = {'event_name': 'pull_request', 'repository': 'public/repo', 'actor': actor,
                          'event': {'pull_request': {'head': {'repo': {'full_name': head}}}}}
                with tempfile.TemporaryDirectory() as directory:
                    root = Path(directory)
                    (root / 'professor-fence/scripts').mkdir(parents=True)
                    summary = root / 'summary'
                    output = ''
                    ran = 0
                    for step in self.steps:
                        body = step.get('run', '')
                        if not ('PFM_PRIVATE_LEAK_TERMS' in step.get('env', {}) or
                                '!structural-only' in body or 'PRIVATE COVERAGE SKIPPED' in body):
                            continue
                        expression = step.get('if', '${{ true }}')[3:-3].strip()
                        selected = subprocess.run(['node', '-e', 'const github=JSON.parse(process.argv[1]); console.log(' + expression + ')', json.dumps(github)],
                                                  capture_output=True, text=True, check=True)
                        if selected.stdout.strip() != 'true':
                            continue
                        ran += 1
                        result = subprocess.run(['bash', '-euo', 'pipefail', '-c', body],
                                                env={**os.environ, 'RUNNER_TEMP': directory,
                                                     'PFM_PRIVATE_LEAK_TERMS': '', 'GITHUB_STEP_SUMMARY': str(summary)},
                                                capture_output=True, text=True)
                        self.assertEqual(result.returncode, 0, 'fork policy staging failed without a secret: ' + result.stderr)
                        output += result.stdout
                    self.assertGreater(ran, 0, 'no policy staging was examined')
                    self.assertEqual((root / 'professor-fence/scripts/leak-terms.txt').read_text(), '!structural-only\n')
                    self.assertFalse((root / 'professor-fence/scripts/leak-terms-private.txt').exists())
                    self.assertIn('PRIVATE COVERAGE SKIPPED', output)
                    self.assertIn('PRIVATE COVERAGE SKIPPED', summary.read_text())

    def test_structural_policy_is_always_staged(self):
        stage = self.step('Stage structural leak-check policy')
        self.assertNotIn('if', stage)
        self.assertNotIn('secrets.', str(stage))
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'professor-fence/scripts').mkdir(parents=True)
            result = subprocess.run(['bash', '-euo', 'pipefail', '-c', stage['run']],
                                    env={**os.environ, 'RUNNER_TEMP': directory},
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / 'professor-fence/scripts/leak-terms.txt').read_text(), '!structural-only\n')

    def test_private_policy_staging_requires_secret(self):
        stage = self.step('Stage private leak-check policy (trusted events only)')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'professor-fence/scripts').mkdir(parents=True)
            for secret, expected in [('', 1), ('syntheticsecret', 0)]:
                with self.subTest(secret=bool(secret)):
                    result = subprocess.run(['bash', '-euo', 'pipefail', '-c', stage['run']],
                                            env={**os.environ, 'RUNNER_TEMP': directory, 'PFM_PRIVATE_LEAK_TERMS': secret},
                                            capture_output=True, text=True)
                    self.assertEqual(result.returncode, expected, result.stderr)
                    if expected:
                        self.assertIn('Missing repository secret', result.stderr)
                    else:
                        self.assertFalse((root / 'professor-fence/scripts/leak-terms-private.txt').exists(),
                                         'private policy must not become structural public-file input')
                        self.assertEqual((root / 'professor-leak-terms-private.txt').read_text(), secret + '\n')


if __name__ == '__main__':
    unittest.main()
