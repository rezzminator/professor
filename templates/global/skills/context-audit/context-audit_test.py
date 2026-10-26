import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

SCRIPT = os.path.join(os.path.dirname(os.path.realpath(__file__)), "context-audit.py")


class Sandbox(unittest.TestCase):
    """A fake home and project: every store is empty, so each probe must say so as ERROR or absent."""

    def setUp(self):
        self.home = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.home, ignore_errors=True)
        self.proj = os.path.join(self.home, "proj")
        os.makedirs(os.path.join(self.proj, ".git"))

    def write(self, rel, text):
        path = os.path.join(self.home, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            f.write(text)

    def run_audit(self, *args, **env):
        e = {"HOME": self.home, "PATH": os.environ["PATH"], "XDG_STATE_HOME": os.path.join(self.home, "state")}
        e.update(env)
        r = subprocess.run([sys.executable, "-I", SCRIPT, self.proj, *args], capture_output=True, text=True, env=e)
        self.assertEqual(r.returncode, 0, r.stderr)
        return [ln.split("\t") for ln in r.stdout.splitlines()]


class CodexBudgetTest(Sandbox):
    def test_cut_line_and_lost_sections(self):
        self.write(".codex/config.toml", 'project_doc_max_bytes = 30\n[projects."%s"]\ntrust_level = "trusted"\n' % os.path.realpath(self.proj))
        self.write("proj/AGENTS.md", "# Kept\n\nfirst rule line here\n\n# Lost one\n\nx\n\n## Lost two\n")
        rows = self.run_audit("--engine", "codex")
        limit = [r for r in rows if r[0] == "LIMIT"]
        self.assertEqual(len(limit), 1, rows)
        self.assertIn("cut at byte 30, line 5; last text read: \"first rule line here\"", limit[0][2])
        self.assertEqual(limit[0][3:], ["# Lost one", "## Lost two"])
        layer = next(r for r in rows if r[0] == "LAYER" and r[1] == "project")
        self.assertEqual(layer[2], "30")

    def test_no_rollout_reads_as_error(self):
        self.write(".codex/config.toml", "")
        rows = self.run_audit("--engine", "codex")
        self.assertTrue(any(r[0] == "ERROR" and "no rollout" in r[1] for r in rows), rows)
        self.assertFalse(any(r[0] == "GAP" for r in rows))


class ClaudeChainTest(Sandbox):
    def test_import_hop_and_missing_transcript(self):
        cfg = os.path.join(self.home, "cfg")
        self.write("cfg/settings.json", "{}")
        self.write("proj/CLAUDE.md", "# Proj\n\nSee @extra.md and `@not-an-import.md`.\n")
        self.write("proj/extra.md", "imported rule text\n")
        rows = self.run_audit("--engine", "claude", CLAUDE_CONFIG_DIR=cfg)
        hop = [r for r in rows if r[0] == "LAYER" and r[1] == "project import hop 1"]
        self.assertEqual([r[6] for r in hop], [os.path.realpath(os.path.join(self.proj, "extra.md"))])
        self.assertTrue(any(r[0] == "ERROR" and "no transcript" in r[1] for r in rows), rows)
        self.assertTrue(any(r[0] == "ERROR" and "usage store unreadable" in r[1] for r in rows), rows)

    def test_directory_marketplace_plugin_reads_its_folder(self):
        cfg, mkt = os.path.join(self.home, "cfg"), os.path.join(self.home, "mkt")
        skill = "---\nname: %s\ndescription: %s skill\n---\n"
        self.write("cfg/settings.json", '{"enabledPlugins": {"pp@pp-dev": true, "gh@gh": true}}')
        self.write("cfg/plugins/installed_plugins.json", json.dumps({"plugins": {
            "pp@pp-dev": [{"scope": "user", "installPath": cfg + "/plugins/cache/pp-dev/pp/1.0.0"}],
            "gh@gh": [{"scope": "user", "installPath": cfg + "/plugins/cache/gh/gh/2.0.0"}]}}))
        self.write("cfg/plugins/known_marketplaces.json", json.dumps({
            "pp-dev": {"source": {"source": "directory", "path": mkt}},
            "gh": {"source": {"source": "github", "repo": "o/gh"}}}))
        self.write("mkt/.claude-plugin/marketplace.json", '{"plugins": [{"name": "pp", "source": "./plugins/pp"}]}')
        self.write("mkt/plugins/pp/skills/folderonly/SKILL.md", skill % ("folderonly", "folder"))
        self.write("cfg/plugins/cache/pp-dev/pp/1.0.0/skills/cacheonly/SKILL.md", skill % ("cacheonly", "cache"))
        self.write("cfg/plugins/cache/gh/gh/2.0.0/skills/released/SKILL.md", skill % ("released", "released"))
        rows = self.run_audit("--engine", "claude", CLAUDE_CONFIG_DIR=cfg)
        names = sorted(os.path.basename(os.path.dirname(r[6])) for r in rows if r[0] == "LAYER" and r[1] == "plugin skill")
        self.assertEqual(names, ["folderonly", "released"], rows)

    def test_directory_marketplace_without_the_plugin_reads_as_error(self):
        cfg, mkt = os.path.join(self.home, "cfg"), os.path.join(self.home, "mkt")
        self.write("cfg/settings.json", '{"enabledPlugins": {"pp@pp-dev": true}}')
        self.write("cfg/plugins/installed_plugins.json", json.dumps({"plugins": {
            "pp@pp-dev": [{"scope": "user", "installPath": cfg + "/plugins/cache/pp-dev/pp/1.0.0"}]}}))
        self.write("cfg/plugins/known_marketplaces.json", json.dumps({"pp-dev": {"source": {"source": "directory", "path": mkt}}}))
        rows = self.run_audit("--engine", "claude", CLAUDE_CONFIG_DIR=cfg)
        self.assertTrue(any(r[0] == "ERROR" and "pp-dev" in r[1] and "pp" in r[1] for r in rows), rows)


if __name__ == "__main__":
    unittest.main()
