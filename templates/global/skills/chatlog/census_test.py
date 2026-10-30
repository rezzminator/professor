"""Tests for the chatlog kit's `census` verb — run through `.claude/scripts/test.sh`.

The fixtures are chatlog_test's: an invented Claude session with sub-agents, a Codex rollout pair and an OpenCode
store with a child session, written into a temp world and read through the entry point
(`python3 -I chatlog.py census ...`) as an agent does. Only chatlog_test's helpers are imported, never its test
classes, so no test runs twice."""
import contextlib
import io
import json
import os
import re
import shutil
import sqlite3
import sys
import unittest
from datetime import timedelta
from unittest import mock

HERE = os.path.dirname(os.path.realpath(__file__))
sys.path.insert(0, HERE)

from chatlog_test import (BASE, Fixture, c_call, c_final, c_result, c_user, codex_records, iso,  # noqa: E402
                          oc_store, x_rec, x_tokens)
from chatlogkit import census as census_mod, cli, cost  # noqa: E402

COLS = ("id engine agent_type description start_utc wall_s calls errors denials compactions spawns usd final "
        "parent").split()
TOTAL_USD = re.compile(r"· results >20KB \d+ · (\$[0-9]+\.[0-9]{4}|n/a)")
LONG = "Inspect the b side of the invented thing and report every file it touched in the whole tree"


def last_reply(text, t, tag):
    """c_final with its own message and request ids: two transcripts of one project holding the same response bill
    it in the first by path, so the fixture's transcripts must not share one."""
    rec = c_final(text, t)
    rec["requestId"], rec["message"]["id"] = "rfin" + tag, "mfin" + tag
    return rec


class World(Fixture):
    """A temp world with `census` run through the entry point and its stdout read as a table."""

    def census(self, *args, check=True, env=None):
        return self.kitcmd("census", *args, check=check, env=env)

    def table(self, out):
        """{id: row dict} of a census stdout; the header must be the contract's."""
        got = [ln.split("\t") for ln in out.splitlines() if "\t" in ln]
        self.assertEqual(got[0], COLS, out)
        return {r[0]: dict(zip(COLS, r)) for r in got[1:]}

    def jsonl(self, path, records):
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            for rec in records:
                f.write(json.dumps(rec) + "\n")
        return path

    def timeline_total(self, target):
        """The run total the TOTALS (M1) line of `prep` writes for a transcript."""
        shutil.rmtree(self.path("run"), True)
        self.prep(target)
        with open(os.path.join(self.dir, "timeline.txt")) as f:
            head = f.read().splitlines()[0]
        self.assertTrue(head.startswith("TIMELINE"), head)
        return TOTAL_USD.search(head).group(1)

    # ---------------------------------------------------------------------------------- Claude
    def claude_session(self, with_third=False):
        """A session with two sub-agents: tracer `aaa111` (one read) and explorer `bbb222` (a denied read, a
        compaction, a shell call), each meta.json naming the Agent tool_use of the session that spawned it."""
        root = [c_user("Audit with two agents.", 0),
                c_call("toolu_a", "Agent", {"subagent_type": "tracer", "description": "look a"}, 1),
                c_result("toolu_a", "found a", 20),
                c_call("toolu_b", "Agent", {"subagent_type": "explorer", "description": "look b"}, 2),
                c_result("toolu_b", "found b", 25), last_reply("All done in the root.", 30, "root")]
        a = [c_user("Look at a.", 3), c_call("a1", "Read", {"file_path": "/fx/a.txt"}, 4),
             c_result("a1", "contents", 5), last_reply("A found it.", 6, "a")]
        b = [c_user("Look at b.", 8), c_call("b1", "Read", {"file_path": "/fx/b.txt"}, 9),
             c_result("b1", "Error: permission denied\nwhile reading /fx/b.txt\nretry later", 10, err=True),
             {"type": "system", "subtype": "compact_boundary", "timestamp": iso(11)},
             c_user("summary of what happened", 12, isCompactSummary=True),
             c_call("b2", "Bash", {"command": "echo ok"}, 13), c_result("b2", "ok", 14),
             last_reply("B found it.", 15, "b")]
        sess = self.path("projects", "p", "sess0001.jsonl")
        self.jsonl(sess, root)
        subs = [("aaa111", a, {"agentType": "tracer", "description": "look a", "toolUseId": "toolu_a"}),
                ("bbb222", b, {"agentType": "explorer", "description": LONG, "toolUseId": "toolu_b"})]
        if with_third:
            c = [c_user("Look at c.", 16), c_call("c1", "Read", {"file_path": "/fx/c.txt"}, 17),
                 c_result("c1", "contents", 18), last_reply("C found it.", 19, "c")]
            subs.append(("ccc333", c, {"agentType": "tracer", "description": "orphan", "toolUseId": "toolu_zzz"}))
        for aid, recs, meta in subs:
            path = self.jsonl(self.path("projects", "p", "sess0001", "subagents", f"agent-{aid}.jsonl"), recs)
            with open(path[:-len(".jsonl")] + ".meta.json", "w") as f:
                json.dump(meta, f)
        return sess

    def sub_path(self, aid):
        return self.path("projects", "p", "sess0001", "subagents", f"agent-{aid}.jsonl")


