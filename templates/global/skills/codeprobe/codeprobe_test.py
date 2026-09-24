import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.realpath(__file__))
SCRIPT = os.path.join(HERE, "codeprobe.py")
PART_CHARS = 50_000


def run_collect(root, out_dir, plan):
    return subprocess.run(
        [sys.executable, SCRIPT, "collect", root, out_dir, "--expect", "1"],
        input=plan, capture_output=True, text=True,
    )


def parse_hint(head_line):
    """Return a list of (offset, limit) pairs parsed out of the head line's parts hint, or [] if none."""
    m = re.search(r"Read it in parts: (.+)$", head_line)
    if not m:
        return []
    out = []
    for piece in m.group(1).split(", "):
        pm = re.match(r"offset (\d+) limit (\d+)", piece)
        assert pm, f"unparseable part spec: {piece!r}"
        out.append((int(pm.group(1)), int(pm.group(2))))
    return out


class CollectPartsTest(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.out_dir = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.out_dir, ignore_errors=True)

    def make_big_file(self, n_lines=3000, line_len=60):
        path = os.path.join(self.root, "big.py")
        with open(path, "w") as fh:
            for i in range(n_lines):
                body = f"x{i}" * ((line_len // 3) + 1)
                fh.write(f"v{i} = '{body}'"[:line_len] + "\n")
        return path

    def make_small_file(self, n_lines=5):
        path = os.path.join(self.root, "small.py")
        with open(path, "w") as fh:
            for i in range(n_lines):
                fh.write(f"v{i} = {i}\n")
        return path

    def test_large_collect_parts_hint_is_valid(self):
        self.make_big_file(n_lines=3000, line_len=60)
        plan = "= 1 the whole file\nfile big.py\n"
        proc = run_collect(self.root, self.out_dir, plan)
        self.assertEqual(proc.returncode, 0, msg=f"collect failed: {proc.stderr}")

        ret_path = os.path.join(self.out_dir, "return.md")
        with open(ret_path) as fh:
            text = fh.read()
        text_lines = text.splitlines()
        head_line = text_lines[0]

        parts = parse_hint(head_line)
        self.assertTrue(parts, f"expected a parts hint for a 3000-line file; head was: {head_line!r}")

        # (a) each part holds at most PART_CHARS characters
        for offset, limit in parts:
            chunk = text_lines[offset - 1: offset - 1 + limit]
            chars = sum(len(l) + 1 for l in chunk)
            self.assertLessEqual(
                chars, PART_CHARS,
                msg=f"part offset {offset} limit {limit} holds {chars} chars > {PART_CHARS}",
            )

        # (b) parts are contiguous, starting at line 1 and ending at the file's last line
        self.assertEqual(parts[0][0], 1, "first part must start at line 1")
        covered_end = 0
        for offset, limit in parts:
            self.assertEqual(offset, covered_end + 1, f"gap or overlap before offset {offset}")
            covered_end = offset + limit - 1
        self.assertEqual(covered_end, len(text_lines), "parts must cover through the file's last line")

    def test_small_collect_has_no_parts_hint(self):
        self.make_small_file(n_lines=5)
        plan = "= 1 the whole file\nfile small.py\n"
        proc = run_collect(self.root, self.out_dir, plan)
        self.assertEqual(proc.returncode, 0, msg=f"collect failed: {proc.stderr}")

        ret_path = os.path.join(self.out_dir, "return.md")
        with open(ret_path) as fh:
            head_line = fh.readline()
        self.assertNotIn("Read it in parts", head_line, f"small collect should have no parts hint; got: {head_line!r}")


class ProbeBaseTest(unittest.TestCase):
    def setUp(self):
        parent = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, parent, ignore_errors=True)
        self.root = os.path.join(parent, ".demo-proj")
        os.makedirs(self.root)
        with open(os.path.join(self.root, "small.py"), "w") as fh:
            fh.write("v = 1\n")

    def tearDown(self):
        shutil.rmtree("/tmp/demo-proj", ignore_errors=True)

    def test_default_dir_is_under_project_scoped_tmp(self):
        plan = "= 1 the whole file\nfile small.py\n"
        proc = subprocess.run(
            [sys.executable, SCRIPT, "collect", self.root, "--expect", "1"],
            input=plan, capture_output=True, text=True,
        )
        self.assertEqual(proc.returncode, 0, msg=f"collect failed: {proc.stderr}")

        m = re.search(r"verbatim text in (\S+return\.md)", proc.stdout)
        self.assertTrue(m, f"expected the manifest head line to name return.md; stdout was: {proc.stdout!r}")
        ret_path = m.group(1)
        self.assertTrue(
            ret_path.startswith("/tmp/demo-proj/codeprobe/"),
            f"expected return.md under /tmp/demo-proj/codeprobe/, got: {ret_path!r}",
        )


if __name__ == "__main__":
    unittest.main()
