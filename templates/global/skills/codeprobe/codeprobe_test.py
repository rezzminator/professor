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

    def test_verb_block(self):
        text = self.collect_one("block config.yml build:")
        self.assertIn("@ config.yml:2-4 · block /build:/ · 3 lines", text)
        self.assertIn("2\t  build:\n3\t    steps:\n4\t      - run: echo hi\n", text)

    def test_verb_consts(self):
        text = self.collect_one("consts consts.go Status")
        self.assertIn("@ consts Status under consts.go · 2 declarations", text)
        self.assertIn("consts.go:6\t\tStatusOK Status = iota\n", text)
        self.assertIn("consts.go:7\t\tStatusFail\n", text)


class ProbeCommandsTest(unittest.TestCase):
    """One real test per probe command (verbs, init, refs, absent, rows, render), each asserted
    on its concrete output — not just a zero exit code."""

    def setUp(self):
        parent = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, parent, ignore_errors=True)
        # A per-run project name, so parallel checkouts never share one probe dir.
        self.project = "cp-" + os.path.basename(parent).lower()
        self.base = os.path.join("/tmp/", self.project, "codeprobe") + os.sep
        self.root = os.path.join(parent, self.project)
        os.makedirs(self.root)
        write_fixture(self.root)

    def tearDown(self):
        shutil.rmtree(os.path.dirname(os.path.dirname(self.base)), ignore_errors=True)

    def init_dir(self):
        """Runs a real init ask (sig + census) and returns (proc, DIR)."""
        plan = "= 1 the greet function, and every caller\nsig pkg/greet.py greet\ncensus greet\n"
        proc = run_cp(["init", self.root, "--expect", "1", "--budget", "2500"], input=plan)
        self.assertEqual(proc.returncode, 0, msg=f"init failed: {proc.stderr}")
        m = re.search(r"^DIR (\S+)$", proc.stdout, re.M)
        self.assertTrue(m, f"init printed no DIR line; stdout was: {proc.stdout!r}")
        return proc, m.group(1)

    def test_verbs_prints_the_extraction_table(self):
        proc = run_cp(["verbs"])
        self.assertEqual(proc.returncode, 0, msg=f"verbs failed: {proc.stderr}")
        self.assertTrue(proc.stdout.startswith("## Extraction verbs"), proc.stdout[:80])
        self.assertIn("| `file PATH` |", proc.stdout)
        self.assertIn("| `consts PATH TYPE` |", proc.stdout)
        self.assertNotIn("## Probe commands", proc.stdout)

    def test_init_computes_census_and_prints_asked_text(self):
        proc, d = self.init_dir()
        self.assertTrue(os.path.isfile(os.path.join(d, "state.json")), f"init named DIR {d} but wrote no state.json")
        self.assertIn("FILES 4 via directory walk", proc.stdout)
        self.assertIn("ASKS 1[census greet]", proc.stdout)
        self.assertIn("REFS greet (CODE) — 3 lines: CODE 3 lines/2 files", proc.stdout)
        self.assertIn("== pkg/greet.py (CODE · 1)", proc.stdout)
        self.assertIn("TEXT 1 `sig pkg/greet.py greet`", proc.stdout)
        self.assertIn("1\tdef greet(name):", proc.stdout)

    def test_refs_lists_hits_with_context_grouped_by_file(self):
        _, d = self.init_dir()
        proc = run_cp(["refs", d, "greet", "-C", "1"])
        self.assertEqual(proc.returncode, 0, msg=f"refs failed: {proc.stderr}")
        self.assertIn("REFS greet (every class) — 3 lines: CODE 3 lines/2 files", proc.stdout)
        self.assertIn("== pkg/caller.py (CODE · 2)", proc.stdout)
        self.assertIn("1:from pkg.greet import greet", proc.stdout)
        self.assertIn("4-def main():", proc.stdout)
        self.assertIn("5:    return greet(\"world\")", proc.stdout)
        self.assertIn("== pkg/greet.py (CODE · 1)", proc.stdout)
        self.assertIn("1:def greet(name):", proc.stdout)

    def test_absent_zero_hits_makes_an_A_row(self):
        _, d = self.init_dir()
        proc = run_cp(["absent", d, "nonexistent_name"])
        self.assertEqual(proc.returncode, 0, msg=f"absent failed: {proc.stderr}")
        self.assertEqual(proc.stdout.strip(), "A1 — `nonexistent_name`: 0 lines in the whole repo (4 files read)")
        with open(os.path.join(d, "state.json")) as fh:
            self.assertIn('"A1"', fh.read())

    def test_absent_nonzero_hits_reports_not_absent(self):
        _, d = self.init_dir()
        proc = run_cp(["absent", d, "greet"])
        self.assertEqual(proc.returncode, 0, msg=f"absent failed: {proc.stderr}")
        self.assertIn("NOT ABSENT — greet is named 3 times in the whole repo; no [A#] row was made:", proc.stdout)
        self.assertIn('pkg/caller.py:1\tfrom pkg.greet import greet', proc.stdout)
        self.assertIn('pkg/greet.py:1\tdef greet(name):', proc.stdout)

    def test_rows_then_render_write_the_verified_return(self):
        _, d = self.init_dir()
        rows = (
            '1 | pkg/caller.py:5 | return greet("world") | main calls greet to build the greeting\n'
            '1 | sig pkg/greet.py greet | - | the greet function signature in full\n'
        )
        proc = run_cp(["rows", d], input=rows)
        self.assertEqual(proc.returncode, 0, msg=f"rows failed: {proc.stderr}")
        self.assertIn("PROBE 1 asks — 1 with rows, 0 partial, 0 not answered · 2 rows, 0 rejected", proc.stdout)
        self.assertIn("END 1 asks", proc.stdout)

        with open(os.path.join(d, "return.md")) as fh:
            written = fh.read()
        self.assertIn(
            "## 1 — the greet function, and every caller  [census greet]  · WITH ROWS", written)
        self.assertIn(
            '  :5 `return greet("world")` — main calls greet to build the greeting', written)
        self.assertIn("- the greet function signature in full", written)
        self.assertIn("@ pkg/greet.py:1-1 · sig greet · 1 lines", written)
        self.assertIn("1\tdef greet(name):", written)
        self.assertIn("## NOT ANSWERED\n- none: every ask has rows", written)

        # render rebuilds the same return purely from the saved rows, without new stdin.
        render_proc = run_cp(["render", d])
        self.assertEqual(render_proc.returncode, 0, msg=f"render failed: {render_proc.stderr}")
        with open(os.path.join(d, "return.md")) as fh:
            rerendered = fh.read()
        self.assertEqual(written, rerendered)

    def test_rows_rejects_a_hedged_note(self):
        _, d = self.init_dir()
        rows = '1 | pkg/greet.py:1 | def greet(name) | probably the greet function\n'
        proc = run_cp(["rows", d], input=rows)
        self.assertEqual(proc.returncode, 0, msg=f"rows failed: {proc.stderr}")
        self.assertIn("1 ROWS REJECTED", proc.stdout)
        self.assertIn(
            "hedged (`probably`): state what the code shows, or write an UNANSWERED row saying what is not settled",
            proc.stdout,
        )
        with open(os.path.join(d, "return.md")) as fh:
            written = fh.read()
        self.assertIn("PROBE 1 asks — 0 with rows, 0 partial, 1 not answered · 0 rows, 1 rejected", written)


if __name__ == "__main__":
    unittest.main()