class CensusClaude(World):
    def test_session_census_rows_with_parents_counts_and_cost(self):
        self.claude_session()
        out = self.census("--session", "sess0001").stdout
        rows = self.table(out)
        self.assertEqual(list(rows), ["sess0001", "aaa111", "bbb222", "TOTAL"], out)
        root, a, b = rows["sess0001"], rows["aaa111"], rows["bbb222"]
        # 100 in * 3 + 10 out * 15 = 450 micro-dollars a tool call; the final reply is 525
        self.assertEqual(root, dict(id="sess0001", engine="claude", agent_type="main", description="-",
                                    start_utc="2026-01-01T10:00:00Z", wall_s="30", calls="2", errors="0",
                                    denials="0", compactions="0", spawns="2", usd="$0.0014",
                                    final="All done in the root.", parent="-"))
        self.assertEqual(a, dict(id="aaa111", engine="claude", agent_type="tracer", description="look a",
                                 start_utc="2026-01-01T10:00:03Z", wall_s="3", calls="1", errors="0", denials="0",
                                 compactions="0", spawns="0", usd="$0.0010", final="A found it.",
                                 parent="sess0001"))
        self.assertEqual((b["errors"], b["denials"], b["compactions"], b["calls"], b["spawns"], b["wall_s"]),
                         ("1", "1", "1", "2", "0", "7"))
        self.assertEqual((b["agent_type"], b["parent"], b["usd"], b["final"]),
                         ("explorer", "sess0001", "$0.0014", "B found it."))
        self.assertEqual(b["description"], LONG[:60], "stdout clips the description to 60 characters")
        total = rows["TOTAL"]
        self.assertEqual((total["agent_type"], total["start_utc"], total["wall_s"], total["calls"], total["errors"],
                          total["denials"], total["compactions"], total["spawns"], total["usd"]),
                         ("3 transcripts", "2026-01-01T10:00:00Z", "40", "5", "1", "1", "1", "2", "$0.0038"))
        self.assertTrue(out.splitlines()[-1].startswith("# census 3 transcripts · 0 FAILED · "), out)

    def test_unresolved_sub_agent_hangs_under_the_session_root_with_a_question_mark(self):
        self.claude_session(with_third=True)
        out = self.census("--session", "sess0001", "--tree").stdout
        rows = self.table(out)
        self.assertEqual(list(rows), ["sess0001", "aaa111", "bbb222", "ccc333", "TOTAL"], out)
        self.assertEqual((rows["aaa111"]["parent"], rows["ccc333"]["parent"]), ("sess0001", "sess0001?"))
        tree = out.split("\nTREE\n")[1].splitlines()
        self.assertEqual([ln.strip().split(" ")[0] for ln in tree[:4]], ["sess0001", "aaa111", "bbb222", "ccc333"])
        self.assertTrue(tree[3].startswith("  ccc333 tracer · calls 1 (branch 1) · $"), tree)
        self.assertTrue(tree[0].startswith("sess0001 main · calls 2 (branch 6) · $"), tree)

    def test_tree_rolls_up_calls_and_usd_per_branch(self):
        sess = self.claude_session()
        out = self.census("--session", sess, "--tree").stdout
        rows = self.table(out)
        calls = sum(int(rows[k]["calls"]) for k in ("sess0001", "aaa111", "bbb222"))
        self.assertEqual(calls, 5)
        tree = out.split("\nTREE\n")[1].splitlines()
        self.assertEqual(tree[:3], [
            "sess0001 main · calls 2 (branch 5) · $0.0014 (branch $0.0038)",
            "  aaa111 tracer · calls 1 (branch 1) · $0.0010 (branch $0.0010)",
            "  bbb222 explorer · calls 2 (branch 2) · $0.0014 (branch $0.0014)"])
        self.assertTrue(tree[3].startswith("# census 3 transcripts"), tree)

    def test_nested_sub_agent_rolls_up_into_its_parents_branch(self):
        """A sub-agent spawned by a sub-agent: its toolUseId is found in that sub-agent's transcript."""
        self.claude_session()
        inner = [c_user("Dig deeper.", 40), c_call("d1", "Read", {"file_path": "/fx/d.txt"}, 41),
                 c_result("d1", "contents", 42), last_reply("D found it.", 43, "d")]
        path = self.jsonl(self.sub_path("ddd444"), inner)
        with open(path[:-len(".jsonl")] + ".meta.json", "w") as f:
            json.dump({"agentType": "tracer", "description": "deeper", "toolUseId": "toolu_nested"}, f)
        a = self.sub_path("aaa111")
        with open(a) as f:
            recs = [json.loads(ln) for ln in f if ln.strip()]
        recs.insert(3, c_call("toolu_nested", "Agent", {"subagent_type": "tracer", "description": "deeper"}, 5))
        recs.insert(4, c_result("toolu_nested", "dug", 6))
        self.jsonl(a, recs)
        out = self.census("--session", "sess0001", "--tree").stdout
        rows = self.table(out)
        self.assertEqual((rows["ddd444"]["parent"], rows["aaa111"]["spawns"]), ("aaa111", "1"))
        tree = out.split("\nTREE\n")[1].splitlines()
        self.assertEqual(tree[:4], [
            "sess0001 main · calls 2 (branch 7) · $0.0014 (branch $0.0052)",
            "  aaa111 tracer · calls 2 (branch 3) · $0.0014 (branch $0.0024)",
            "    ddd444 tracer · calls 1 (branch 1) · $0.0010 (branch $0.0010)",
            "  bbb222 explorer · calls 2 (branch 2) · $0.0014 (branch $0.0014)"])

    def test_type_form_selects_sub_agents_by_type_project_and_age(self):
        self.claude_session()
        out = self.census("--type", "tracer", "--since", "7d", "--project", "p").stdout
        rows = self.table(out)
        self.assertEqual(list(rows), ["aaa111", "TOTAL"], out)
        self.assertEqual(rows["aaa111"]["parent"], "-", "its parent is outside the selected set")
        self.assertEqual(rows["TOTAL"]["agent_type"], "1 transcripts")
        none = self.census("--type", "tracer", "--project", "no-such-project").stdout
        self.assertEqual(list(self.table(none)), ["TOTAL"], none)
        self.assertIn("# census 0 transcripts · 0 FAILED", none)

    def test_type_form_reports_an_unreadable_meta(self):
        self.claude_session()
        meta = self.sub_path("bbb222")[:-len(".jsonl")] + ".meta.json"
        with open(meta, "w") as f:
            f.write("{not json")
        done = self.census("--type", "tracer", check=False)
        self.assertEqual(done.returncode, 1, done.stdout)
        failed = [ln for ln in done.stdout.splitlines() if ln.startswith("FAILED ")]
        self.assertEqual(len(failed), 1, done.stdout)
        self.assertTrue(failed[0].startswith(f"FAILED {meta} — "), failed)
        self.assertEqual(list(self.table(done.stdout)), ["aaa111", "TOTAL"])
        self.assertIn("· 1 FAILED ·", done.stdout.splitlines()[-1])

    def test_path_form_keeps_the_order_given_and_resolves_parents_over_the_set(self):
        sess = self.claude_session()
        out = self.census(self.sub_path("bbb222"), sess, "aaa111").stdout
        rows = self.table(out)
        self.assertEqual(list(rows), ["bbb222", "sess0001", "aaa111", "TOTAL"], out)
        self.assertEqual((rows["bbb222"]["parent"], rows["aaa111"]["parent"]), ("sess0001", "sess0001"))
        alone = self.table(self.census(self.sub_path("bbb222")).stdout)
        self.assertEqual(alone["bbb222"]["parent"], "-")

    def test_out_writes_the_full_tsv_with_a_path_column(self):
        sess = self.claude_session()
        dest = self.path("out", "census.tsv")
        os.makedirs(os.path.dirname(dest))
        out = self.census("--session", sess, "--out", dest).stdout
        self.assertIn(f"OUT {dest}", out.splitlines())
        self.assertTrue(out.splitlines()[-1].startswith("# census 3 transcripts"), out)
        with open(dest) as f:
            lines = f.read().splitlines()
        self.assertEqual(lines[0].split("\t"), COLS + ["path"])
        got = {r[0]: r for r in (ln.split("\t") for ln in lines[1:])}
        self.assertEqual(list(got), ["sess0001", "aaa111", "bbb222", "TOTAL"])
        self.assertTrue(all(len(r) == len(COLS) + 1 for r in got.values()), lines)
        self.assertEqual(got["bbb222"][3], LONG, "the file holds the description whole")
        self.assertEqual(got["bbb222"][-1], self.sub_path("bbb222"))
        self.assertEqual(got["sess0001"][-1], sess)
        self.assertEqual(got["TOTAL"][2], "3 transcripts")
        self.assertEqual(got["TOTAL"][11], "$0.0038")

    def test_usd_column_equals_the_totals_line_prep_writes(self):
        sess = self.claude_session()
        rows = self.table(self.census("--session", sess).stdout)
        for rid, target in (("sess0001", sess), ("aaa111", self.sub_path("aaa111")),
                            ("bbb222", self.sub_path("bbb222"))):
            self.assertEqual(rows[rid]["usd"], self.timeline_total(target), rid)

    def test_unpriced_model_is_n_a_in_the_row_and_counted_in_the_total(self):
        recs = [c_user("Brief.", 0), c_call("u1", "Bash", {"command": "echo hi"}, 1), c_result("u1", "hi", 2),
                last_reply("Done.", 3, "u")]
        recs[1]["message"]["model"] = "claude-unlisted-9"
        recs[3]["message"]["model"] = "claude-unlisted-9"
        path = self.jsonl(self.path("projects", "p", "unpriced.jsonl"), recs)
        good = self.claude_session()
        out = self.census(path, good).stdout
        rows = self.table(out)
        self.assertEqual(rows["unpriced"]["usd"], "n/a")
        self.assertEqual(rows["unpriced"]["usd"], self.timeline_total(path))
        self.assertEqual(rows["TOTAL"]["usd"], "$0.0014 +1 n/a")
        gaps = [ln for ln in out.splitlines() if ln.startswith("GAPS ")]
        self.assertEqual(len(gaps), 1, out)
        self.assertTrue(gaps[0].startswith(f"GAPS {path} — data gaps: 2 UNPRICED calls "), gaps)

    def test_a_read_error_in_the_ownership_walk_fails_the_row_and_reaches_the_out_file(self):
        """A file beside the transcript that cannot be read may hold copies of its calls: prep names it in its data
        gaps, and the census names it as a FAILED line, exit 1, in stdout and in the --out file alike."""
        sess = self.claude_session()
        dead = self.path("projects", "p", "dead.jsonl")
        os.symlink(self.path("projects", "p", "gone.jsonl"), dead)
        dest = self.path("census.tsv")
        done = self.census(sess, "--out", dest, check=False)
        self.assertEqual(done.returncode, 1, done.stdout + done.stderr)
        failed = [ln for ln in done.stdout.splitlines() if ln.startswith("FAILED ")]
        self.assertEqual(len(failed), 1, done.stdout)
        self.assertTrue(failed[0].startswith(f"FAILED {sess} — data gaps: 1 READ ERRORS: {dead}: "), failed)
        self.assertEqual(self.table(done.stdout)["sess0001"]["usd"], "$0.0014", "the row stays beside its FAILED line")
        with open(dest) as f:
            self.assertIn(failed[0], f.read().splitlines(), "the file alone shows the unsure dollars")

    def test_a_failed_transcript_is_named_in_the_out_file(self):
        good = self.claude_session()
        missing = self.path("projects", "p", "missing.jsonl")
        dest = self.path("census.tsv")
        done = self.census(good, missing, "--out", dest, check=False)
        self.assertEqual(done.returncode, 1, done.stdout + done.stderr)
        with open(dest) as f:
            lines = f.read().splitlines()
        self.assertEqual([ln for ln in lines if ln.startswith("FAILED ")],
                         [ln for ln in done.stdout.splitlines() if ln.startswith("FAILED ")])
        self.assertTrue(any(ln.startswith(f"FAILED {missing} — ") for ln in lines), lines)

    def test_type_form_counts_a_run_once_when_two_roots_name_it(self):
        self.claude_session()
        alias = self.path("alias")
        os.symlink(self.path("projects"), alias)
        roots = os.pathsep.join([self.path("projects"), self.path("projects"), alias])
        out = self.census("--type", "tracer", env={"PFM_CLAUDE_ROOTS": roots}).stdout
        rows = self.table(out)
        self.assertEqual(list(rows), ["aaa111", "TOTAL"], out)
        self.assertEqual((rows["TOTAL"]["agent_type"], rows["TOTAL"]["usd"]), ("1 transcripts", "$0.0010"))

    def test_unreadable_transcripts_are_failed_lines_and_exit_1(self):
        good = self.claude_session()
        empty = self.jsonl(self.path("projects", "p", "empty.jsonl"), [])
        junk = self.path("projects", "p", "junk.jsonl")
        with open(junk, "w") as f:
            f.write("this is no json\n")
        missing = self.path("projects", "p", "missing.jsonl")
        done = self.census(empty, good, junk, missing, check=False)
        self.assertEqual(done.returncode, 1, done.stdout + done.stderr)
        lines = done.stdout.splitlines()
        self.assertEqual(list(self.table(done.stdout)), ["sess0001", "TOTAL"], done.stdout)
        failed = [ln for ln in lines if ln.startswith("FAILED ")]
        self.assertEqual(len(failed), 3, done.stdout)
        self.assertTrue(failed[0].startswith(f"FAILED {empty} — "), failed)
        self.assertIn("empty transcript", failed[0])
        self.assertTrue(failed[1].startswith(f"FAILED {junk} — "), failed)
        self.assertTrue(failed[2].startswith(f"FAILED {missing} — "), failed)
        self.assertTrue(lines[-1].startswith("# census 1 transcripts · 3 FAILED · "), lines[-1])
        last_row = max(i for i, ln in enumerate(lines) if "\t" in ln)
        self.assertLess(last_row, lines.index(failed[0]), "the rows come before the FAILED lines")

    def test_an_unreadable_timeline_keeps_its_row_with_usd_failed(self):
        path = self.claude_session()
        buf = io.StringIO()

        def broken(p, parsed, kit, shared=None, book=None):
            return [f"UNREADABLE — {p}: price walk broke"], f"UNREADABLE — {p}: price walk broke"

        with mock.patch.object(cost, "timeline", broken), mock.patch.object(cli, "KIT_DIR", self.kit), \
                mock.patch.dict(os.environ, {"PFM_CLAUDE_ROOTS": self.path("projects")}), \
                contextlib.redirect_stdout(buf):
            with self.assertRaises(SystemExit) as stop:
                census_mod.census([path])
        self.assertEqual(stop.exception.code, 1)
        out = buf.getvalue()
        self.assertEqual(self.table(out)["sess0001"]["usd"], "FAILED")
        self.assertIn(f"FAILED {path} — price walk broke", out.splitlines())
        self.assertEqual(self.table(out)["TOTAL"]["usd"], "$0.0000 +1 FAILED", "a failed cell is never counted n/a")

    def test_session_refuses_a_sub_agent_with_a_usage_error(self):
        self.claude_session()
        done = self.census("--session", self.sub_path("aaa111"), check=False)
        self.assertEqual(done.returncode, 2, done.stdout)
        self.assertIn(self.sub_path("aaa111"), done.stdout)
        self.assertNotIn("\t", done.stdout)

    def test_usage_errors_exit_2(self):
        sess = self.claude_session()
        for args in ((), ("--type", "tracer", "--session", "sess0001"), ("--session", "sess0001", sess),
                     ("--since", "7d", sess), ("--bogus", sess), ("--type", "tracer", sess)):
            done = self.census(*args, check=False)
            self.assertEqual(done.returncode, 2, f"{args}: {done.stdout}")
            self.assertIn("usage: chatlog.py census", done.stdout)

    def test_runs_type_output_is_unchanged_by_the_shared_selection(self):
        self.claude_session()
        meta = self.sub_path("bbb222")[:-len(".jsonl")] + ".meta.json"
        with open(meta, "w") as f:
            f.write("{not json")
        out = self.kitcmd("runs", "--type", "tracer").stdout.splitlines()
        self.assertEqual(out[0], f"UNREADABLE {meta} — Expecting property name enclosed in double quotes: "
                                 "line 1 column 2 (char 1)")
        cols = out[1].split("\t")
        self.assertEqual(len(cols), 5, out)
        self.assertEqual((cols[2], cols[3], cols[4]), ("-", "look a", self.sub_path("aaa111")))
        self.assertEqual(int(cols[1]), os.path.getsize(self.sub_path("aaa111")))
        self.assertEqual(out[2], "# runs 1 of tracer since 30d")
        bad = self.kitcmd("runs", "--type", "tracer", "--since", "5x", check=False)
        self.assertEqual((bad.returncode, bad.stdout.strip()), (1, "KIT FAILED — --since 5x: use Nh or Nd"))


