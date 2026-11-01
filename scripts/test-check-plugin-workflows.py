#!/usr/bin/env python3
"""check-plugin-workflows.py on fixtures: the template shape passes, each broken rule is named."""

from __future__ import annotations

from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("check-plugin-workflows.py")

CI = """name: ci
on:
  pull_request:
    branches: [main]
    types: [opened, synchronize, reopened]

jobs:
  check:
    runs-on: ${{ matrix.os }}
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-15]
    steps:
      - run: make check

  release:
    runs-on: ubuntu-latest
    steps:
      - run: make release

  ci:
    needs: [check, release]
    if: always()
    runs-on: ubuntu-latest
    steps:
      - run: echo ok
"""

PUBLISH = """name: publish
on:
  pull_request:
    branches: [main]
    types: [closed]

jobs:
  publish:
    if: github.event.action == 'closed' && github.event.pull_request.merged == true
    runs-on: ubuntu-latest
    steps:
      - run: gh release create
  verify:
    needs: publish
    runs-on: ubuntu-latest
    steps:
      - run: gh release view
"""

NO_MACOS = "        os: [ubuntu-latest, macos-15]\n"

# (case, file, old, new, the exact FAIL line, or None for a pass)
MUTATIONS = [
    ("ci push trigger", "ci.yml", "on:\n", "on:\n  push:\n    branches: [develop]\n",
     "FAIL plugins/demo: ci.yml: triggers must be pull_request alone, found ['pull_request', 'push']"),
    ("ci workflow_dispatch", "ci.yml", "on:\n", "on:\n  workflow_dispatch:\n",
     "FAIL plugins/demo: ci.yml: triggers must be pull_request alone, found ['pull_request', 'workflow_dispatch']"),
    ("ci pull request into develop", "ci.yml", "branches: [main]", "branches: [main, develop]",
     "FAIL plugins/demo: ci.yml: pull_request branches must be [main], found ['main', 'develop']"),
    ("ci types without reopened", "ci.yml", "types: [opened, synchronize, reopened]", "types: [opened, synchronize]",
     "FAIL plugins/demo: ci.yml: pull_request types must be ['opened', 'reopened', 'synchronize'], found ['opened', 'synchronize']"),
    ("ci job without always", "ci.yml", "    if: always()\n", "",
     "FAIL plugins/demo: ci.yml: ci job must run if: always(), found None"),
    ("ci job needing only check", "ci.yml", "needs: [check, release]", "needs: [check]",
     "FAIL plugins/demo: ci.yml: ci job must need every other job, missing ['release']"),
    ("ci job missing", "ci.yml", "  ci:\n    needs: [check, release]\n", "  aggregate:\n    needs: [check, release]\n",
     "FAIL plugins/demo: ci.yml: ci.yml has no `ci` job"),
    ("macos-15 dropped silently", "ci.yml", NO_MACOS, "        os: [ubuntu-latest]\n",
     "FAIL plugins/demo: ci.yml: check matrix lacks macos-15 and no comment line in the check job says why"),
    ("macos-15 dropped with its comment", "ci.yml", NO_MACOS,
     "        # No macos-15: the holders are systemd user services, Linux only.\n        os: [ubuntu-latest]\n",
     None),
    ("macos-15 in a matrix include", "ci.yml", NO_MACOS,
     "        include:\n          - os: ubuntu-latest\n          - os: macos-15\n", None),
    ("publish on push", "publish.yml", "on:\n", "on:\n  push:\n    tags: ['v*']\n",
     "FAIL plugins/demo: publish.yml: triggers must be pull_request alone, found ['pull_request', 'push']"),
    ("publish on opened", "publish.yml", "types: [closed]", "types: [opened, closed]",
     "FAIL plugins/demo: publish.yml: pull_request types must be ['closed'], found ['opened', 'closed']"),
    ("publish not gated on the merge", "publish.yml",
     "    if: github.event.action == 'closed' && github.event.pull_request.merged == true\n", "",
     "FAIL plugins/demo: publish.yml: publish job `publish` must be gated on github.event.pull_request.merged == true"),
    ("ci pull_request as a list", "ci.yml",
     "  pull_request:\n    branches: [main]\n    types: [opened, synchronize, reopened]\n",
     "  pull_request: [main]\n",
     "FAIL plugins/demo: ci.yml: pull_request must be a mapping of branches and types, found ['main']"),
    ("publish job not a mapping", "publish.yml",
     "  verify:\n    needs: publish\n    runs-on: ubuntu-latest\n    steps:\n      - run: gh release view\n",
     "  verify: gh release view\n",
     "FAIL plugins/demo: publish.yml: publish.yml jobs must be a mapping of job mappings"),
]

