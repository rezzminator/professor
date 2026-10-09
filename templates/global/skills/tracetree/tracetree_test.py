import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import unittest

HERE = os.path.dirname(os.path.realpath(__file__))
# TRACETREE_SCRIPT lets a watched-failure proof point this suite at a mutated copy of the script.
SCRIPT = os.environ.get("TRACETREE_SCRIPT") or os.path.join(HERE, "tracetree.py")

SERVER = [
    "package srv",
    "",
    "func Handle(req Request) error {",
    "\tif req.Target == \"\" {",
    "\t\treturn errors.New(\"target is required\")",
    "\t}",
    "\treturn kill.Run(req.Target)",
    "}",
]
KILL = [
    "package kill",
    "",
    "// Run closes the pane.",
    "func Run(target string) error {",
    "\tif err := exec.Command(\"tmux\", \"kill-pane\", \"-t\", target).Run(); err != nil {",
    "\t\treturn fmt.Errorf(\"kill pane %s: %w\", target, err)",
    "\t}",
    "\treturn nil",
    "}",
]


def tt(args, input=None, timeout=60):
    return subprocess.run([sys.executable, SCRIPT] + args, input=input, capture_output=True, text=True,
                          timeout=timeout)


def hops(node, answer, hop_lines, tail):
    return "\n".join([f"THREAD {node}: the handler's error path", "ANSWER", *answer, *hop_lines, *tail]) + "\n"