# ------------------------------------------------------------------------------------------ Codex

def codex_child_records():
    """A sub-agent rollout: session_meta naming the thread that spawned it, one shell call, one reply."""
    spawn = {"parent_thread_id": "01aa", "depth": 1, "agent_path": "/root/ada", "agent_nickname": "Ada",
             "agent_role": "worker"}
    return [
        x_rec("session_meta", {"id": "01bb", "cwd": "/fx", "cli_version": "9.9", "agent_nickname": "Ada",
                               "agent_role": "worker", "source": {"subagent": {"thread_spawn": spawn}}}, 20),
        x_rec("turn_context", {"model": "gpt-5.5", "effort": "high"}, 20),
        x_rec("response_item", {"type": "message", "role": "user",
                                "content": [{"type": "input_text", "text": "Do the child thing."}]}, 21),
        x_rec("response_item", {"type": "function_call", "name": "exec_command", "call_id": "k1",
                                "arguments": json.dumps({"cmd": "ls"})}, 22),
        x_rec("response_item", {"type": "function_call_output", "call_id": "k1",
                                "output": "Process exited with code 0\na"}, 23),
        x_tokens((800, 100, 40), (800, 100, 40), 24),
        x_rec("response_item", {"type": "message", "role": "assistant", "phase": "final_answer",
                                "content": [{"type": "output_text", "text": "Child done."}]}, 30)]


