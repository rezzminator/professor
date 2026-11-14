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


class RuleControlTest(Sandbox):
    """visible-to counts a rule only for the loops rule-control hands it to (rule-control's src/visible-to.ts)."""

    BODY = "# Main only\n\nmain-only rule text\n"

    def setUpRules(self, rule_control, frontmatter, extra=None):
        self.cfg = os.path.join(self.home, "cfg")
        if rule_control:
            self.write("cfg/settings.json", '{"enabledPlugins": {"rule-control@professor": true}}')
            self.write("cfg/plugins/installed_plugins.json", json.dumps({"plugins": {
                "rule-control@professor": [{"scope": "user", "installPath": self.cfg + "/plugins/cache/professor/rule-control/0.1.0"}]}}))
        else:
            self.write("cfg/settings.json", "{}")
        self.write("cfg/rules/scoped.md", "---\n%s---\n\n%s" % (frontmatter, self.BODY))
        for rel, text in (extra or {}).items():
            self.write(rel, text)

    def rule_rows(self, loop):
        rows = self.run_audit("--engine", "claude", "--loop", loop, CLAUDE_CONFIG_DIR=self.cfg)
        return rows, [r for r in rows if r[0] == "LAYER" and r[1] == "user rule" and r[6].endswith("scoped.md")]

    def test_main_only_rule_counts_for_main_not_a_sub_agent(self):
        self.setUpRules(True, 'visible-to:\n  include: ["main"]\n')
        rows, main = self.rule_rows("main")
        self.assertEqual(len(main), 1, rows)
        self.assertEqual(main[0][2], str(len(self.BODY.rstrip().encode())), "the delivered body, without frontmatter")
        rows, sub = self.rule_rows("flights:foreman")
        self.assertEqual(sub, [], rows)
        self.assertTrue(any(r[:2] == ["PROBE", "visible-to"] and "withheld from flights:foreman" in r[2] for r in rows), rows)

    def test_exclude_glob_leaves_out_matching_agent_types(self):
        self.setUpRules(True, 'visible-to: {exclude: ["flights:*"]}\n')
        self.assertEqual(len(self.rule_rows("main")[1]), 1)
        self.assertEqual(self.rule_rows("flights:lander")[1], [])

    def test_without_rule_control_the_key_is_inert(self):
        self.setUpRules(False, 'visible-to:\n  include: ["main"]\n')
        rows, sub = self.rule_rows("flights:foreman")
        self.assertEqual(len(sub), 1, rows)
        self.assertTrue(any(r[:2] == ["PROBE", "rule-control"] and r[2].startswith("not enabled") for r in rows), rows)

    def test_unusable_value_fails_open(self):
        self.setUpRules(True, "visible-to: include main\n")
        rows, sub = self.rule_rows("flights:foreman")
        self.assertEqual(len(sub), 1, rows)
        self.assertTrue(any(r[0] == "WARN" and "fails open" in r[1] and r[2].endswith("scoped.md") for r in rows), rows)

    def test_importing_rule_stays_for_every_loop(self):
        self.setUpRules(True, 'visible-to: {include: [main]}\n', {"cfg/rules/scoped.md": "---\nvisible-to: {include: [main]}\n---\n\nSee @more.txt\n", "cfg/rules/more.txt": "imported\n"})
        rows, sub = self.rule_rows("flights:foreman")
        self.assertEqual(len(sub), 1, rows)


if __name__ == "__main__":
    unittest.main()
