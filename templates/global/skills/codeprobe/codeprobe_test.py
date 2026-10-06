import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.realpath(__file__))
# CODEPROBE_SCRIPT lets the watched-failure proof point this whole suite at a mutated
# copy of the script without ever touching codeprobe.py itself.
SCRIPT = os.environ.get("CODEPROBE_SCRIPT") or os.path.join(HERE, "codeprobe.py")
PART_CHARS = 50_000


def run_collect(root, out_dir, plan):
    return subprocess.run(
        [sys.executable, SCRIPT, "collect", root, out_dir, "--expect", "1"],
        input=plan, capture_output=True, text=True,
    )


def run_cp(args, input=None):
    return subprocess.run(
        [sys.executable, SCRIPT] + args, input=input, capture_output=True, text=True,
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
        # A per-run project name, so parallel checkouts never share one probe dir.
        self.project = "cp-" + os.path.basename(parent).lower()
        self.base = os.path.join("/tmp/", self.project, "codeprobe") + os.sep
        self.root = os.path.join(parent, "." + self.project)
        os.makedirs(self.root)
        with open(os.path.join(self.root, "small.py"), "w") as fh:
            fh.write("v = 1\n")

    def tearDown(self):
        shutil.rmtree(os.path.dirname(os.path.dirname(self.base)), ignore_errors=True)

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
            ret_path.startswith(self.base),
            f"expected return.md under {self.base}, got: {ret_path!r}",
        )


GREET_PY = (
    'def greet(name):\n'
    '    return f"hello {name}"\n'
    '\n'
    '\n'
    'def add(a, b):\n'
    '    return a + b\n'
)
CALLER_PY = (
    'from pkg.greet import greet\n'
    '\n'
    '\n'
    'def main():\n'
    '    return greet("world")\n'
)
CONSTS_GO = (
    'package config\n'
    '\n'
    'type Status int\n'
    '\n'
    'const (\n'
    '\tStatusOK Status = iota\n'
    '\tStatusFail\n'
    ')\n'
)
CONFIG_YML = (
    'jobs:\n'
    '  build:\n'
    '    steps:\n'
    '      - run: echo hi\n'
    '  test:\n'
    '    steps:\n'
    '      - run: echo bye\n'
)


def write_fixture(root):
    os.makedirs(os.path.join(root, "pkg"), exist_ok=True)
    with open(os.path.join(root, "pkg", "greet.py"), "w") as fh:
        fh.write(GREET_PY)
    with open(os.path.join(root, "pkg", "caller.py"), "w") as fh:
        fh.write(CALLER_PY)
    with open(os.path.join(root, "consts.go"), "w") as fh:
        fh.write(CONSTS_GO)
    with open(os.path.join(root, "config.yml"), "w") as fh:
        fh.write(CONFIG_YML)