class CensusCodex(World):
    def test_runs_lists_native_children(self):
        root, child = self.pair()
        out = self.kitcmd("runs", "--session", root).stdout
        self.assertIn("AGENTS 1", out)
        self.assertIn("01bb · /root/ada · worker · parent 01aa", out)
        self.assertIn(child, out)

    def rollout(self, name, records):
        return self.jsonl(self.path("codex", "sessions", "2026", "01", "01", name), records)

    def pair(self):
        root = self.rollout("rollout-2026-01-01T10-00-00-01aa.jsonl", codex_records())
        child = self.rollout("rollout-2026-01-01T10-00-20-01bb.jsonl", codex_child_records())
        return root, child

    def test_path_form_reads_a_rollout(self):
        root, _ = self.pair()
        out = self.census(root).stdout
        rows = self.table(out)
        self.assertEqual(list(rows), ["01aa", "TOTAL"], out)
        r = rows["01aa"]
        self.assertEqual((r["engine"], r["agent_type"], r["description"], r["start_utc"], r["parent"]),
                         ("codex", "main", "-", "2026-01-01T10:00:00Z", "-"))
        self.assertEqual((r["calls"], r["errors"], r["denials"], r["compactions"], r["spawns"], r["final"]),
                         ("5", "1", "1", "1", "1", "Codex done."))
        self.assertEqual(r["usd"], self.timeline_total(root))
        self.assertTrue(r["usd"].startswith("$"), r)

    def test_malformed_usage_fails_timeline_and_census_even_with_valid_calls(self):
        for field in ("total_token_usage", "last_token_usage"):
            for value in ("bad", True, 1.5, -1):
                for position in ("only", "before", "after", "duplicate"):
                    with self.subTest(field=field, value=value, position=position):
                        good = x_tokens((1000, 200, 50), (1000, 200, 50), 4)
                        bad = x_tokens((900, 300, 40), (1900, 500, 90), 5)
                        if position == "duplicate":
                            bad["payload"]["info"]["total_token_usage"] = dict(
                                good["payload"]["info"]["total_token_usage"])
                        bad["payload"]["info"][field]["input_tokens"] = value
                        events = {"only": [bad], "before": [bad, good],
                                  "after": [good, bad], "duplicate": [good, bad]}[position]
                        path = self.rollout("rollout-invalid.jsonl", codex_records()[:3] + events)
                        shutil.rmtree(self.path("run"), True)
                        self.prep(path)
                        with open(os.path.join(self.dir, "timeline.txt")) as f:
                            timeline = f.read()
                        dest = self.path("invalid.tsv")
                        done = self.census(path, "--tree", "--out", dest, check=False)
                        rows = self.table(done.stdout)
                        with open(dest) as f:
                            saved = f.read()
                        evidence = timeline + "\n" + done.stdout
                        self.assertEqual((timeline.startswith("UNREADABLE"), done.returncode,
                                          rows["01aa"]["usd"], rows["TOTAL"]["usd"]),
                                         (True, 1, "FAILED", "$0.0000 +1 FAILED"), evidence)
                        self.assertIn("malformed token_count usage at line ", timeline)
                        self.assertIn(field, timeline)
                        self.assertNotIn("TOKENS input=", timeline)
                        self.assertIn(f"FAILED {path} — ", saved)
                        self.assertIn("FAILED (branch $0.0000 +1 FAILED)", done.stdout)

    def test_malformed_usage_block_shapes_are_not_optional_absence(self):
        for field in ("info", "total_token_usage", "last_token_usage"):
            for value in ("bad", [], False):
                with self.subTest(field=field, value=value):
                    bad = x_tokens((1000, 200, 50), (1000, 200, 50), 4)
                    if field == "info":
                        bad["payload"][field] = value
                    else:
                        bad["payload"]["info"][field] = value
                    path = self.rollout("rollout-invalid.jsonl", codex_records()[:3] + [bad])
                    done = self.census(path, check=False)
                    self.assertEqual(done.returncode, 1, done.stdout)
                    self.assertEqual(self.table(done.stdout)["01aa"]["usd"], "FAILED")
                    self.assertIn(field, done.stdout)

    def test_optional_usage_absence_is_no_calls_but_last_absence_uses_deltas(self):
        absent = [x_rec("event_msg", dict(type="token_count", **extra), 2)
                  for extra in ({}, {"info": None}, {"info": {}},
                                {"info": {"total_token_usage": None}})]
        path = self.rollout("rollout-absent.jsonl", codex_records()[:3] + absent)
        self.prep(path)
        with open(os.path.join(self.dir, "timeline.txt")) as f:
            self.assertTrue(f.read().startswith("NO CALLS"))
        done = self.census(path)
        self.assertEqual(self.table(done.stdout)["01aa"]["usd"], "$0.0000")

        first = x_tokens((1000, 200, 50), (1000, 200, 50), 4)
        delta = x_tokens((1500, 1000, 80), (2500, 1200, 130), 9)
        del delta["payload"]["info"]["last_token_usage"]
        compact = x_tokens((900, 300, 40), (900, 300, 40), 14)
        compact["payload"]["info"]["last_token_usage"] = None
        resume = x_tokens((0, 0, 0), (500, 100, 20), 18)
        records = codex_records()[:3] + [first, first, delta,
                  x_rec("compacted", {"message": ""}, 12), compact,
                  x_rec("session_meta", {"id": "01aa", "cwd": "/fx"}, 16), resume]
        path = self.rollout("rollout-valid.jsonl", records)
        shutil.rmtree(self.path("run"), True)
        self.prep(path)
        with open(os.path.join(self.dir, "timeline.txt")) as f:
            timeline = f.read()
        self.assertIn("· 4 calls ·", timeline)
        self.assertIn("TOKENS input=3900 cached_input=1600 uncached_input=2300 output=190", timeline)
        self.assertIn("3 token_count events without last_token_usage", timeline)
        self.assertEqual(self.table(self.census(path).stdout)["01aa"]["usd"], "$0.0180")

    def test_session_form_collects_the_spawned_rollouts_by_parent_thread(self):
        root, child = self.pair()
        out = self.census("--session", root, "--tree").stdout
        rows = self.table(out)
        self.assertEqual(list(rows), ["01aa", "01bb", "TOTAL"], out)
        c = rows["01bb"]
        self.assertEqual((c["engine"], c["agent_type"], c["description"], c["parent"], c["calls"], c["final"]),
                         ("codex", "worker", "Ada", "01aa", "1", "Child done."))
        self.assertEqual(c["usd"], self.timeline_total(child))
        self.assertEqual(rows["01aa"]["usd"], self.timeline_total(root))
        tree = out.split("\nTREE\n")[1].splitlines()
        self.assertEqual(tree[0].split(" · ")[0], "01aa main")
        self.assertEqual(tree[1].split(" · ")[0], "  01bb worker")
        self.assertIn("calls 5 (branch 6)", tree[0])
        self.assertEqual(rows["TOTAL"]["calls"], "6")

    def test_session_form_of_a_lone_rollout_is_a_family_of_one(self):
        self.pair()
        solo = self.rollout("rollout-2026-01-02T10-00-00-02cc.jsonl", codex_records())
        with open(solo) as f:
            recs = [json.loads(ln) for ln in f if ln.strip()]
        recs[0]["payload"]["id"] = "02cc"
        self.jsonl(solo, recs)
        self.assertEqual(list(self.table(self.census("--session", solo).stdout)), ["02cc", "TOTAL"])

    def test_session_refuses_a_spawned_rollout(self):
        _, child = self.pair()
        done = self.census("--session", child, check=False)
        self.assertEqual(done.returncode, 2, done.stdout)
        self.assertIn(child, done.stdout)


