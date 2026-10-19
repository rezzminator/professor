import os
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.realpath(__file__))
# LANDER_INVENTORY_SCRIPT points the suite at another copy (the unfixed one, for the watched-failure proof).
SCRIPT = os.environ.get("LANDER_INVENTORY_SCRIPT") or os.path.join(HERE, "lander-inventory.py")


class WorkingTreeTest(unittest.TestCase):
    """A flight's work sits committed, uncommitted and untracked; the inventory measures all three."""

    def setUp(self):
        self.repo = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.repo, ignore_errors=True)
        self.write("a.py", "old_alpha_name = 1\n")
        self.write("b.py", "old_beta_name = 2\n")
        self.write("old_delta_file.py", "x = 1\n")
        self.write("ref.py", "old_alpha_name old_beta_name old_delta_file\n")
        self.git("init", "-q", "-b", "main", ".")
        self.git("add", ".")
        self.git("commit", "-qm", "base")
        self.base = self.git("rev-parse", "HEAD").strip()
        self.write("a.py", "new_alpha_name = 1\n")
        self.git("commit", "-qam", "committed")
        self.write("b.py", "new_beta_name = 2\n")  # uncommitted
        os.remove(os.path.join(self.repo, "old_delta_file.py"))  # uncommitted deletion
        self.write("c.py", "# replaces old_delta_file\n" + "".join(f"v{i} = {i}\n" for i in range(450)))  # untracked

    def write(self, name, text):
        with open(os.path.join(self.repo, name), "w") as f:
            f.write(text)

    def git(self, *args):
        p = subprocess.run(["git", "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-C", self.repo, *args],
                           capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stderr)
        return p.stdout

    def run_inv(self, *args):
        return subprocess.run([sys.executable, SCRIPT, self.repo, self.base, *args], capture_output=True, text=True)

    def test_working_tree_carries_committed_uncommitted_and_untracked(self):
        p = self.run_inv()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        rows = p.stdout.splitlines()
        self.assertIn(f"INVENTORY {self.base}..WORKTREE", rows[0])
        left = {r.split(" ")[1]: r for r in rows if r.startswith("LEFT ") and not r.split(" ")[1].isdigit()}
        self.assertIn("old_alpha_name", left, "the committed change")
        self.assertIn("old_beta_name", left, "the uncommitted change")
        gone = [r for r in rows if r.startswith("GONE old_delta_file ")]
        self.assertEqual(len(gone), 1, "the uncommitted deletion")
        self.assertIn("c.py:1", gone[0], "an untracked file is searched")
        hunters = [r for r in rows if r.startswith("PLAN HUNTER ")]
        self.assertEqual(len(hunters), 1, rows)
        self.assertIn("c.py", hunters[0], "an untracked file is counted")

    def test_only_uncommitted_work_is_no_empty_diff(self):
        self.base = self.git("rev-parse", "HEAD").strip()
        p = self.run_inv()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn("LEFT old_beta_name ", p.stdout)
        self.assertNotIn("LEFT old_alpha_name ", p.stdout)

    def test_head_keeps_the_commit_range(self):
        p = self.run_inv("HEAD")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn(f"INVENTORY {self.base}..HEAD", p.stdout)
        self.assertIn("LEFT old_alpha_name ", p.stdout)
        self.assertNotIn("old_beta_name", p.stdout)
        self.assertIn("PLAN HUNTERS none", p.stdout)


if __name__ == "__main__":
    unittest.main()
