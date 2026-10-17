"""Tests for segment.py, the `tracetree.py segment` command — run through `.claude/scripts/test-templates.sh`."""
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.realpath(__file__))
TRACETREE = os.path.join(HERE, "tracetree.py")


def git(root, *args):
    env = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@example.invalid", GIT_COMMITTER_NAME="t",
               GIT_COMMITTER_EMAIL="t@example.invalid", GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_SYSTEM=os.devnull)
    done = subprocess.run(["git", "-C", root, *args], capture_output=True, text=True, env=env)
    if done.returncode:
        raise AssertionError(f"git {' '.join(args)}: {done.stderr}")
    return done.stdout.strip()


CORE = """package core

// Shared is the contract every caller switches onto.
func Shared(path string) (int, error) {
	return 0, nil
}

type Record struct {
	ID string
}

func helper() int { return 1 }
"""

# eight dead statements, so each caller's component outweighs its door lines
PAD = "".join(f"\tv{k} := {k}\n\t_ = v{k}\n" for k in range(8))

A = """package a

import "example.invalid/m/core"

func Alpha() int {
""" + PAD + """	n, _ := core.Shared("x")
	return n
}

func AlphaTwo() int {
""" + PAD + """	n, _ := core.Shared("y")
	return n + 1
}

func alphaLocal() int {
	return 3
}
"""

B = """package b

import "example.invalid/m/core"

func Beta(r core.Record) int {
""" + PAD + """	n, _ := core.Shared(r.ID)
	return n
}

func BetaMore() int {
	return 7
}
"""

C = """package c

func Gamma() int { return 4 }
"""

DOC = """# Guide

Callers use `Shared` from core.

""" + "".join(f"Line {k} of the guide's body.\n" for k in range(12))