# ---------------------------------------------------------------------------------------- OpenCode

class CensusOpenCode(World):
    def setUp(self):
        super().setUp()
        self.db = self.path("opencode", "opencode.db")
        os.makedirs(os.path.dirname(self.db))
        self.sid = oc_store(self.db)
        ms = lambda s: int((BASE + timedelta(seconds=s)).timestamp() * 1000)  # noqa: E731
        con = sqlite3.connect(self.db)
        tokens = {"input": 300, "output": 10, "reasoning": 0, "cache": {"read": 100, "write": 0}}
        con.execute("INSERT INTO message VALUES (?,?,?,?)", ("c01", "ses_child01", ms(5), json.dumps(
            dict(role="user", agent="explore"))))
        con.execute("INSERT INTO message VALUES (?,?,?,?)", ("c02", "ses_child01", ms(6), json.dumps(
            dict(role="assistant", providerID="openai", modelID="gpt-5.5", finish="stop", tokens=tokens,
                 time={"created": ms(6), "completed": ms(8)}))))
        for pid, mid, t, data in (
                ("cp01", "c01", 5, dict(type="text", text="Scan it.")),
                ("cp02", "c02", 6, dict(type="tool", tool="bash", state={
                    "status": "completed", "input": {"command": "ls"}, "output": "a",
                    "time": {"start": ms(6), "end": ms(7)}})),
                ("cp03", "c02", 8, dict(type="text", text="Scanned the tree."))):
            con.execute("INSERT INTO part VALUES (?,?,?,?,?)", (pid, mid, "ses_child01", ms(t), json.dumps(data)))
        con.commit()
        con.close()

    def test_session_with_a_child_session(self):
        out = self.census("--session", self.sid, "--tree").stdout
        rows = self.table(out)
        self.assertEqual(list(rows), ["ses_fixture01", "ses_child01", "TOTAL"], out)
        root, child = rows["ses_fixture01"], rows["ses_child01"]
        self.assertEqual((root["engine"], root["agent_type"], root["description"], root["parent"]),
                         ("opencode", "main", "Invented store task", "-"))
        self.assertEqual((root["calls"], root["errors"], root["denials"], root["compactions"], root["spawns"],
                          root["final"]), ("3", "1", "1", "1", "1", "Store done."))
        self.assertEqual(root["start_utc"], "2026-01-01T10:00:00Z")
        self.assertEqual((child["engine"], child["agent_type"], child["description"], child["parent"],
                          child["calls"], child["final"]),
                         ("opencode", "explore", "child", "ses_fixture01", "1", "Scanned the tree."))
        self.assertEqual(child["start_utc"], "2026-01-01T10:00:05Z")
        tree = out.split("\nTREE\n")[1].splitlines()
        self.assertTrue(tree[0].startswith("ses_fixture01 main · calls 3 (branch 4) · $"), tree)
        self.assertTrue(tree[1].startswith("  ses_child01 explore · calls 1 (branch 1) · $"), tree)
        self.assertEqual(rows["TOTAL"]["calls"], "4")

    def test_locator_path_form_and_usd_equal_the_totals_line(self):
        loc = f"{self.db}#{self.sid}"
        rows = self.table(self.census(loc).stdout)
        self.assertEqual(list(rows), [self.sid, "TOTAL"])
        self.assertEqual(rows[self.sid]["usd"], self.timeline_total(loc))
        child = self.table(self.census(f"{self.db}#ses_child01").stdout)["ses_child01"]
        self.assertEqual(child["usd"], self.timeline_total(f"{self.db}#ses_child01"))
        self.assertEqual(child["parent"], self.sid)

    def test_session_refuses_a_child_session(self):
        done = self.census("--session", f"{self.db}#ses_child01", check=False)
        self.assertEqual(done.returncode, 2, done.stdout)
        self.assertIn(f"{self.db}#ses_child01", done.stdout)


if __name__ == "__main__":
    unittest.main()