class Repo(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp(prefix="tracetree-root-")
        for rel, lines in (("app/srv/server.go", SERVER), ("app/kill/kill.go", KILL)):
            path = os.path.join(self.root, rel)
            os.makedirs(os.path.dirname(path))
            with open(path, "w") as f:
                f.write("\n".join(lines) + "\n")
        self.run_dir = tempfile.mkdtemp(prefix="tracetree-run-")
        with open(os.path.join(self.run_dir, "ROOT"), "w") as f:
            f.write(self.root + "\n")

    def tearDown(self):
        shutil.rmtree(self.root, ignore_errors=True)
        shutil.rmtree(self.run_dir, ignore_errors=True)

    def write(self, node, text):
        got = tt(["write", self.run_dir, node], input=text)
        self.assertEqual(got.returncode, 0, got.stderr)
        return got

    def store(self, node, text):
        """The hops file straight into the run directory, as an older run or a second write left it: no check of the
        hops, no rewrite of their addresses, no sidecar."""
        with open(os.path.join(self.run_dir, node + ".hops"), "w") as f:
            f.write(text)

    def wait(self, *ids):
        got = tt(["wait", self.run_dir, *ids, "--grace", "0", "--timeout", "30"])
        self.assertEqual(got.returncode, 0, got.stderr)
        return got.stdout

    def good(self, node="1"):
        return hops(node,
                    ["- Handle refuses an empty target with `target is required` [app/srv/server.go:5]"],
                    ["@ app/srv/server.go:5", "> \t\treturn errors.New(\"target is required\")",
                     "= the error returns to the caller"],
                    ["END returned: the error goes back to the caller @ app/srv/server.go:5"])


class NewTest(Repo):
    def test_new_creates_a_run_holding_the_root(self):
        got = tt(["new", self.root])
        self.assertEqual(got.returncode, 0, got.stderr)
        run = got.stdout.strip()
        project = os.path.basename(self.root)
        try:
            self.assertTrue(run.startswith(os.path.join("/tmp", project, "tracer") + os.sep), run)
            with open(os.path.join(run, "ROOT")) as f:
                self.assertEqual(f.read().strip(), os.path.abspath(self.root))
        finally:
            shutil.rmtree(os.path.join("/tmp", project), ignore_errors=True)

    def test_new_names_the_project_after_the_repository_not_its_worktree(self):
        repo = tempfile.mkdtemp(prefix=".tracetree-proj-")
        plain = tempfile.mkdtemp(prefix="tracetree-plain-")
        project = os.path.basename(repo).lstrip(".")
        git = ["git", "-c", "user.name=t", "-c", "user.email=t@example.com", "-C", repo]
        try:
            for cmd in (["init", "-q"], ["commit", "-q", "--allow-empty", "-m", "one"],
                        ["worktree", "add", "-q", os.path.join(repo, ".worktrees", "wt")]):
                done = subprocess.run(git + cmd, capture_output=True, text=True)
                self.assertEqual(done.returncode, 0, done.stderr)
            wt = os.path.join(repo, ".worktrees", "wt")
            for root in (repo, wt):
                got = tt(["new", root])
                self.assertEqual(got.returncode, 0, got.stderr)
                self.assertTrue(got.stdout.strip().startswith(os.path.join("/tmp", project, "tracer") + os.sep),
                                got.stdout)
            got = tt(["new", plain])
            self.assertEqual(got.returncode, 0, got.stderr)
            self.assertTrue(got.stdout.strip().startswith(os.path.join("/tmp", os.path.basename(plain), "tracer")
                                                          + os.sep), got.stdout)
        finally:
            for name in (project, os.path.basename(plain), "wt"):
                shutil.rmtree(os.path.join("/tmp", name), ignore_errors=True)
            shutil.rmtree(repo, ignore_errors=True)
            shutil.rmtree(plain, ignore_errors=True)

    def test_new_refuses_a_root_that_is_not_a_directory(self):
        got = tt(["new", os.path.join(self.root, "missing")])
        self.assertEqual(got.returncode, 2)
        self.assertIn("TRACETREE FAILED", got.stderr)


class StartTest(Repo):
    def test_start_registers_the_node_and_prints_its_from_window_numbered(self):
        got = tt(["start", self.run_dir, "2", "srv/server.go:5"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertTrue(os.path.exists(os.path.join(self.run_dir, "2.started")))
        self.assertIn("app/srv/server.go", got.stdout)
        self.assertRegex(got.stdout, r"\n\s+5\t\t\treturn errors.New")

    def test_start_registers_even_when_from_cannot_be_read(self):
        got = tt(["start", self.run_dir, "3", "nowhere.go:4"])
        self.assertEqual(got.returncode, 2)
        self.assertIn("not under", got.stderr)
        self.assertTrue(os.path.exists(os.path.join(self.run_dir, "3.started")))

    def test_start_refuses_an_id_that_is_not_dotted_numbers(self):
        got = tt(["start", self.run_dir, "1x", "app/srv/server.go:5"])
        self.assertEqual(got.returncode, 2)
        self.assertFalse(os.path.exists(os.path.join(self.run_dir, "1x.started")))


class StartQuestionTest(Repo):
    """A card whose QUESTION is Q{n} gets question n from {RUN}/questions printed above the FROM window."""

    def ask(self, text):
        with open(os.path.join(self.run_dir, "questions"), "w") as f:
            f.write(text)

    def test_a_card_question_numbered_q_n_prints_that_question_above_the_window(self):
        self.ask("1. What calls Handle?\n2) What does Run wrap?\n   and with what text\n")
        got = tt(["start", self.run_dir, "2", "srv/server.go:5", "Q2"])
        self.assertEqual(got.returncode, 0, got.stderr)
        lines = got.stdout.splitlines()
        self.assertEqual(lines[:3], ["QUESTION Q2: What does Run wrap?", "and with what text",
                                     "started 2 — app/srv/server.go"])
        self.assertRegex(got.stdout, r"\n\s+5\t\t\treturn errors.New")

    def test_any_other_question_text_prints_exactly_what_start_printed_before(self):
        self.ask("1. What calls Handle?\n")
        plain = tt(["start", self.run_dir, "2", "srv/server.go:5"])
        worded = tt(["start", self.run_dir, "3", "srv/server.go:5", "which tests pin Run"])
        self.assertEqual(worded.returncode, 0, worded.stderr)
        self.assertEqual(worded.stdout.replace("started 3", "started 2"), plain.stdout)

    def test_start_records_its_from_and_question_for_the_map_page(self):
        tt(["start", self.run_dir, "2", "srv/server.go:5", "Q2"])
        tt(["start", self.run_dir, "3", "nowhere.go:4"])
        for node, want in (("2", "srv/server.go:5\tQ2\n"), ("3", "nowhere.go:4\t\n")):
            with open(os.path.join(self.run_dir, node + ".started"), encoding="utf-8") as f:
                self.assertEqual(f.read(), want)

    def test_a_question_number_the_file_lacks_is_named_not_dropped(self):
        self.ask("1. What calls Handle?\n")
        got = tt(["start", self.run_dir, "2", "srv/server.go:5", "Q7"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertEqual(got.stdout.splitlines()[0], f"QUESTION Q7: (not in {self.run_dir}/questions)")
        os.remove(os.path.join(self.run_dir, "questions"))
        none = tt(["start", self.run_dir, "4", "srv/server.go:5", "Q1"])
        self.assertEqual(none.stdout.splitlines()[0], f"QUESTION Q1: (not in {self.run_dir}/questions)")


class WriteTest(Repo):
    def test_write_stores_once_and_refuses_a_second_write(self):
        self.write("1", self.good())
        again = tt(["write", self.run_dir, "1"], input=self.good().replace("handler", "rewritten"))
        self.assertEqual(again.returncode, 2)
        self.assertIn("already written", again.stderr)
        with open(os.path.join(self.run_dir, "1.hops")) as f:
            self.assertNotIn("rewritten", f.read())

    def test_write_refuses_an_incomplete_file_unstored(self):
        got = tt(["write", self.run_dir, "1"], input="THREAD 1: x\n@ app/srv/server.go:5\n> x\n")
        self.assertEqual(got.returncode, 2)
        self.assertIn("an ANSWER line", got.stderr)
        self.assertIn("an END or SPLIT line", got.stderr)
        self.assertFalse(os.path.exists(os.path.join(self.run_dir, "1.hops")))


class CheckTest(Repo):
    def test_hops_are_verified_re_addressed_or_rejected_against_the_file(self):
        self.store("1", hops("1", ["- x [app/kill/kill.go:5]"],
                             ["@ app/kill/kill.go:5", "> \tif err := exec.Command(\"tmux\", \"kill-pane\", \"-t\", target).Run(); err != nil {",
                              "@ app/kill/kill.go:2", "> \t\treturn fmt.Errorf(\"kill pane %s: %w\", target, err)",
                              "@ app/kill/kill.go:6", "> \t\treturn fmt.Errorf(\"no such text\")"],
                             ["END executed: tmux kill-pane @ app/kill/kill.go:5"]))
        got = tt(["check", self.run_dir, "--hops"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("app/kill/kill.go:5  if err := exec.Command", got.stdout)
        self.assertIn("app/kill/kill.go:6  return fmt.Errorf(\"kill pane %s: %w\", target, err)   (re-addressed from :2)",
                      got.stdout)
        self.assertIn("REJECTED app/kill/kill.go:6 — quote not in the file", got.stdout)
        self.assertIn("hops=3 · verified=2 · moved=1 · rejected=1", got.stdout)

    def test_answers_view_moves_a_cite_to_its_quoted_text_and_flags_an_unbacked_one(self):
        self.write("1", hops("1",
                             ["- Run wraps the tmux failure as `kill pane %s: %w` [app/kill/kill.go:1]",
                              "- Run ends [app/kill/kill.go:9]"],
                             ["@ app/kill/kill.go:5", "> \tif err := exec.Command(\"tmux\", \"kill-pane\", \"-t\", target).Run(); err != nil {"],
                             ["END executed: tmux kill-pane @ app/kill/kill.go:5"]))
        got = tt(["check", self.run_dir])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("`kill pane %s: %w` [app/kill/kill.go:6]   (↻ app/kill/kill.go:1→6)", got.stdout)
        self.assertIn("unbacked app/kill/kill.go:9 — no verified hop or quoted text there", got.stdout)
        self.assertIn("flagged=1 · recited=1", got.stdout)

    def test_a_bare_file_name_resolves_to_the_file_holding_the_quote(self):
        other = os.path.join(self.root, "app", "other", "server.go")
        os.makedirs(os.path.dirname(other))
        with open(other, "w") as f:
            f.write("package other\n\nfunc Serve() {}\n")
        self.store("1", hops("1", ["- Handle refuses an empty target with `target is required` [server.go:5]"],
                             ["@ server.go:5", "> \t\treturn errors.New(\"target is required\")"],
                             ["END returned: x @ server.go:5"]))
        got = tt(["check", self.run_dir, "--hops"])
        self.assertIn("app/srv/server.go:5  return errors.New(\"target is required\")   (path resolved from server.go)",
                      got.stdout)
        self.assertIn("rejected=0", got.stdout)
        answers = tt(["check", self.run_dir])
        self.assertIn("`target is required` [app/srv/server.go:5]", answers.stdout)
        self.assertIn("flagged=0", answers.stdout)

    def test_full_view_prints_every_line_of_a_quoted_range(self):
        self.write("1", hops("1", ["- x [app/srv/server.go:3]"],
                             ["@ app/srv/server.go:3", "> func Handle(req Request) error {",
                              "> \tif req.Target == \"\" {", "> \t\treturn errors.New(\"target is required\")"],
                             ["END returned: x @ app/srv/server.go:5"]))
        got = tt(["check", self.run_dir, "--full", "1"])
        self.assertEqual(got.returncode, 0, got.stderr)
        for n in (3, 4, 5):
            self.assertRegex(got.stdout, rf"\n\s+{n}\| ")

    def test_a_split_child_is_missing_only_when_it_started(self):
        self.write("1", hops("1", ["- x [app/srv/server.go:7]"],
                             ["@ app/srv/server.go:7", "> \treturn kill.Run(req.Target)"],
                             ["SPLIT 1.1: kill.Run @ app/srv/server.go:7", "SPLIT 1.2: logging @ app/srv/server.go:7"]))
        tt(["start", self.run_dir, "1.1", "app/kill/kill.go:4"])
        got = tt(["check", self.run_dir])
        self.assertIn("SPLIT 1.1: kill.Run @ app/srv/server.go:7   (✗ MISSING: this child started and wrote no file)",
                      got.stdout)
        self.assertIn("SPLIT 1.2: logging @ app/srv/server.go:7   (not spawned: no child ran under this ID)", got.stdout)
        self.assertIn("missing=1", got.stdout)

    def test_check_fails_loudly_on_a_run_with_no_hops_and_on_a_non_run(self):
        empty = tt(["check", self.run_dir])
        self.assertEqual(empty.returncode, 2)
        self.assertIn("holds no .hops file", empty.stderr)
        stray = tt(["check", self.root])
        self.assertEqual(stray.returncode, 2)
        self.assertIn("no ROOT file", stray.stderr)


class WaitTest(Repo):
    def test_wait_returns_once_every_started_node_has_written(self):
        tt(["start", self.run_dir, "1", "app/srv/server.go:3"])
        tt(["start", self.run_dir, "1.1", "app/kill/kill.go:4"])
        self.write("1", self.good())
        late = threading.Timer(0.5, lambda: self.write("1.1", self.good("1.1")))
        late.start()
        try:
            got = tt(["wait", self.run_dir, "1", "--grace", "0.5", "--timeout", "30"])
        finally:
            late.join()
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("WAIT done", got.stdout)
        self.assertIn("[1.1] THREAD 1.1", got.stdout)

    def test_wait_times_out_naming_a_started_node_that_never_wrote(self):
        tt(["start", self.run_dir, "2", "app/kill/kill.go:4"])
        self.write("1", self.good())
        began = time.time()
        got = tt(["wait", self.run_dir, "1", "--grace", "0.5", "--timeout", "1.5"])
        self.assertLess(time.time() - began, 20)
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("WAIT TIMEOUT after", got.stdout)
        self.assertIn("started but never wrote: 2", got.stdout)
        self.assertIn("COUNTS:", got.stdout)

    def test_wait_stops_waiting_for_a_named_node_that_never_started(self):
        self.write("1", self.good())
        began = time.time()
        got = tt(["wait", self.run_dir, "1", "2", "--start", "0.5", "--grace", "0.5", "--timeout", "30"])
        self.assertLess(time.time() - began, 15)
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("WAIT done after", got.stdout)
        self.assertIn("never started: 2", got.stdout)
        self.assertIn("[1] THREAD 1", got.stdout)

    def test_wait_ends_when_no_named_node_started(self):
        got = tt(["wait", self.run_dir, "1", "2", "--start", "0.5", "--grace", "0.5", "--timeout", "30"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("WAIT ended after", got.stdout)
        self.assertIn("never started: 1, 2", got.stdout)
        self.assertNotIn("COUNTS:", got.stdout)

    def test_wait_prints_only_the_nodes_it_waited_for_and_counts_the_whole_run(self):
        self.write("1", self.good())
        self.write("2", self.good("2"))
        got = tt(["wait", self.run_dir, "2", "--grace", "0.2", "--timeout", "30"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("[2] THREAD 2", got.stdout)
        self.assertIn("Handle refuses an empty target", got.stdout)
        self.assertNotIn("[1] THREAD 1", got.stdout)
        self.assertIn("nodes=2", got.stdout)


class OpenTest(Repo):
    """`wait` ends with the OPEN block: every NOT READ and LEAD line of the nodes it shows, tied to its node."""

    def node(self, name, extra, hop=("app/srv/server.go:5", "\t\treturn errors.New(\"target is required\")")):
        self.write(name, hops(name, ["- Handle refuses an empty target with `target is required` [app/srv/server.go:5]"],
                              [f"@ {hop[0]}", f"> {hop[1]}"], [*extra, "END returned: x @ app/srv/server.go:5"]))

    def test_a_not_read_line_ends_with_its_node(self):
        self.node("1", ["NOT READ whether Run retries after a failed kill"])
        out = self.wait("1").splitlines()
        self.assertEqual(out[-2:], ["OPEN", "NOT READ whether Run retries after a failed kill — run: node 1"])

    def test_a_cite_a_verified_hop_of_the_run_holds_is_not_flagged_and_one_none_holds_is(self):
        self.node("1", ["LEAD the handler refuses first @ app/srv/server.go:5",
                        "NOT READ what Run does at app/kill/kill.go:6 and at app/srv/server.go:5",
                        "NOT READ the wrapped error at app/kill/kill.go:6"])
        self.node("2", [], hop=("app/kill/kill.go:6", "\t\treturn fmt.Errorf(\"kill pane %s: %w\", target, err)"))
        held = self.wait("1").splitlines()
        self.assertEqual(held[-4], "OPEN")
        self.assertEqual(held[-3], "LEAD the handler refuses first @ app/srv/server.go:5 — run: node 1")
        self.assertEqual(held[-2], "NOT READ what Run does at app/kill/kill.go:6 and at app/srv/server.go:5 — run: node 1")
        self.assertEqual(held[-1], "NOT READ the wrapped error at app/kill/kill.go:6 — run: node 1")
        os.remove(os.path.join(self.run_dir, "2.hops"))
        os.remove(os.path.join(self.run_dir, "1.hops"))
        self.node("1", ["LEAD the handler refuses first @ app/srv/server.go:5",
                        "NOT READ the wrapped error at app/kill/kill.go:6"])
        flagged = self.wait("1").splitlines()
        self.assertEqual(flagged[-2], "LEAD the handler refuses first @ app/srv/server.go:5 — run: node 1")
        self.assertEqual(flagged[-1], "NOT READ the wrapped error at app/kill/kill.go:6 — run: node 1 (grep only — read it)")

    def test_nothing_to_list_prints_open_none_and_only_shown_nodes_are_listed(self):
        self.node("1", [])
        self.assertEqual(self.wait("1").splitlines()[-1], "OPEN: none")
        self.node("2", ["NOT READ the other thread"])
        self.assertEqual(self.wait("1").splitlines()[-1], "OPEN: none")
        self.assertEqual(self.wait("2").splitlines()[-2:], ["OPEN", "NOT READ the other thread — run: node 2"])


class CeilingTest(Repo):
    """`wait` prints at most 28000 characters and always keeps its full view in {RUN}/answers.txt."""

    def big(self, nodes=3, lines=60):
        for n in range(1, nodes + 1):
            node = str(n)
            filler = [f"- filler {i} of node {node}: " + "the handler checks the target before it kills the pane " * 4
                      + "[app/srv/server.go:5]" for i in range(lines)]
            self.write(node, hops(node, [*filler, "- Run ends [app/kill/kill.go:9]"],
                                  ["@ app/srv/server.go:5", "> \t\treturn errors.New(\"target is required\")"],
                                  [f"NOT READ whether node {node} retries", "END returned: x @ app/srv/server.go:5"]))

    def test_a_small_run_prints_whole_and_answers_txt_holds_the_same_view(self):
        self.write("1", self.good())
        out = self.wait("1")
        with open(os.path.join(self.run_dir, "answers.txt")) as f:
            self.assertEqual(f.read(), out)
        self.assertNotIn("FULL ", out)
        self.assertNotIn("lines: ", out)
        self.assertIn("[1] THREAD 1", out)

    def test_a_big_run_prints_at_most_28000_characters_and_keeps_counts_map_and_open(self):
        self.big()
        out = self.wait("1", "2", "3")
        self.assertLessEqual(len(out), 28000)
        lines = out.splitlines()
        self.assertTrue(lines[0].startswith("WAIT done"), lines[0])
        at = {name: next(i for i, l in enumerate(lines) if l.startswith(name)) for name in
              ("COUNTS: ", "NOT READ whether node 1", "OPEN", "MAP ", "[1] THREAD 1")}
        flagged = next(i for i, l in enumerate(lines) if "unbacked app/kill/kill.go:9" in l)
        self.assertLess(at["COUNTS: "], flagged)
        self.assertLess(flagged, at["OPEN"])
        self.assertLess(at["OPEN"], at["MAP "])
        self.assertLess(at["MAP "], at["[1] THREAD 1"])
        self.assertIn("NOT READ whether node 3 retries — run: node 3", out)
        self.assertEqual(lines[-1], f"FULL {self.run_dir}/answers.txt")

    def test_each_trimmed_node_says_how_many_lines_it_lost_and_answers_txt_holds_them_all(self):
        self.big()
        out = self.wait("1", "2", "3")
        for name in ("[1] THREAD 1", "[2] THREAD 2", "[3] THREAD 3"):
            self.assertIn(name, out)
        trimmed = re.findall(rf"^\+(\d+) lines: {re.escape(self.run_dir)}/answers\.txt$", out, re.M)
        self.assertEqual(len(trimmed), 3, out[-600:])
        with open(os.path.join(self.run_dir, "answers.txt")) as f:
            full = f.read()
        self.assertGreater(len(full), 28000)
        self.assertEqual(full.count("- filler "), 180)
        self.assertLess(out.count("- filler "), 180)


class ReadTest(Repo):
    """`read RUN FILE:A-B ...`: numbered blocks of at most 30 lines, one call per round, never a block a hop holds."""

    def read(self, *blocks):
        return tt(["read", self.run_dir, *blocks])

    def test_a_block_prints_under_its_header_numbered_by_the_files_own_lines(self):
        got = self.read("app/srv/server.go:3-5", "app/kill/kill.go:4")
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertEqual(got.stdout.splitlines(), [
            "== app/srv/server.go:3-5",
            "     3\tfunc Handle(req Request) error {",
            "     4\t\tif req.Target == \"\" {",
            "     5\t\t\treturn errors.New(\"target is required\")",
            "== app/kill/kill.go:4-4",
            "     4\tfunc Run(target string) error {"])

    def test_a_block_over_thirty_lines_is_refused_on_its_own_line_and_the_others_still_print(self):
        with open(os.path.join(self.root, "app", "big.txt"), "w") as f:
            f.write("".join(f"row {i}\n" for i in range(1, 60)))
        got = self.read("app/big.txt:1-31", "app/big.txt:10-39", "app/srv/server.go:8-8")
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertEqual(got.stdout.splitlines()[:1], ["REFUSED app/big.txt:1-31 — over 30 lines"])
        self.assertEqual(got.stdout.count("== app/big.txt:10-39"), 1)
        self.assertIn("    39\trow 39", got.stdout)
        self.assertIn("== app/srv/server.go:8-8\n     8\t}", got.stdout)

    def test_a_block_one_verified_hop_holds_whole_is_refused_naming_the_node(self):
        self.write("1", hops("1", ["- x [app/srv/server.go:3]"],
                             ["@ app/srv/server.go:3", "> func Handle(req Request) error {",
                              "> \tif req.Target == \"\" {", "> \t\treturn errors.New(\"target is required\")"],
                             ["END returned: x @ app/srv/server.go:5"]))
        got = self.read("app/srv/server.go:4-5", "app/srv/server.go:3-3", "app/srv/server.go:5-7")
        self.assertEqual(got.returncode, 0, got.stderr)
        lines = got.stdout.splitlines()
        self.assertEqual(lines[0], f"REFUSED app/srv/server.go:4-5 — node 1 holds it: tt check {self.run_dir} --full 1")
        self.assertEqual(lines[1], f"REFUSED app/srv/server.go:3-3 — node 1 holds it: tt check {self.run_dir} --full 1")
        self.assertEqual(lines[2], "== app/srv/server.go:5-7")
        self.assertEqual(len(lines), 6, lines)

    def test_a_missing_file_or_range_is_refused_with_its_reason(self):
        got = self.read("nowhere.go:1-3", "app/srv/server.go:6-12", "app/srv/server.go:0-2", "app/srv/server.go:x")
        self.assertEqual(got.returncode, 0, got.stderr)
        lines = got.stdout.splitlines()
        self.assertEqual(len(lines), 4, lines)
        self.assertTrue(lines[0].startswith("REFUSED nowhere.go:1-3 — no such file"), lines[0])
        self.assertTrue(lines[1].startswith("REFUSED app/srv/server.go:6-12 — "), lines[1])
        self.assertIn("8 lines", lines[1])
        self.assertTrue(lines[2].startswith("REFUSED app/srv/server.go:0-2 — "), lines[2])
        self.assertTrue(lines[3].startswith("REFUSED app/srv/server.go:x — "), lines[3])

    def test_a_second_read_call_fails_whole_until_a_wait_resets_the_count(self):
        self.write("1", self.good())
        first = self.read("app/srv/server.go:1-2")
        self.assertEqual(first.returncode, 0, first.stderr)
        second = self.read("app/srv/server.go:3-4")
        self.assertEqual(second.returncode, 2)
        self.assertEqual(second.stdout, "")
        self.assertIn("TRACETREE FAILED — one read per round: put every block in one call", second.stderr)
        waited = tt(["wait", self.run_dir, "1", "--grace", "0", "--timeout", "30"])
        self.assertEqual(waited.returncode, 0, waited.stderr)
        again = self.read("app/srv/server.go:3-4")
        self.assertEqual(again.returncode, 0, again.stderr)
        self.assertIn("== app/srv/server.go:3-4", again.stdout)

    def test_read_needs_a_run_and_a_block(self):
        self.assertEqual(tt(["read", self.run_dir]).returncode, 2)
        stray = tt(["read", self.root, "app/srv/server.go:1-2"])
        self.assertEqual(stray.returncode, 2)
        self.assertIn("no ROOT file", stray.stderr)


LOG = ["package log", "", "func Note(msg string) {", "\tprintln(msg)", "}"]
MAKEFILE = ["test:", "\t./scripts/check.sh all", "", "lint:", "\techo lint"]
CHECK_SH = ["#!/usr/bin/env bash", "set -euo pipefail", "", "main() {", "  go test ./...", "}", "", "main \"$@\""]
GUIDE = ["# Guide", "", "## Run", "", "```bash", "# not a heading", "make test", "```"]


class MapTest(Repo):
    def setUp(self):
        super().setUp()
        for rel, lines in (("app/log/log.go", LOG), ("Makefile", MAKEFILE), ("scripts/check.sh", CHECK_SH),
                           ("docs/guide.md", GUIDE)):
            path = os.path.join(self.root, rel)
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w") as f:
                f.write("\n".join(lines) + "\n")

    def walk(self, node="1", tail=("END executed: tmux kill-pane @ app/kill/kill.go:5",), extra=(), raw=False):
        (self.store if raw else self.write)(node, hops(node, ["- Handle hands the target to kill.Run [app/srv/server.go:7]"],
                              ["@ app/srv/server.go:7", "> \treturn kill.Run(req.Target)", "= the target goes to kill.Run",
                               "@ app/kill/kill.go:5",
                               "> \tif err := exec.Command(\"tmux\", \"kill-pane\", \"-t\", target).Run(); err != nil {",
                               "= tmux kills the pane", *extra], list(tail)))

    def test_map_declares_each_component_and_the_verified_call_between_them(self):
        self.walk()
        got = tt(["map", self.run_dir])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertTrue(got.stdout.startswith("MAP "), got.stdout)
        self.assertRegex(got.stdout, r"\napp/srv/server.go\n  Handle :3 · hops 7 · threads 1\n"
                                     r"    → app/kill/kill.go::Run — tmux kills the pane\n")
        self.assertRegex(got.stdout, r"\napp/kill/kill.go\n  Run :4 · hops 5 · threads 1\n    ■ executed — tmux kill-pane\n")

    def test_a_walk_order_link_shows_only_with_all_and_a_rejected_hop_is_no_component(self):
        self.walk(extra=["@ app/log/log.go:4", "> \tprintln(msg)", "= the message is printed",
                         "@ app/srv/server.go:2", "> \tghost()", "= a line no file holds"], raw=True)
        plain = tt(["map", self.run_dir])
        self.assertEqual(plain.returncode, 0, plain.stderr)
        self.assertIn("  Note :3 · hops 4", plain.stdout)
        self.assertNotIn("· app/log/log.go::Note", plain.stdout)
        self.assertNotIn("ghost", plain.stdout)
        every = tt(["map", self.run_dir, "--all"])
        self.assertIn("    · app/log/log.go::Note — the message is printed", every.stdout)

    def test_a_split_points_at_the_component_of_the_childs_first_verified_hop(self):
        self.write("1", hops("1", ["- x [app/srv/server.go:7]"],
                             ["@ app/srv/server.go:7", "> \treturn kill.Run(req.Target)"],
                             ["SPLIT 1.1: kill.Run @ app/srv/server.go:7"]))
        self.write("1.1", hops("1.1", ["- x [app/kill/kill.go:6]"],
                               ["@ app/kill/kill.go:6", "> \t\treturn fmt.Errorf(\"kill pane %s: %w\", target, err)"],
                               ["END returned: wrapped @ app/kill/kill.go:6"]))
        got = tt(["map", self.run_dir])
        self.assertIn("    ⇉ app/kill/kill.go::Run — split to 1.1", got.stdout)
        self.assertRegex(got.stdout, r"Run :4 · hops 6 · threads 1\.1\n    ■ returned — wrapped")

    def test_a_make_target_names_a_script_file_and_a_fenced_hash_line_is_no_section(self):
        self.write("1", hops("1", ["- make test runs check.sh [Makefile:2]"],
                             ["@ Makefile:2", "> \t./scripts/check.sh all", "= make runs the check script",
                              "@ scripts/check.sh:5", ">   go test ./...", "= the script runs go test",
                              "@ docs/guide.md:7", "> make test", "= the guide says make test"],
                             ["END executed: go test @ scripts/check.sh:5"]))
        got = tt(["map", self.run_dir, "--all"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("\nMakefile\n  test :1 · hops 2 · threads 1\n    ⇢ scripts/check.sh::main — the script runs go test",
                      got.stdout)
        self.assertIn("\ndocs/guide.md\n  Run :3 · hops 7", got.stdout)
        self.assertNotIn("not a heading", got.stdout)

    def test_a_same_named_method_call_is_no_call_to_a_package_function(self):
        path = os.path.join(self.root, "app", "exec", "exec.go")
        os.makedirs(os.path.dirname(path))
        with open(path, "w") as f:
            f.write("package exec\n\nfunc Start(cmd *Cmd) error {\n\treturn cmd.Run()\n}\n")
        self.write("1", hops("1", ["- x [app/exec/exec.go:4]"],
                             ["@ app/exec/exec.go:4", "> \treturn cmd.Run()", "= the command runs",
                              "@ app/kill/kill.go:5",
                              "> \tif err := exec.Command(\"tmux\", \"kill-pane\", \"-t\", target).Run(); err != nil {",
                              "= tmux kills the pane"],
                             ["END executed: tmux kill-pane @ app/kill/kill.go:5"]))
        got = tt(["map", self.run_dir, "--all"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("  Start :3 · hops 4 · threads 1\n    · app/kill/kill.go::Run", got.stdout)

    def test_from_prints_the_callee_tree_and_to_the_caller_tree(self):
        self.walk()
        down = tt(["map", self.run_dir, "--from", "Handle"])
        self.assertEqual(down.returncode, 0, down.stderr)
        self.assertRegex(down.stdout, r"Handle  app/srv/server.go:3\n└─ → Run  app/kill/kill.go:4\n   ■ executed")
        up = tt(["map", self.run_dir, "--to", "app/kill/kill.go::Run"])
        self.assertEqual(up.returncode, 0, up.stderr)
        self.assertRegex(up.stdout, r"Run  app/kill/kill.go:4\n└─ ← Handle  app/srv/server.go:3")
        none = tt(["map", self.run_dir, "--from", "Nowhere"])
        self.assertEqual(none.returncode, 2)
        self.assertIn("no component matches Nowhere", none.stderr)

    def test_json_carries_components_edges_threads_and_each_threads_walk(self):
        self.walk()
        got = tt(["map", self.run_dir, "--json"])
        self.assertEqual(got.returncode, 0, got.stderr)
        data = json.loads(got.stdout)
        ids = [c["id"] for c in data["components"]]
        self.assertEqual(ids, ["app/kill/kill.go::Run", "app/srv/server.go::Handle"])
        self.assertEqual([(e["from"], e["to"], e["kind"]) for e in data["edges"]],
                         [("app/srv/server.go::Handle", "app/kill/kill.go::Run", "call")])
        self.assertEqual([s["c"] for s in data["threads"]["1"]["walk"]],
                         ["app/srv/server.go::Handle", "app/kill/kill.go::Run"])

    def test_html_is_written_only_on_request_and_loads_nothing_from_the_network(self):
        self.walk()
        out = os.path.join(self.run_dir, "page.html")
        tt(["map", self.run_dir])
        self.assertFalse([n for n in os.listdir(self.run_dir) if n.endswith(".html")])
        got = tt(["map", self.run_dir, "--html", out])
        self.assertEqual(got.returncode, 0, got.stderr)
        with open(out) as f:
            page = f.read()
        self.assertIn("app/kill/kill.go::Run", page)
        self.assertNotRegex(page, r"(src|href)\s*=\s*[\"']?(https?:)?//")
        self.assertNotIn("@import", page)

    def test_wait_writes_the_text_map_beside_the_run_and_names_it(self):
        tt(["start", self.run_dir, "1", "app/srv/server.go:3"])
        self.walk()
        got = tt(["wait", self.run_dir, "1", "--grace", "0.2", "--timeout", "30"])
        self.assertEqual(got.returncode, 0, got.stderr)
        path = os.path.join(self.run_dir, "map.txt")
        self.assertIn(f"MAP {path} — 2 components", got.stdout)
        with open(path) as f:
            self.assertIn("→ app/kill/kill.go::Run", f.read())

    def test_the_map_line_names_the_hub_component_crossed_by_the_most_threads(self):
        self.walk()
        self.write("2", self.good("2"))
        tt(["start", self.run_dir, "1", "app/srv/server.go:3"])
        got = tt(["wait", self.run_dir, "1", "2", "--grace", "0.2", "--timeout", "30"])
        self.assertEqual(got.returncode, 0, got.stderr)
        line = next(l for l in got.stdout.splitlines() if l.startswith("MAP "))
        self.assertRegex(line, r" · hub app/srv/server\.go::Handle \(2 threads, 2 hops\)$")

    def test_the_file_block_with_the_most_hops_comes_first(self):
        self.write("1", hops("1", ["- x [app/srv/server.go:5]"],
                             ["@ app/srv/server.go:5", "> \t\treturn errors.New(\"target is required\")", "= refused",
                              "@ app/srv/server.go:7", "> \treturn kill.Run(req.Target)", "= handed on",
                              "@ app/kill/kill.go:5",
                              "> \tif err := exec.Command(\"tmux\", \"kill-pane\", \"-t\", target).Run(); err != nil {",
                              "= tmux kills the pane"],
                             ["END executed: tmux kill-pane @ app/kill/kill.go:5"]))
        got = tt(["map", self.run_dir])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertLess(got.stdout.index("\napp/srv/server.go\n"), got.stdout.index("\napp/kill/kill.go\n"))

    def page(self):
        out = os.path.join(self.run_dir, "page.html")
        got = tt(["map", self.run_dir, "--html", out])
        self.assertEqual(got.returncode, 0, got.stderr)
        with open(out, encoding="utf-8") as f:
            page = f.read()
        m = re.search(r'<script type="application/json" id="tracemap-data">(.*?)</script>', page, re.S)
        self.assertIsNotNone(m, "the page carries no data script")
        return page, json.loads(m.group(1))

    def test_json_steps_say_how_each_hop_holds_and_components_where_they_end(self):
        self.walk(extra=["@ app/srv/server.go:3", "> \treturn kill.Run(req.Target)", "= the call again, cited at the func"],
                  raw=True)
        data = json.loads(tt(["map", self.run_dir, "--json"]).stdout)
        self.assertEqual([(s["c"], s["line"], s["b"], s["status"]) for s in data["threads"]["1"]["walk"]],
                         [("app/srv/server.go::Handle", 7, 7, "verified"), ("app/kill/kill.go::Run", 5, 5, "verified"),
                          ("app/srv/server.go::Handle", 7, 7, "moved")])
        self.assertEqual({c["id"]: (c["line"], c["end"]) for c in data["components"]},
                         {"app/srv/server.go::Handle": (3, 8), "app/kill/kill.go::Run": (4, 9)})

    def test_the_page_carries_the_questions_each_nodes_checked_answers_its_clock_and_the_file_text(self):
        with open(os.path.join(self.run_dir, "questions"), "w") as f:
            f.write("1. What does Handle hand on?\n2. Who kills the pane?\n")
        tt(["start", self.run_dir, "1", "app/srv/server.go:3", "Q2"])
        tt(["start", self.run_dir, "2", "app/kill/kill.go:4"])
        self.write("1", hops("1", ["- Handle hands the target to kill.Run [app/srv/server.go:7]",
                                   "- Run returns `nothing at all` [app/kill/kill.go:5]"],
                             ["@ app/srv/server.go:7", "> \treturn kill.Run(req.Target)", "= the target goes to kill.Run",
                              "@ app/kill/kill.go:5", f"> {EXEC}", "= tmux kills the pane"],
                             ["END executed: tmux kill-pane @ app/kill/kill.go:5"]))
        _, data = self.page()
        self.assertEqual(data["title"], os.path.basename(self.root))
        self.assertEqual(data["questions"], {"1": "What does Handle hand on?", "2": "Who kills the pane?"})
        node = data["nodes"]["1"]
        self.assertEqual((node["question"], node["from"]), (2, "app/srv/server.go:3"))
        self.assertEqual([a["flags"] for a in node["answers"]], [[], ["? `nothing at all` not in the cited file"]])
        self.assertEqual((data["counts"]["answers"], data["counts"]["flagged"]), (2, 1))
        self.assertLessEqual(data["times"]["new"], node["started"])
        self.assertLessEqual(node["started"], node["written"])
        self.assertIsNone(data["times"]["done"])
        self.assertEqual((list(data["missing"]), list(data["nodes"])), (["2"], ["1"]))
        self.assertEqual({f["path"]: f["lines"] for f in data["files"]},
                         {"app/srv/server.go": SERVER, "app/kill/kill.go": KILL})

    def test_file_text_that_closes_a_script_stays_inside_the_pages_data(self):
        web = ["package web", "", "func Page() string {", '\treturn "<script>boot()</script>"', "}"]
        path = os.path.join(self.root, "app", "web", "web.go")
        os.makedirs(os.path.dirname(path))
        with open(path, "w") as f:
            f.write("\n".join(web) + "\n")
        self.write("1", hops("1", ["- Page closes its own </script> [app/web/web.go:4]"],
                             ["@ app/web/web.go:4", f"> {web[3]}", "= the page ends its </script>"],
                             ["END returned: the markup @ app/web/web.go:4"]))
        page, data = self.page()
        with open(os.path.join(os.path.dirname(SCRIPT), "tracemap.html"), encoding="utf-8") as f:
            self.assertEqual(page.count("</script>"), f.read().count("</script>"))
        self.assertEqual(next(f["lines"] for f in data["files"] if f["path"] == "app/web/web.go"), web)
        self.assertEqual(data["threads"]["1"]["walk"][0]["note"], "the page ends its </script>")

    def test_past_the_text_budget_a_file_carries_only_the_lines_near_its_hops(self):
        big = ["package big", "", "func Big() error {", "\treturn nil", "}"] + [f"// {i:06d} " + "x" * 80 for i in range(50000)]
        path = os.path.join(self.root, "app", "big", "big.go")
        os.makedirs(os.path.dirname(path))
        with open(path, "w") as f:
            f.write("\n".join(big) + "\n")
        self.write("1", hops("1", ["- Big returns nil [app/big/big.go:4]"], ["@ app/big/big.go:4", "> \treturn nil"],
                             ["END returned: nil @ app/big/big.go:4"]))
        _, data = self.page()
        entry = next(f for f in data["files"] if f["path"] == "app/big/big.go")
        self.assertEqual((entry["count"], entry["lines"]), (len(big), None))
        self.assertEqual(entry["sparse"], {str(i): big[i - 1] for i in range(1, 13)})
        self.assertLess(os.path.getsize(os.path.join(self.run_dir, "page.html")), 1024 * 1024)

EXEC = '\tif err := exec.Command("tmux", "kill-pane", "-t", target).Run(); err != nil {'
ERRF = '\t\treturn fmt.Errorf("kill pane %s: %w", target, err)'
REQUIRED = '\t\treturn errors.New("target is required")'


class Stored(Repo):
    """Hops files stored straight into the run directory, so a bad hop reaches `check` whatever `write` thinks of it."""

    def add(self, rel, lines):
        path = os.path.join(self.root, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            f.write("\n".join(lines) + "\n")

    def hop(self, path, ln, quote, answer="- x", view="--hops"):
        self.store("1", hops("1", [answer], [f"@ {path}:{ln}", f"> {quote}"],
                             ["END returned: x @ app/srv/server.go:5"]))
        got = tt(["check", self.run_dir, view] if view else ["check", self.run_dir])
        self.assertEqual(got.returncode, 0, got.stderr)
        return got.stdout


class ResolveTest(Stored):
    """A hop no exact or tail path places is resolved by basename and quote, sibling file, or unique line."""

    def test_an_invented_directory_resolves_to_the_one_file_with_that_basename_holding_the_quote(self):
        out = self.hop("pkg/kill/kill.go", 5, EXEC)
        self.assertIn("app/kill/kill.go:5  if err := exec.Command", out)
        self.assertIn("(path resolved from pkg/kill/kill.go: basename and quote)", out)
        self.assertIn("rejected=0", out)
        self.assertIn("app/kill/kill.go:5  " + EXEC.strip() + "   (path resolved from pkg/kill/kill.go: basename and quote)",
                      self.hop("pkg/kill/kill.go", 5, EXEC, view="--full"))

    def test_the_answers_view_and_the_map_use_the_resolved_path(self):
        out = self.hop("pkg/kill/kill.go", 5, EXEC, answer="- the pane is killed [pkg/kill/kill.go:5]", view=None)
        self.assertIn("[app/kill/kill.go:5]", out)
        self.assertIn("flagged=0", out)
        mapped = tt(["map", self.run_dir])
        self.assertEqual(mapped.returncode, 0, mapped.stderr)
        self.assertIn("app/kill/kill.go", mapped.stdout)

    def test_a_dot_dot_path_is_normalised_before_the_tail_is_tried(self):
        out = self.hop("elsewhere/../srv/server.go", 5, REQUIRED)
        self.assertIn("app/srv/server.go:5  return errors.New", out)
        self.assertIn("(path resolved from elsewhere/../srv/server.go)", out)
        self.assertIn("rejected=0", out)

    def test_two_files_with_the_basename_resolve_to_the_one_holding_the_quote(self):
        self.add("app/other/kill.go", ["package other", "", "func Other() {}"])
        out = self.hop("pkg/kill.go", 5, EXEC)
        self.assertIn("app/kill/kill.go:5  if err := exec.Command", out)
        self.assertIn("basename and quote)", out)
        self.assertIn("rejected=0", out)

    def test_two_files_holding_the_quote_resolve_to_the_one_at_the_exact_line_and_to_none_when_no_one_is(self):
        self.add("app/other/kill.go", ["package other", "", "", "", "", "", "", EXEC, "}"])
        at_kill = self.hop("pkg/kill.go", 5, EXEC)
        self.assertIn("app/kill/kill.go:5  if err := exec.Command", at_kill)
        at_other = self.hop("pkg/kill.go", 8, EXEC)
        self.assertIn("app/other/kill.go:8  if err := exec.Command", at_other)
        neither = self.hop("pkg/kill.go", 20, EXEC)
        self.assertIn("REJECTED pkg/kill.go:20", neither)
        self.assertIn("rejected=1", neither)

    def test_a_quote_in_neither_file_stays_rejected(self):
        self.add("app/other/kill.go", ["package other", "", "func Other() {}"])
        for quote in ("\tghost()", '\tpanic("this text is nowhere in the repository at all")'):
            out = self.hop("pkg/kill.go", 5, quote)
            self.assertIn("REJECTED pkg/kill.go:5 — no such file", out)
            self.assertIn("rejected=1", out)

    def test_a_quote_in_a_sibling_file_of_the_cited_directory_resolves_there(self):
        self.add("app/kill/pane.go", ["package kill", "", "// Pane names the target.", "func Pane(t string) string {",
                                      "\treturn \"%\" + t", "}"])
        out = self.hop("app/kill/pane.go", 6, EXEC)
        self.assertIn("app/kill/kill.go:5  if err := exec.Command", out)
        self.assertIn("(path resolved from app/kill/pane.go: sibling file; re-addressed from :6)", out)
        self.assertIn("rejected=0", out)

    def test_a_line_found_in_one_other_file_of_the_same_extension_resolves_there(self):
        out = self.hop("app/srv/server.go", 3, ERRF)
        self.assertIn("app/kill/kill.go:6  return fmt.Errorf", out)
        self.assertIn("(path resolved from app/srv/server.go: unique line; re-addressed from :3)", out)
        self.assertIn("moved=1 · rejected=0", out)

    def test_a_short_line_found_in_another_file_does_not_resolve_and_twenty_four_characters_do(self):
        self.add("app/lib/a.go", ["package lib", "x" * 23, "y" * 24])
        short = self.hop("app/srv/server.go", 3, "\treturn nil")
        self.assertIn("REJECTED app/srv/server.go:3 — quote not in the file", short)
        self.assertIn("rejected=1", short)
        just_short = self.hop("app/srv/server.go", 3, "x" * 23)
        self.assertIn("rejected=1", just_short)
        long_enough = self.hop("app/srv/server.go", 3, "y" * 24)
        self.assertIn("app/lib/a.go:3  " + "y" * 24, long_enough)
        self.assertIn("(path resolved from app/srv/server.go: unique line", long_enough)

    def test_a_line_found_in_two_files_is_no_unique_line(self):
        self.add("app/lib/a.go", ["package lib", ERRF])
        out = self.hop("app/srv/server.go", 3, ERRF)
        self.assertIn("REJECTED app/srv/server.go:3 — quote not in the file", out)


class WriteVerifyTest(Stored):
    """`write` runs every hop through the resolver: a hop that does not hold is refused once, then stored as it is,
    and every stored hop carries its real address and a sidecar entry."""

    GHOST = '\t\treturn fmt.Errorf("this text is written nowhere in the tree")'

    def put(self, node, hop_lines, answer="- x"):
        return tt(["write", self.run_dir, node], input=hops(node, [answer], hop_lines,
                                                           ["END returned: x @ app/srv/server.go:5"]))

    def stored(self, node="1"):
        with open(os.path.join(self.run_dir, node + ".hops")) as f:
            return f.read()

    def sidecar(self, node="1"):
        with open(os.path.join(self.run_dir, node + ".write.json")) as f:
            return json.load(f)

    def sha(self, rel):
        with open(os.path.join(self.root, rel), "rb") as f:
            return hashlib.sha256(f.read()).hexdigest()

    def test_a_hop_with_an_invented_directory_is_stored_with_its_real_path_and_the_sidecar_says_how(self):
        got = self.put("1", ["@ pkg/kill/kill.go:5", f"> {EXEC}", "@ app/kill/kill.go:2", f"> {ERRF}"])
        self.assertEqual(got.returncode, 0, got.stderr)
        text = self.stored()
        self.assertIn("@ app/kill/kill.go:5\n", text)
        self.assertIn("@ app/kill/kill.go:6\n", text)
        self.assertNotIn("pkg/kill", text)
        first, second = self.sidecar()
        self.assertEqual(first, dict(given="pkg/kill/kill.go:5", path="app/kill/kill.go", line=5, end=5,
                                     how="basename and quote", status="verified", sha256=self.sha("app/kill/kill.go")))
        self.assertEqual((second["given"], second["path"], second["line"], second["status"]),
                         ("app/kill/kill.go:2", "app/kill/kill.go", 6, "moved"))
        self.assertFalse(os.path.exists(os.path.join(self.run_dir, "1.refused")))

    def test_an_invented_quote_refuses_the_first_write_and_the_hint_names_the_real_line(self):
        got = self.put("1", ["@ app/srv/server.go:5", f"> {REQUIRED}", "@ app/kill/kill.go:6", f"> {self.GHOST}"])
        self.assertEqual(got.returncode, 2)
        lines = got.stderr.splitlines()
        self.assertEqual(lines[0], "TRACETREE REFUSED — 1 of 2 hops do not hold; fix each, then write again "
                                   "(a second write stores the file as it is)")
        self.assertEqual(len(lines), 2, lines)
        self.assertTrue(lines[1].startswith("@ app/kill/kill.go:6 — quote not in the file; "), lines[1])
        self.assertIn('6| return fmt.Errorf("kill pane %s: %w", target, err)', lines[1])
        self.assertIn("8| return nil", lines[1])
        self.assertNotIn("9|", lines[1])
        self.assertFalse(os.path.exists(os.path.join(self.run_dir, "1.hops")))
        self.assertFalse(os.path.exists(os.path.join(self.run_dir, "1.write.json")))
        self.assertTrue(os.path.exists(os.path.join(self.run_dir, "1.refused")))

    def test_the_second_write_stores_the_file_with_that_hop_rejected_and_a_third_is_refused(self):
        hop_lines = ["@ app/srv/server.go:5", f"> {REQUIRED}", "@ app/kill/kill.go:6", f"> {self.GHOST}"]
        self.assertEqual(self.put("1", hop_lines).returncode, 2)
        again = self.put("1", hop_lines)
        self.assertEqual(again.returncode, 0, again.stderr)
        self.assertIn("@ app/kill/kill.go:6\n", self.stored())
        good, bad = self.sidecar()
        self.assertEqual(good["status"], "verified")
        self.assertEqual((bad["given"], bad["path"], bad["line"], bad["how"], bad["status"]),
                         ("app/kill/kill.go:6", "app/kill/kill.go", 6, None, "rejected"))
        third = self.put("1", hop_lines)
        self.assertEqual(third.returncode, 2)
        self.assertIn("already written", third.stderr)

    def test_an_elided_quote_is_refused_and_so_is_a_directory(self):
        got = self.put("1", ["@ app/kill/kill.go:6", '> \t\treturn fmt.Errorf("kill pane %s: ...", target, err)',
                             "@ app/kill/:1"])
        self.assertEqual(got.returncode, 2)
        lines = got.stderr.splitlines()
        self.assertTrue(lines[0].startswith("TRACETREE REFUSED — 2 of 2 hops do not hold"), lines[0])
        self.assertIn("@ app/kill/kill.go:6 — quote every line whole, no elision; ", lines[1])
        self.assertIn("6| return fmt.Errorf", lines[1])
        self.assertTrue(lines[2].startswith("@ app/kill/:1 — a hop cites a file line — name the file the listing "
                                            "points at; "), lines[2])

    def test_the_hint_for_a_path_that_names_no_file_lists_the_files_with_that_basename_or_says_none(self):
        got = self.put("1", ["@ pkg/kill.go:5", "> \tghost()", "@ pkg/zzz.go:5", "> \tghost()"])
        self.assertEqual(got.returncode, 2)
        lines = got.stderr.splitlines()
        self.assertIn("@ pkg/zzz.go:5 — no such file; no file named zzz.go under ROOT", lines[2])
        self.assertIn("@ pkg/kill.go:5 — ", lines[1])

    def test_a_clean_write_is_stored_as_written_with_only_the_sidecar_added(self):
        text = self.good()
        got = tt(["write", self.run_dir, "1"], input=text)
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertEqual(got.stdout, f"stored 1: 1 hops, 1 answer lines — {self.run_dir}/1.hops\n")
        self.assertEqual(self.stored(), text)
        self.assertEqual(sorted(os.listdir(self.run_dir)), ["1.hops", "1.write.json", "ROOT"])
        (entry,) = self.sidecar()
        self.assertEqual((entry["given"], entry["path"], entry["line"], entry["status"], entry["how"]),
                         ("app/srv/server.go:5", "app/srv/server.go", 5, "verified", "exact"))

    def test_a_refused_node_is_still_running_for_wait(self):
        tt(["start", self.run_dir, "1", "app/srv/server.go:3"])
        self.assertEqual(self.put("1", ["@ app/kill/kill.go:6", f"> {self.GHOST}"]).returncode, 2)
        got = tt(["wait", self.run_dir, "1", "--grace", "0.5", "--timeout", "1.5"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("WAIT TIMEOUT after", got.stdout)
        self.assertIn("started but never wrote: 1", got.stdout)


HEADER_BROKE = ("⚠ WARNINGS — {n} of {m} nodes broke the hop contract; their answers rest on placed, refused or "
                "rejected hops: drill before trusting them")
HEADER_NONE_BROKE = "⚠ WARNINGS — no node broke the hop contract"
GHOST_HOP = '\t\treturn fmt.Errorf("this text is written nowhere in the tree")'


class WarningsTest(Stored):
    """The kit says out loud, without drilling, that a node invented paths, stored hops that do not hold, or flagged
    answers: a WARNINGS block first in `check` and right after the WAIT line in `wait`."""

    def put(self, node, hop_lines, answer="- x", tail=("END returned: x @ app/srv/server.go:5",)):
        return tt(["write", self.run_dir, node], input=hops(node, [answer], hop_lines, list(tail)))

    def placed_node(self, node="1", paths=("pkg/kill/kill.go",)):
        lines = []
        for path in paths:
            lines += [f"@ {path}:5", f"> {EXEC}"]
        got = self.put(node, lines)
        self.assertEqual(got.returncode, 0, got.stderr)
        return got

    def check(self, *flags):
        got = tt(["check", self.run_dir, *flags])
        self.assertEqual(got.returncode, 0, got.stderr)
        return got.stdout

    def test_a_node_that_invented_a_directory_is_named_with_given_and_real_path_and_counted(self):
        self.placed_node()
        lines = self.check().splitlines()
        self.assertEqual(lines[0], HEADER_BROKE.format(n=1, m=1))
        self.assertEqual(lines[1], "⚠ node 1: 1 hops named a path that is not the file — the kit placed them "
                                   "(pkg/kill/kill.go → app/kill/kill.go)")
        self.assertIn("drifted=0 · placed=1 · followed=0 · refused=0", lines[-1])

    def test_a_run_without_a_sidecar_is_judged_by_the_check_time_resolver(self):
        out = self.hop("pkg/kill/kill.go", 5, EXEC, view=None).splitlines()
        self.assertEqual(out[0], HEADER_BROKE.format(n=1, m=1))
        self.assertEqual(out[1], "⚠ node 1: 1 hops named a path that is not the file — the kit placed them "
                                 "(pkg/kill/kill.go → app/kill/kill.go)")
        self.assertIn("placed=1 · followed=0 · refused=0", out[-1])
        sibling = self.hop("app/kill/pane.go", 5, EXEC, view=None)
        self.assertIn("(app/kill/pane.go → app/kill/kill.go)", sibling)

    def test_a_later_hop_on_the_same_invented_path_is_placed_through_the_first_and_still_counted(self):
        self.store("1", hops("1", ["- x"], ["@ pkg/kill/kill.go:5", f"> {EXEC}", "@ pkg/kill/kill.go:6", f"> {ERRF}"],
                             ["END returned: x @ app/srv/server.go:5"]))
        lines = self.check().splitlines()
        self.assertEqual(lines[1], "⚠ node 1: 2 hops named a path that is not the file — the kit placed them "
                                   "(pkg/kill/kill.go → app/kill/kill.go)")
        self.assertIn("placed=2", lines[-1])

    def test_a_short_path_resolved_by_its_tail_is_no_incident(self):
        self.add("app/other/server.go", ["package other", "", "func Serve() {}"])
        out = self.hop("server.go", 5, REQUIRED, view=None)
        self.assertEqual(out.splitlines()[0], "WARNINGS: none")
        self.assertIn("placed=0 · followed=0 · refused=0", out)
        # a stored write of the same short path is no incident either
        os.remove(os.path.join(self.run_dir, "1.hops"))
        got = self.put("1", ["@ server.go:5", f"> {REQUIRED}"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertNotIn("WARNING", got.stderr)
        self.assertEqual(self.check().splitlines()[0], "WARNINGS: none")

    def test_more_than_three_placed_paths_are_cut_with_the_count_left(self):
        self.placed_node(paths=("pkg/a/kill.go", "pkg/b/kill.go", "pkg/c/kill.go", "pkg/d/kill.go", "pkg/a/kill.go"))
        line = self.check().splitlines()[1]
        self.assertEqual(line, "⚠ node 1: 5 hops named a path that is not the file — the kit placed them "
                               "(pkg/a/kill.go → app/kill/kill.go, pkg/b/kill.go → app/kill/kill.go, "
                               "pkg/c/kill.go → app/kill/kill.go, +1 more)")

    def test_a_write_refused_once_and_stored_with_bad_hops_is_named_and_counted(self):
        hop_lines = ["@ app/srv/server.go:5", f"> {REQUIRED}", "@ app/kill/kill.go:6", f"> {GHOST_HOP}"]
        self.assertEqual(self.put("1", hop_lines).returncode, 2)
        self.assertEqual(self.put("1", hop_lines).returncode, 0)
        lines = self.check().splitlines()
        self.assertEqual(lines[0], HEADER_BROKE.format(n=1, m=1))
        self.assertEqual(lines[1], "⚠ node 1: write refused once, then stored with 1 hops that still do not hold")
        self.assertEqual(lines[2], "⚠ node 1: 1 hops REJECTED")
        self.assertIn("rejected=1 · drifted=0 · placed=0 · followed=0 · refused=1", lines[-1])

    def test_a_refused_write_the_second_attempt_fixed_is_no_incident(self):
        bad = ["@ app/srv/server.go:5", f"> {REQUIRED}", "@ app/kill/kill.go:6", f"> {GHOST_HOP}"]
        fixed = ["@ app/srv/server.go:5", f"> {REQUIRED}", "@ app/kill/kill.go:6", f"> {ERRF}"]
        self.assertEqual(self.put("1", bad).returncode, 2)
        self.assertEqual(self.put("1", fixed).returncode, 0)
        out = self.check()
        self.assertEqual(out.splitlines()[0], "WARNINGS: none")
        self.assertIn("refused=1", out)

    def test_a_rejected_hop_gets_its_own_line_and_counts_its_node(self):
        out = self.hop("app/srv/server.go", 5, "\tghost()", view=None).splitlines()
        self.assertEqual(out[0], HEADER_BROKE.format(n=1, m=1))
        self.assertEqual(out[1], "⚠ node 1: 1 hops REJECTED")

    def test_the_header_counts_only_nodes_that_broke_the_contract_out_of_the_whole_run(self):
        self.store("1", self.good())
        self.placed_node("2")
        self.placed_node("3")
        lines = self.check().splitlines()
        self.assertEqual(lines[0], HEADER_BROKE.format(n=2, m=3))
        self.assertTrue(lines[1].startswith("⚠ node 2: 1 hops named"), lines[1])
        self.assertTrue(lines[2].startswith("⚠ node 3: 1 hops named"), lines[2])

    def test_flagged_answers_alone_raise_no_warning_so_a_clean_walk_never_screams(self):
        out = self.hop("app/srv/server.go", 5, REQUIRED, answer="- Run ends [app/kill/kill.go:9]", view=None)
        lines = out.splitlines()
        self.assertEqual(lines[0], "WARNINGS: none")
        self.assertFalse([l for l in lines if l.startswith("⚠ ")], lines)

    def test_a_clean_run_states_that_there_is_nothing_to_warn_about(self):
        self.write("1", self.good())
        for flags in ((), ("--hops",), ("--full", "1")):
            out = self.check(*flags)
            self.assertEqual(out.splitlines()[0], "WARNINGS: none", flags)
            self.assertNotIn("⚠", out)

    def test_check_hops_and_full_print_the_same_block_first(self):
        self.placed_node()
        block = self.check().splitlines()[:2]
        for flags in (("--hops",), ("--hops", "1"), ("--full",)):
            self.assertEqual(self.check(*flags).splitlines()[:2], block, flags)

    def test_an_unusable_sidecar_is_a_could_not_look_never_a_clean_none(self):
        self.write("1", self.good())
        with open(os.path.join(self.run_dir, "1.write.json"), "w") as f:
            f.write("{not json")
        lines = self.check().splitlines()
        self.assertNotEqual(lines[0], "WARNINGS: none")
        self.assertTrue(lines[0].startswith("⚠ WARNINGS — none found, but the kit could not look: "), lines[0])
        self.assertIn("1.write.json cannot be used", lines[0])

    def test_wait_prints_the_block_right_after_the_wait_line_and_before_the_answers(self):
        self.placed_node()
        lines = self.wait("1").splitlines()
        self.assertTrue(lines[0].startswith("WAIT done"), lines[0])
        self.assertEqual(lines[1], HEADER_BROKE.format(n=1, m=1))
        self.assertTrue(lines[2].startswith("⚠ node 1: 1 hops named"), lines[2])
        self.assertTrue(lines[3].startswith("[1] THREAD 1"), lines[3])
        with open(os.path.join(self.run_dir, "answers.txt")) as f:
            self.assertEqual(f.read().splitlines()[:3], lines[:3])

    def test_wait_on_a_clean_run_says_none_after_the_wait_line(self):
        self.write("1", self.good())
        lines = self.wait("1").splitlines()
        self.assertEqual(lines[1], "WARNINGS: none")
        self.assertTrue(lines[2].startswith("[1] THREAD 1"), lines[2])
        self.assertNotIn("⚠", next(l for l in lines if l.startswith("MAP ")))
        self.assertNotIn("⚠", tt(["map", self.run_dir]).stdout)

    def test_the_ceiling_keeps_the_whole_warnings_block_before_everything_else(self):
        for n in range(1, 4):
            node = str(n)
            filler = [f"- filler {i} of node {node}: " + "the handler checks the target before it kills the pane " * 4
                      + "[app/srv/server.go:5]" for i in range(60)]
            self.write(node, hops(node, [*filler, "- Run ends [app/kill/kill.go:9]"],
                                  ["@ pkg/srv/server.go:5", "> \t\treturn errors.New(\"target is required\")"],
                                  [f"NOT READ whether node {node} retries", "END returned: x @ app/srv/server.go:5"]))
        out = self.wait("1", "2", "3")
        self.assertLessEqual(len(out), 28000)
        lines = out.splitlines()
        self.assertEqual(lines[0].split()[0], "WAIT")
        self.assertEqual(lines[1], HEADER_BROKE.format(n=3, m=3))
        for i, node in enumerate("123"):
            self.assertTrue(lines[2 + i].startswith(f"⚠ node {node}: 1 hops named a path"), lines[2 + i])
        self.assertTrue(lines[5].startswith("COUNTS: "), lines[5])
        self.assertEqual(lines[-1], f"FULL {self.run_dir}/answers.txt")

    def test_the_map_line_and_the_map_text_name_the_nodes_that_broke_the_contract(self):
        self.placed_node()
        line = next(l for l in self.wait("1").splitlines() if l.startswith("MAP "))
        self.assertTrue(line.endswith(" · ⚠ 1 nodes broke the hop contract"), line)
        with open(os.path.join(self.run_dir, "map.txt")) as f:
            first = f.readline().rstrip("\n")
        self.assertTrue(first.startswith("MAP ") and first.endswith(" · ⚠ 1 nodes broke the hop contract"), first)
        mapped = tt(["map", self.run_dir])
        self.assertEqual(mapped.returncode, 0, mapped.stderr)
        self.assertEqual(mapped.stdout.splitlines()[0], first)

    def test_json_carries_the_warning_lines_and_an_empty_list_when_there_are_none(self):
        self.write("1", self.good())
        clean = json.loads(tt(["map", self.run_dir, "--json"]).stdout)
        self.assertEqual(clean["warnings"], [])
        os.remove(os.path.join(self.run_dir, "1.hops"))
        self.placed_node()
        dirty = json.loads(tt(["map", self.run_dir, "--json"]).stdout)
        self.assertEqual(dirty["warnings"], self.check().splitlines()[:2])

    def test_the_html_page_opens_its_ledger_with_a_red_block_of_the_lines_only_when_there_are_some(self):
        self.write("1", self.good())
        out = os.path.join(self.run_dir, "page.html")
        self.assertEqual(tt(["map", self.run_dir, "--html", out]).returncode, 0)
        with open(out) as f:
            clean = f.read()
        self.assertNotIn('id="warnings"', clean)
        os.remove(os.path.join(self.run_dir, "1.hops"))
        self.placed_node()
        self.assertEqual(tt(["map", self.run_dir, "--html", out]).returncode, 0)
        with open(out) as f:
            page = f.read()
        self.assertIn('id="warnings"', page)
        self.assertLess(page.index('id="warnings"'), page.index('id="stage"'))
        self.assertIn("1 of 1 nodes broke the hop contract", page)
        self.assertIn("pkg/kill/kill.go → app/kill/kill.go", page)
        self.assertLess(page.index('id="warnings"'), page.index('id="evidence"'))
        head = page[:page.index("</head>")]
        for sel in (":root {", ':root:not([data-theme="light"]) {', ':root[data-theme="dark"] {'):
            at = head.index(sel)
            self.assertRegex(head[at:head.index("}", at)], r"--alarm: #[0-9a-f]{6}; --alarm-ink: #[0-9a-f]{6};", sel)
        self.assertRegex(head, r"#warnings \{[^}]*background: var\(--alarm\); color: var\(--alarm-ink\);")
        self.assertNotRegex(page, r"(src|href)\s*=\s*[\"']?(https?:)?//")


class WriteWarnsItsOwnNodeTest(Stored):
    """`write` tells the node that wrote the file, on stderr, when the kit placed its hops or stored it with bad hops."""

    def put(self, hop_lines, tail=("END returned: x @ app/srv/server.go:5",)):
        return tt(["write", self.run_dir, "1"], input=hops("1", ["- x"], hop_lines, list(tail)))

    def test_a_store_with_placed_hops_warns_with_the_pairs_and_exits_zero(self):
        got = self.put(["@ pkg/kill/kill.go:5", f"> {EXEC}", "@ pkg/kill/kill.go:5", f"> {EXEC}"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertEqual(got.stderr, "⚠ WARNING — 2 of your hops named a path that is not the file; the kit placed "
                                     "them: pkg/kill/kill.go → app/kill/kill.go. Copy paths as your read printed them.\n")
        self.assertTrue(got.stdout.startswith("stored 1: 2 hops"), got.stdout)

    def test_the_warning_lists_three_pairs_then_the_rest_as_a_count(self):
        paths = ["pkg/a/kill.go", "pkg/b/kill.go", "pkg/c/kill.go", "pkg/d/kill.go"]
        got = self.put([x for p in paths for x in (f"@ {p}:5", f"> {EXEC}")])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertEqual(got.stderr, "⚠ WARNING — 4 of your hops named a path that is not the file; the kit placed "
                                     "them: pkg/a/kill.go → app/kill/kill.go, pkg/b/kill.go → app/kill/kill.go, "
                                     "pkg/c/kill.go → app/kill/kill.go, +1 more. Copy paths as your read printed them.\n")

    def test_a_second_store_with_bad_hops_left_warns_that_they_do_not_hold(self):
        hop_lines = ["@ app/srv/server.go:5", f"> {REQUIRED}", "@ app/kill/kill.go:6", f"> {GHOST_HOP}",
                     "@ app/kill/kill.go:6", f"> {GHOST_HOP}"]
        first = self.put(hop_lines)
        self.assertEqual(first.returncode, 2)
        self.assertNotIn("⚠ WARNING", first.stderr)
        again = self.put(hop_lines)
        self.assertEqual(again.returncode, 0, again.stderr)
        self.assertEqual(again.stderr, "⚠ WARNING — stored with 2 hops that do not hold; your tracer sees them "
                                       "REJECTED and is warned\n")

    def test_both_warnings_print_placed_first_and_a_clean_store_prints_none(self):
        self.assertEqual(self.put(["@ app/srv/server.go:5", f"> {REQUIRED}"]).stderr, "")
        os.remove(os.path.join(self.run_dir, "1.hops"))
        os.remove(os.path.join(self.run_dir, "1.write.json"))
        hop_lines = ["@ pkg/kill/kill.go:5", f"> {EXEC}", "@ app/kill/kill.go:6", f"> {GHOST_HOP}"]
        self.assertEqual(self.put(hop_lines).returncode, 2)
        got = self.put(hop_lines)
        self.assertEqual(got.returncode, 0, got.stderr)
        lines = got.stderr.splitlines()
        self.assertEqual(len(lines), 2, lines)
        self.assertTrue(lines[0].startswith("⚠ WARNING — 1 of your hops named a path"), lines[0])
        self.assertTrue(lines[1].startswith("⚠ WARNING — stored with 1 hops that do not hold"), lines[1])


HOUR = 3600


def git(root, *args, when=None):
    env = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@example.com", GIT_COMMITTER_NAME="t",
               GIT_COMMITTER_EMAIL="t@example.com")
    if when is not None:
        stamp = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(when))
        env.update(GIT_AUTHOR_DATE=stamp, GIT_COMMITTER_DATE=stamp)
    done = subprocess.run(["git", "-C", root, *args], capture_output=True, text=True, env=env)
    if done.returncode != 0:
        raise AssertionError(f"git {' '.join(args)}: {done.stderr}")
    return done.stdout.strip()


class DriftTest(Stored):
    """A hop that held when walked and whose file changed since is DRIFTED, never REJECTED."""

    def setUp(self):
        super().setUp()
        git(self.root, "init", "-q")
        git(self.root, "add", "-A")
        git(self.root, "commit", "-q", "-m", "one", when=time.time() - 2 * HOUR)
        self.base = git(self.root, "rev-parse", "HEAD")

    def edit(self, rel="app/srv/server.go", old="target is required", new="target is needed", commit=False):
        path = os.path.join(self.root, rel)
        with open(path) as f:
            text = f.read()
        with open(path, "w") as f:
            f.write(text.replace(old, new))
        if commit:
            git(self.root, "commit", "-q", "-am", "two")

    def walked(self, cite="- x"):
        """The hop for `REQUIRED`, stored by `write`: a sidecar says it held."""
        got = tt(["write", self.run_dir, "1"], input=hops("1", [cite], ["@ app/srv/server.go:5", f"> {REQUIRED}"],
                                                         ["END returned: x @ app/srv/server.go:5"]))
        self.assertEqual(got.returncode, 0, got.stderr)

    def old_run(self, quote=REQUIRED, line=5):
        """The same hop in a run `write` never saw: no sidecar."""
        self.store("1", hops("1", ["- x"], [f"@ app/srv/server.go:{line}", f"> {quote}"],
                             ["END returned: x @ app/srv/server.go:5"]))

    def check(self, *flags):
        got = tt(["check", self.run_dir, *flags])
        self.assertEqual(got.returncode, 0, got.stderr)
        return got.stdout

    def test_new_writes_base_in_a_git_root_and_no_file_in_any_other(self):
        plain = tempfile.mkdtemp(prefix="tracetree-plain-")
        names = {os.path.basename(self.root), os.path.basename(plain)}
        try:
            got = tt(["new", self.root])
            self.assertEqual(got.returncode, 0, got.stderr)
            with open(os.path.join(got.stdout.strip(), "BASE")) as f:
                self.assertEqual(f.read().strip(), self.base)
            none = tt(["new", plain])
            self.assertEqual(none.returncode, 0, none.stderr)
            self.assertEqual(none.stderr, "")
            self.assertFalse(os.path.exists(os.path.join(none.stdout.strip(), "BASE")))
        finally:
            for name in names:
                shutil.rmtree(os.path.join("/tmp", name), ignore_errors=True)
            shutil.rmtree(plain, ignore_errors=True)

    def test_a_hop_verified_at_write_whose_file_was_edited_is_drifted_not_rejected(self):
        self.walked()
        self.edit()
        out = self.check("--hops")
        self.assertIn("≈ DRIFTED app/srv/server.go:5 — held when walked; the file changed since: re-read before pasting", out)
        self.assertNotIn("REJECTED", out)
        self.assertIn("rejected=0 · drifted=1", out)
        full = self.check("--full")
        self.assertIn("≈ DRIFTED app/srv/server.go:5 — held when walked;", full)
        self.assertIn("hops: 0 verified, 0 rejected, 1 drifted", self.check())
        mapped = tt(["map", self.run_dir, "--all"])
        self.assertEqual(mapped.returncode, 0, mapped.stderr)
        self.assertNotIn("Handle", mapped.stdout)

    def test_a_hop_verified_at_write_whose_file_is_gone_is_drifted(self):
        self.walked()
        os.remove(os.path.join(self.root, "app/srv/server.go"))
        out = self.check("--hops")
        self.assertIn("≈ DRIFTED app/srv/server.go:5 — held when walked;", out)
        self.assertIn("rejected=0 · drifted=1", out)

    def test_a_hop_with_no_sidecar_is_drifted_when_the_commit_in_base_held_the_quote(self):
        self.old_run()
        with open(os.path.join(self.run_dir, "BASE"), "w") as f:
            f.write(self.base + "\n")
        self.edit(commit=True)
        out = self.check("--hops")
        self.assertIn(f"≈ DRIFTED app/srv/server.go:5 — held when walked (at {self.base[:8]}); the file changed since: "
                      "re-read before pasting", out)
        self.assertIn("rejected=0 · drifted=1", out)

    def test_a_hop_with_no_sidecar_and_no_base_is_drifted_by_the_reflog_at_the_time_the_run_began(self):
        self.old_run()
        stamp = time.time() - HOUR
        os.utime(os.path.join(self.run_dir, "ROOT"), (stamp, stamp))
        self.edit(commit=True)
        self.assertFalse(os.path.exists(os.path.join(self.run_dir, "BASE")))
        out = self.check("--hops")
        self.assertIn(f"≈ DRIFTED app/srv/server.go:5 — held when walked (at {self.base[:8]});", out)
        self.assertIn("rejected=0 · drifted=1", out)

    def test_a_hop_that_never_held_stays_rejected_with_or_without_a_sidecar(self):
        ghost = '\t\treturn errors.New("never in any commit")'
        self.old_run(quote=ghost)
        with open(os.path.join(self.run_dir, "BASE"), "w") as f:
            f.write(self.base + "\n")
        self.edit()
        out = self.check("--hops")
        self.assertIn("REJECTED app/srv/server.go:5 — quote not in the file", out)
        self.assertIn("rejected=1 · drifted=0", out)
        os.remove(os.path.join(self.run_dir, "1.hops"))
        hop_lines = ["@ app/srv/server.go:5", f"> {ghost}"]
        tail = ["END returned: x @ app/srv/server.go:5"]
        for _ in (1, 2):
            tt(["write", self.run_dir, "1"], input=hops("1", ["- x"], hop_lines, tail))
        self.edit(old="target is needed", new="target is wanted")
        again = self.check("--hops")
        self.assertIn("REJECTED app/srv/server.go:5 — quote not in the file", again)
        self.assertIn("rejected=1 · drifted=0", again)
        self.assertNotIn("DRIFTED", again)

    def test_an_answer_cite_backed_only_by_a_drifted_hop_is_marked_and_not_unbacked(self):
        self.walked(cite="- Handle refuses an empty target [app/srv/server.go:5]")
        self.edit()
        out = self.check()
        self.assertIn("≈ app/srv/server.go:5", out)
        self.assertNotIn("unbacked", out)
        self.assertIn("rejected=0 · drifted=1", out)

    def test_a_git_command_that_fails_leaves_the_hop_rejected_and_says_so_once(self):
        self.store("1", hops("1", ["- x"], ["@ app/srv/server.go:5", f"> {REQUIRED}", "@ app/srv/server.go:6",
                                           "> \t\treturn errors.New(\"another ghost\")"],
                             ["END returned: x @ app/srv/server.go:5"]))
        with open(os.path.join(self.run_dir, "BASE"), "w") as f:
            f.write("0" * 40 + "\n")
        self.edit()
        out = self.check("--hops")
        self.assertEqual(out.count("DRIFT UNKNOWN — "), 1, out)
        self.assertNotIn("DRIFTED", out)
        self.assertIn("rejected=2 · drifted=0", out)

    def test_a_run_older_than_the_reflog_is_unknown_not_drifted(self):
        self.old_run()
        stamp = time.time() - 40 * 24 * HOUR
        os.utime(os.path.join(self.run_dir, "ROOT"), (stamp, stamp))
        self.edit(commit=True)
        out = self.check("--hops")
        self.assertEqual(out.count("DRIFT UNKNOWN — "), 1, out)
        self.assertIn("REJECTED app/srv/server.go:5", out)
        self.assertNotIn("DRIFTED", out)

    def test_a_drifted_run_gets_one_run_line_and_no_node_broke_the_contract(self):
        self.walked()
        self.edit()
        lines = self.check().splitlines()
        self.assertEqual(lines[0], HEADER_NONE_BROKE)
        self.assertEqual(lines[1], "⚠ run: 1 hops DRIFTED — the code changed since the walk: re-read before pasting")
        self.assertEqual(sum(1 for l in lines if l.startswith("⚠ ")), 2, lines)
        self.assertEqual(self.check("--hops").splitlines()[:2], lines[:2])

    def test_the_map_names_the_drift_on_its_first_line_and_in_its_json(self):
        self.walked()
        self.edit()
        mapped = tt(["map", self.run_dir])
        self.assertEqual(mapped.returncode, 0, mapped.stderr)
        self.assertTrue(mapped.stdout.splitlines()[0].endswith(" · ⚠ 1 drifted"), mapped.stdout.splitlines()[0])
        data = json.loads(tt(["map", self.run_dir, "--json"]).stdout)
        self.assertEqual(data["warnings"][-1],
                         "⚠ run: 1 hops DRIFTED — the code changed since the walk: re-read before pasting")
        waited = tt(["wait", self.run_dir, "1", "--grace", "0", "--timeout", "30"]).stdout
        self.assertTrue(next(l for l in waited.splitlines() if l.startswith("MAP ")).endswith(" · ⚠ 1 drifted"))

    def test_a_run_with_no_sidecar_gets_the_drift_line_from_the_commit_it_started_at(self):
        self.old_run()
        with open(os.path.join(self.run_dir, "BASE"), "w") as f:
            f.write(self.base + "\n")
        self.edit(commit=True)
        self.assertEqual(self.check().splitlines()[1],
                         "⚠ run: 1 hops DRIFTED — the code changed since the walk: re-read before pasting")


class PlainDriftTest(Stored):
    def test_a_root_that_is_no_git_work_tree_gets_no_drift_label_and_no_noise(self):
        self.store("1", hops("1", ["- x"], ["@ app/srv/server.go:5", "> \t\treturn errors.New(\"was here once\")"],
                             ["END returned: x @ app/srv/server.go:5"]))
        got = tt(["check", self.run_dir, "--hops"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("REJECTED app/srv/server.go:5", got.stdout)
        self.assertIn("rejected=1 · drifted=0", got.stdout)
        self.assertNotIn("DRIFT", got.stdout)
        self.assertEqual(got.stderr, "")


FOLLOWED_FLAG = "(path resolved from app/kill/kill.go: followed a file moved since the walk)"
FOLLOWED_COUNTS = "placed=0 · followed=1 · refused=0"


class FollowedTest(Stored):
    """A hop whose given path held the quote when walked and whose file was moved since is followed, never placed: the
    node did nothing wrong. Only a given path that never held the quote is a placement incident."""

    def setUp(self):
        super().setUp()
        git(self.root, "init", "-q")
        git(self.root, "add", "-A")
        git(self.root, "commit", "-q", "-m", "one", when=time.time() - 2 * HOUR)
        self.base = git(self.root, "rev-parse", "HEAD")

    def move_kill(self):
        os.makedirs(os.path.join(self.root, "app/proc"))
        git(self.root, "mv", "app/kill/kill.go", "app/proc/kill.go")
        git(self.root, "commit", "-q", "-m", "two")

    def set_base(self, value=None):
        with open(os.path.join(self.run_dir, "BASE"), "w") as f:
            f.write((value or self.base) + "\n")

    def hop_lines(self, *pairs):
        return [l for path, ln, quote in pairs for l in (f"@ {path}:{ln}", f"> {quote}")]

    def check(self, *flags):
        got = tt(["check", self.run_dir, *flags])
        self.assertEqual(got.returncode, 0, got.stderr)
        return got.stdout

    def test_a_file_moved_after_the_write_is_followed_not_placed(self):
        got = tt(["write", self.run_dir, "1"], input=hops("1", ["- x"], self.hop_lines(("app/kill/kill.go", 5, EXEC)),
                                                          ["END returned: x @ app/srv/server.go:5"]))
        self.assertEqual(got.returncode, 0, got.stderr)
        with open(os.path.join(self.run_dir, "1.write.json")) as f:
            self.assertEqual(json.load(f)[0]["how"], "exact")
        self.move_kill()
        out = self.check()
        self.assertEqual(out.splitlines()[0], "WARNINGS: none")
        self.assertNotIn("⚠", out)
        self.assertIn(FOLLOWED_COUNTS, out)
        self.assertIn("verified=1", out)
        self.assertIn("rejected=0 · drifted=0", out)
        hops_view = self.check("--hops")
        self.assertIn("app/proc/kill.go:5  " + EXEC.strip() + "   " + FOLLOWED_FLAG, hops_view)
        self.assertNotIn("basename and quote", hops_view)
        self.assertEqual(hops_view.splitlines()[0], "WARNINGS: none")

    def test_a_followed_hop_stays_in_the_map(self):
        self.store("1", hops("1", ["- x"], self.hop_lines(("app/kill/kill.go", 5, EXEC)),
                             ["END returned: x @ app/srv/server.go:5"]))
        self.set_base()
        self.move_kill()
        mapped = tt(["map", self.run_dir, "--all"])
        self.assertEqual(mapped.returncode, 0, mapped.stderr)
        self.assertIn("kill", mapped.stdout)
        self.assertNotIn("broke the hop contract", mapped.stdout)

    def test_a_run_with_no_sidecar_follows_a_file_the_base_commit_held_at_the_given_path(self):
        self.store("1", hops("1", ["- x"], self.hop_lines(("app/kill/kill.go", 5, EXEC)),
                             ["END returned: x @ app/srv/server.go:5"]))
        self.set_base()
        self.move_kill()
        out = self.check("--hops")
        self.assertEqual(out.splitlines()[0], "WARNINGS: none")
        self.assertIn(FOLLOWED_COUNTS, out)
        self.assertIn("app/proc/kill.go:5  " + EXEC.strip() + "   " + FOLLOWED_FLAG, out)

    def test_a_run_with_no_sidecar_and_no_base_follows_through_the_reflog(self):
        self.store("1", hops("1", ["- x"], self.hop_lines(("app/kill/kill.go", 5, EXEC)),
                             ["END returned: x @ app/srv/server.go:5"]))
        stamp = time.time() - HOUR
        os.utime(os.path.join(self.run_dir, "ROOT"), (stamp, stamp))
        self.move_kill()
        self.assertFalse(os.path.exists(os.path.join(self.run_dir, "BASE")))
        out = self.check()
        self.assertEqual(out.splitlines()[0], "WARNINGS: none")
        self.assertIn(FOLLOWED_COUNTS, out)

    def test_a_given_directory_that_never_existed_at_the_base_stays_placed(self):
        self.store("1", hops("1", ["- x"], self.hop_lines(("pkg/kill/kill.go", 5, EXEC)),
                             ["END returned: x @ app/srv/server.go:5"]))
        self.set_base()
        self.move_kill()
        lines = self.check().splitlines()
        self.assertEqual(lines[0], HEADER_BROKE.format(n=1, m=1))
        self.assertEqual(lines[1], "⚠ node 1: 1 hops named a path that is not the file — the kit placed them "
                                   "(pkg/kill/kill.go → app/proc/kill.go)")
        self.assertIn("placed=1 · followed=0 · refused=0", lines[-1])

    def test_a_given_file_the_base_held_without_the_quote_stays_placed(self):
        self.add("app/kill/pane.go", ["package kill", "", "func Pane() {}"])
        git(self.root, "add", "-A")
        git(self.root, "commit", "-q", "-m", "pane")
        self.base = git(self.root, "rev-parse", "HEAD")
        self.store("1", hops("1", ["- x"], self.hop_lines(("app/kill/pane.go", 5, EXEC)),
                             ["END returned: x @ app/srv/server.go:5"]))
        self.set_base()
        out = self.check()
        self.assertIn("⚠ node 1: 1 hops named a path that is not the file — the kit placed them "
                      "(app/kill/pane.go → app/kill/kill.go)", out)
        self.assertIn("placed=1 · followed=0", out)

    def test_a_quote_the_base_held_far_from_the_cited_line_is_not_followed(self):
        self.add("app/kill/kill.go", ["package kill"] + [f"// filler {i}" for i in range(60)] + KILL[1:])
        git(self.root, "add", "-A")
        git(self.root, "commit", "-q", "-m", "long")
        self.base = git(self.root, "rev-parse", "HEAD")
        self.store("1", hops("1", ["- x"], self.hop_lines(("app/kill/kill.go", 2, EXEC)),
                             ["END returned: x @ app/srv/server.go:5"]))
        self.set_base()
        self.move_kill()
        out = self.check()
        self.assertIn("placed=1 · followed=0", out)
        self.assertIn("⚠ node 1: 1 hops named a path that is not the file", out)

    def test_a_git_that_cannot_answer_leaves_the_hop_placed_and_says_it_could_not_look(self):
        self.store("1", hops("1", ["- x"], self.hop_lines(("app/kill/kill.go", 5, EXEC)),
                             ["END returned: x @ app/srv/server.go:5"]))
        self.move_kill()
        self.set_base("0" * 40)
        out = self.check()
        self.assertEqual(out.count("DRIFT UNKNOWN — "), 1, out)
        self.assertIn("⚠ node 1: 1 hops named a path that is not the file — the kit placed them "
                      "(app/kill/kill.go → app/proc/kill.go)", out)
        self.assertIn("placed=1 · followed=0", out)

    def test_a_followed_hop_beside_a_real_incident_adds_one_run_line(self):
        self.store("1", hops("1", ["- x"], self.hop_lines(("app/kill/kill.go", 5, EXEC), ("app/srv/server.go", 5, GHOST_HOP)),
                             ["END returned: x @ app/srv/server.go:5"]))
        self.set_base()
        self.move_kill()
        lines = self.check().splitlines()
        self.assertEqual(lines[0], HEADER_BROKE.format(n=1, m=1))
        self.assertEqual(lines[1], "⚠ node 1: 1 hops REJECTED")
        self.assertEqual(lines[2], "⚠ run: 1 hops followed files moved since the walk "
                                   "(app/kill/kill.go → app/proc/kill.go)")
        self.assertEqual(sum(1 for l in lines if l.startswith("⚠ ")), 3, lines)
        self.assertIn("rejected=1 · drifted=0 · placed=0 · followed=1 · refused=0", lines[-1])

    def test_the_followed_run_line_cuts_after_three_pairs(self):
        spec = []
        for n in "abcd":
            quote = f"func {n.upper()}() error {{ return nil }}  // distinct {n}{n}{n}"
            self.add(f"app/{n}/{n}.go", [f"package {n}", "", quote])
            spec.append((f"app/{n}/{n}.go", 3, quote))
        git(self.root, "add", "-A")
        git(self.root, "commit", "-q", "-m", "four")
        self.base = git(self.root, "rev-parse", "HEAD")
        self.store("1", hops("1", ["- x"], self.hop_lines(*spec, ("app/srv/server.go", 5, GHOST_HOP)),
                             ["END returned: x @ app/srv/server.go:5"]))
        self.set_base()
        os.makedirs(os.path.join(self.root, "moved"))
        for n in "abcd":
            git(self.root, "mv", f"app/{n}/{n}.go", f"moved/{n}.go")
        git(self.root, "commit", "-q", "-m", "move")
        lines = self.check().splitlines()
        self.assertEqual(lines[2], "⚠ run: 4 hops followed files moved since the walk (app/a/a.go → moved/a.go, "
                                   "app/b/b.go → moved/b.go, app/c/c.go → moved/c.go, +1 more)")
        self.assertIn("followed=4", lines[-1])

    def test_a_followed_hop_beside_a_drifted_one_prints_both_run_lines(self):
        self.store("1", hops("1", ["- x"], self.hop_lines(("app/kill/kill.go", 5, EXEC), ("app/srv/server.go", 5, REQUIRED)),
                             ["END returned: x @ app/srv/server.go:5"]))
        self.set_base()
        self.move_kill()
        path = os.path.join(self.root, "app/srv/server.go")
        with open(path) as f:
            text = f.read()
        with open(path, "w") as f:
            f.write(text.replace("target is required", "target is needed"))
        git(self.root, "commit", "-q", "-am", "three")
        lines = self.check().splitlines()
        self.assertEqual(lines[0], HEADER_NONE_BROKE)
        self.assertEqual(lines[1], "⚠ run: 1 hops DRIFTED — the code changed since the walk: re-read before pasting")
        self.assertEqual(lines[2], "⚠ run: 1 hops followed files moved since the walk "
                                   "(app/kill/kill.go → app/proc/kill.go)")


class SeekTest(Repo):
    """`new ROOT --seek REGEX` and `new ROOT --questions`: earlier runs on the same root, and the stored questions."""

    def setUp(self):
        super().setUp()
        self.parent = os.path.join("/tmp", os.path.basename(self.root), "tracer")
        os.makedirs(self.parent)

    def tearDown(self):
        shutil.rmtree(os.path.dirname(self.parent), ignore_errors=True)
        super().tearDown()

    def sibling(self, name, text, root=None, age_hours=0.0, started=()):
        run = os.path.join(self.parent, name)
        os.makedirs(run)
        with open(os.path.join(run, "ROOT"), "w") as f:
            f.write((root or self.root) + "\n")
        if text is not None:
            with open(os.path.join(run, "1.hops"), "w") as f:
                f.write(text)
        for node in started:
            open(os.path.join(run, node + ".started"), "a").close()
        stamp = time.time() - age_hours * 3600
        os.utime(run, (stamp, stamp))
        return run

    def new(self, *flags, input=None):
        got = tt(["new", self.root, *flags], input=input)
        self.assertEqual(got.returncode, 0, got.stderr)
        lines = got.stdout.splitlines()
        shutil.rmtree(lines[0], ignore_errors=True)
        return lines[0], lines[1:]

    def test_seek_lists_a_matching_hop_of_a_finished_run_as_the_file_has_it_now(self):
        prior = self.sibling("20260101-000000-aaaa", self.good("1"))
        run, rest = self.new("--seek", "target is required")
        self.assertTrue(run.startswith(self.parent + os.sep), run)
        self.assertEqual(rest, [f'PRIOR {prior} node 1: app/srv/server.go:5  return errors.New("target is required")',
                                f"PRIOR: 1 hops in 1 runs — tt check {prior} --hops 1"])

    def test_the_first_output_line_stays_the_run_path_without_flags(self):
        self.sibling("20260101-000000-aaaa", self.good("1"))
        got = tt(["new", self.root])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertEqual(got.stdout.count("\n"), 1)
        self.assertTrue(os.path.isdir(got.stdout.strip()))

    def test_seek_counts_a_run_still_running_with_hops_and_no_answer_yet(self):
        text = "THREAD 2: kill\n@ app/kill/kill.go:5\n> \tif err := exec.Command(\"tmux\", \"kill-pane\", \"-t\", target).Run(); err != nil {\n"
        prior = self.sibling("20260101-000000-bbbb", text, started=("1",))
        _, rest = self.new("--seek", "kill/kill\\.go")
        self.assertEqual(len(rest), 2, rest)
        self.assertRegex(rest[0], rf"^PRIOR {prior} node 1: app/kill/kill\.go:5  if err := exec\.Command")
        self.assertEqual(rest[1], f"PRIOR: 1 hops in 1 runs — tt check {prior} --hops 1")

    def test_seek_skips_a_run_on_another_root_and_a_run_older_than_six_hours(self):
        self.sibling("20260101-000000-cccc", self.good("1"), root=os.path.join(self.root, "elsewhere"))
        self.sibling("20260101-000000-dddd", self.good("1"), age_hours=7)
        _, rest = self.new("--seek", "target")
        self.assertEqual(rest, ["PRIOR: none in 0 earlier runs on this root"])

    def test_seek_orders_the_newest_run_first_and_names_the_runs_it_scanned_when_nothing_matches(self):
        older = self.sibling("20260101-000000-eeee", self.good("1"), age_hours=3)
        newer = self.sibling("20260101-000000-ffff", self.good("1"), age_hours=1)
        _, rest = self.new("--seek", "target is required")
        self.assertEqual([l.split()[1] for l in rest[:2]], [newer, older])
        self.assertEqual(rest[2], f"PRIOR: 2 hops in 2 runs — tt check {newer} --hops 1")
        _, rest = self.new("--seek", "nothing_like_this")
        self.assertEqual(rest, ["PRIOR: none in 2 earlier runs on this root"])

    def test_seek_prints_at_most_thirty_lines_and_counts_the_rest_as_not_shown(self):
        many = hops("1", ["- x [app/srv/server.go:5]"],
                    ["@ app/srv/server.go:5", "> \t\treturn errors.New(\"target is required\")"] * 35,
                    ["END returned: x @ app/srv/server.go:5"])
        prior = self.sibling("20260101-000000-aaaa", many)
        _, rest = self.new("--seek", "target is required")
        self.assertEqual(len(rest), 31, rest)
        self.assertTrue(all(l.startswith(f"PRIOR {prior} node 1: ") for l in rest[:30]))
        self.assertEqual(rest[30], f"PRIOR: 35 hops in 1 runs, 5 not shown — tt check {prior} --hops 1")

    def test_seek_names_a_run_it_could_not_read_in_the_summary(self):
        self.sibling("20260101-000000-aaaa", self.good("1"))
        stray = os.path.join(self.parent, "20260101-000000-9999")
        os.makedirs(stray)
        _, rest = self.new("--seek", "target is required")
        self.assertRegex(rest[-1], r"^PRIOR: 1 hops in 1 runs — tt check \S+ --hops 1, 1 runs unreadable$")
        _, rest = self.new("--seek", "nothing_like_this")
        self.assertEqual(rest, ["PRIOR: none in 1 earlier runs on this root, 1 runs unreadable"])

    def test_seek_names_a_hops_file_it_could_not_read(self):
        prior = self.sibling("20260101-000000-aaaa", self.good("1"))
        os.remove(os.path.join(prior, "1.hops"))
        os.makedirs(os.path.join(prior, "1.hops"))  # open() fails for every user, root included
        _, rest = self.new("--seek", "target")
        self.assertEqual(rest, ["PRIOR: none in 1 earlier runs on this root, 1 runs unreadable"])

    def test_seek_refuses_a_regex_that_does_not_compile_before_it_creates_a_run(self):
        got = tt(["new", self.root, "--seek", "(unclosed"])
        self.assertEqual(got.returncode, 2)
        self.assertIn("TRACETREE FAILED", got.stderr)
        self.assertEqual(os.listdir(self.parent), [])

    def test_questions_are_stored_as_read(self):
        asked = "1. What calls Handle?\n   and from where\n2) What does Run wrap?\nQ3 Which tests pin it?\n"
        got = tt(["new", self.root, "--questions"], input=asked)
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertEqual(got.stdout.count("\n"), 1)
        with open(os.path.join(got.stdout.strip(), "questions")) as f:
            self.assertEqual(f.read(), asked)

    def test_stdin_is_never_read_without_the_flag(self):
        held = subprocess.Popen([sys.executable, SCRIPT, "new", self.root], stdin=subprocess.PIPE,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            held.wait(timeout=15)
            out = held.stdout.read()
        finally:
            held.kill()
            held.wait()
            for pipe in (held.stdin, held.stdout, held.stderr):
                pipe.close()
        self.assertEqual(held.returncode, 0)
        self.assertFalse(os.path.exists(os.path.join(out.strip(), "questions")))

    def test_empty_or_unnumbered_questions_fail_and_create_no_run(self):
        for text in ("", "\n  \n", "what calls Handle?\nand Run?\n"):
            got = tt(["new", self.root, "--questions"], input=text)
            self.assertEqual(got.returncode, 2, text)
            self.assertIn("TRACETREE FAILED", got.stderr)
            self.assertEqual(got.stdout, "")
        self.assertEqual(os.listdir(self.parent), [])


if __name__ == "__main__":
    unittest.main()