def write(root, rel, text):
    path = os.path.join(root, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        f.write(text)


def fixture(root, extra=None):
    files = {"core/core.go": CORE, "core/core_test.go": "package core\n\nfunc TestShared() { Shared(\"\") }\n",
             "a/a.go": A, "a/a_test.go": "package a\n\nfunc TestAlpha() { Alpha() }\n", "b/b.go": B,
             "b/b_test.go": "package b\n\nfunc TestBeta() { BetaMore() }\n", "c/c.go": C, "docs/guide.md": DOC,
             "a/testdata/fixture.go": "package fixture\n\nvar x = Shared\n"}
    files.update(extra or {})
    for rel, text in files.items():
        write(root, rel, text)
    git(root, "init", "-q")
    git(root, "add", "-A")
    git(root, "commit", "-q", "-m", "base")
    return git(root, "rev-parse", "HEAD")


def run(*args, env=None):
    return subprocess.run([sys.executable, TRACETREE, "segment", *args], capture_output=True, text=True,
                          timeout=60, env=env)


X = """package x

import "example.invalid/m/svc/y"

func Xenon() int {
	return y.Yttrium()
}
"""

Y = """package y

func Yttrium() int {
	return 7
}
"""


def tracer_run(base, prefix, at):
    """A tracetree run directory over base, its one thread walking from svc/x into svc/y (addresses below base)."""
    rdir = tempfile.mkdtemp(prefix="segment-run-")
    with open(os.path.join(rdir, "ROOT"), "w") as f:
        f.write(base + "\n")
    hops = [f"@ {prefix}{at}", "> \treturn y.Yttrium()", "= x hands the call to y", f"@ {prefix}y/y.go:4", "> \treturn 7",
            "= y answers"]
    with open(os.path.join(rdir, "1.hops"), "w") as f:
        f.write("\n".join(["THREAD 1: what x gets back from y", "ANSWER", f"- Xenon returns y.Yttrium [{prefix}{at}]",
                           *hops, f"END returned: seven @ {prefix}y/y.go:4"]) + "\n")
    return rdir


class SegmentCommandTest(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp(prefix="segment-test-")
        self.addCleanup(shutil.rmtree, self.root, True)

    def json_of(self, *args):
        done = run(*args, "--json")
        self.assertEqual(done.returncode, 0, done.stderr)
        return json.loads(done.stdout)

    def test_a_shared_seed_goes_to_the_spine_and_each_calling_package_is_a_segment(self):
        sha = fixture(self.root)
        done = run(self.root, "--rev", sha, "--seed", "Shared", "--floor", "0", "--read-max", "1")
        self.assertEqual(done.returncode, 0, done.stderr)
        out = done.stdout
        self.assertRegex(out, r"(?m)^SPINE core/core\.go .*Shared")
        segs = [l for l in out.splitlines() if l.startswith("SEGMENT S")]
        cells = sorted(l.split(" · ")[1] for l in segs)
        self.assertEqual(cells, ["a", "b", "docs"])
        self.assertRegex(out, r"(?m)^VERDICT SPLIT 3")
        self.assertNotIn("testdata", out)
        self.assertRegex(out, r"(?m)^  ORDER S\d · .*Shared")
        self.assertRegex(out, r"(?m)^  ESCALATE S\d · ")
        again = run(self.root, "--rev", sha, "--seed", "Shared", "--floor", "0", "--read-max", "1",
                    env=dict(os.environ, PYTHONHASHSEED="7"))
        self.assertEqual(again.stdout, out)  # deterministic: the same ROOT, rev and seeds print the same bytes

    def test_seeds_reaching_no_file_are_empty_each_with_its_reason_never_solo(self):
        # a wire key 61 files name and nothing defines: BROAD drops every mention
        sha = fixture(self.root, {f"notes/n{k}/n.txt": "sends wire-key-everywhere\n" for k in range(61)})
        cases = [
            (["NoSuchSymbolAnywhere"], {"NoSuchSymbolAnywhere": "no tracked line"}),
            (["use=Shared", "NoSuchSymbolAnywhere", "id"],
             {"use=Shared": "contract", "NoSuchSymbolAnywhere": "no tracked line", "id": "too short or common"}),
            (["wire-key-everywhere"], {"wire-key-everywhere": "BROAD: 61 files name it and none defines it"}),
            (["gone=core/old.go"], {"gone=core/old.go": "no tracked file"}),  # never a file to create
            (["use=core/iface.go"], {"use=core/iface.go": "no tracked file"}),
        ]
        for seeds, why in cases:
            with self.subTest(seeds=seeds):
                done = run(self.root, "--rev", sha, *[x for s in seeds for x in ("--seed", s)])
                self.assertEqual(done.returncode, 1, done.stdout + done.stderr)
                self.assertRegex(done.stdout, r"(?m)^VERDICT EMPTY")
                for seed, reason in why.items():
                    self.assertRegex(done.stdout, r"(?m)^NOTFOUND " + re.escape(seed) + " — .*" + re.escape(reason))
                self.assertNotRegex(done.stdout, r"(?m)^SEED ")
                self.assertNotIn("SOLO", done.stdout)

    def test_a_seed_found_nowhere_beside_a_found_one_is_named(self):
        sha = fixture(self.root)
        done = run(self.root, "--rev", sha, "--seed", "Shared", "--seed", "Missing99", "--floor", "0")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertRegex(done.stdout, r"(?m)^NOTFOUND Missing99")

    def test_an_input_it_cannot_read_fails_loudly(self):
        sha = fixture(self.root)
        latin = os.path.join(self.root, "..", os.path.basename(self.root) + "-latin1.txt")
        self.addCleanup(lambda: os.path.exists(latin) and os.remove(latin))
        with open(latin, "wb") as f:
            f.write("R1: Shared — the caller's contract\n".encode("cp1252"))  # a dash pasted from a document
        bad_run = tempfile.mkdtemp(prefix="segment-run-")
        self.addCleanup(shutil.rmtree, bad_run, True)
        with open(os.path.join(bad_run, "ROOT"), "wb") as f:
            f.write((self.root + "-r\u00e9po\n").encode("cp1252"))  # a ROOT path git never wrote
        cases = [
            ("a tracer run whose ROOT is not UTF-8", ["--rev", sha, "--seed", "Shared", "--variant", "tracer", "--run",
                                                      bad_run], "UnicodeDecodeError"),
            ("a revision naming no commit", ["--rev", "deadbeefdeadbeef", "--seed", "Shared"], "deadbeefdeadbeef"),
            ("a seeds file that is not UTF-8", ["--rev", sha, "--seeds", latin], "--seeds"),
        ]
        for name, args, why in cases:
            with self.subTest(name):
                done = run(self.root, *args)
                self.assertEqual(done.returncode, 2, done.stdout + done.stderr)
                self.assertRegex(done.stderr, r"^TRACETREE FAILED — .*" + re.escape(why))
                self.assertEqual(done.stdout, "")

    def test_segments_under_the_floor_fold_into_the_parent_and_the_verdict_says_solo(self):
        sha = fixture(self.root)
        done = run(self.root, "--rev", sha, "--seed", "Shared", "--floor", "100000")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertRegex(done.stdout, r"(?m)^VERDICT SOLO — ")
        self.assertNotRegex(done.stdout, r"(?m)^SEGMENT S")

    def test_a_segment_whose_order_needs_most_of_its_code_folds_back(self):
        sha = fixture(self.root)
        done = run(self.root, "--rev", sha, "--seed", "Shared", "--floor", "0", "--read-max", "0.01")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertRegex(done.stdout, r"(?m)^VERDICT SOLO — ")
        self.assertRegex(done.stdout, r"(?m)^PARENT .*read")

    def test_a_package_qualified_seed_restricts_its_references(self):
        sha = fixture(self.root)
        done = run(self.root, "--rev", sha, "--seed", "core.Shared", "--floor", "0", "--read-max", "1", "--json")
        self.assertEqual(done.returncode, 0, done.stderr)
        doc = json.loads(done.stdout)
        blast = sorted(doc["blast"])
        self.assertIn("a/a.go", blast)
        self.assertNotIn("docs/guide.md", blast)

    def test_within_limits_the_blast(self):
        sha = fixture(self.root)
        done = run(self.root, "--rev", sha, "--seed", "Shared", "--within", "a", "--within", "core", "--json",
                   "--floor", "0", "--read-max", "1")
        self.assertEqual(done.returncode, 0, done.stderr)
        doc = json.loads(done.stdout)
        self.assertTrue(all(p.startswith(("a/", "core/")) for p in doc["blast"]), doc["blast"])

    def test_a_path_seed_blasts_the_file_and_its_mentions(self):
        sha = fixture(self.root, {"tools/port.py": "def main():\n    return 1\n",
                                  "docs/tools.md": "Run `tools/port.py` to port.\n"})
        done = run(self.root, "--rev", sha, "--seed", "gone=tools/port.py", "--json", "--floor", "0")
        self.assertEqual(done.returncode, 0, done.stderr)
        doc = json.loads(done.stdout)
        self.assertEqual(sorted(doc["blast"]), ["docs/tools.md", "tools/port.py"])
        edited = json.loads(run(self.root, "--rev", sha, "--seed", "tools/port.py", "--json", "--floor", "0").stdout)
        self.assertEqual(edited["blast"], ["tools/port.py"])  # an edited file's mentions stay as they are

    def test_the_worktree_is_read_when_no_revision_is_given(self):
        fixture(self.root)
        write(self.root, "c/c.go", C + "\nfunc UsesShared() { Shared(\"z\") }\n")
        done = run(self.root, "--seed", "Shared", "--json", "--floor", "0")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertIn("c/c.go", json.loads(done.stdout)["blast"])

    def test_the_package_variant_keeps_the_contract_package_a_unit_with_no_spine(self):
        sha = fixture(self.root)
        pkg = self.json_of(self.root, "--rev", sha, "--seed", "Shared", "--variant", "package", "--floor", "0",
                           "--read-max", "1")
        self.assertEqual(pkg["spine"], [])  # package: top-level directories, no spine cut
        self.assertIn(["core"], [g["cells"] for g in pkg["segments"]] + [[c] for c in pkg["parent"]["cells"]])

    def test_max_packs_the_lightest_segment_into_its_closest_partner(self):
        sha = fixture(self.root)
        doc = self.json_of(self.root, "--rev", sha, "--seed", "Shared", "--floor", "0", "--read-max", "1", "--max", "2")
        self.assertEqual(doc["verdict"], "SPLIT")
        self.assertEqual(len(doc["segments"]), 2)
        self.assertEqual(sorted(c for g in doc["segments"] for c in g["cells"]), ["a", "b", "docs"])
        solo = run(self.root, "--rev", sha, "--seed", "Shared", "--floor", "0", "--read-max", "1", "--max", "1")
        self.assertRegex(solo.stdout, r"(?m)^VERDICT SOLO — one segment ")

    def test_a_use_seed_is_a_frozen_contract_that_blasts_none_of_its_callers(self):
        sha = fixture(self.root)
        done = run(self.root, "--rev", sha, "--seed", "use=Shared", "--seed", "a/a.go", "--seed", "b/b.go",
                   "--floor", "0", "--read-max", "1")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertRegex(done.stdout, r"(?m)^  CONTRACT Shared · core/core\.go:4 · .* · used by S1, S2$")
        self.assertEqual(len(re.findall(r"(?m)^  ORDER S\d · .* · contracts Shared$", done.stdout)), 2, done.stdout)
        doc = json.loads(run(self.root, "--rev", sha, "--seed", "use=Shared", "--seed", "a/a.go", "--seed", "b/b.go",
                             "--json", "--floor", "0", "--read-max", "1").stdout)
        self.assertEqual(doc["blast"], ["a/a.go", "b/b.go"])  # neither core/ nor docs/guide.md

    def test_a_file_the_request_creates_is_placed_and_its_new_symbol_becomes_a_contract(self):
        sha = fixture(self.root)
        seeds = os.path.join(self.root, "..", os.path.basename(self.root) + "-seeds.txt")
        self.addCleanup(lambda: os.path.exists(seeds) and os.remove(seeds))
        with open(seeds, "w") as f:
            f.write("# rows of the request\nR1: core/lines.go ReadLines a/a.go b/b.go\n")
        done = run(self.root, "--rev", sha, "--seeds", seeds, "--floor", "0", "--read-max", "1")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertRegex(done.stdout, r"(?m)^  CONTRACT ReadLines · not written yet · `NEW ReadLines — .*home core/lines\.go")
        self.assertRegex(done.stdout, r"(?m)^  ORDER S\d · core/lines\.go: create core/lines\.go")
        self.assertNotRegex(done.stdout, r"(?m)^NOTFOUND (ReadLines|core/lines\.go)")
        lone = run(self.root, "--rev", sha, "--seed", "R2=ReadLines", "--seed", "R2=a/a.go", "--floor", "0")
        self.assertRegex(lone.stdout, r"(?m)^NOTFOUND ReadLines")  # one cell: no contract to write first

    def test_a_name_too_short_or_common_to_grep_is_named_not_searched(self):
        sha = fixture(self.root)
        done = run(self.root, "--rev", sha, "--seed", "id", "--seed", "load", "--seed", "Shared", "--floor", "0")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertRegex(done.stdout, r"(?m)^NOTFOUND id — too short or common")
        self.assertRegex(done.stdout, r"(?m)^NOTFOUND load — too short or common")
        self.assertRegex(done.stdout, r"(?m)^SEED Shared ")

    def test_a_large_tree_answers_well_under_ten_seconds(self):
        extra = {f"p{i}/f{j}.go": f"package p{i}\n\nfunc F{i}x{j}() {{ Shared(\"{j}\") }}\n" for i in range(40)
                 for j in range(25)}
        sha = fixture(self.root, extra)
        t = time.monotonic()
        done = run(self.root, "--rev", sha, "--seed", "Shared", "--floor", "0")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertLess(time.monotonic() - t, 10)
        self.assertRegex(done.stdout, r"(?m)^SEED Shared · .* · BROAD 100\d files — kept only its own cells")

    def test_each_seed_form_resolves_to_its_kind_and_definition(self):
        sha = fixture(self.root, {
            "core/record.go": "package core\n\nfunc (r *Record) Label() string {\n\treturn r.ID\n}\n",
            "b/label.go": "package b\n\nimport \"example.invalid/m/core\"\n\nfunc Show(r core.Record) string { return r.Label() }\n",
            "c/other.go": "package c\n\ntype Other struct{}\n\nfunc (o *Other) Label() string { return \"\" }\n"})
        cases = [
            ("Shared", "symbol", ["core/core.go:4"]),
            ("core.Shared", "symbol", ["core/core.go:4"]),
            ("Record.Label", "symbol", ["core/record.go:3"]),  # the type picks its method, never Other.Label
            ("core/core.go", "path", ["core/core.go"]),
            ("core/", "path", ["core/core.go", "core/core_test.go", "core/record.go"]),
            ("a/a.go:20", "path", ["a/a.go:20"]),
            ("gone=c/c.go", "path", ["c/c.go"]),
        ]
        for seed, kind, defs in cases:
            with self.subTest(seed=seed):
                done = run(self.root, "--rev", sha, "--seed", seed, "--json", "--floor", "0")
                self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
                got = json.loads(done.stdout)["seeds"][0]
                self.assertEqual((got["kind"], sorted(got["defs"]), got["notfound"]), (kind, defs, None))

    def test_a_tracked_file_the_work_tree_cannot_read_fails_loudly(self):
        fixture(self.root)
        os.remove(os.path.join(self.root, "a", "a.go"))
        os.mkfifo(os.path.join(self.root, "a", "a.go"))  # a read would block forever
        done = run(self.root, "--seed", "a/a.go", "--floor", "0")
        self.assertEqual(done.returncode, 2, done.stdout)
        self.assertRegex(done.stderr, r"^TRACETREE FAILED — .*a/a\.go")
        self.assertEqual(done.stdout, "")

    def test_a_work_tree_read_stays_in_the_repository_and_matches_the_commit(self):
        outside = tempfile.mkdtemp(prefix="segment-outside-")
        self.addCleanup(shutil.rmtree, outside, True)
        write(outside, "big.txt", "".join(f"row {k}\n" for k in range(300)))
        write(outside, "attrs", "* linguist-generated\n")
        write(outside, "c/c.go", "package c\n" + "// far\n" * 300)
        fixture(self.root, {"a/zz.go": "package a\n" + "// pad\n" * 40, "a/odd\nname.go": "package a\n"})
        os.symlink("loop.go", os.path.join(self.root, "a", "loop.go"))  # resolves to itself
        os.symlink(os.path.join(outside, "big.txt"), os.path.join(self.root, "a", "out.txt"))
        os.symlink(os.path.join(outside, "attrs"), os.path.join(self.root, ".gitattributes"))
        git(self.root, "add", "-A")
        git(self.root, "commit", "-q", "-m", "links")
        head = git(self.root, "rev-parse", "HEAD")
        args = ("--seed", "a/", "--floor", "0", "--read-max", "1")
        tree, at = self.json_of(self.root, *args), self.json_of(self.root, "--rev", head, *args)
        shape = lambda d: (d["blast"], d["parent"], [(g["cells"], g["files"], g["weight"]) for g in d["segments"]])
        self.assertEqual(shape(tree), shape(at))  # a link reads as its text, as the commit holds it
        self.assertNotIn("a/odd\nname.go", tree["blast"])  # a name no line-based git output can carry
        shutil.rmtree(os.path.join(self.root, "c"))
        os.symlink(os.path.join(outside, "c"), os.path.join(self.root, "c"))  # git sees c/c.go deleted
        far = self.json_of(self.root, "--seed", "c/c.go", "--floor", "0", "--read-max", "1")
        self.assertEqual([g["weight"] for g in far["segments"]], [1])  # never the 301 lines outside

    def test_the_tracer_variant_joins_the_cells_a_run_links(self):
        sha = fixture(self.root, {"svc/x/x.go": X, "svc/y/y.go": Y})
        args = ("--rev", sha, "--seed", "svc/x/x.go", "--seed", "svc/y/y.go", "--floor", "0", "--read-max", "1")
        self.assertEqual(len(self.json_of(self.root, *args)["segments"]), 2)
        for name, base, prefix in (("a run over the repository", self.root, "svc/"),
                                   ("a run over a subdirectory", os.path.join(self.root, "svc"), "")):
            with self.subTest(name):
                rdir = tracer_run(base, prefix, "x/x.go:6")
                self.addCleanup(shutil.rmtree, rdir, True)
                doc = self.json_of(self.root, *args, "--variant", "tracer", "--run", rdir)
                self.assertEqual([g["cells"] for g in doc["segments"]], [["svc/x", "svc/y"]])
        elsewhere = os.path.join(self.root, "other")
        write(elsewhere, "x/x.go", X)
        write(elsewhere, "y/y.go", Y)
        rdir = tracer_run(elsewhere, "", "x/x.go:6")  # verified hops, none in the blast
        self.addCleanup(shutil.rmtree, rdir, True)
        done = run(self.root, *args, "--variant", "tracer", "--run", rdir)
        self.assertEqual(done.returncode, 2, done.stdout)
        self.assertRegex(done.stderr, r"^TRACETREE FAILED — --run .*no file of the blast")

    def test_the_cochange_variant_joins_cells_that_change_together(self):
        fixture(self.root, {"caf\u00e9/c.go": "package cafe\n", "na\u00efve/n.go": "package naive\n"})
        for k in range(2):
            for rel in ("caf\u00e9/c.go", "na\u00efve/n.go"):
                with open(os.path.join(self.root, rel), "a") as f:
                    f.write(f"// change {k}\n")
            git(self.root, "commit", "-q", "-am", f"change {k}")
        args = ("--seed", "caf\u00e9/c.go", "--seed", "na\u00efve/n.go", "--floor", "0", "--read-max", "1")
        self.assertEqual(len(self.json_of(self.root, *args)["segments"]), 2)
        co = self.json_of(self.root, *args, "--variant", "cochange")
        self.assertEqual([g["cells"] for g in co["segments"]], [["caf\u00e9", "na\u00efve"]])

    def test_the_named_root_is_read_whatever_git_variables_the_caller_carries(self):
        fixture(self.root)
        other = tempfile.mkdtemp(prefix="segment-other-")
        self.addCleanup(shutil.rmtree, other, True)
        write(other, "README.txt", "another repository\n")
        git(other, "init", "-q")
        git(other, "add", "-A")
        git(other, "commit", "-q", "-m", "other")
        done = run(self.root, "--seed", "Shared", "--floor", "0",
                   env=dict(os.environ, GIT_DIR=os.path.join(other, ".git"), GIT_WORK_TREE=other))  # as a hook runs
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        self.assertRegex(done.stdout, r"(?m)^SPINE core/core\.go ")

    def test_a_row_head_in_a_seeds_file_ties_its_seeds_and_is_never_a_seed(self):
        # the doors file's `{row}:` line head, whatever the row's name: `A:`, `R2:`, `door-3:`, `ui.pick:`
        sha = fixture(self.root)
        seeds = os.path.join(self.root, "..", os.path.basename(self.root) + "-doors.txt")
        self.addCleanup(lambda: os.path.exists(seeds) and os.remove(seeds))
        with open(seeds, "w") as f:
            f.write("A: Shared\nR2: use=Record\ndoor-3: gone=c/c.go\nui.pick: a/a.go # the alpha caller\nB:\n")
        done = run(self.root, "--rev", sha, "--seeds", seeds, "--floor", "0")
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        self.assertNotRegex(done.stdout, r"(?m)^NOTFOUND ")
        got = json.loads(run(self.root, "--rev", sha, "--seeds", seeds, "--floor", "0", "--json").stdout)["seeds"]
        self.assertEqual([(s["row"], s["raw"]) for s in got],
                         [("A", "Shared"), ("R2", "use=Record"), ("door-3", "gone=c/c.go"), ("ui.pick", "a/a.go")])

    def seeds_file(self, text):
        path = os.path.join(self.root, "..", os.path.basename(self.root) + "-decide.txt")
        self.addCleanup(lambda: os.path.exists(path) and os.remove(path))
        with open(path, "w") as f:
            f.write(text)
        return path

    def decide(self, out):
        """{segment cells: (CHILD|KEEP, reason)} from the text output."""
        cells = dict(re.findall(r"(?m)^SEGMENT (S\d+) · ([^·]+) · ", out))
        return {cells[sid].strip(): (cut, why) for sid, cut, why in
                re.findall(r"(?m)^  DECIDE (S\d+) (CHILD|KEEP) · (.+)$", out)}

    def test_a_new_seed_is_row_tied_placed_and_never_notfound(self):
        sha = fixture(self.root)
        seeds = self.seeds_file("A: new=Foo\n")
        got = json.loads(run(self.root, "--rev", sha, "--seeds", seeds, "--seed", "R1=new=Id", "--seed", "R1=a/a.go",
                             "--floor", "0", "--json").stdout)["seeds"]
        self.assertEqual([(s["row"], s["raw"], s["kind"], s["notfound"]) for s in got],
                         [("R1", "new=Id", "new", None), ("R1", "a/a.go", "path", None), ("A", "new=Foo", "new", None)])

    def test_a_segment_building_a_new_seed_is_a_child_and_one_without_is_kept(self):
        sha = fixture(self.root)
        seeds = self.seeds_file("R1: a/a.go new=AlphaCache\nR2: b/b.go\nR3: new=c/cache.go\n")
        done = run(self.root, "--rev", sha, "--seeds", seeds, "--floor", "0", "--read-max", "1", "--child-min", "0")
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        self.assertNotRegex(done.stdout, r"(?m)^NOTFOUND ")
        self.assertEqual(self.decide(done.stdout), {"a": ("CHILD", "builds new=AlphaCache"),
                                                    "b": ("KEEP", "1 row, no new symbol"),
                                                    "c": ("CHILD", "builds new=c/cache.go")})
        self.assertRegex(done.stdout, r"(?m)^  ORDER S\d · .*new=AlphaCache: create it")
        self.assertRegex(done.stdout, r"(?m)^VERDICT SPLIT 3 — .* · CHILD 2$")
        for sid in re.findall(r"(?m)^SEGMENT (S\d+) ", done.stdout):  # each DECIDE right after its ESCALATE
            self.assertRegex(done.stdout, rf"(?m)^  ESCALATE {sid} · .*\n  DECIDE {sid} ")
        doc = json.loads(run(self.root, "--rev", sha, "--seeds", seeds, "--floor", "0", "--read-max", "1", "--child-min", "0",
                             "--json").stdout)
        self.assertEqual(doc["child"], 2)
        self.assertEqual({g["cells"][0]: g["decide"]["cut"] for g in doc["segments"]},
                         {"a": "CHILD", "b": "KEEP", "c": "CHILD"})
        self.assertEqual([g["decide"]["reason"] for g in doc["segments"] if g["cells"] == ["a"]],
                         ["builds new=AlphaCache"])

    def test_a_segment_carrying_three_rows_is_a_child(self):
        sha = fixture(self.root)
        seeds = self.seeds_file("R1: Alpha\nR2: AlphaTwo\nR3: alphaLocal\nR4: b/b.go\n")
        done = run(self.root, "--rev", sha, "--seeds", seeds, "--floor", "0", "--read-max", "1", "--child-min", "0")
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        self.assertEqual(self.decide(done.stdout), {"a": ("CHILD", "3 rows (R1, R2, R3)"),
                                                    "b": ("KEEP", "1 row, no new symbol")})
        self.assertRegex(done.stdout, r"(?m)^VERDICT SPLIT 2 — .* · CHILD 1$")

    def test_a_segment_under_the_child_floor_is_kept_whatever_its_rows(self):
        sha = fixture(self.root)
        seeds = self.seeds_file("R1: Alpha\nR2: AlphaTwo\nR3: alphaLocal\nR4: b/b.go\n")
        done = run(self.root, "--rev", sha, "--seeds", seeds, "--floor", "0", "--read-max", "1")
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        got = self.decide(done.stdout)
        self.assertEqual(got["a"][0], "KEEP")
        self.assertRegex(got["a"][1], r"^3 rows \(R1, R2, R3\), but its doors read \d+ lines, under the 300-line child floor$")
        self.assertRegex(done.stdout, r"(?m)^VERDICT SPLIT 2 — .* · CHILD 0$")

    def test_call_site_swaps_or_one_function_are_kept(self):
        sha = fixture(self.root)
        done = run(self.root, "--rev", sha, "--seed", "Shared", "--floor", "0", "--read-max", "1")
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        self.assertEqual(self.decide(done.stdout), {c: ("KEEP", "call-site swaps only") for c in ("a", "b", "docs")})
        self.assertRegex(done.stdout, r"(?m)^VERDICT SPLIT 3 — .* · CHILD 0$")
        one = run(self.root, "--rev", sha, "--seed", "Gamma", "--floor", "0", "--read-max", "1")
        self.assertEqual(self.decide(one.stdout), {"c": ("KEEP", "one function (Gamma)")})
        self.assertRegex(one.stdout, r"(?m)^VERDICT SOLO — .* · CHILD 0$")

    def test_a_new_seed_whose_row_spans_two_segments_is_spine(self):
        sha = fixture(self.root)
        seeds = self.seeds_file("R1: a/a.go b/b.go new=Ledger\n")
        done = run(self.root, "--rev", sha, "--seeds", seeds, "--floor", "0", "--read-max", "1")
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        self.assertRegex(done.stdout, r"(?m)^SPINE .*Ledger")
        self.assertRegex(done.stdout, r"(?m)^  NEW Ledger · rows R1 · freeze as a compiling stub$")
        self.assertEqual(self.decide(done.stdout), {"a": ("KEEP", "1 row, no new symbol"),
                                                    "b": ("KEEP", "1 row, no new symbol")})
        self.assertRegex(done.stdout, r"(?m)^VERDICT SPLIT 2 — .* · CHILD 0$")
        spine = json.loads(run(self.root, "--rev", sha, "--seeds", seeds, "--floor", "0", "--read-max", "1",
                               "--json").stdout)["spine"]
        self.assertEqual([(x["name"], x["rows"], x["used_by"]) for x in spine if x.get("new")],
                         [("Ledger", ["R1"], ["S1", "S2"])])


if __name__ == "__main__":
    unittest.main()