class ExtractionVerbsTest(unittest.TestCase):
    """One real collect order per extraction verb (VERBS in codeprobe.py), each asserted on its
    exact @ header and body text, not just a zero exit code."""

    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.out_dir = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.out_dir, ignore_errors=True)
        write_fixture(self.root)

    def collect_one(self, cmd):
        plan = f"= 1 t\n{cmd}\n"
        proc = run_collect(self.root, self.out_dir, plan)
        self.assertEqual(proc.returncode, 0, msg=f"collect failed: {proc.stderr}")
        with open(os.path.join(self.out_dir, "return.md")) as fh:
            return fh.read()

    def test_verb_file(self):
        text = self.collect_one("file pkg/greet.py")
        self.assertIn("@ pkg/greet.py:1-6 · whole file · 6 lines", text)
        self.assertIn("1\tdef greet(name):\n", text)
        self.assertIn('6\t    return a + b\n', text)

    def test_verb_lines(self):
        text = self.collect_one("lines pkg/greet.py 1 2")
        self.assertIn("@ pkg/greet.py:1-2 · 2 lines", text)
        self.assertIn("1\tdef greet(name):\n2\t    return f\"hello {name}\"\n", text)

    def test_verb_def(self):
        text = self.collect_one("def pkg/greet.py greet")
        self.assertIn("@ pkg/greet.py:1-2 · def greet · 2 lines", text)
        self.assertIn("1\tdef greet(name):\n2\t    return f\"hello {name}\"\n", text)

    def test_verb_sig(self):
        text = self.collect_one("sig pkg/greet.py greet")
        self.assertIn("@ pkg/greet.py:1-1 · sig greet · 1 lines", text)
        self.assertIn("1\tdef greet(name):\n", text)
        self.assertNotIn("return f\"hello", text)

    def test_verb_defs(self):
        text = self.collect_one("defs pkg/greet.py")
        self.assertIn("@ pkg/greet.py · 2 definitions · signatures", text)
        self.assertIn("1\tdef greet(name):\n5\tdef add(a, b):\n", text)

    def test_verb_grep(self):
        text = self.collect_one("grep greet pkg")
        self.assertIn("@ grep /greet/ in pkg · 3 hits in 2 files", text)
        self.assertIn('pkg/caller.py:1\tfrom pkg.greet import greet\n', text)
        self.assertIn('pkg/caller.py:5\t    return greet("world")\n', text)
        self.assertIn("pkg/greet.py:1\tdef greet(name):\n", text)

    def test_grep_readings_support_bre_boundaries_intervals_and_literal_fallback(self):
        with open(os.path.join(self.root, "pkg", "idx.py"), "w") as handle:
            handle.write("x = a.Do[0]\n")
        for pattern, count, reading, hit in ((r"\<greet\>", "3 hits in 2 files", "read as grep BRE", "pkg/greet.py:1\tdef greet(name):"),
                                             (r"l\{2\}", "1 hits in 1 files", "read as grep BRE", 'pkg/greet.py:2\t    return f"hello {name}"'),
                                             (r"\.Do[", "1 hits in 1 files", "not a valid regex; matched literally", "pkg/idx.py:1\tx = a.Do[0]")):
            with self.subTest(pattern=pattern), tempfile.TemporaryDirectory() as self.out_dir:
                text = self.collect_one(f"grep '{pattern}' pkg")
                self.assertIn(f"· {count} · {reading}", text)
                self.assertIn(hit, text)

    def test_python_regex_misses_name_the_reading_for_grep_and_block(self):
        for command in ("grep 'zz+q' pkg", "block config.yml 'zz+q'", "block config.yml 'build:' -n 2"):
            with self.subTest(command=command), tempfile.TemporaryDirectory() as out_dir:
                proc = run_collect(self.root, out_dir, f"= 1 t\n{command}\n")
                self.assertEqual(proc.returncode, 0, proc.stderr)
                self.assertIn("MISS 1", proc.stdout)
                self.assertIn("read as a Python regex", proc.stdout)

    def test_verb_grep_reads_an_invalid_regex_the_way_grep_does(self):
        with open(os.path.join(self.root, "pkg", "client.py"), "w") as fh:
            fh.write("resp = client.Do(req)\n")
        text = self.collect_one(r"grep '\.Do(' pkg")
        self.assertIn("· 1 hits in 1 files · read as grep BRE", text)
        self.assertIn("pkg/client.py:1\tresp = client.Do(req)\n", text)

    def test_a_grep_miss_names_how_its_pattern_was_read(self):
        proc = run_collect(self.root, self.out_dir, "= 1 t\ngrep '\\.Do[' pkg\n")
        self.assertIn("MISS 1", proc.stdout)
        self.assertIn("not a valid regex; matched literally", proc.stdout)

    def test_verb_block(self):
        text = self.collect_one("block config.yml build:")
        self.assertIn("@ config.yml:2-4 · block /build:/ · 3 lines", text)
        self.assertIn("2\t  build:\n3\t    steps:\n4\t      - run: echo hi\n", text)

    def test_verb_consts(self):
        text = self.collect_one("consts consts.go Status")
        self.assertIn("@ consts Status under consts.go · 2 declarations", text)
        self.assertIn("consts.go:6\t\tStatusOK Status = iota\n", text)
        self.assertIn("consts.go:7\t\tStatusFail\n", text)


class VerbsCommandTest(unittest.TestCase):
    def test_verbs_prints_the_extraction_table_and_the_collect_syntax(self):
        proc = run_cp(["verbs"])
        self.assertEqual(proc.returncode, 0, msg=f"verbs failed: {proc.stderr}")
        self.assertTrue(proc.stdout.startswith("## Extraction verbs"), proc.stdout[:80])
        self.assertIn("| `file PATH` |", proc.stdout)
        self.assertIn("| `consts PATH TYPE` |", proc.stdout)
        self.assertIn("## collect", proc.stdout)


if __name__ == "__main__":
    unittest.main()
