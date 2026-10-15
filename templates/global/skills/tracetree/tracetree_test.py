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


def tt(args, input=None, timeout=60, cwd=None):
    return subprocess.run([sys.executable, SCRIPT] + args, input=input, capture_output=True, text=True,
                          timeout=timeout, cwd=cwd)


def hops(node, answer, hop_lines, tail):
    return "\n".join([f"THREAD {node}: the handler's error path", "ANSWER", *answer, *hop_lines, *tail]) + "\n"


def page_model(case, page, script):
    """The JSON a page carries in its data script: parsed straight out of the page, so a `</` left unescaped in file
    text would cut it short and fail here."""
    m = re.search(r'<script type="application/json" id="' + script + '">(.*?)</script>', page, re.S)
    case.assertIsNotNone(m, f"the page carries no {script} script")
    return json.loads(m.group(1))


def no_network(case, page):
    """Nothing in the page reaches off the machine: no remote src or href, import, stylesheet font or url()."""
    case.assertNotRegex(page, r"(src|href|action)\s*=\s*[\"']?(https?:)?//")
    case.assertNotRegex(page, r"(?i)@import|googleapis|gstatic|url\(\s*[\"']?(https?:)?//|\bfetch\(|XMLHttpRequest|\bhttps?://")


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

    def test_a_card_question_naming_several_numbers_prints_every_one_of_them_above_the_window(self):
        self.ask("1. What calls Handle?\n2) What does Run wrap?\n3. Who kills the pane?\n")
        got = tt(["start", self.run_dir, "2", "srv/server.go:5", "Q1 and Q3 — both ask for the caller (also Q3)"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertEqual(got.stdout.splitlines()[:3], ["QUESTION Q1: What calls Handle?",
                                                       "QUESTION Q3: Who kills the pane?",
                                                       "started 2 — app/srv/server.go"])
        named = tt(["start", self.run_dir, "3", "srv/server.go:5", "which of Q1 or Q2 is it"])
        self.assertEqual(named.stdout.splitlines()[0], "started 3 — app/srv/server.go")

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

    def test_a_cite_at_the_line_its_reader_gave_a_re_addressed_hop_moves_with_the_hop(self):
        for put in (self.write, self.store):
            with self.subTest(put=put.__name__):
                for f in os.listdir(self.run_dir):
                    if f.startswith("1."):
                        os.remove(os.path.join(self.run_dir, f))
                put("1", hops("1", ["- Run wraps the tmux failure in its own error [app/kill/kill.go:2]",
                                    "- and returns it to its caller [app/kill/kill.go:1-3]"],
                              ["@ app/kill/kill.go:2", "> \t\treturn fmt.Errorf(\"kill pane %s: %w\", target, err)"],
                              ["END returned: the wrapped error @ app/kill/kill.go:6"]))
                out = tt(["check", self.run_dir]).stdout
                self.assertIn("own error [app/kill/kill.go:6]   (↻ app/kill/kill.go:2→6)", out)
                self.assertIn("its caller [app/kill/kill.go:5-7]   (↻ app/kill/kill.go:1→5)", out)
                self.assertNotIn("unbacked", out)

    def test_a_cite_near_a_re_addressed_hops_given_line_moves_and_one_beyond_reach_stays_unbacked(self):
        path = os.path.join(self.root, "app", "wide", "wide.go")
        os.makedirs(os.path.dirname(path))
        with open(path, "w") as f:
            f.write("\n".join(["package wide", ""] + [f"// line {i}" for i in range(3, 21)]) + "\n")
        self.write("1", hops("1", ["- the hop's line, cited two short [app/wide/wide.go:11]",
                                   "- far from both addresses [app/wide/wide.go:4]"],
                             ["@ app/wide/wide.go:13", "> // line 15"],
                             ["END returned: the line @ app/wide/wide.go:15"]))
        out = tt(["check", self.run_dir]).stdout
        self.assertIn("cited two short [app/wide/wide.go:13]   (↻ app/wide/wide.go:11→13)", out)
        self.assertIn("unbacked app/wide/wide.go:4 — no verified hop or quoted text there", out)

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


class AnswerTest(Repo):
    """`answer RUN Q…` blocks until the nodes whose cards named those questions, and their children, have written,
    then prints only those questions' checked answers."""

    def setUp(self):
        super().setUp()
        with open(os.path.join(self.run_dir, "questions"), "w") as f:
            f.write("1. Where does an empty target fail?\n2. What closes the pane?\n")

    def answer(self, *args):
        got = tt(["answer", self.run_dir, *args])
        self.assertEqual(got.returncode, 0, got.stderr)
        return got.stdout

    def test_answer_waits_for_a_late_child_and_prints_only_its_question(self):
        tt(["start", self.run_dir, "1", "app/srv/server.go:3", "Q1 — the empty target"])
        tt(["start", self.run_dir, "1.1", "app/kill/kill.go:4"])
        tt(["start", self.run_dir, "2", "app/kill/kill.go:4", "Q2 — the pane"])
        self.write("1", self.good())
        late = threading.Timer(0.5, lambda: self.write("1.1", self.good("1.1")))
        late.start()
        try:
            out = self.answer("1", "--grace", "0.5", "--timeout", "30")
        finally:
            late.join()
        self.assertTrue(out.startswith("ANSWER after "), out)
        self.assertIn("· Q1 · every [path:N] below was re-read from disk just now", out)
        self.assertIn("Q1 · nodes 1, 1.1 · Where does an empty target fail?", out)
        self.assertIn("[1.1] THREAD 1.1", out)
        self.assertIn("Handle refuses an empty target", out)
        self.assertNotIn("[2]", out)
        self.assertNotIn("STILL WALKING", out)

    def test_answer_times_out_naming_the_node_still_walking(self):
        tt(["start", self.run_dir, "2", "app/kill/kill.go:4", "Q2 — the pane"])
        began = time.time()
        out = self.answer("2", "--grace", "0.2", "--timeout", "1.5")
        self.assertLess(time.time() - began, 20)
        self.assertTrue(out.startswith("ANSWER TIMEOUT after "), out)
        self.assertIn(f"STILL WALKING Q2: nodes 2 started, no file yet — `answer {self.run_dir} 2` again", out)

    def test_a_question_no_node_took_is_named_and_the_others_answered(self):
        tt(["start", self.run_dir, "1", "app/srv/server.go:3", "Q1 — the empty target"])
        self.write("1", self.good())
        began = time.time()
        out = self.answer("Q1", "Q2", "--start", "0.5", "--grace", "0.2", "--timeout", "30")
        self.assertLess(time.time() - began, 15)
        self.assertIn("NO THREAD Q2: no node took it", out)
        self.assertIn("[1] THREAD 1", out)

    def test_no_number_answers_every_stored_question(self):
        tt(["start", self.run_dir, "1", "app/srv/server.go:3", "Q1 — the empty target"])
        tt(["start", self.run_dir, "2", "app/kill/kill.go:4", "Q2 — the pane"])
        self.write("1", self.good())
        self.write("2", self.good("2"))
        out = self.answer("--grace", "0.2", "--timeout", "30")
        self.assertIn("· Q1, Q2 ·", out)
        self.assertIn("[1] THREAD 1", out)
        self.assertIn("[2] THREAD 2", out)

    def test_a_run_with_no_node_started_reports_it_on_timeout(self):
        out = self.answer("1", "--grace", "0.2", "--timeout", "1")
        self.assertIn("no question answered yet", out)
        self.assertIn(f"NOT STARTED Q1: no node has started yet — `answer {self.run_dir} 1` again", out)

    def test_a_directory_that_is_no_run_fails_naming_it(self):
        empty = tempfile.mkdtemp(prefix="tracetree-norun-")
        try:
            got = tt(["answer", empty, "1"])
        finally:
            shutil.rmtree(empty, ignore_errors=True)
        self.assertEqual(got.returncode, 2)
        self.assertIn("is not a tracetree run", got.stderr)


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
        for node in ("1", "2"):
            for extension in (".hops", ".write.json"):
                os.remove(os.path.join(self.run_dir, node + extension))
        self.node("1", ["LEAD the handler refuses first @ app/srv/server.go:5",
                        "NOT READ the wrapped error at app/kill/kill.go:6"])
        flagged = self.wait("1").splitlines()
        self.assertEqual(flagged[-2], "LEAD the handler refuses first @ app/srv/server.go:5 — run: node 1")
        self.assertEqual(flagged[-1], "NOT READ the wrapped error at app/kill/kill.go:6 — run: node 1 (grep only — read it)")

    def test_a_line_range_a_verified_hop_of_any_node_holds_is_marked_held_never_dropped(self):
        self.node("1", [])
        self.node("2", ["NOT READ server.go lines 1-4 and 5-8 beyond what was needed",
                        "NOT READ app/srv/server.go:1-4, the package header",
                        "LEAD app/kill/kill.go lines 1-9 untouched"],
                  hop=("app/kill/kill.go:6", "\t\treturn fmt.Errorf(\"kill pane %s: %w\", target, err)"))
        out = self.wait("2").splitlines()
        self.assertEqual(out[-4:], [
            "OPEN",
            "NOT READ server.go lines 1-4 and 5-8 beyond what was needed — run: node 2 "
            "(held: node 1 verified app/srv/server.go:5)",
            "NOT READ app/srv/server.go:1-4, the package header — run: node 2 (grep only — read it)",
            "LEAD app/kill/kill.go lines 1-9 untouched — run: node 2 (held: node 2 verified app/kill/kill.go:6)"])

    def test_counts_names_nodes_with_no_end_noend_so_it_never_reads_as_the_open_block(self):
        self.store("1", hops("1", ["- x [app/srv/server.go:5]"],
                             ["@ app/srv/server.go:5", "> \t\treturn errors.New(\"target is required\")"],
                             ["NOT READ whether Run retries"]))
        out = self.wait("1")
        counts = next(l for l in out.splitlines() if l.startswith("COUNTS: "))
        self.assertTrue(counts.endswith(" · noend=1"), counts)
        self.assertNotIn("open=", counts)
        self.assertEqual(out.splitlines()[-2:], ["OPEN", "NOT READ whether Run retries — run: node 1"])

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


class ShowTest(Repo):
    """`show RUN FILE:A-B ...`: the caller's read through the run — each block as the file holds it now, every line a
    verified hop holds marked with its nodes, the open items of those nodes under it; any size, any number of calls."""

    def show(self, *blocks):
        return tt(["show", self.run_dir, *blocks])

    def held(self):
        self.write("1", hops("1", ["- x [app/srv/server.go:3]"],
                             ["@ app/srv/server.go:3", "> func Handle(req Request) error {",
                              "> \tif req.Target == \"\" {", "> \t\treturn errors.New(\"target is required\")"],
                             ["NOT READ whether Run retries after a failed kill",
                              "END returned: x @ app/srv/server.go:5"]))
        self.write("2", self.good("2"))

    def test_each_line_a_verified_hop_holds_is_marked_with_its_nodes_and_their_open_items_follow(self):
        self.held()
        got = self.show("app/srv/server.go:2-7", "app/kill/kill.go:4")
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertEqual(got.stdout.splitlines(), [
            "== app/srv/server.go:2-7 · held 3/6",
            "     2\t",
            "     3 [1]\tfunc Handle(req Request) error {",
            "     4 [1]\t\tif req.Target == \"\" {",
            "     5 [1,2]\t\t\treturn errors.New(\"target is required\")",
            "     6\t\t}",
            "     7\t\treturn kill.Run(req.Target)",
            "   open: NOT READ whether Run retries after a failed kill — run: node 1",
            "== app/kill/kill.go:4-4 · held 0/1",
            "     4\tfunc Run(target string) error {"])

    def test_after_an_edit_moves_the_code_the_marks_follow_the_quoted_lines(self):
        self.held()
        with open(os.path.join(self.root, "app", "srv", "server.go"), "w") as f:
            f.write("\n".join([*SERVER[:2], "// Handle serves one request.", "// It refuses an empty target.",
                               *SERVER[2:]]) + "\n")
        got = self.show("app/srv/server.go:4-7")
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertEqual(got.stdout.splitlines()[:5], [
            "== app/srv/server.go:4-7 · held 3/4",
            "     4\t// It refuses an empty target.",
            "     5 [1]\tfunc Handle(req Request) error {",
            "     6 [1]\t\tif req.Target == \"\" {",
            "     7 [1,2]\t\t\treturn errors.New(\"target is required\")"])

    def test_a_block_over_thirty_lines_prints_whole_and_a_second_call_still_prints(self):
        self.held()
        with open(os.path.join(self.root, "app", "big.txt"), "w") as f:
            f.write("".join(f"row {i}\n" for i in range(1, 60)))
        got = self.show("app/big.txt:1-59")
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertEqual(len(got.stdout.splitlines()), 60)
        self.assertIn("    59\trow 59", got.stdout)
        again = self.show("app/srv/server.go:3-3")
        self.assertEqual(again.returncode, 0, again.stderr)
        self.assertIn("     3 [1]\tfunc Handle(req Request) error {", again.stdout)

    def test_a_missing_file_or_range_is_refused_on_its_own_line_and_the_others_still_print(self):
        self.held()
        got = self.show("nowhere.go:1-3", "app/srv/server.go:6-12", "app/srv/server.go:x", "app/srv/server.go:8")
        self.assertEqual(got.returncode, 0, got.stderr)
        lines = got.stdout.splitlines()
        self.assertTrue(lines[0].startswith("REFUSED nowhere.go:1-3 — no such file"), lines[0])
        self.assertTrue(lines[1].startswith("REFUSED app/srv/server.go:6-12 — "), lines[1])
        self.assertIn("8 lines", lines[1])
        self.assertEqual(lines[2], "REFUSED app/srv/server.go:x — not FILE:A-B")
        self.assertEqual(lines[3:], ["== app/srv/server.go:8-8 · held 0/1", "     8\t}"])

    def test_show_needs_a_run_with_hops_and_a_block(self):
        self.assertEqual(tt(["show", self.run_dir]).returncode, 2)
        empty = self.show("app/srv/server.go:1-2")
        self.assertEqual(empty.returncode, 2)
        self.assertIn("no .hops file", empty.stderr)


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

    def test_each_walk_step_carries_the_kind_of_link_from_the_step_before_it(self):
        self.walk(extra=["@ app/kill/kill.go:6", "> \t\treturn fmt.Errorf(\"kill pane %s: %w\", target, err)",
                         "= the error is wrapped",
                         "@ app/log/log.go:4", "> \tprintln(msg)", "= the message is printed"])
        self.write("2", hops("2", ["- make test runs check.sh [Makefile:2]"],
                             ["@ Makefile:2", "> \t./scripts/check.sh all", "= make runs the check script",
                              "@ scripts/check.sh:5", ">   go test ./...", "= the script runs go test"],
                             ["END executed: go test @ scripts/check.sh:5"]))
        data = json.loads(tt(["map", self.run_dir, "--json"]).stdout)
        self.assertEqual([(s["c"], s["link"]) for s in data["threads"]["1"]["walk"]],
                         [("app/srv/server.go::Handle", None), ("app/kill/kill.go::Run", "call"),
                          ("app/kill/kill.go::Run", "same"), ("app/log/log.go::Note", "then")])
        self.assertEqual([s["link"] for s in data["threads"]["2"]["walk"]], [None, "names"])
        _, model = self.page()
        self.assertEqual([s["ln"] for s in model["threads"][0]["steps"]], [None, "call", "same", "then"])
        self.assertEqual([s["ln"] for s in model["threads"][1]["steps"]], [None, "names"])

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
        no_network(self, page)

    def test_the_page_model_carries_the_components_and_links_the_views_draw(self):
        self.walk()
        _, model = self.page()
        self.assertEqual(model["repo"], os.path.basename(self.root))
        self.assertEqual([c["id"] for c in model["comps"]], ["app/kill/kill.go::Run", "app/srv/server.go::Handle"])
        self.assertEqual([(model["comps"][e[0]]["name"], model["comps"][e[1]]["name"], e[2]) for e in model["edges"]],
                         [("Handle", "Run", "call")])
        self.assertEqual((model["focal"], model["all"]), (None, False))

    def test_to_and_from_pick_the_hourglass_component_and_all_sets_its_toggle(self):
        self.walk()
        _, model = self.page(["--to", "Run", "--all"])
        self.assertEqual((model["comps"][model["focal"]]["id"], model["all"]), ("app/kill/kill.go::Run", True))
        _, model = self.page(["--from", "app/srv/server.go::Handle"])
        self.assertEqual((model["comps"][model["focal"]]["name"], model["all"]), ("Handle", False))
        out = os.path.join(self.run_dir, "none.html")
        got = tt(["map", self.run_dir, "--html", out, "--to", "Nowhere"])
        self.assertEqual(got.returncode, 2)
        self.assertIn("no component matches Nowhere", got.stderr)
        for extra, why in ((["--to", "app"], "app matches 2 components (app/kill/kill.go::Run, app/srv/server.go::Handle)"),
                           (["--from", "Handle", "--to", "Run"], "--html centres the hourglass on one component")):
            got = tt(["map", self.run_dir, "--html", out, *extra])
            self.assertEqual(got.returncode, 2, extra)
            self.assertIn(why, got.stderr)
        self.assertFalse(os.path.exists(out))

    def test_a_rejected_hop_is_a_hole_step_and_a_file_that_is_gone_is_named_not_dropped(self):
        self.walk(extra=["a stray line of prose", "@ app/gone/gone.go:3", "> \tvanished()", "= a file no longer there",
                         "@ app/log/log.go:4", "> \tprintln(msg)", "= the message is printed",
                         "@ app/srv/server.go:2", "> \tghost()", "= a line no file holds"], raw=True)
        _, model = self.page()
        steps = model["threads"][0]["steps"]
        self.assertEqual([(model["files"][s["f"]]["p"], s["st"]) for s in steps],
                         [("app/srv/server.go", "verified"), ("app/kill/kill.go", "verified"), ("app/gone/gone.go", "rejected"),
                          ("app/log/log.go", "verified"), ("app/srv/server.go", "rejected")])
        self.assertEqual([s["c"] for s in steps][2::2], [-1, -1])
        self.assertEqual([a["g"] for a in model["threads"][0]["answers"]], ["broke"])
        files = {f["p"]: f for f in model["files"]}
        self.assertTrue(files["app/gone/gone.go"]["gone"])
        self.assertNotIn("lines", files["app/gone/gone.go"])
        self.assertEqual(files["app/gone/gone.go"]["n"], 3)
        self.assertEqual((files["app/srv/server.go"]["bad"], files["app/srv/server.go"]["hops"]), (1, 1))
        self.assertNotIn("gone", files["app/srv/server.go"])

    def test_every_question_a_node_names_reaches_the_page_the_first_as_its_question(self):
        with open(os.path.join(self.run_dir, "questions"), "w") as f:
            f.write("1. What does Handle hand on?\n2. Who kills the pane?\n3. Where is it logged?\n")
        for node, q in (("1", "Q1 — tt start prints its text"), ("2", "Q2 and Q3 and what Run wraps (also Q6)"),
                        ("3", "Q3"), ("4", "which tests pin Run")):
            tt(["start", self.run_dir, node, "app/srv/server.go:3", q])
            self.write(node, self.good(node))
        _, model = self.page()
        by = {t["id"]: t for t in model["threads"]}
        self.assertEqual([(by[n]["q"], by[n]["qs"]) for n in "1234"],
                         [(1, [1]), (2, [2, 3, 6]), (3, [3]), (None, [])])

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

    def page(self, extra=()):
        out = os.path.join(self.run_dir, "page.html")
        got = tt(["map", self.run_dir, "--html", out, *extra])
        self.assertEqual(got.returncode, 0, got.stderr)
        with open(out, encoding="utf-8") as f:
            page = f.read()
        return page, page_model(self, page, "loom-data")

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
        _, model = self.page()
        self.assertEqual(model["repo"], os.path.basename(self.root))
        self.assertEqual(model["questions"], {"1": "What does Handle hand on?", "2": "Who kills the pane?"})
        node = model["threads"][0]
        self.assertEqual((node["id"], node["q"], node["from"]), ("1", 2, "app/srv/server.go:3"))
        self.assertEqual([(a["g"], a["why"]) for a in node["answers"]],
                         [("held", []), ("missing", ["? `nothing at all` not in the cited file"])])
        self.assertEqual([c["p"] + ":" + str(c["l"]) for a in node["answers"] for c in a["cites"]],
                         ["app/srv/server.go:7", "app/kill/kill.go:5"])
        self.assertEqual((model["counts"]["answers"], model["counts"]["flagged"]), (2, 1))
        self.assertEqual(node["end"][0]["k"], "executed")
        self.assertGreaterEqual(node["s"], 0)
        self.assertLessEqual(node["s"], node["w"])
        self.assertEqual(model["missing"].keys(), {"2"})
        self.assertIsNone(model["dur"])
        self.assertEqual({f["p"]: f["lines"] for f in model["files"]},
                         {"app/srv/server.go": SERVER, "app/kill/kill.go": KILL})

    def test_each_cite_is_graded_by_its_own_address_and_one_no_hop_backs_is_never_held(self):
        wide = ["package wide", ""] + [f"// line {i}" for i in range(3, 21)]
        path = os.path.join(self.root, "app", "wide", "wide.go")
        os.makedirs(os.path.dirname(path))
        with open(path, "w") as f:
            f.write("\n".join(wide) + "\n")
        self.store("1", hops("1", ["- The package is wide [app/wide/wide.go:1] and so is [app/wide/wide.go:15]",
                                   "- Ghost lives in [app/nope/nope.go:3]",
                                   "- Handle ends past its file [app/srv/server.go:99]",
                                   "- Run calls `kill-pane` [app/kill/kill.go:1]"],
                             ["@ app/wide/wide.go:1", "> package wide", "= the package line",
                              "@ app/kill/kill.go:5", f"> {EXEC}", "= tmux kills the pane"],
                             ["END executed: tmux kill-pane @ app/kill/kill.go:5"]))
        _, model = self.page()
        self.assertEqual([(a["g"], [(c["p"], c["l"], c["g"]) for c in a["cites"]]) for a in model["threads"][0]["answers"]],
                         [("unbacked", [("app/wide/wide.go", 1, "held"), ("app/wide/wide.go", 15, "unbacked")]),
                          ("unbacked", [("app/nope/nope.go", 3, "unbacked")]),
                          ("unbacked", [("app/srv/server.go", 99, "unbacked")]),
                          ("moved", [("app/kill/kill.go", 5, "moved")])])

    def test_file_text_that_closes_a_script_stays_inside_the_pages_data(self):
        web = ["package web", "", "func Page() string {", '\treturn "<script>boot()</script>"', "}",
               f"// built in {self.root}/out and {self.root}2"]
        path = os.path.join(self.root, "app", "web", "web.go")
        os.makedirs(os.path.dirname(path))
        with open(path, "w") as f:
            f.write("\n".join(web) + "\n")
        self.write("1", hops("1", ["- Page closes its own </script> [app/web/web.go:4]"],
                             ["@ app/web/web.go:4", f"> {web[3]}", f"= the page ends its </script> in {self.root}2"],
                             ["END returned: the markup @ app/web/web.go:4"]))
        page, model = self.page()
        self.assertNotIn("<script>boot()", page)
        self.assertEqual(next(f["lines"] for f in model["files"] if f["p"] == "app/web/web.go"), web)
        self.assertEqual(model["threads"][0]["steps"][0]["note"], f"the page ends its </script> in {self.root}2")

    def test_past_the_text_budget_a_file_carries_only_the_lines_near_its_hops(self):
        big = ["package big", "", "func Big() error {", "\treturn nil", "}"] + [f"// {i:06d} " + "x" * 80 for i in range(50000)]
        path = os.path.join(self.root, "app", "big", "big.go")
        os.makedirs(os.path.dirname(path))
        with open(path, "w") as f:
            f.write("\n".join(big) + "\n")
        self.write("1", hops("1", ["- Big returns nil [app/big/big.go:4]"], ["@ app/big/big.go:4", "> \treturn nil"],
                             ["END returned: nil @ app/big/big.go:4"]))
        _, model = self.page()
        entry = next(f for f in model["files"] if f["p"] == "app/big/big.go")
        self.assertEqual((entry["n"], entry.get("lines")), (len(big), None))
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

    def test_a_quote_whose_indent_was_escaped_with_one_backslash_is_stored_on_the_first_write(self):
        got = self.put("1", ["@ app/kill/kill.go:5", "> \\    " + EXEC.strip(), "@ app/kill/kill.go:6", "> \\" + ERRF])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertFalse(os.path.exists(os.path.join(self.run_dir, "1.refused")))
        self.assertEqual([(e["line"], e["status"]) for e in self.sidecar()], [(5, "verified"), (6, "verified")])
        kept = self.put("2", ["@ app/kill/kill.go:5", "> \\" + EXEC.strip()])
        self.assertEqual(kept.returncode, 2, "a backslash before no whitespace is part of the quote")

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

    def test_a_second_writer_of_one_id_cannot_replace_the_winners_sidecar(self):
        won = self.put("1", ["@ app/srv/server.go:5", f"> {REQUIRED}"])
        self.assertEqual(won.returncode, 0, won.stderr)
        sidecar, hops_file = (os.path.join(self.run_dir, "1" + ext) for ext in (".write.json", ".hops"))
        with open(sidecar, "rb") as f:
            winners_sidecar = f.read()
        with open(hops_file, "rb") as f:
            winners_hops = f.read()
        # the loser passed the "already written" check before the winner stored: its view of the hops file is absent
        loser = ("import importlib.util, os, sys\n"
                 "sys.path.insert(0, os.path.dirname(os.path.abspath(sys.argv[1])))\n"
                 "spec = importlib.util.spec_from_file_location('tracetree_loser', sys.argv[1])\n"
                 "kit = importlib.util.module_from_spec(spec)\n"
                 "spec.loader.exec_module(kit)\n"
                 "stale, exists = os.path.join(os.path.abspath(sys.argv[2]), '1.hops'), os.path.exists\n"
                 "os.path.exists = lambda p: False if p == stale else exists(p)\n"
                 "kit.cmd_write(sys.argv[2], '1')\n")
        got = subprocess.run([sys.executable, "-c", loser, SCRIPT, self.run_dir],
                             input=hops("1", ["- x"], ["@ app/kill/kill.go:5", f"> {EXEC}"],
                                        ["END returned: x @ app/kill/kill.go:5"]),
                             capture_output=True, text=True, timeout=60)
        self.assertEqual(got.returncode, 2, got.stdout + got.stderr)
        with open(sidecar, "rb") as f:
            self.assertEqual(f.read(), winners_sidecar)
        with open(hops_file, "rb") as f:
            self.assertEqual(f.read(), winners_hops)
        self.assertIn("already written", got.stderr)
        self.assertEqual(sorted(os.listdir(self.run_dir)), ["1.hops", "1.write.json", "ROOT"])

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
MISCOPY = ("· node 1: the kit corrected a miscopied path on 1 hop — each quote sits at its cited line "
           "(pkg/kill/kill.go → app/kill/kill.go)")


class WarningsTest(Stored):
    """The kit says out loud, without drilling, that a node invented paths, stored hops that do not hold, or flagged
    answers: a WARNINGS block first in `check` and right after the WAIT line in `wait`."""

    def put(self, node, hop_lines, answer="- x", tail=("END returned: x @ app/srv/server.go:5",)):
        return tt(["write", self.run_dir, node], input=hops(node, [answer], hop_lines, list(tail)))

    def placed_node(self, node="1", paths=("pkg/kill/kill.go",)):
        lines = []
        for path in paths:
            lines += [f"@ {path}:4", f"> {EXEC}"]
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
        self.assertEqual(lines[1], "⚠ node 1: 1 hop named a path that is not the file — the kit placed them "
                                   "(pkg/kill/kill.go → app/kill/kill.go)")
        self.assertIn("drifted=0 · placed=1 · followed=0 · refused=0", lines[-1])

    def test_a_run_without_a_sidecar_is_judged_by_the_check_time_resolver(self):
        out = self.hop("pkg/kill/kill.go", 4, EXEC, view=None).splitlines()
        self.assertEqual(out[0], HEADER_BROKE.format(n=1, m=1))
        self.assertEqual(out[1], "⚠ node 1: 1 hop named a path that is not the file — the kit placed them "
                                 "(pkg/kill/kill.go → app/kill/kill.go)")
        self.assertIn("placed=1 · followed=0 · refused=0", out[-1])
        sibling = self.hop("app/kill/pane.go", 4, EXEC, view=None)
        self.assertIn("(app/kill/pane.go → app/kill/kill.go)", sibling)

    def test_a_later_hop_on_the_same_invented_path_is_placed_through_the_first_and_still_counted(self):
        self.store("1", hops("1", ["- x"], ["@ pkg/kill/kill.go:4", f"> {EXEC}", "@ pkg/kill/kill.go:7", f"> {ERRF}"],
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

    def test_a_placed_hop_quoting_its_very_cited_line_only_miscopied_its_path_and_breaks_no_contract(self):
        got = self.put("1", ["@ pkg/kill/kill.go:5", f"> {EXEC}"])
        self.assertEqual(got.returncode, 0, got.stderr)
        lines = self.check().splitlines()
        self.assertEqual(lines[:2], ["WARNINGS: none", MISCOPY])
        self.assertIn("placed=1 · followed=0 · refused=0", lines[-1])
        line = next(l for l in self.wait("1").splitlines() if l.startswith("MAP "))
        self.assertNotIn("broke the hop contract", line)
        data = json.loads(tt(["map", self.run_dir, "--json"]).stdout)
        self.assertEqual((data["warnings"], data["notes"]), ([], [MISCOPY]))

    def test_a_run_without_a_sidecar_tells_a_miscopy_from_a_placement_by_the_cited_line(self):
        self.assertEqual(self.hop("pkg/kill/kill.go", 5, EXEC, view=None).splitlines()[:2], ["WARNINGS: none", MISCOPY])
        self.assertEqual(self.hop("pkg/kill/kill.go", 4, EXEC, view=None).splitlines()[0], HEADER_BROKE.format(n=1, m=1))

    def test_a_miscopy_beside_a_broken_node_follows_the_block_and_counts_no_node(self):
        self.assertEqual(self.put("1", ["@ pkg/kill/kill.go:5", f"> {EXEC}"]).returncode, 0)
        self.store("2", hops("2", ["- x"], ["@ app/srv/server.go:5", f"> {GHOST_HOP}"],
                             ["END returned: x @ app/srv/server.go:5"]))
        lines = self.check().splitlines()
        self.assertEqual(lines[:2], [HEADER_BROKE.format(n=1, m=2), "⚠ node 2: 1 hop REJECTED"])
        self.assertIn(MISCOPY, lines)
        self.assertTrue(any("could not look" in line and "2.write.json" in line for line in lines))

    def test_the_page_shows_a_miscopy_as_a_quiet_note_and_no_red_block(self):
        self.assertEqual(self.put("1", ["@ pkg/kill/kill.go:5", f"> {EXEC}"]).returncode, 0)
        out = os.path.join(self.run_dir, "page.html")
        self.assertEqual(tt(["map", self.run_dir, "--html", out]).returncode, 0)
        with open(out, encoding="utf-8") as f:
            page = f.read()
        self.assertNotIn('id="warnings"', page)
        self.assertIn(f'<section id="kitnotes" aria-label="Kit notes"><p>{MISCOPY}</p></section>', page)
        self.assertLess(page.index('id="kitnotes"'), page.index('id="view-loom"'))

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
        self.assertEqual(lines[1], "⚠ node 1: write refused once, then stored with 1 hop that still do not hold")
        self.assertEqual(lines[2], "⚠ node 1: 1 hop REJECTED")
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
        self.assertEqual(out[1], "⚠ node 1: 1 hop REJECTED")

    def test_the_header_counts_only_nodes_that_broke_the_contract_out_of_the_whole_run(self):
        self.store("1", self.good())
        self.placed_node("2")
        self.placed_node("3")
        lines = self.check().splitlines()
        self.assertEqual(lines[0], HEADER_BROKE.format(n=2, m=3))
        self.assertTrue(lines[1].startswith("⚠ node 2: 1 hop named"), lines[1])
        self.assertTrue(lines[2].startswith("⚠ node 3: 1 hop named"), lines[2])

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

    def test_missing_sidecar_in_a_modern_run_reports_unknown_placement(self):
        self.write("1", self.good())
        self.write("2", self.good("2"))
        os.remove(os.path.join(self.run_dir, "2.write.json"))
        for flags in ((), ("--hops", "2"), ("--full", "2")):
            with self.subTest(flags=flags):
                out = self.check(*flags)
                self.assertIn("2.write.json cannot be used", out)
                self.assertIn("missing sidecar", out)
                self.assertIn("could not look", out)

        self.store("3", hops("3", ["- rejected quote"], ["@ app/srv/server.go:5", f"> {GHOST_HOP}"],
                             ["END returned: x @ app/srv/server.go:5"]))
        for flags in ((), ("--hops", "2"), ("--full", "2")):
            with self.subTest(incident="rejected", flags=flags):
                out = self.check(*flags)
                self.assertIn("REJECTED", out)
                self.assertIn("2.write.json cannot be used", out)
                self.assertIn("missing sidecar", out)
                self.assertIn("could not look", out)
        mapped = tt(["map", self.run_dir, "--json"])
        self.assertEqual(mapped.returncode, 0, mapped.stderr)
        warnings = json.loads(mapped.stdout)["warnings"]
        self.assertTrue(any("REJECTED" in line for line in warnings))
        self.assertTrue(any("could not look" in line and "2.write.json" in line for line in warnings))

    def test_interrupted_writer_never_leaves_hops_without_metadata(self):
        # Stop at sidecar publication. Old order has already published hops;
        # metadata-first publication leaves no readable node at this point.
        interrupted = ("import builtins, importlib.util, os, sys\n"
                       "sys.path.insert(0, os.path.dirname(os.path.abspath(sys.argv[1])))\n"
                       "spec = importlib.util.spec_from_file_location('interrupted_kit', sys.argv[1])\n"
                       "kit = importlib.util.module_from_spec(spec)\n"
                       "spec.loader.exec_module(kit)\n"
                       "real_open = builtins.open\n"
                       "def stop_at_sidecar(path, mode='r', *args, **kwargs):\n"
                       "    if '.write.json' in str(path) and mode in ('w', 'x'):\n"
                       "        os._exit(97)\n"
                       "    return real_open(path, mode, *args, **kwargs)\n"
                       "builtins.open = stop_at_sidecar\n"
                       "kit.cmd_write(sys.argv[2], '1')\n")
        got = subprocess.run([sys.executable, '-I', '-c', interrupted, SCRIPT, self.run_dir],
                             input=self.good(), capture_output=True, text=True, timeout=60)
        self.assertEqual(got.returncode, 97, got.stderr)
        self.assertFalse(os.path.exists(os.path.join(self.run_dir, '1.hops')),
                         'an interrupted writer must not publish a node before its placement metadata')
        checked = tt(['check', self.run_dir])
        self.assertEqual(checked.returncode, 2)
        self.assertIn('holds no .hops file', checked.stderr)

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
        self.assertTrue(lines[2].startswith("⚠ node 1: 1 hop named"), lines[2])
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
                                  ["@ pkg/srv/server.go:4", "> \t\treturn errors.New(\"target is required\")"],
                                  [f"NOT READ whether node {node} retries", "END returned: x @ app/srv/server.go:5"]))
        out = self.wait("1", "2", "3")
        self.assertLessEqual(len(out), 28000)
        lines = out.splitlines()
        self.assertEqual(lines[0].split()[0], "WAIT")
        self.assertEqual(lines[1], HEADER_BROKE.format(n=3, m=3))
        for i, node in enumerate("123"):
            self.assertTrue(lines[2 + i].startswith(f"⚠ node {node}: 1 hop named a path"), lines[2 + i])
        self.assertTrue(lines[5].startswith("COUNTS: "), lines[5])
        self.assertEqual(lines[-1], f"FULL {self.run_dir}/answers.txt")

    def test_the_map_line_and_the_map_text_name_the_nodes_that_broke_the_contract(self):
        self.placed_node()
        line = next(l for l in self.wait("1").splitlines() if l.startswith("MAP "))
        self.assertTrue(line.endswith(" · ⚠ 1 node broke the hop contract"), line)
        with open(os.path.join(self.run_dir, "map.txt")) as f:
            first = f.readline().rstrip("\n")
        self.assertTrue(first.startswith("MAP ") and first.endswith(" · ⚠ 1 node broke the hop contract"), first)
        mapped = tt(["map", self.run_dir])
        self.assertEqual(mapped.returncode, 0, mapped.stderr)
        self.assertEqual(mapped.stdout.splitlines()[0], first)

    def test_json_carries_the_warning_lines_and_an_empty_list_when_there_are_none(self):
        self.write("1", self.good())
        clean = json.loads(tt(["map", self.run_dir, "--json"]).stdout)
        self.assertEqual(clean["warnings"], [])
        for extension in (".hops", ".write.json"):
            os.remove(os.path.join(self.run_dir, "1" + extension))
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
        for extension in (".hops", ".write.json"):
            os.remove(os.path.join(self.run_dir, "1" + extension))
        self.placed_node()
        self.assertEqual(tt(["map", self.run_dir, "--html", out]).returncode, 0)
        with open(out) as f:
            page = f.read()
        self.assertIn('id="warnings"', page)
        self.assertIn('<section id="warnings" role="alert" aria-label="Warnings">', page)
        self.assertLess(page.index('id="warnings"'), page.index('id="view-loom"'))
        self.assertIn("1 of 1 nodes broke the hop contract", page)
        self.assertIn("pkg/kill/kill.go → app/kill/kill.go", page)
        head = page[:page.index("</head>")]
        at = head.index(":root {")
        self.assertRegex(head[at:head.index("}", at)], r"--alarm: #[0-9a-f]{6}; --alarm-ink: #[0-9a-f]{6};")
        self.assertRegex(head, r"#warnings \{[^}]*background: var\(--alarm\); color: var\(--alarm-ink\);")
        no_network(self, page)


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
        self.assertTrue(lines[1].startswith("⚠ WARNING — stored with 1 hop that do not hold"), lines[1])


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
        self.assertEqual(lines[1], "⚠ run: 1 hop DRIFTED — the code changed since the walk: re-read before pasting")
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
                         "⚠ run: 1 hop DRIFTED — the code changed since the walk: re-read before pasting")
        waited = tt(["wait", self.run_dir, "1", "--grace", "0", "--timeout", "30"]).stdout
        self.assertTrue(next(l for l in waited.splitlines() if l.startswith("MAP ")).endswith(" · ⚠ 1 drifted"))

    def test_a_run_with_no_sidecar_gets_the_drift_line_from_the_commit_it_started_at(self):
        self.old_run()
        with open(os.path.join(self.run_dir, "BASE"), "w") as f:
            f.write(self.base + "\n")
        self.edit(commit=True)
        self.assertEqual(self.check().splitlines()[1],
                         "⚠ run: 1 hop DRIFTED — the code changed since the walk: re-read before pasting")


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
        self.store("1", hops("1", ["- x"], self.hop_lines(("pkg/kill/kill.go", 4, EXEC)),
                             ["END returned: x @ app/srv/server.go:5"]))
        self.set_base()
        self.move_kill()
        lines = self.check().splitlines()
        self.assertEqual(lines[0], HEADER_BROKE.format(n=1, m=1))
        self.assertEqual(lines[1], "⚠ node 1: 1 hop named a path that is not the file — the kit placed them "
                                   "(pkg/kill/kill.go → app/proc/kill.go)")
        self.assertIn("placed=1 · followed=0 · refused=0", lines[-1])

    def test_a_given_file_the_base_held_without_the_quote_stays_placed(self):
        self.add("app/kill/pane.go", ["package kill", "", "func Pane() {}"])
        git(self.root, "add", "-A")
        git(self.root, "commit", "-q", "-m", "pane")
        self.base = git(self.root, "rev-parse", "HEAD")
        self.store("1", hops("1", ["- x"], self.hop_lines(("app/kill/pane.go", 4, EXEC)),
                             ["END returned: x @ app/srv/server.go:5"]))
        self.set_base()
        out = self.check()
        self.assertIn("⚠ node 1: 1 hop named a path that is not the file — the kit placed them "
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
        self.assertIn("⚠ node 1: 1 hop named a path that is not the file", out)

    def test_a_git_that_cannot_answer_leaves_the_hop_placed_and_says_it_could_not_look(self):
        self.store("1", hops("1", ["- x"], self.hop_lines(("app/kill/kill.go", 4, EXEC)),
                             ["END returned: x @ app/srv/server.go:5"]))
        self.move_kill()
        self.set_base("0" * 40)
        out = self.check()
        self.assertEqual(out.count("DRIFT UNKNOWN — "), 1, out)
        self.assertIn("⚠ node 1: 1 hop named a path that is not the file — the kit placed them "
                      "(app/kill/kill.go → app/proc/kill.go)", out)
        self.assertIn("placed=1 · followed=0", out)

    def test_a_followed_hop_beside_a_real_incident_adds_one_run_line(self):
        self.store("1", hops("1", ["- x"], self.hop_lines(("app/kill/kill.go", 5, EXEC), ("app/srv/server.go", 5, GHOST_HOP)),
                             ["END returned: x @ app/srv/server.go:5"]))
        self.set_base()
        self.move_kill()
        lines = self.check().splitlines()
        self.assertEqual(lines[0], HEADER_BROKE.format(n=1, m=1))
        self.assertEqual(lines[1], "⚠ node 1: 1 hop REJECTED")
        self.assertEqual(lines[2], "⚠ run: 1 hop followed files moved since the walk "
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
        self.assertEqual(lines[1], "⚠ run: 1 hop DRIFTED — the code changed since the walk: re-read before pasting")
        self.assertEqual(lines[2], "⚠ run: 1 hop followed files moved since the walk "
                                   "(app/kill/kill.go → app/proc/kill.go)")


class IntakeTest(Repo):
    """`intake`: the PreToolUse hook that opens a run for a tracer spawn and tells its caller how to pull answers."""

    def setUp(self):
        super().setUp()
        self.parent = os.path.join("/tmp", os.path.basename(self.root), "tracer")

    def tearDown(self):
        shutil.rmtree(os.path.dirname(self.parent), ignore_errors=True)
        super().tearDown()

    def hook(self, prompt, subagent="tracer", tool="Agent", cwd="/nonexistent-cwd"):
        payload = {"hook_event_name": "PreToolUse", "tool_name": tool, "cwd": cwd,
                   "tool_input": {"subagent_type": subagent, "description": "trace the pane", "prompt": prompt}}
        got = tt(["intake"], input=json.dumps(payload))
        self.assertEqual(got.returncode, 0, got.stderr)
        return got.stdout

    def runs(self):
        return sorted(os.listdir(self.parent)) if os.path.isdir(self.parent) else []

    def test_a_tracer_spawn_gets_its_run_opened_and_its_brief_carries_it(self):
        for head in (f"Repo root: {self.root} (read-only; Go module in app/)", f"Repo root / TREE = {self.root}",
                     f"Repo root {self.root} (Go module in app/). Goal: I will add a cost ledger.",
                     f"Repo root: {self.root}."):
            with self.subTest(head=head):
                out = json.loads(self.hook(f"{head}\nGoal: close panes safely\n"
                                           "1. Where does an empty target fail?\n2. What closes the pane?\n"))
                spec = out["hookSpecificOutput"]
                lines = spec["updatedInput"]["prompt"].splitlines()
                self.assertEqual(lines[0], head)
                self.assertTrue(lines[1].startswith("RUN: " + self.parent + os.sep), lines[1])
                run = lines[1][len("RUN: "):]
                with open(os.path.join(run, "ROOT")) as f:
                    self.assertEqual(f.read().strip(), self.root)
                with open(os.path.join(run, "questions")) as f:
                    self.assertEqual(f.read(), "1. Where does an empty target fail?\n2. What closes the pane?\n")
                self.assertEqual(spec["updatedInput"]["description"], "trace the pane")
                self.assertEqual(spec["updatedInput"]["subagent_type"], "tracer")
                self.assertEqual(spec["permissionDecision"], "allow")
                self.assertIn(f"tracer RUN {run} holds your questions 1, 2. Its final, returned when the run ends, "
                              "is your answer", spec["additionalContext"])
                self.assertIn(f"tracetree.py answer {run} ", spec["additionalContext"])
                self.assertIn("never a ruling", spec["additionalContext"])

    def test_a_root_cause_line_is_no_root_and_the_repo_root_line_after_it_is(self):
        prompt = (f"Root cause: /a/b was suspected\nRepo root: {self.root}\nGoal: close panes safely\n"
                  "1. Where does an empty target fail?\n")
        spec = json.loads(self.hook(prompt))["hookSpecificOutput"]
        self.assertIn("updatedInput", spec, spec["additionalContext"])
        lines = spec["updatedInput"]["prompt"].splitlines()
        self.assertEqual(lines[:2], ["Root cause: /a/b was suspected", f"Repo root: {self.root}"])
        self.assertTrue(lines[2].startswith("RUN: " + self.parent + os.sep), lines[2])
        with open(os.path.join(lines[2][len("RUN: "):], "ROOT")) as f:
            self.assertEqual(f.read().strip(), self.root)

    def test_a_root_cause_line_alone_leaves_the_root_to_the_hook_cwd(self):
        spec = json.loads(self.hook("Root cause: /a/b\n1. What closes the pane?", cwd=self.root))["hookSpecificOutput"]
        self.assertIn("updatedInput", spec, spec["additionalContext"])
        lines = spec["updatedInput"]["prompt"].splitlines()
        self.assertEqual(lines[1], "Root cause: /a/b")
        run = lines[0][len("RUN: "):]
        with open(os.path.join(run, "ROOT")) as f:
            self.assertEqual(f.read().strip(), self.root)

    def test_a_root_label_followed_directly_by_the_path_names_the_root(self):
        for head in (f"Root: {self.root}", f"root = {self.root}", f"Root {self.root}", f"Repository root: {self.root}",
                     f"Root: `{self.root}`"):
            with self.subTest(head=head):
                spec = json.loads(self.hook(f"{head}\n1. What closes the pane?"))["hookSpecificOutput"]
                self.assertIn("updatedInput", spec, spec["additionalContext"])
                run = spec["updatedInput"]["prompt"].splitlines()[1][len("RUN: "):]
                with open(os.path.join(run, "ROOT")) as f:
                    self.assertEqual(f.read().strip(), self.root)

    def test_a_brief_naming_no_root_takes_the_hook_cwd(self):
        out = json.loads(self.hook("1. What closes the pane?", cwd=self.root))
        first = out["hookSpecificOutput"]["updatedInput"]["prompt"].splitlines()[0]
        self.assertTrue(first.startswith("RUN: " + self.parent + os.sep), first)

    def test_every_other_call_passes_untouched(self):
        asked = f"Repo root: {self.root}\n1. What closes the pane?"
        for name, kw in (("another agent", dict(prompt=asked, subagent="reviewer")),
                         ("another tool", dict(prompt=asked, tool="Bash")),
                         ("a run its caller opened", dict(prompt=f"Repo root: {self.root}\nRUN: {os.path.join(self.parent, 'x')}\n1. What closes the pane?")),
                         ("no numbered question", dict(prompt=f"Repo root: {self.root}\nHow does the pane close?"))):
            with self.subTest(name):
                self.assertEqual(self.hook(**kw), "")
        self.assertEqual(self.runs(), [])

    def test_a_root_that_is_no_directory_passes_the_brief_on_and_says_why(self):
        out = json.loads(self.hook("Repo root: /nonexistent/tree\n1. What closes the pane?"))
        spec = out["hookSpecificOutput"]
        self.assertNotIn("updatedInput", spec)
        self.assertIn("tracer intake opened no run: repo root /nonexistent/tree is not a directory", spec["additionalContext"])

    def test_an_unreadable_payload_is_logged_never_swallowed(self):
        log = os.path.join("/tmp", os.path.basename(self.root).lstrip("."), "tracer", "intake.log")
        before = os.path.getsize(log) if os.path.exists(log) else 0
        got = tt(["intake"], input="not a payload", cwd=self.root)
        self.assertEqual((got.returncode, got.stdout), (0, ""), got.stderr)
        with open(log, encoding="utf-8") as f:
            f.seek(before)
            self.assertIn("unreadable hook payload", f.read())


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

    def test_seek_on_a_run_the_caller_opened_prints_its_path_then_the_prior_hops(self):
        prior = self.sibling("20260101-000000-aaaa", self.good("1"))
        run, _ = self.new()
        os.makedirs(run)
        with open(os.path.join(run, "ROOT"), "w") as f:
            f.write(self.root + "\n")
        got = tt(["seek", run, "target is required"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertEqual(got.stdout.splitlines(),
                         [run, f'PRIOR {prior} node 1: app/srv/server.go:5  return errors.New("target is required")',
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

    def test_seek_cuts_a_line_longer_than_160_characters_and_ends_it_with_dots(self):
        long = "\tconfig := " + "\"" + "x" * 300 + "\""
        with open(os.path.join(self.root, "app/srv/conf.go"), "w") as f:
            f.write("package srv\n\nfunc Conf() {\n" + long + "\n}\n")
        prior = self.sibling("20260101-000000-aaaa", hops("1", ["- x"], ["@ app/srv/conf.go:4", "> " + long], []))
        _, rest = self.new("--seek", "conf\\.go")
        head = f"PRIOR {prior} node 1: app/srv/conf.go:4  "
        self.assertTrue(rest[0].startswith(head), rest[0])
        self.assertEqual(rest[0][len(head):], long.strip()[:157] + "...")
        self.assertEqual(len(rest[0]) - len(head), 160)

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


def row(i):
    return f"row {i:06d} " + "x" * 30


class BigFileTest(Stored):
    """A file past the search limit is checked only within NEAR lines of a hop's address, and one past the read limit
    is not read: no file makes a command search or read without bound. The files are named once, where the lead reads."""

    def setUp(self):
        super().setUp()
        self.add("app/big/big.go", [row(i) for i in range(1, 30001)])
        self.assertGreater(os.path.getsize(os.path.join(self.root, "app/big/big.go")), 1 << 20)

    def two_nodes(self):
        for node in ("1", "2"):
            self.store(node, hops(node, [f"- row [app/big/big.go:{20000 + int(node)}]"],
                                  [f"@ app/big/big.go:{20000 + int(node)}", f"> {row(20000 + int(node))}"],
                                  ["END returned: x @ app/big/big.go:20001"]))

    def test_a_quote_within_thirty_lines_of_its_address_verifies_and_one_further_off_is_rejected_unsearched(self):
        near = self.hop("app/big/big.go", 20000, row(20010))
        self.assertIn("app/big/big.go:20010  row 020010", near)
        self.assertIn("(re-addressed from :20000)", near)
        far = self.hop("app/big/big.go", 20000, row(25000))
        self.assertIn("✗ REJECTED app/big/big.go:20000 — quote not within 30 lines of the address", far)
        self.assertIn("rejected=1", far)

    def test_a_cite_in_a_big_file_is_not_moved_to_a_quote_far_from_it(self):
        out = self.hop("app/big/big.go", 100, row(100), answer=f"- the row `{row(25000)}` [app/big/big.go:100]",
                       view=None)
        self.assertIn("not in the cited file", out)
        self.assertNotIn("↻", out)

    def test_the_big_file_is_named_once_in_the_notes_of_check_wait_and_the_json_map(self):
        self.two_nodes()
        note = "· app/big/big.go is "
        checked = tt(["check", self.run_dir])
        self.assertEqual(checked.returncode, 0, checked.stderr)
        waited = self.wait("1", "2")
        data = json.loads(tt(["map", self.run_dir, "--json"]).stdout)
        for out in (checked.stdout, waited):
            self.assertEqual(len([l for l in out.splitlines() if l.startswith(note)]), 1, out)
            self.assertIn("checks a hop or cite only within 30 lines of its address", out)
        self.assertEqual(sum(n.startswith(note) for n in data["notes"]), 1, data["notes"])
        self.assertEqual(checked.stdout.splitlines()[0], "WARNINGS: none")

    def test_start_names_a_big_file_to_the_node_that_will_cite_it(self):
        got = tt(["start", self.run_dir, "1", "app/big/big.go:100"])
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("app/big/big.go is ", got.stdout)
        self.assertIn("within 30 lines of its address", got.stdout)
        self.assertIn(row(100), got.stdout)

    def test_a_file_over_the_read_limit_is_not_read_and_the_hop_says_why(self):
        huge = os.path.join(self.root, "app/huge/huge.go")
        os.makedirs(os.path.dirname(huge))
        with open(huge, "wb") as f:
            f.truncate((128 << 20) + 1)
        out = self.hop("app/huge/huge.go", 5, "anything")
        self.assertIn("✗ REJECTED app/huge/huge.go:5 — file is 128.0 MB, over the 128 MB read limit", out)
        self.assertIn("· app/huge/huge.go is 128.0 MB — over the 128 MB read limit: the kit reads none of it", out)
        tail = self.hop("huge.go", 5, "anything")
        self.assertIn("✗ REJECTED huge.go:5 — file is 128.0 MB, over the 128 MB read limit", tail)

    def test_a_hop_on_a_fifo_is_rejected_without_opening_it(self):
        fifo = os.path.join(self.root, "app/fifo/pipe.go")
        os.makedirs(os.path.dirname(fifo))
        os.mkfifo(fifo)
        out = self.hop("app/fifo/pipe.go", 1, "anything")
        self.assertIn("✗ REJECTED app/fifo/pipe.go:1 — not a regular file", out)

    def test_a_big_file_only_a_sibling_search_read_is_not_named(self):
        self.add("app/big/small.go", ["package big", "func Small() {}"])
        out = self.hop("app/big/small.go", 2, "a quote written nowhere in this tree at all", view=None)
        self.assertIn("rejected=1", out)
        self.assertNotIn("app/big/big.go is", out)

    def test_the_map_looks_for_a_big_file_s_component_no_further_up_than_one_body(self):
        self.add("app/big/far.go", ["package big", "func Far() {"] + [row(i) for i in range(1, 30001)] + ["}"])
        self.hop("app/big/far.go", 20002, row(20000))
        mapped = tt(["map", self.run_dir])
        self.assertEqual(mapped.returncode, 0, mapped.stderr)
        self.assertIn("app/big/far.go", mapped.stdout)
        self.assertNotIn("far.go::Far", mapped.stdout)


class BrokenNodeMarkTest(Stored):
    """An answer line of a node that broke the hop contract carries a mark at the line itself."""

    MARK = " ⚠ node {} broke the hop contract"
    END = ["END returned: x @ app/srv/server.go:5"]

    def nodes(self):
        self.store("1", hops("1", ["- the pane is killed [app/kill/kill.go:5]", "- the error is wrapped [app/kill/kill.go:6]"],
                             ["@ pkg/kill/kill.go:4", f"> {EXEC}"], self.END))
        self.store("2", hops("2", ["- Handle refuses an empty target [app/srv/server.go:5]"],
                             ["@ app/srv/server.go:5", f"> {REQUIRED}"], self.END))
        self.store("3", hops("3", ["- a ghost [app/srv/server.go:7]"],
                             ["@ app/srv/server.go:7", f"> {GHOST_HOP}"], self.END))
        self.store("4", hops("4", ["- the pane is killed again [app/kill/kill.go:5]"],
                             ["@ pkg/kill/kill.go:5", f"> {EXEC}"], self.END))

    def answers(self, out):
        return [l for l in out.splitlines() if l.lstrip().startswith("- ")]

    def test_every_answer_line_of_a_placed_or_rejected_node_ends_in_the_mark_and_no_other_line_has_one(self):
        self.nodes()
        for out in (tt(["check", self.run_dir]).stdout, self.wait("1", "2", "3", "4")):
            lines = self.answers(out)
            self.assertEqual(len(lines), 5, out)
            self.assertEqual(len([l for l in lines if "⚠ node" in l]), 3, out)
            for l in lines:
                if "pane is killed [" in l or "error is wrapped" in l:
                    self.assertTrue(l.endswith(self.MARK.format(1)), l)
                if "a ghost" in l:
                    self.assertTrue(l.endswith(self.MARK.format(3)), l)
                if "Handle refuses" in l or "killed again" in l:
                    self.assertNotIn("⚠", l)

    def test_a_line_citing_only_a_broken_node_s_hops_carries_that_node_s_mark(self):
        self.store("1", hops("1", ["- the pane is killed [app/kill/kill.go:5]"], ["@ pkg/kill/kill.go:4", f"> {EXEC}"], self.END))
        self.store("2", hops("2", ["- Handle refuses an empty target [app/srv/server.go:5]",
                                   "- and the pane is killed [app/kill/kill.go:5]"],
                             ["@ app/srv/server.go:5", f"> {REQUIRED}"], self.END))
        lines = self.answers(tt(["check", self.run_dir]).stdout)
        self.assertTrue(next(l for l in lines if "and the pane" in l).endswith(self.MARK.format(1)), lines)
        self.assertNotIn("⚠", next(l for l in lines if "Handle refuses" in l))

    def test_the_mark_follows_the_line_s_own_flags(self):
        self.store("1", hops("1", ["- the row `ghost` [app/kill/kill.go:5]"], ["@ pkg/kill/kill.go:4", f"> {EXEC}"], self.END))
        line = self.answers(tt(["check", self.run_dir]).stdout)[0]
        self.assertRegex(line, r"\(.*not in the cited file.*\)" + re.escape(self.MARK.format(1)) + "$")


class PinnedRevisionTest(Stored):
    """A hop pinned to a git revision of the root, `{rev}:{path}:{line}`, is verified against `git show` of that
    revision: it never drifts, is never placed in the working tree, and nothing is checked out."""

    NEEDED = REQUIRED.replace("is required", "is needed")

    def setUp(self):
        super().setUp()
        git(self.root, "init", "-q")
        git(self.root, "add", "-A")
        git(self.root, "commit", "-q", "-m", "one", when=time.time() - 2 * HOUR)
        self.old = git(self.root, "rev-parse", "HEAD")
        self.old12 = self.old[:12]
        path = os.path.join(self.root, "app/srv/server.go")
        with open(path) as f:
            text = f.read()
        with open(path, "w") as f:
            f.write(text.replace("target is required", "target is needed"))
        git(self.root, "commit", "-q", "-am", "two")
        with open(os.path.join(self.run_dir, "BASE"), "w") as f:
            f.write(git(self.root, "rev-parse", "HEAD") + "\n")

    def test_a_pinned_hop_holds_at_its_revision_whatever_the_working_tree_says_and_however_the_revision_is_spelled(self):
        plain = self.hop("app/srv/server.go", 5, REQUIRED)
        self.assertIn("REJECTED app/srv/server.go:5", plain)
        for rev in (self.old, self.old12, "HEAD~1"):
            out = self.hop(f"{rev}:app/srv/server.go", 5, REQUIRED)
            self.assertEqual(out.splitlines()[0], "WARNINGS: none", out)
            self.assertIn(f"{self.old12}:app/srv/server.go:5  return errors.New", out)
            self.assertIn("verified=1 · moved=0 · rejected=0 · drifted=0 · placed=0", out)

    def test_a_pinned_quote_on_another_line_of_the_revision_is_re_addressed(self):
        out = self.hop(f"{self.old12}:app/srv/server.go", 3, REQUIRED)
        self.assertIn(f"{self.old12}:app/srv/server.go:5  return errors.New", out)
        self.assertIn("re-addressed from :3", out)
        self.assertIn("moved=1", out)

    def test_a_pinned_quote_only_the_working_tree_holds_is_rejected_never_drifted_or_placed_there(self):
        out = self.hop(f"{self.old12}:app/srv/server.go", 5, self.NEEDED)
        self.assertIn(f"REJECTED {self.old12}:app/srv/server.go:5 — quote not in the file", out)
        self.assertNotIn("DRIFTED", out)
        self.assertNotIn("basename and quote", out)
        self.assertIn("rejected=1 · drifted=0", out)

    def test_a_pinned_path_the_revision_lacks_and_a_revision_that_names_no_commit_are_rejected_with_the_reason(self):
        gone = self.hop(f"{self.old12}:app/nope/server.go", 5, REQUIRED)
        self.assertIn(f"REJECTED {self.old12}:app/nope/server.go:5 — no such file at {self.old12}", gone)
        none = self.hop("nosuchrev:app/srv/server.go", 5, self.NEEDED)
        self.assertIn("REJECTED nosuchrev:app/srv/server.go:5 — no commit named nosuchrev", none)
        self.assertNotIn("basename and quote", none)

    def test_write_stores_the_revision_as_its_commit_so_a_moving_ref_cannot_move_the_hop(self):
        got = tt(["write", self.run_dir, "1"], input=hops("1", ["- x [HEAD~1:app/srv/server.go:5]"],
                 ["@ HEAD~1:app/srv/server.go:5", f"> {REQUIRED}"], ["END returned: x @ app/srv/server.go:5"]))
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertNotIn("WARNING", got.stderr)
        with open(os.path.join(self.run_dir, "1.hops")) as f:
            stored = f.read()
        self.assertIn(f"@ {self.old12}:app/srv/server.go:5", stored)
        self.assertIn(f"- x [{self.old12}:app/srv/server.go:5]", stored)
        with open(os.path.join(self.root, "app/srv/server.go"), "a") as f:
            f.write("// more\n")
        git(self.root, "commit", "-q", "-am", "three")
        out = tt(["check", self.run_dir]).stdout
        self.assertEqual(out.splitlines()[0], "WARNINGS: none", out)
        self.assertIn("verified=1 · moved=0 · rejected=0 · drifted=0 · placed=0", out)
        self.assertIn("flagged=0", out)

    def test_start_read_and_the_map_take_a_pinned_address(self):
        started = tt(["start", self.run_dir, "1", f"{self.old12}:app/srv/server.go:5"])
        self.assertEqual(started.returncode, 0, started.stderr)
        self.assertIn(REQUIRED, started.stdout)
        self.assertNotIn("target is needed", started.stdout)
        read = tt(["read", self.run_dir, "HEAD~1:app/srv/server.go:3-6"])
        self.assertEqual(read.returncode, 0, read.stderr)
        self.assertIn("== HEAD~1:app/srv/server.go:3-6", read.stdout)
        self.assertIn(REQUIRED, read.stdout)
        self.hop(f"{self.old12}:app/srv/server.go", 5, REQUIRED)
        mapped = tt(["map", self.run_dir])
        self.assertEqual(mapped.returncode, 0, mapped.stderr)
        self.assertIn("app/srv/server.go", mapped.stdout)

    def written(self, node="1"):
        with open(os.path.join(self.run_dir, node + ".hops")) as f:
            return f.read()

    def test_a_pinned_range_in_a_not_read_line_is_stored_as_its_commit_and_never_held_by_the_working_tree(self):
        self.store("2", hops("2", ["- y [app/srv/server.go:5]"], ["@ app/srv/server.go:5", f"> {self.NEEDED}"],
                             ["END returned: y @ app/srv/server.go:5"]))
        got = tt(["write", self.run_dir, "1"], input=hops("1", ["- x [HEAD~1:app/srv/server.go:5]"],
                 ["@ HEAD~1:app/srv/server.go:5", f"> {REQUIRED}"],
                 ["END returned: x @ HEAD~1:app/srv/server.go:5", "NOT READ HEAD~1:app/srv/server.go:3-6 — the caller"]))
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn(f"NOT READ {self.old12}:app/srv/server.go:3-6", self.written())
        self.assertIn(f"END returned: x @ {self.old12}:app/srv/server.go:5", self.written())
        self.store("3", hops("3", ["- z [app/srv/server.go:5]"], ["@ app/srv/server.go:5", f"> {self.NEEDED}"],
                             ["END returned: z @ app/srv/server.go:5", "NOT READ HEAD~1:app/srv/server.go:4-6 — as written"]))
        opened = [l for l in self.wait("1", "2", "3").splitlines() if l.startswith("NOT READ")]
        stored = next(l for l in opened if f"{self.old12}:app/srv/server.go:3-6" in l)
        self.assertIn(f"(held: node 1 verified {self.old12}:app/srv/server.go:5)", stored)
        self.assertNotIn("node 2", stored)
        kept = next(l for l in opened if "HEAD~1:app/srv/server.go:4-6" in l)
        self.assertNotIn("held: node 2", kept)
        self.assertNotIn("held: node 3", kept)

    def test_a_quoted_line_that_names_a_revision_is_stored_as_written(self):
        quote = "\t// see [HEAD~1:app/srv/server.go:5]"
        self.add("app/doc/notes.go", ["package doc", quote])
        got = tt(["write", self.run_dir, "1"], input=hops("1", ["- x [app/doc/notes.go:2]"],
                 ["@ app/doc/notes.go:2", f"> {quote}", f"= see [HEAD~1:app/srv/server.go:5]"],
                 ["END returned: x @ app/doc/notes.go:2"]))
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn(f"> {quote}\n= see [HEAD~1:app/srv/server.go:5]\n", self.written())
        self.assertIn("verified=1 · moved=0 · rejected=0", tt(["check", self.run_dir]).stdout)

    def test_a_pinned_hop_git_cannot_answer_says_so_never_no_commit(self):
        self.store("1", hops("1", ["- x"], ["@ HEAD~1:app/srv/server.go:5", f"> {REQUIRED}"],
                             ["END returned: x @ app/srv/server.go:5"]))
        empty = tempfile.mkdtemp(prefix="tracetree-nogit-")
        self.addCleanup(shutil.rmtree, empty, True)
        got = subprocess.run([sys.executable, SCRIPT, "check", self.run_dir, "--hops"], capture_output=True, text=True,
                             timeout=60, env=dict(os.environ, PATH=empty))
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("✗ REJECTED HEAD~1:app/srv/server.go:5 — git could not say: git rev-parse could not run", got.stdout)
        self.assertNotIn("no commit named", got.stdout)

    def test_a_pinned_directory_is_rejected_as_one(self):
        out = self.hop(f"{self.old12}:app/srv", 1, "server.go")
        self.assertIn(f"✗ REJECTED {self.old12}:app/srv:1 — a directory, not a file", out)

    def test_a_pinned_file_over_the_read_limit_is_not_read(self):
        huge = os.path.join(self.root, "app/huge/huge.go")
        os.makedirs(os.path.dirname(huge))
        with open(huge, "wb") as f:
            f.truncate((128 << 20) + 1)
        git(self.root, "add", "app/huge/huge.go")
        git(self.root, "commit", "-q", "-m", "huge")
        sha12 = git(self.root, "rev-parse", "HEAD")[:12]
        os.remove(huge)
        out = self.hop(f"{sha12}:app/huge/huge.go", 5, "anything")
        self.assertIn(f"✗ REJECTED {sha12}:app/huge/huge.go:5 — file is 128.0 MB, over the 128 MB read limit", out)

    def test_nothing_is_checked_out_or_created(self):
        def state():
            return (git(self.root, "status", "--porcelain"), git(self.root, "worktree", "list"),
                    git(self.root, "rev-parse", "HEAD"), git(self.root, "stash", "list"),
                    sorted(os.listdir(self.root)), git(self.root, "branch", "--list"))
        before = state()
        self.hop(f"{self.old12}:app/srv/server.go", 5, REQUIRED)
        tt(["check", self.run_dir, "--full"])
        tt(["map", self.run_dir, "--json"])
        self.assertIn("verified=1 · moved=0 · rejected=0", tt(["check", self.run_dir]).stdout)
        self.assertEqual(state(), before)


class BookTest(Repo):
    """`book` lists every run under this project's runs root, newest first, one swatch row each; `--html` draws them."""

    def setUp(self):
        super().setUp()
        self.runs = os.path.join("/tmp", os.path.basename(self.root).lstrip("."), "tracer")
        self.addCleanup(shutil.rmtree, os.path.dirname(self.runs), ignore_errors=True)

    def make(self, rid, node_text=None, root=None, questions=None):
        run = os.path.join(self.runs, rid)
        os.makedirs(run)
        with open(os.path.join(run, "ROOT"), "w") as f:
            f.write((root or self.root) + "\n")
        if questions:
            with open(os.path.join(run, "questions"), "w") as f:
                f.write(questions)
        if node_text:
            with open(os.path.join(run, "1.hops"), "w") as f:
                f.write(node_text)
        return run

    def seed(self):
        self.make("20261001-100000-aaaa", self.good())
        self.make("20261003-090000-bbbb", self.good(), questions="1. What calls Handle?\n2. Who kills it?\n")
        self.make("20261002-120000-cccc", hops("1", ["- x"], ["@ app/srv/server.go:2", "> \tghost()"],
                                              ["END returned: x @ app/srv/server.go:5"]))

    def book(self, *args):
        return tt(["book", *args], cwd=self.root)

    def test_book_lists_one_swatch_row_per_run_newest_first(self):
        self.seed()
        got = self.book()
        self.assertEqual(got.returncode, 0, got.stderr)
        lines = got.stdout.splitlines()
        self.assertEqual(lines[0], f"BOOK {self.runs} — 3 runs, newest first")
        header = lines[1].split()
        rows = [dict(zip(header, l.split())) for l in lines[2:5]]
        self.assertEqual([r["RUN"] for r in rows], ["20261003-090000-bbbb", "20261002-120000-cccc", "20261001-100000-aaaa"])
        self.assertEqual({r["REPO"] for r in rows}, {os.path.basename(self.root)})
        self.assertEqual([(r["Q"], r["NODES"], r["HOPS"], r["HELD"], r["HOLES"], r["CLAIMS"]) for r in rows],
                         [("2", "1", "1", "1", "0", "1"), ("0", "1", "1", "0", "1", "1"), ("0", "1", "1", "1", "0", "1")])
        self.assertEqual(len(lines), 5)

    def test_a_run_the_kit_cannot_load_is_listed_with_its_reason_not_dropped(self):
        self.seed()
        self.make("20261004-100000-dddd")
        os.makedirs(os.path.join(self.runs, "20261005-100000-eeee"))
        mixed = self.make("20261006-100000-ffff", self.good())
        shutil.copy(os.path.join(mixed, "1.hops"), os.path.join(mixed, "x.hops"))
        piped = self.make("20261007-100000-0000")
        os.mkfifo(os.path.join(piped, "1.hops"))
        os.symlink(os.path.join(self.runs, "20261001-100000-aaaa"), os.path.join(self.runs, "20261008-100000-1111"))
        got = self.book()
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn("8 runs, newest first", got.stdout)
        row = {l.split()[0]: l for l in got.stdout.splitlines()[2:]}
        self.assertRegex(row["20261006-100000-ffff"], r"  UNREADABLE — TypeError: ")
        self.assertRegex(row["20261007-100000-0000"], r"  UNREADABLE — 1\.hops is not a regular file$")
        self.assertRegex(row["20261008-100000-1111"], r"  UNREADABLE — a symlink, not a run directory$")
        lines = got.stdout.splitlines()
        self.assertIn("20261005-100000-eeee  UNREADABLE — ", next(l for l in lines if "eeee" in l))
        self.assertRegex(next(l for l in lines if "dddd" in l), r"^20261004-100000-dddd  UNREADABLE — .*no \.hops file")
        self.assertNotIn("TRACETREE FAILED", got.stdout + got.stderr)

    def test_an_empty_runs_root_says_so_with_exit_zero_and_html_still_writes_a_page(self):
        got = self.book()
        self.assertEqual((got.returncode, got.stdout), (0, f"book: no runs under {self.runs}\n"))
        out = os.path.join(self.root, "book.html")
        again = self.book("--html", out)
        self.assertEqual(again.returncode, 0, again.stderr)
        with open(out, encoding="utf-8") as f:
            data = page_model(self, f.read(), "book-data")
        self.assertEqual((data["book"], data["failed"]), ([], []))

    def test_html_writes_the_swatch_book_page_with_every_run_and_every_failure(self):
        self.seed()
        self.make("20261004-100000-dddd")
        weird = os.path.join(self.root, "a<img src=x>&b")
        os.makedirs(weird)
        self.make("20261006-100000-ffff", self.good(), root=weird)
        out = os.path.join(self.root, "book.html")
        got = self.book("--html", out)
        self.assertEqual(got.returncode, 0, got.stderr)
        self.assertIn(f"HTML {out}", got.stdout)
        with open(out, encoding="utf-8") as f:
            page = f.read()
        data = page_model(self, page, "book-data")
        self.assertEqual([r["id"] for r in data["book"]],
                         ["20261006-100000-ffff", "20261003-090000-bbbb", "20261002-120000-cccc", "20261001-100000-aaaa"])
        self.assertEqual(data["book"][0]["repo"], "a<img src=x>&b")
        self.assertEqual([(r["id"], "no .hops file" in r["why"]) for r in data["failed"]], [("20261004-100000-dddd", True)])
        self.assertEqual({"id", "repo", "live", "q", "dur", "broke", "comps", "edges", "nodes", "hops", "verified", "moved",
                          "drifted", "rejected", "placed", "followed", "refused", "partial", "answers", "flagged",
                          "recited", "badaddr", "missing"}, set(data["book"][1]))
        self.assertEqual((data["book"][1]["q"], data["book"][2]["broke"], data["book"][2]["rejected"]), (2, 1, 1))
        self.assertNotIn("a<img", page)
        no_network(self, page)

    def test_book_takes_only_html_and_names_its_usage_otherwise(self):
        for args in (["--bogus"], ["--html"], ["extra"]):
            got = self.book(*args)
            self.assertEqual(got.returncode, 2, args)
            self.assertIn("usage: tracetree.py", got.stderr)
            self.assertIn("book [--html OUT]", got.stderr)


KIT = "python3 $HOME/.claude/skills/tracetree/tracetree.py segment /w --rev abc123 --seeds /f/doors.txt"
DENY = {"hookSpecificOutput": {
    "hookEventName": "PreToolUse", "permissionDecision": "deny",
    "permissionDecisionReason": "cut-gate: step 6 first. Write the doors file (step 3), run python3 "
    "$HOME/.claude/skills/tracetree/tracetree.py segment {worktree} --rev {baseline} --seeds {doors file}, log its "
    "CUT line in run.md, then edit. Flight-directory writes stay open."}}


def brief(text):
    return {"type": "user", "message": {"role": "user", "content": text}}


def bash(tid, command, result, blocks=False):
    """A Bash call and its result as a transcript holds them; blocks=True stores the result as text blocks."""
    content = [{"type": "text", "text": result}] if blocks else result
    return [{"type": "assistant", "message": {"role": "assistant", "content": [
                {"type": "tool_use", "id": tid, "name": "Bash", "input": {"command": command}}]}},
            {"type": "user", "message": {"role": "user", "content": [
                {"type": "tool_result", "tool_use_id": tid, "content": content}]}}]


class CutGateTest(unittest.TestCase):
    """`cutgate`: the foreman's PreToolUse hook that refuses a code edit until the segment kit has cut."""

    def setUp(self):
        # HOME sits outside /tmp, so a path under it is no scratch write; the transcripts sit in a scratch directory
        self.home = "/nonexistent-cutgate-home"
        self.tmp = tempfile.mkdtemp(prefix="cutgate-")
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.session = os.path.join(self.tmp, "projects", "p", "sess.jsonl")
        self.sub = os.path.join(self.tmp, "projects", "p", "sess", "subagents")
        os.makedirs(self.sub)
        self.transcript(self.session, [brief("lead the flight")])

    def transcript(self, path, records):
        with open(path, "w") as f:
            f.write("".join(json.dumps(r) + "\n" for r in records))
        return path

    def agent(self, records, agent_id="a1b2c3"):
        self.transcript(os.path.join(self.sub, f"agent-{agent_id}.jsonl"), records)
        return agent_id

    def hook(self, tool="Edit", path="/w/pkg/a.go", raw=None, home=None, **fields):
        if raw is None:
            key = "notebook_path" if tool == "NotebookEdit" else "file_path"
            event = {"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": {key: path},
                     "transcript_path": self.session, "agent_type": "flights-foreman"}
            event.update(fields)
            raw = json.dumps(event)
        got = subprocess.run([sys.executable, SCRIPT, "cutgate"], input=raw, capture_output=True, text=True,
                             timeout=30, env=dict(os.environ, HOME=home or self.home))
        self.assertEqual(got.returncode, 0, got.stderr)
        return got

    def assertDenied(self, got):
        self.assertEqual(json.loads(got.stdout), DENY)

    def assertAllowedSilently(self, got):
        self.assertEqual((got.stdout, got.stderr), ("", ""))

    def assertAllowedUnchecked(self, got, cause):
        """Allowed, and the agent and stderr both told the cut went unchecked: never a silent pass."""
        spec = json.loads(got.stdout)["hookSpecificOutput"]
        self.assertEqual(set(spec), {"hookEventName", "additionalContext"})
        self.assertTrue(spec["additionalContext"].startswith("cut-gate: could not check the cut ("), spec)
        self.assertTrue(spec["additionalContext"].endswith("); run step 6's segment before editing"), spec)
        self.assertIn(cause, spec["additionalContext"])
        self.assertIn(cause, got.stderr)

    def test_a_code_edit_before_any_kit_run_is_denied(self):
        aid = self.agent([brief("CHILD f · unit pfm\nbuild it"), *bash("t1", "git status --short", "VERDICT SPLIT 2")])
        for tool in ("Edit", "Write", "MultiEdit", "NotebookEdit"):
            with self.subTest(tool=tool):
                self.assertDenied(self.hook(tool=tool, agent_id=aid))

    def test_a_kit_run_with_a_verdict_opens_the_edits(self):
        for name, verdict, blocks in (("split", "VERDICT SPLIT 2 — 2 segments share only the spine", False),
                                      ("solo as text blocks", "VERDICT SOLO — every segment folds", True)):
            with self.subTest(name):
                aid = self.agent([brief("build it"), *bash("t1", KIT, f"SEGMENT x\nSEED Shared\n{verdict}\n", blocks)])
                self.assertAllowedSilently(self.hook(agent_id=aid))

    def test_an_empty_verdict_alone_still_denies_and_a_later_cut_opens(self):
        empty = bash("t1", KIT, "NOTFOUND A: — not a symbol, a key or a path\nVERDICT EMPTY — no seed reaches a file")
        aid = self.agent([brief("build it"), *empty])
        self.assertDenied(self.hook(agent_id=aid))
        aid = self.agent([brief("build it"), *empty, *bash("t2", KIT, "VERDICT SOLO — every segment folds")])
        self.assertAllowedSilently(self.hook(agent_id=aid))

    def test_a_verdict_from_another_command_or_an_unanswered_kit_call_is_no_cut(self):
        unanswered = {"type": "assistant", "message": {"role": "assistant", "content": [
            {"type": "tool_use", "id": "t9", "name": "Bash", "input": {"command": KIT}}]}}
        for name, records in (("a grep printing a verdict", bash("t1", "grep VERDICT run.md", "VERDICT SPLIT 2")),
                              ("a kit call with no result yet", [unanswered]),
                              ("a verdict mid-line", bash("t1", KIT, "the CUT said VERDICT SPLIT 2"))):
            with self.subTest(name):
                self.assertDenied(self.hook(agent_id=self.agent([brief("build it"), *records])))

    def test_flight_directory_and_scratch_writes_stay_open(self):
        aid = self.agent([brief("build it")])
        state = os.path.join(self.home, ".local", "state", "pfm")
        for path in (os.path.join(state, "flights", "professor", "f", "run.md"),
                     os.path.join(state, "flights", "professor", "f", "doors.txt"),
                     *(os.path.join(scratch, "p", "x", "notes.md") for scratch in ("/tmp", "/private/tmp"))):
            with self.subTest(path=path):
                self.assertAllowedSilently(self.hook(path=path, agent_id=aid))
        for path in (os.path.join(state, *[".."] * 64, "w", "a.go"), os.path.join(state + "-not", "a.go")):
            with self.subTest(path=path):
                self.assertDenied(self.hook(path=path, agent_id=aid))

    def test_a_scratch_symlink_into_the_tree_is_no_scratch_write(self):
        link = os.path.join("/tmp", os.path.basename(self.tmp) + "-link")
        os.symlink("/w/tree", link)  # resolved even while its target is missing
        self.addCleanup(os.remove, link)
        self.assertDenied(self.hook(path=os.path.join(link, "a.go"), agent_id=self.agent([brief("build it")])))

    def test_a_child_whose_parent_already_cut_is_open(self):
        flight = os.path.join(self.tmp, ".local", "state", "pfm", "flights", "professor", "f", "briefs")
        os.makedirs(flight)
        seg = os.path.join(flight, "pfm-S1-r1.md")
        with open(seg, "w") as f:
            f.write("CHILD f · unit pfm-S1\nSEGMENT S1 · cells a\nORDER build Alpha\n")
        for name, first in (("a SEGMENT line", "CHILD f · unit pfm-S1\nSEGMENT S1 · cells a, b\nORDER add X"),
                            ("a DEPTH 3 line", "CHILD f · unit pfm\nDEPTH 3\nbuild it"),
                            ("a brief file holding a SEGMENT line", f"Your brief is {seg}; read it and execute it.")):
            with self.subTest(name):
                self.assertAllowedSilently(self.hook(agent_id=self.agent([brief(first)]), home=self.tmp))
        elsewhere = os.path.join(self.tmp, "pfm-S1-r1.md")  # a brief outside the state directory is never read
        shutil.copy(seg, elsewhere)
        self.assertDenied(self.hook(agent_id=self.agent([brief(f"Your brief is {elsewhere}.")]), home=self.tmp))

    def test_a_segment_line_after_the_first_message_or_a_depth_2_child_is_no_exemption(self):
        later = self.agent([brief("CHILD f · unit pfm\nbuild it"), *bash("t1", "cat brief.md", "SEGMENT S1 · cells a")])
        self.assertDenied(self.hook(agent_id=later))
        self.assertDenied(self.hook(agent_id=self.agent([brief("CHILD f · unit pfm\nDEPTH 2\nRESEGMENT S1 now")])))

    def test_every_other_tool_passes_untouched(self):
        aid = self.agent([brief("build it")])
        for tool in ("Bash", "Read", "Agent", "Grep"):
            with self.subTest(tool=tool):
                self.assertAllowedSilently(self.hook(tool=tool, agent_id=aid))

    def test_the_agent_id_picks_its_own_transcript_over_the_session(self):
        cut = bash("t1", KIT, "VERDICT SPLIT 2")
        self.transcript(self.session, [brief("lead the flight"), *cut])
        self.assertDenied(self.hook(agent_id=self.agent([brief("build it")])))  # the session's cut is not its own
        self.transcript(self.session, [brief("lead the flight")])
        self.assertAllowedSilently(self.hook(agent_id=self.agent([brief("build it"), *cut])))
        own = self.agent([brief("build it"), *cut], agent_id="f00d")
        self.assertAllowedSilently(self.hook(agent_id=own, transcript_path=os.path.join(self.sub, "agent-f00d.jsonl")))
        self.assertDenied(self.hook())  # no agent_id: the transcript named is the agent's own
        self.transcript(self.session, [brief("lead the flight"), *cut])
        self.assertAllowedSilently(self.hook())

    def test_what_it_cannot_read_allows_and_says_so(self):
        for name, kw, cause in (
                ("a payload that is no JSON", dict(raw="not a payload"), "unreadable hook payload"),
                ("a payload that is no object", dict(raw="[1, 2]"), "unreadable hook payload"),
                ("no transcript_path", dict(transcript_path=""), "names no transcript_path"),
                ("an agent id that is no id", dict(agent_id="../../etc"), "is no agent id"),
                ("a missing agent transcript", dict(agent_id="nosuchagent"), "agent-nosuchagent.jsonl")):
            with self.subTest(name):
                self.assertAllowedUnchecked(self.hook(**kw), cause)
        broken = self.agent([brief("build it")], agent_id="dir0")
        os.remove(os.path.join(self.sub, "agent-dir0.jsonl"))
        os.mkdir(os.path.join(self.sub, "agent-dir0.jsonl"))
        self.assertAllowedUnchecked(self.hook(agent_id=broken), "agent-dir0.jsonl")


if __name__ == "__main__":
    unittest.main()