# (case, {file name: its text} beside ci.yml and publish.yml, the exact FAIL line, or None for a pass)
OTHERS = [
    ("one dispatch suite", {"suite.yml": "on: workflow_dispatch\njobs: {}\n"}, None),
    ("one dispatch suite as a mapping", {"suite.yaml": "on:\n  workflow_dispatch:\njobs: {}\n"}, None),
    ("a leftover release workflow on push", {"release.yml": "on:\n  push:\n    branches: [develop]\njobs: {}\n"},
     "FAIL plugins/demo: release.yml: triggers must be workflow_dispatch alone, found ['push']"),
    ("a dispatch suite also on schedule", {"suite.yml": "on: [workflow_dispatch, schedule]\njobs: {}\n"},
     "FAIL plugins/demo: suite.yml: triggers must be workflow_dispatch alone, found ['schedule', 'workflow_dispatch']"),
    ("two dispatch suites", {"a.yml": "on: workflow_dispatch\njobs: {}\n", "b.yml": "on: workflow_dispatch\njobs: {}\n"},
     "FAIL plugins/demo: at most one workflow beside ci.yml and publish.yml, found ['a.yml', 'b.yml']"),
]


def plugin_root(base: Path, ci: str | None = CI, publish: str = PUBLISH) -> Path:
    (base / ".gitmodules").write_text(
        '[submodule "demo"]\n\tpath = plugins/demo\n\turl = https://example.com/demo.git\n', encoding="utf-8")
    workflows = base / "plugins/demo/.github/workflows"
    workflows.mkdir(parents=True)
    if ci is not None:
        (workflows / "ci.yml").write_text(ci, encoding="utf-8")
    (workflows / "publish.yml").write_text(publish, encoding="utf-8")
    return base


def run(root: Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["python3", str(SCRIPT), str(root)], capture_output=True, text=True, timeout=60)


def verdict(want: str | None) -> tuple[int, str]:
    """The exit code and output for one plugin: a pass, or the one FAIL line `want`."""
    if want is None:
        return 0, "PASS plugins/demo\nplugin workflows: 1 checked, 0 failed, 0 not checked\n"
    return 1, f"{want}\nplugin workflows: 1 checked, 1 failed, 0 not checked\n"


class CheckPluginWorkflowsTests(unittest.TestCase):
    def test_template_shape_passes(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            result = run(plugin_root(Path(tmp)))
        self.assertEqual(
            (result.returncode, result.stdout),
            (0, "PASS plugins/demo\nplugin workflows: 1 checked, 0 failed, 0 not checked\n"),
            result.stderr)

    def test_each_mutation_is_named(self) -> None:
        for case, name, old, new, want in MUTATIONS:
            with self.subTest(case), tempfile.TemporaryDirectory() as tmp:
                ci, publish = CI, PUBLISH
                if name == "ci.yml":
                    self.assertIn(old, ci, case)
                    ci = ci.replace(old, new, 1)
                else:
                    self.assertIn(old, publish, case)
                    publish = publish.replace(old, new, 1)
                result = run(plugin_root(Path(tmp), ci, publish))
                self.assertEqual((result.returncode, result.stdout), verdict(want), result.stderr)

    def test_other_workflows_are_one_dispatch_suite_at_most(self) -> None:
        for case, files, want in OTHERS:
            with self.subTest(case), tempfile.TemporaryDirectory() as tmp:
                root = plugin_root(Path(tmp))
                for name, text in files.items():
                    (root / "plugins/demo/.github/workflows" / name).write_text(text, encoding="utf-8")
                result = run(root)
                self.assertEqual((result.returncode, result.stdout), verdict(want), result.stderr)

    def test_missing_workflow_fails_and_unread_plugin_is_not_a_pass(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            result = run(plugin_root(Path(tmp), ci=None))
        self.assertEqual(
            (result.returncode, result.stdout),
            (1, "FAIL plugins/demo: ci.yml is missing\nplugin workflows: 1 checked, 1 failed, 0 not checked\n"))
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / ".gitmodules").write_text(
                '[submodule "demo"]\n\tpath = plugins/demo\n\turl = https://example.com/demo.git\n', encoding="utf-8")
            (root / "plugins/demo").mkdir(parents=True)
            result = run(root)
        self.assertEqual(
            (result.returncode, result.stdout),
            (2, "NOT CHECKED plugins/demo: submodule not checked out\n"
                "plugin workflows: 0 checked, 0 failed, 1 not checked\n"))
        with tempfile.TemporaryDirectory() as tmp:
            result = run(Path(tmp))
        self.assertEqual((result.returncode, result.stdout), (2, f"NOT CHECKED: no .gitmodules under {Path(tmp).resolve()}\n"))


if __name__ == "__main__":
    unittest.main(verbosity=2)
