"""Tests for the chatlog reader kit — run through `.claude/scripts/test.sh`.

Every transcript here is invented: a Claude jsonl, a Codex rollout and an OpenCode store the tests build in a
temp directory, then read through the entry point (`python3 -I chatlog.py {verb}`) exactly as an agent does.
Prices come from a fixture table seeded in the kit directory, so no test calls pfm."""
import contextlib
import copy
import io
import multiprocessing
import json
import os
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from unittest import mock

HERE = os.path.dirname(os.path.realpath(__file__))
ENTRY = os.path.join(HERE, "chatlog.py")
sys.path.insert(0, HERE)

from chatlogkit import cli, locate  # noqa: E402
from chatlogkit.cost import claude as cost_claude, prices as cost_prices  # noqa: E402
from chatlogkit.engines import opencode as oc_engine  # noqa: E402
from chatlogkit.model import parse_stamp  # noqa: E402

BASE = datetime(2026, 1, 1, 10, 0, 0, tzinfo=timezone.utc)
PRICES = {"version": 2, "fetched_at": "2026-01-01T00:00:00Z", "refresh": {"status": "current"}, "rows": [
    {"key": "claude-sonnet-4-6", "engine": "claude", "in": 3, "out": 15, "hit": 0.3, "w5m": 3.75, "w1h": 6},
    {"key": "gpt-5.5", "engine": "codex", "in": 5, "out": 30, "cached": 0.5}]}


def iso(sec):
    return (BASE + timedelta(seconds=sec)).strftime("%Y-%m-%dT%H:%M:%S.000Z")


class Fixture(unittest.TestCase):
    """A temp world: a kit directory holding the price table, and a place for transcripts."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="chatlog-test.")
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.kit = os.path.join(self.tmp, "kit")
        os.makedirs(self.kit)
        with open(os.path.join(self.kit, "prices.json"), "w") as f:
            json.dump(PRICES, f)

    def path(self, *parts):
        return os.path.join(self.tmp, *parts)

    def kitcmd(self, *args, stdin=None, check=True, env=None):
        env = dict(os.environ, CHATLOG_KIT_DIR=self.kit, HOME=self.tmp, PFM_CLAUDE_ROOTS=self.path("projects"),
                   PFM_CODEX_ROOT=self.path("codex"), PFM_OPENCODE_ROOT=self.path("opencode")) | (env or {})
        env = {k: v for k, v in env.items() if v is not None}
        done = subprocess.run([sys.executable, "-I", ENTRY, *args], capture_output=True, text=True, env=env,
                              input=stdin)
        if check and done.returncode:
            self.fail(f"chatlog.py {' '.join(args)} rc={done.returncode}\n{done.stdout}\n{done.stderr}")
        return done

    def prep(self, target, *extra, env=None):
        out = self.kitcmd("prep", target, "--dir", self.path("run"), *extra, env=env).stdout
        self.dir = self.path("run")
        return out

    def digest(self):
        with open(os.path.join(self.dir, "digest.txt")) as f:
            return f.read().splitlines()

    def mech(self):
        with open(os.path.join(self.dir, "mech.tsv")) as f:
            return [ln.split("\t") for ln in f.read().splitlines()]

    def labelled(self, label):
        """The digest lines (or timeline lines for M rows) a mechanical label sits on."""
        dig = self.digest()
        return [dig[int(r[0][1:]) - 1] for r in self.mech() if r[1] == label and r[0].startswith("D")]

    def counts(self):
        out = {}
        for r in self.mech():
            out[r[1]] = out.get(r[1], 0) + 1
        return out


# ------------------------------------------------------------------------------------------ Claude

def c_user(text, t, **extra):
    return {"type": "user", "timestamp": iso(t), "sessionId": "s1", "cwd": "/fx", "message": {"role": "user", "content": text},
            **extra}


def c_call(cid, name, inp, t, usage=(100, 10, 0, 0), stop="tool_use", mid=None):
    i, o, cr, cw = usage
    return {"type": "assistant", "timestamp": iso(t), "sessionId": "s1", "requestId": "r" + cid,
            "message": {"id": mid or "m" + cid, "role": "assistant", "model": "claude-sonnet-4-6", "stop_reason": stop,
                        "content": [{"type": "tool_use", "id": cid, "name": name, "input": inp}],
                        "usage": {"input_tokens": i, "output_tokens": o, "cache_read_input_tokens": cr,
                                  "cache_creation_input_tokens": cw}}}


def c_result(cid, text, t, err=False):
    return {"type": "user", "timestamp": iso(t), "sessionId": "s1",
            "message": {"role": "user", "content": [{"type": "tool_result", "tool_use_id": cid, "content": text,
                                                     "is_error": err}]}}


def c_final(text, t):
    return {"type": "assistant", "timestamp": iso(t), "sessionId": "s1", "requestId": "rfin",
            "message": {"id": "mfin", "role": "assistant", "model": "claude-sonnet-4-6", "stop_reason": "end_turn",
                        "content": [{"type": "text", "text": text}],
                        "usage": {"input_tokens": 50, "output_tokens": 5, "cache_read_input_tokens": 1000,
                                  "cache_creation_input_tokens": 0}}}


def claude_records():
    """Brief; a shell write; a read that is denied and retried (an error with a tail, the call after it, a
    repeat); a tool write; a spawn that waits 75 s; a notification; a failed compact call; three
    compactions; a second prompt; the final reply; one line that is no JSON."""
    r = [c_user("Audit the invented thing and report.", 0),
         c_call("t1", "Bash", {"command": "echo hi > /fx/out.txt"}, 1), c_result("t1", "hi", 2),
         c_call("t2", "Read", {"file_path": "/fx/a.txt"}, 3),
         c_result("t2", "Error: permission denied\nwhile reading /fx/a.txt\nretry later", 4, err=True),
         c_call("t3", "Read", {"file_path": "/fx/a.txt"}, 5), c_result("t3", "contents", 6),
         c_call("t4", "Write", {"file_path": "/fx/b.txt", "content": "x"}, 7), c_result("t4", "ok", 8),
         c_call("t5", "Agent", {"subagent_type": "tracer", "description": "look"}, 9), c_result("t5", "found", 84),
         c_user("<task-notification>agent done</task-notification>", 85),
         c_call("t6", "mcp__x__compact", {"why": "full"}, 86), c_result("t6", "compact failed", 87, err=True)]
    for k in range(3):
        r.append({"type": "system", "subtype": "compact_boundary", "timestamp": iso(90 + k)})
        r.append(c_user(f"summary {k} of what happened", 91 + k, isCompactSummary=True))
    r += [c_user("Now finish up.", 100), c_final("All done.", 101)]
    return r


SID_A = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
SID_B = "5f6e7d8c-9b0a-4c1d-8e2f-3a4b5c6d7e8f"
SID_C = "c3b2a190-8d7c-4b6a-9f5e-4d3c2b1a0f9e"


def session_records(sid, tag):
    """claude_records() as one session wrote them: its sessionId and message ids (msg_...) and request ids of its own."""
    recs = copy.deepcopy(claude_records())
    for r in recs:
        r["sessionId"] = sid
        if r["type"] == "assistant":
            r["requestId"] = tag + r["requestId"]
            r["message"]["id"] = "msg_" + tag + r["message"]["id"]
    return recs


def call_keys(recs):
    return ["message:" + r["message"]["id"] for r in recs if r["type"] == "assistant"]


class ClaudeKit(Fixture):
    def test_typed_blocked_field_in_error_output_is_not_a_denial(self):
        path = self.write_claude([
            c_user("Read the contract.", 0),
            c_call("t1", "Bash", {"command": "cat spec.py missing.py"}, 1),
            c_result("t1", "Exit code 1\n    blocked: str | None = None\nmissing.py: No such file", 2, err=True),
            c_final("Missing file.", 3),
        ])
        self.prep(path)
        self.assertEqual(self.counts().get("ERROR"), 1)
        self.assertEqual(self.counts().get("DENIAL", 0), 0)

    def test_prompt_snapshot_is_readable_historical_instruction_evidence(self):
        snapshot = {"type": "attachment", "timestamp": iso(1),
                    "attachment": {"type": "prompt_snapshot",
                                   "systemPrompt": ["Budget: 45 tool calls.", "Prefer no rendering."]}}
        path = self.write_claude([c_user("Screenshots first.", 0), snapshot, c_final("Done.", 2)])
        self.prep(path)
        self.assertEqual(len(self.labelled("PROMPT-SNAPSHOT")), 1)
        out = self.kitcmd("entry", self.dir, "L2").stdout
        self.assertIn("PROMPT SNAPSHOT\nBudget: 45 tool calls.\nPrefer no rendering.", out)
        self.assertNotIn("DROPPED attachment.prompt_snapshot", "\n".join(self.digest()))

    def write_claude(self, records=None, name="s1.jsonl"):
        path = self.path("projects", "p", name)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            for rec in records or claude_records():
                f.write(json.dumps(rec) + "\n")
            f.write("this line is not json\n")
        return path

    def test_evidence_concurrent_prep_keeps_separate_generations(self):
        """Both processes reach build after the directory check; no scheduler luck proves isolation."""
        paths = [self.write_claude([c_user(tag, 0), c_final(tag, 1)], name=tag + ".jsonl")
                 for tag in ("first-evidence", "second-evidence")]
        ctx = multiprocessing.get_context("fork")
        barrier, results = ctx.Barrier(2), ctx.Queue()
        build = cli.build

        def synchronized_build(*args):
            barrier.wait(timeout=15)
            return build(*args)

        def worker(path):
            output = io.StringIO()
            try:
                with contextlib.redirect_stdout(output):
                    cli.prep([path, "--dir", self.path("run")])
                results.put((True, output.getvalue()))
            except BaseException as e:
                results.put((False, repr(e)))

        env = {"HOME": self.tmp, "CHATLOG_KIT_DIR": self.kit}
        with mock.patch.dict(os.environ, env), mock.patch.object(cli, "KIT_DIR", self.kit), \
                mock.patch.object(cli, "build", synchronized_build):
            workers = [ctx.Process(target=worker, args=(p,)) for p in paths]
            for proc in workers:
                proc.start()
            try:
                got = [results.get(timeout=25) for _ in workers]
                for proc in workers:
                    proc.join(timeout=5)
                    self.assertEqual(proc.exitcode, 0)
            finally:
                for proc in workers:
                    if proc.is_alive():
                        proc.terminate()
                        proc.join()
                results.close()
                results.join_thread()
        self.assertTrue(all(ok for ok, _ in got), got)
        dirs = [next(ln[4:] for ln in text.splitlines() if ln.startswith("DIR ")) for _, text in got]
        self.assertEqual(len(set(dirs)), 2, got)
        for dirp in dirs:
            with open(os.path.join(dirp, "kit.json")) as f:
                kit = json.load(f)
            tag = os.path.basename(kit["path"]).removesuffix(".jsonl")
            with open(os.path.join(dirp, "digest.txt")) as f:
                self.assertIn(tag, f.read())
            self.kitcmd("report", dirp)
            self.kitcmd("verify", os.path.join(dirp, "report.tsv"))

    def test_evidence_same_target_prep_preserves_existing_generation(self):
        path = self.write_claude([c_user("original evidence", 0), c_final("old final", 1)])
        self.prep(path)
        self.kitcmd("note", self.dir, "D1 CLAIM")
        self.kitcmd("report", self.dir)
        names = ("kit.json", "digest.txt", "timeline.txt", "mech.tsv", "notes.tsv", "report.tsv")
        before = {}
        for name in names:
            with open(os.path.join(self.dir, name), "rb") as f:
                before[name] = f.read()
        self.write_claude([c_user("replacement evidence", 0), c_final("new final", 1)])
        out = self.kitcmd("prep", path, "--dir", self.dir).stdout
        newdir = next(ln[4:] for ln in out.splitlines() if ln.startswith("DIR "))
        self.assertNotEqual(newdir, self.dir)
        for name in names:
            with open(os.path.join(self.dir, name), "rb") as f:
                self.assertEqual(f.read(), before[name], name)
        with open(os.path.join(newdir, "digest.txt")) as f:
            self.assertIn("replacement evidence", f.read())
        self.kitcmd("verify", os.path.join(self.dir, "report.tsv"))

    def test_labels(self):
        out = self.prep(self.write_claude())
        self.assertIn("DIGEST", out)
        n = self.counts()
        for label, want in (("BRIEF", 1), ("PROMPT", 1), ("FINAL", 1), ("COMPACTION", 3), ("COMPACT-SUMMARY", 3),
                            ("NOTICE", 1), ("COMPACT-CALL", 1), ("SPAWN", 1), ("WAIT", 1), ("WRITE", 2),
                            ("REPEAT", 1), ("DENIAL", 1), ("ERROR", 2)):
            self.assertEqual(n.get(label), want, f"{label}: {n}")
        self.assertIn("mcp__x__compact", self.labelled("COMPACT-CALL")[0])
        self.assertIn("tracer: look", self.labelled("SPAWN")[0])
        self.assertIn("Read ./a.txt", self.labelled("REPEAT")[0])
        self.assertIn("Read ./a.txt", self.labelled("AFTER-ERROR")[0])
        self.assertTrue(any("permission denied" in ln for ln in self.labelled("ERROR-OUTPUT")))
        writes = " ".join(self.labelled("WRITE"))
        self.assertIn("echo hi > ./out.txt", writes)
        self.assertIn("Write ./b.txt", writes)

    def test_whole_transcript_accounting(self):
        """Nothing silently dropped: every record is a digest line, a header row or counted; the bad line is named."""
        self.prep(self.write_claude())
        head = "\n".join(self.digest()[:12])
        self.assertIn("RECORDS 23 = 22 rendered + 0 header + 0 dropped + 1 unparsed", head)
        self.assertIn("UNPARSED L23", head)

    def test_timeline_priced_from_usage(self):
        self.prep(self.write_claude())
        with open(os.path.join(self.dir, "timeline.txt")) as f:
            tl = f.read().splitlines()
        self.assertTrue(tl[0].startswith("TIMELINE"), tl[0])
        self.assertIn("· 7 calls ·", tl[0])
        rows = [ln for ln in tl if ln.startswith("  #")]
        self.assertEqual(len(rows), 7)
        first = rows[0]
        # 100 in * 3 + 10 out * 15 = 450 micro-dollars
        self.assertIn("$0.0004", first)
        want = 6 * (100 * 3 + 10 * 15) + (50 * 3 + 5 * 15 + 1000 * 0.3)
        self.assertIn(f"${want / 1e6:.4f}", tl[0])
        counts = self.counts()
        self.assertEqual((counts["TOTALS"], counts["COSTLIEST"]), (3, 7))

    def test_failed_refresh_of_a_stale_table_is_a_data_gap(self):
        self.prep(self.write_claude(), env=self.fake_pfm("echo 'model-cost: offline' >&2\nexit 1\n"))
        with open(os.path.join(self.dir, "timeline.txt")) as f:
            gaps = [ln for ln in f.read().splitlines() if ln.startswith("data gaps:")]
        self.assertEqual(len(gaps), 1, gaps)
        self.assertIn("price refresh failed (pfm model-cost rc 1: model-cost: offline)", gaps[0])

    def fake_pfm(self, script):
        bin_dir = self.path("bin")
        os.makedirs(bin_dir, exist_ok=True)
        with open(os.path.join(bin_dir, "pfm"), "w") as f:
            f.write("#!/bin/sh\n" + script)
        os.chmod(os.path.join(bin_dir, "pfm"), 0o755)
        old = BASE.timestamp() - 3 * 86400
        os.utime(os.path.join(self.kit, "prices.json"), (old, old))
        return {"PATH": bin_dir + os.pathsep + os.environ.get("PATH", "")}

    def test_refreshed_table_replaces_the_file_whole(self):
        """A refresh never rewrites prices.json in place: a parallel prep reads the old table or the new one."""
        fresh = self.path("fresh.json")
        with open(fresh, "w") as f:
            json.dump(PRICES, f)
        env = self.fake_pfm(f"cat '{fresh}'\n")
        table = os.path.join(self.kit, "prices.json")
        before = os.stat(table).st_ino
        self.prep(self.write_claude(), env=env)
        self.assertGreater(os.path.getmtime(table), BASE.timestamp())
        self.assertNotEqual(os.stat(table).st_ino, before)

    def test_response_another_transcript_owns_is_not_billed_here(self):
        """pfm's owner rule: a response two transcripts of one project hold is billed in the first by path."""
        recs = claude_records()
        self.write_claude([recs[1]], name="s0.jsonl")
        self.prep(self.write_claude(recs))
        with open(os.path.join(self.dir, "timeline.txt")) as f:
            tl = f.read().splitlines()
        self.assertIn("· 6 calls ·", tl[0])
        self.assertIn("1 calls copied from another transcript", tl[-1])

    def test_shared_census_walks_and_reads_the_tree_once_with_identical_results(self):
        """One Shared across a census: same owners and same timeline as a fresh read per transcript, the domain
        walked once and each other transcript's bytes read once."""
        recs = claude_records()
        fork = copy.deepcopy(recs)
        for r in fork:
            r["sessionId"] = "fk1"
            if r["type"] == "assistant":
                r["forkedFrom"] = {"sessionId": "s1", "messageUuid": "u1"}
        orig = self.write_claude(recs, name="s1.jsonl")
        copy_ = self.write_claude(fork, name="s2.jsonl")
        keys = ["message:" + r["message"]["id"] for r in recs if r["type"] == "assistant"]
        book = cost_prices.load(self.kit)

        def census(shared_for_all):
            walks, opens, results = [], {}, []
            real_walk = cost_claude._walk

            def counting_walk(d, out, scan):
                walks.append(d)
                return real_walk(d, out, scan)

            def counting_open(path, mode="r", *a, **kw):
                if mode == "rb" and str(path).endswith(".jsonl"):
                    opens.setdefault(self.current, []).append(path)
                return open(path, mode, *a, **kw)

            shared = cost_claude.Shared() if shared_for_all else None
            with mock.patch.object(cost_claude, "_walk", counting_walk), \
                    mock.patch.object(cost_claude, "open", counting_open, create=True):
                for path in (orig, copy_, orig, copy_):
                    self.current = path
                    scan = cost_claude.Scan()
                    owned = cost_claude.owners(path, keys, scan, shared)
                    head, rows, run_scan = cost_claude.run(path, book, shared)
                    results.append((owned, scan.read_errors, head, cost_claude.render(rows), run_scan.copied,
                                    run_scan.read_errors))
            others = {}
            for me, paths in opens.items():
                for p in paths:
                    if p != me:
                        others[p] = others.get(p, 0) + 1
            return results, len(walks), others

        fresh, fresh_walks, fresh_reads = census(False)
        shared, shared_walks, shared_reads = census(True)
        self.assertEqual(shared, fresh)
        self.assertEqual(set(fresh[1][0].values()), {orig}, "the fixture must exercise an owner")
        self.assertEqual(fresh_walks, 8)
        self.assertEqual(shared_walks, 1, "a shared census walks the domain once")
        self.assertEqual(shared_reads, {orig: 1, copy_: 1}, "each other transcript's bytes are read once")
        self.assertEqual(fresh_reads, {orig: 4, copy_: 4})

    def counted_reads(self, work):
        """work() under a count of the bytes opens of .jsonl files: {asker path: {file path: opens}}, the asker
        being self.current; an asker's own file is left out."""
        opens = {}

        def counting_open(path, mode="r", *a, **kw):
            if mode == "rb" and str(path).endswith(".jsonl") and path != self.current:
                per = opens.setdefault(self.current, {})
                per[path] = per.get(path, 0) + 1
            return open(path, mode, *a, **kw)

        with mock.patch.object(cost_claude, "open", counting_open, create=True):
            work()
        return opens

    def census_of(self, askers, shared):
        """owners() of each (path, keys) in order, with the one Shared (None: a fresh read per ask)."""
        out = []

        def work():
            for path, keys in askers:
                self.current = path
                scan = cost_claude.Scan()
                out.append((cost_claude.owners(path, keys, scan, shared), scan.read_errors))

        return out, self.counted_reads(work)

    def test_shared_census_reads_each_other_file_once_across_different_sessions(self):
        """Two transcripts of different sessions have different session ids; one Shared still reads every other
        file of the domain once, whatever ids the next transcript brings."""
        recs_a, recs_b = session_records(SID_A, "a"), session_records(SID_B, "b")
        a = self.write_claude(recs_a, name=SID_A + ".jsonl")
        b = self.write_claude(recs_b, name=SID_B + ".jsonl")
        copy_a = self.write_claude(recs_a, name="copy-of-a.jsonl")
        # a third session's file fails the ids gate for both; a file naming A only in its text fails B's ids
        # gate and A's tokens gate
        c = self.write_claude(session_records(SID_C, "c"), name=SID_C + ".jsonl")
        mention = session_records(SID_C, "z") + [c_user(f"see /projects/p/{SID_A}.jsonl", 200)]
        mentions = self.write_claude(mention, name="mentions-a.jsonl")
        # a file of session B that holds none of B's calls: fails A's ids gate, passes B's, fails B's msg gate
        sibling_b = self.write_claude(session_records(SID_B, "bz"), name="sibling-of-b.jsonl")
        askers = [(a, call_keys(recs_a)), (b, call_keys(recs_b))]
        fresh, fresh_reads = self.census_of(askers, None)
        shared = cost_claude.Shared()
        got, reads = self.census_of(askers, shared)
        self.assertEqual(got, fresh)
        self.assertEqual(set(got[0][0].values()), {min(a, copy_a)}, "the fixture must exercise an owner")
        self.assertEqual(got[1][0], {}, "B's calls are held by no other transcript")
        others = {a: {copy_a, b, c, mentions, sibling_b}, b: {a, copy_a, c, mentions, sibling_b}}
        self.assertEqual({p: set(per) for p, per in fresh_reads.items()}, others)
        every = {}
        for per in reads.values():
            for f, n in per.items():
                every[f] = every.get(f, 0) + n
        self.assertEqual(every, {copy_a: 1, b: 1, c: 1, mentions: 1, a: 1, sibling_b: 1}, "each other file is opened once")
        self.assertEqual(shared.reads, 6)
        self.assertLess(shared.index_bytes, shared.read_bytes, "the index is smaller than the bytes it answers for")

    def test_a_file_skipped_for_one_session_and_admitted_for_the_next_is_read_twice_at_most(self):
        """The index is built on the first read; the tokens and the holders are read on the first ask that passes
        the ids gate, which may come after an ask that failed it: a second read, never a third."""
        recs_a, recs_b = session_records(SID_A, "a"), session_records(SID_B, "b")
        a = self.write_claude(recs_a, name=SID_A + ".jsonl")
        b = self.write_claude(recs_b, name=SID_B + ".jsonl")
        copy_b = self.write_claude(recs_b, name="copy-of-b.jsonl")
        askers = [(a, call_keys(recs_a)), (b, call_keys(recs_b)), (a, call_keys(recs_a)), (b, call_keys(recs_b))]
        fresh, _ = self.census_of(askers, None)
        got, reads = self.census_of(askers, cost_claude.Shared())
        self.assertEqual(got, fresh)
        self.assertEqual(set(got[1][0].values()), {min(b, copy_b)}, "the fixture must exercise an owner")
        total = sum(per.get(copy_b, 0) for per in reads.values())
        self.assertEqual(total, 2)

    def test_the_session_id_gate_is_the_substring_gate(self):
        """Whatever surrounds a session id in another transcript's bytes, the Shared gate admits the file exactly
        when `any(id in bytes)` does: a path, a glued run, the file start, case, a split, a short id."""
        u, other = SID_A.encode(), SID_B.encode()
        needle = b'"needle-1"\n'
        cases = [
            ("a path in text", b'{"text":"see /p/' + u + b'.jsonl"}\n', [u], True),
            ("glued hex before", b'{"text":"cafebabe' + u + b'"}\n', [u], True),
            ("glued run after", b'{"text":"' + u + b'deadbeef-0"}\n', [u], True),
            ("two ids glued", b'{"text":"' + other + u + b'"}\n', [u], True),
            ("the file start", u + b"\n", [u], True),
            ("upper case in both", u.upper() + b"\n", [u.upper()], True),
            ("upper case data, lower id", u.upper() + b"\n", [u], False),
            ("lower case data, upper id", u + b"\n", [u.upper()], False),
            ("one char off", u[:-1] + b"0\n", [u], False),
            ("split by a newline", u[:20] + b"\n" + u[20:] + b"\n", [u], False),
            ("an absent id and a present one", u + b"\n", [other, u], True),
            ("two absent ids", b"nothing here\n", [other, u], False),
            ("a short id inside a word", b'{"text":"xs1y"}\n', [b"s1"], True),
            ("a short id absent", b'{"text":"xs2y"}\n', [b"s1"], False),
            ("a short id and an absent long one", b'{"text":"s1"}\n', [other, b"s1"], True),
            ("a long id present and a short one absent", u + b"\n", [b"zz", u], True),
            ("a truncated id", b'{"text":"' + u[:-1] + b'"}\n', [u[:-1]], True),
        ]
        for name, body, ids, want in cases:
            with self.subTest(name):
                path = self.path("projects", "p", "case.jsonl")
                os.makedirs(os.path.dirname(path), exist_ok=True)
                with open(path, "wb") as f:
                    f.write(body + needle)
                self.assertEqual(any(i in body + needle for i in ids), want, "the case itself")
                shared = cost_claude.Shared()
                got = shared.calls(path, set(ids), False, cost_claude.Needles({b"needle-1"}), cost_claude.Scan())
                self.assertEqual(got is not None, want)
                self.assertEqual(shared.reads, 1, "one read answers the gate, the tokens and the holders")

    def test_the_msg_gate_is_the_token_gate(self):
        """Message-id needles are told from the index, and the index never hides a file the token gate admits:
        whatever surrounds a msg_ string, the file is admitted exactly when TOKEN reads it as a quoted token."""
        needle = b"msg_abc"
        cases = [
            ("a quoted id", b'{"id":"msg_abc","type":"x"}\n', True),
            ("two quoted ids", b'"msg_abc""msg_abc"\n', True),
            ("after a stray quote", b'""msg_abc"\n', True),
            ("a name in text", b'{"text":"see msg_abc here"}\n', False),
            ("an escaped quote", b'{"text":"{\\"id\\":\\"msg_abc\\"}"}\n', False),
            ("a longer id", b'{"id":"msg_abcd"}\n', False),
            ("a glued prefix", b'{"id":"xmsg_abc"}\n', False),
            ("no closing quote", b'{"id":"msg_abc\n', False),
            ("hidden by the token before it", b'"x"msg_abc"\n', False),
        ]
        for name, body, want in cases:
            with self.subTest(name):
                path = self.path("projects", "p", "msg.jsonl")
                os.makedirs(os.path.dirname(path), exist_ok=True)
                with open(path, "wb") as f:
                    f.write(body)
                self.assertEqual(needle in cost_claude.TOKEN.findall(body), want, "the case itself")
                shared = cost_claude.Shared()
                needles = cost_claude.Needles({needle})
                self.assertTrue(needles.by_msg)
                got = shared.calls(path, set(), False, needles, cost_claude.Scan())
                self.assertEqual(got is not None, want)
                self.assertEqual(shared.reads, 1)
                self.assertEqual(bool(shared.tokens), needle in cost_claude.MSG_RUNS.findall(body),
                                 "the token set is built only when the index cannot tell")

    def test_a_short_session_id_reads_the_file_again_only_for_an_id_it_has_not_answered(self):
        path = self.path("projects", "p", "short.jsonl")
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "wb") as f:
            f.write(b'{"text":"s1 and s2"}\n"needle-1"\n')
        shared = cost_claude.Shared()
        for ids, want, reads in (({b"s1"}, True, 1), ({b"s1"}, True, 1), ({b"s3"}, False, 2), ({b"s3"}, False, 2),
                                 ({b"s3", b"s2"}, True, 3), ({b"s3", b"s4"}, False, 4)):
            got = shared.calls(path, ids, False, cost_claude.Needles({b"needle-1"}), cost_claude.Scan())
            self.assertEqual((got is not None, shared.reads), (want, reads), ids)

    def test_a_session_id_only_inside_a_longer_string_still_makes_the_file_a_holder(self):
        """End to end: another transcript that holds the calls and names the session only in a path of its text
        is a holder of them, as the substring gate always made it; the same file one character off is not."""
        recs = session_records(SID_A, "a")
        a = self.write_claude(recs, name=SID_A + ".jsonl")
        mention = copy.deepcopy(recs)
        for r in mention:
            r["sessionId"] = "other-session"
        near = copy.deepcopy(mention)
        mention.append(c_user(f"see /projects/p/{SID_A}.jsonl", 200))
        near.append(c_user(f"see /projects/p/{SID_A[:-1]}0.jsonl", 200))
        held = self.write_claude(mention, name="holds-a.jsonl")
        not_held = self.write_claude(near, name="near-a.jsonl")
        got, _ = self.census_of([(a, call_keys(recs))], cost_claude.Shared())
        self.assertEqual(got[0][0], {k: min(a, held) for k in set(call_keys(recs))})
        self.assertNotIn(not_held, got[0][0].values())

    def test_a_file_that_changes_mid_census_loses_its_facts(self):
        """A Shared fact lives only while its file is unchanged: a file rewritten with a copy of A's calls after its
        first read is read again, and holds them as a fresh read finds."""
        recs_a, recs_b = session_records(SID_A, "a"), session_records(SID_B, "b")
        a = self.write_claude(recs_a, name=SID_A + ".jsonl")
        b = self.write_claude(recs_b, name=SID_B + ".jsonl")
        live = self.write_claude(session_records(SID_C, "c"), name=SID_C + ".jsonl")
        shared = cost_claude.Shared(fresh_s=0)
        first, _ = self.census_of([(b, call_keys(recs_b)), (a, call_keys(recs_a))], shared)
        self.assertEqual(first[1][0], {}, "no other file holds A's calls yet")
        self.write_claude(session_records(SID_C, "c") + recs_a, name=SID_C + ".jsonl")
        got, reads = self.census_of([(a, call_keys(recs_a))], shared)
        fresh, _ = self.census_of([(a, call_keys(recs_a))], None)
        self.assertEqual(got, fresh)
        self.assertEqual(got[0][0], {k: min(a, live) for k in set(call_keys(recs_a))})
        self.assertEqual(reads[a].get(live), 1, "the changed file is read again")

    def test_a_stat_is_reused_within_fresh_s_and_taken_again_after(self):
        path = self.write_claude(session_records(SID_A, "a"), name=SID_A + ".jsonl")
        shared, clock = cost_claude.Shared(fresh_s=5), [100.0]
        with mock.patch.object(cost_claude.time, "monotonic", lambda: clock[0]), \
                mock.patch.object(cost_claude.os, "stat", wraps=os.stat) as stat:
            shared.stat(path)
            clock[0] += 4.9
            shared.stat(path)
            self.assertEqual(stat.call_count, 1, "a stat younger than fresh_s answers")
            clock[0] += 0.2
            shared.stat(path)
            self.assertEqual(stat.call_count, 2, "an older one is taken again")

    def test_one_session_census_builds_the_uuid_index_only_when_a_second_id_asks(self):
        """Without eager_index the first read answers the ids by substring and leaves no windows; a new id on an
        unchanged file reads it again and leaves them, so a third id is answered with no read; every answer is
        a fresh read's."""
        recs_a, recs_b = session_records(SID_A, "a"), session_records(SID_B, "b")
        a = self.write_claude(recs_a, name=SID_A + ".jsonl")
        b = self.write_claude(recs_b, name=SID_B + ".jsonl")
        copy_a = self.write_claude(recs_a, name="copy-of-a.jsonl")
        c = self.write_claude(session_records(SID_C, "c"), name=SID_C + ".jsonl")
        askers = [(a, call_keys(recs_a)), (a, call_keys(recs_a))]
        shared = cost_claude.Shared(eager_index=False)
        got, reads = self.census_of(askers, shared)
        fresh, _ = self.census_of(askers, None)
        self.assertEqual(got, fresh)
        self.assertEqual(set(got[0][0].values()), {min(a, copy_a)}, "the fixture must exercise an owner")
        self.assertEqual((shared.uuids, reads[a].get(c)), ({}, 1), "one id: no windows, one read")
        got, reads = self.census_of([(b, call_keys(recs_b))], shared)
        self.assertEqual(got, self.census_of([(b, call_keys(recs_b))], None)[0])
        self.assertEqual(reads[b].get(c), 1, "a second id reads the file again")
        self.assertIn(c, shared.uuids, "and leaves its windows")
        before = shared.reads
        self.assertFalse(shared.calls(c, {SID_A.upper().encode()}, False, cost_claude.Needles({b"x"}),
                                      cost_claude.Scan()))
        self.assertEqual(shared.reads, before, "a third id is told from the windows")

    def test_claude_config_dir_adds_to_pfm_roots(self):
        """With no PFM_CLAUDE_ROOTS the kit searches pfm's ~/.claude/projects, and CLAUDE_CONFIG_DIR's projects too."""
        for sid, home in (("sess-home", os.path.join(self.tmp, ".claude")), ("sess-acct", self.path("acct"))):
            path = os.path.join(home, "projects", "p", sid + ".jsonl")
            os.makedirs(os.path.dirname(path))
            with open(path, "w") as f:
                f.write("\n".join(json.dumps(r) for r in claude_records()) + "\n")
            shutil.rmtree(self.path("run"), True)
            out = self.prep(sid, env={"PFM_CLAUDE_ROOTS": None, "CLAUDE_CONFIG_DIR": self.path("acct")})
            self.assertIn("DIGEST", out, sid)

    def test_unusable_kit_json_in_dir_is_a_fresh_reader_dir(self):
        os.makedirs(self.path("run"))
        with open(self.path("run", "kit.json"), "w") as f:
            f.write("[]")
        out = self.prep(self.write_claude())
        self.assertIn("DIR " + self.path("run", "reader."), out)

    def test_unpriced_model_is_marked_not_zero(self):
        recs = claude_records()
        recs[1]["message"]["model"] = "claude-unlisted-9"
        self.prep(self.write_claude(recs))
        with open(os.path.join(self.dir, "timeline.txt")) as f:
            tl = f.read()
        self.assertIn("UNPRICED calls", tl)
        self.assertIn("claude-unlisted-9", tl)
        self.assertIn("n/a", tl.splitlines()[1])

    def test_split_plan_for_a_large_transcript(self):
        recs = [c_user("Brief.", 0)]
        for k in range(30):
            recs += [c_call(f"x{k}", "Bash", {"command": f"echo {k} " + "y" * 150}, 1 + 2 * k),
                     c_result(f"x{k}", "z" * 100, 2 + 2 * k)]
        recs.append(c_final("Done.", 100))
        out = self.prep(self.write_claude(recs), "--reader-max", "3000")
        split = [ln for ln in out.splitlines() if ln.startswith("SPLIT")]
        self.assertEqual(len(split), 1, out)
        self.assertIn("Lines: ", split[0])
        self.assertRegex(split[0], r"Lines: 1-\d+ \d+-\d+( \d+-\d+)* \d+-$")

    def test_sub_agent_meta_feeds_header(self):
        path = self.path("projects", "p", "sess", "subagents", "agent-abc123.jsonl")
        os.makedirs(os.path.dirname(path))
        with open(path, "w") as f:
            for rec in claude_records():
                f.write(json.dumps(rec) + "\n")
        with open(path[:-len(".jsonl")] + ".meta.json", "w") as f:
            json.dump({"agentType": "tracer", "description": "invented task"}, f)
        out = self.prep(path)
        self.assertIn("tracer", out)
        self.assertIn("invented task", out)
        found = self.kitcmd("prep", "abc123", "--dir", self.path("run2"))
        self.assertIn("DIGEST", found.stdout)


WRITTEN = "line one\n" + "x" * 3000 + "\nlast line"
FINAL = "All " + "done " * 200 + "END"


class EntryVerb(ClaudeKit):
    """`entry` prints one event whole, where the digest caps it: a Write's content, a result up to --max, a final."""

    def prepared(self):
        recs = [c_user("Write the file.", 0),
                c_call("w1", "Write", {"file_path": "/fx/big.txt", "content": WRITTEN}, 1), c_result("w1", "ok", 2),
                c_call("b1", "Bash", {"command": "cat /fx/big.txt"}, 3), c_result("b1", "R" * 500 + "TAIL", 4),
                c_call("b2", "Read", {"file_path": "/fx/none"}, 5), c_result("b2", "Error: no such file", 6, err=True),
                c_call("b3", "Bash", {"command": "sleep 1"}, 7), c_final(FINAL, 9)]
        path = self.write_claude(recs)
        self.prep(path)
        return path

    def entry(self, *args, check=True):
        return self.kitcmd("entry", self.dir, *args, check=check)

    def test_a_call_prints_its_whole_input_and_result(self):
        self.prepared()
        out = self.entry("L2").stdout.splitlines()
        self.assertEqual(out[:2], ["ENTRY L2 2026-01-01T10:00:01.000Z CALL Write", "INPUT"])
        self.assertEqual(json.loads(out[2]), {"file_path": "/fx/big.txt", "content": WRITTEN})
        self.assertEqual(out[3:], ["RESULT ok · 2 chars", "ok"])

    def test_a_digest_line_names_the_same_event(self):
        self.prepared()
        d = next(i for i, ln in enumerate(self.digest(), 1) if ln.startswith("L2 "))
        self.assertEqual(self.entry(f"D{d}").stdout, self.entry("L2").stdout)

    def test_a_result_past_max_ends_in_a_marker_naming_the_rest(self):
        self.prepared()
        out = self.entry("L4", "--max", "100").stdout.splitlines()
        self.assertEqual(out[3:], ["RESULT ok · 504 chars", "R" * 100, "[+404 chars not shown — raise --max]"])
        self.assertEqual(self.entry("L4").stdout.splitlines()[4], "R" * 500 + "TAIL")

    def test_a_failed_and_an_unanswered_call(self):
        self.prepared()
        self.assertEqual(self.entry("L6").stdout.splitlines()[3:], ["RESULT ERR · 19 chars", "Error: no such file"])
        self.assertEqual(self.entry("L8").stdout.splitlines()[3:], ["RESULT none"])

    def test_the_final_reply_whole(self):
        self.prepared()
        self.assertEqual(self.entry("L9").stdout.splitlines(), ["ENTRY L9 2026-01-01T10:00:09.000Z FINAL", FINAL])

    def test_a_selector_naming_no_event_fails_never_prints_nothing(self):
        path = self.prepared()
        for args, why in ((("D1",), "names no transcript line"), (("D999",), "the digest has D1-D"),
                          (("L3",), "no rendered event at that transcript line"), (("X3",), "not D{n} or L{n}"),
                          (("L2", "--max", "-5"), "--max -5"), (("L2", "--max", "abc"), "--max abc")):
            done = self.entry(*args, check=False)
            self.assertEqual(done.returncode, 1, (args, done.stdout))
            self.assertTrue(done.stdout.startswith("KIT FAILED — "), (args, done.stdout))
            self.assertIn(why, done.stdout, args)
        done = self.entry("L2", "L4", check=False)
        self.assertEqual(done.returncode, 2, done.stdout)
        self.assertIn("usage: chatlog.py entry DIR", done.stdout)
        done = self.kitcmd("entry", self.path("not-prepared"), "L2", check=False)
        self.assertEqual(done.returncode, 1, done.stdout)
        self.assertIn("is not a prepared directory", done.stdout)
        os.unlink(path)
        done = self.entry("L2", check=False)
        self.assertEqual(done.returncode, 1, done.stdout)
        self.assertTrue(done.stdout.startswith(f"FAILED entry {path} — "), done.stdout)


# ------------------------------------------------------------------------------------------ Codex

def x_rec(kind, payload, t):
    return {"timestamp": iso(t), "type": kind, "payload": payload}


def x_tokens(last, total, t):
    def usage(u):
        i, c, o = u
        return {"input_tokens": i, "cached_input_tokens": c, "output_tokens": o, "total_tokens": i + o}
    return x_rec("event_msg", {"type": "token_count",
                               "info": {"total_token_usage": usage(total), "last_token_usage": usage(last)}}, t)


def codex_records():
    patch = "*** Begin Patch\n*** Add File: /fx/notes.txt\n+hello\n*** End Patch"
    return [
        x_rec("session_meta", {"id": "01aa", "cwd": "/fx", "cli_version": "9.9"}, 0),
        x_rec("turn_context", {"model": "gpt-5.5", "effort": "high"}, 0),
        x_rec("response_item", {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Do the codex thing."}]}, 1),
        x_rec("response_item", {"type": "function_call", "name": "exec_command", "call_id": "c1",
                                "arguments": json.dumps({"cmd": "echo hi > /fx/out.txt"})}, 2),
        x_rec("response_item", {"type": "function_call_output", "call_id": "c1", "output": "Process exited with code 0\nhi"}, 3),
        x_tokens((1000, 200, 50), (1000, 200, 50), 4),
        x_rec("response_item", {"type": "custom_tool_call", "name": "apply_patch", "call_id": "c2", "input": patch}, 5),
        x_rec("response_item", {"type": "custom_tool_call_output", "call_id": "c2", "output": "Done"}, 6),
        x_rec("response_item", {"type": "function_call", "name": "exec_command", "call_id": "c3",
                                "arguments": json.dumps({"cmd": "make test"})}, 7),
        x_rec("response_item", {"type": "function_call_output", "call_id": "c3",
                                "output": "Process exited with code 2\nboom: permission denied"}, 8),
        x_tokens((1500, 1000, 80), (2500, 1200, 130), 9),
        x_rec("response_item", {"type": "function_call", "name": "spawn_agent", "call_id": "c4",
                                "arguments": json.dumps({"message": "go"})}, 10),
        x_rec("response_item", {"type": "function_call_output", "call_id": "c4", "output": "agent started"}, 11),
        x_rec("compacted", {"message": ""}, 12),
        x_rec("event_msg", {"type": "item_completed", "item": {
            "type": "CommandExecution", "command": ["bash", "-lc", "printf x > /fx/z.txt"], "exit_code": 0,
            "aggregated_output": "", "parsed_cmd": [{"type": "unknown"}], "status": "completed",
            "duration": {"secs": 61, "nanos": 0}}}, 13),
        x_tokens((900, 300, 40), (3400, 1500, 170), 14),
        x_rec("some_future_record", {"type": "novel"}, 15),
        x_rec("response_item", {"type": "message", "role": "assistant", "phase": "final_answer",
                              "content": [{"type": "output_text", "text": "Codex done."}]}, 16)]


class CodexKit(Fixture):
    def test_entry_recovers_session_launch_instructions_without_expanding_digest(self):
        recs = codex_records()
        recs[0]["payload"]["base_instructions"] = {"text": "Exact launch instructions\nSecond line"}
        self.prep(self.write_rollout(recs))
        self.assertNotIn("Exact launch instructions", "\n".join(self.digest()))
        out = self.kitcmd("entry", self.dir, "L1").stdout.splitlines()
        self.assertEqual(out[0], "ENTRY L1 2026-01-01T10:00:00.000Z NOTE SESSION_META")
        self.assertEqual(json.loads(out[1]), recs[0]["payload"])

    def test_entry_recovers_a_folded_mixed_action_wrapper(self):
        script = 'text(await tools.apply_patch("patch")); text(await tools.exec_command({cmd:"capture",yield_time_ms:1000}));'
        recs = [x_rec("session_meta", {"id": "folded", "cwd": "/fx"}, 0),
                x_rec("response_item", {"type": "custom_tool_call", "name": "exec", "call_id": "mixed",
                                        "input": script}, 1),
                x_rec("event_msg", {"type": "item_completed", "item": {
                    "type": "FileChange", "changes": {"/fx/file.py": {"type": "update", "unified_diff": "+x"}},
                    "status": "completed"}}, 2),
                x_rec("response_item", {"type": "custom_tool_call_output", "call_id": "mixed",
                                        "output": '{"session_id":75180}'}, 3)]
        self.prep(self.write_rollout(recs))
        self.assertNotIn("yield_time_ms", "\n".join(self.digest()))
        out = self.kitcmd("entry", self.dir, "L2").stdout
        self.assertIn(script, out)
        self.assertIn('{"session_id":75180}', out)

    def test_header_uses_latest_turn_context(self):
        recs = codex_records()
        recs.insert(1, x_rec("turn_context", {"model": "parent-model", "effort": "medium"}, 0))
        out = self.prep(self.write_rollout(recs))
        self.assertIn("gpt-5.5 high", out)
        self.assertNotIn("parent-model", out)

    def test_image_view_records_are_rendered(self):
        recs = codex_records()
        recs[-1:-1] = [x_rec("event_msg", {"type": "item_completed", "item": {
            "type": "ImageView", "path": "file:///fx/piece-01.png"}}, 15)]
        self.prep(self.write_rollout(recs))
        self.assertIn("view_image file://./piece-01.png", "\n".join(self.digest()))

    def test_timeline_token_classes(self):
        recs = codex_records()
        for rec in recs:
            if rec.get("payload", {}).get("type") == "token_count":
                rec["payload"]["info"]["last_token_usage"]["reasoning_output_tokens"] = 10
        self.prep(self.write_rollout(recs))
        with open(os.path.join(self.dir, "timeline.txt")) as f:
            timeline = f.read()
        self.assertIn("TOKENS input=3400 cached_input=1500 uncached_input=1900 output=170 "
                      "reasoning_output=30 nonreasoning_output=140", timeline)

    def write_rollout(self, records=None):
        path = self.path("codex", "sessions", "2026", "01", "01", "rollout-2026-01-01T10-00-00-01aa.jsonl")
        os.makedirs(os.path.dirname(path))
        with open(path, "w") as f:
            for rec in records or codex_records():
                f.write(json.dumps(rec) + "\n")
        return path

    def test_labels(self):
        out = self.prep(self.write_rollout())
        n = self.counts()
        for label, want in (("BRIEF", 1), ("FINAL", 1), ("COMPACTION", 1), ("SPAWN", 1), ("ERROR", 1), ("WAIT", 1),
                            ("DENIAL", 1), ("AFTER-ERROR", 1), ("WRITE", 3)):
            self.assertEqual(n.get(label), want, f"{label}: {n}")
        writes = " ".join(self.labelled("WRITE"))
        for want in ("echo hi > ./out.txt", "patch add ./notes.txt", "printf x > ./z.txt"):
            self.assertIn(want, writes)
        self.assertIn("spawn_agent", self.labelled("SPAWN")[0])
        self.assertIn("NOTICE n/a (Codex has no task-notification record)", out + "\n".join(self.digest()))
        self.assertIn("COMPACT-SUMMARY n/a", "\n".join(self.digest()))

    def test_patch_through_a_function_call_or_a_shell_is_a_write(self):
        """The JSON apply_patch tool (`{"input": patch}`) and a shell running apply_patch write the patched files."""
        recs = codex_records()

        def patch(name):
            return f"*** Begin Patch\n*** Update File: /fx/{name}\n@@\n-a\n+b\n*** End Patch"
        recs[-1:-1] = [
            x_rec("response_item", {"type": "function_call", "name": "apply_patch", "call_id": "c5",
                                    "arguments": json.dumps({"input": patch("j.txt")})}, 15),
            x_rec("response_item", {"type": "function_call_output", "call_id": "c5", "output": "Done"}, 15),
            x_rec("response_item", {"type": "function_call", "name": "shell", "call_id": "c6",
                                    "arguments": json.dumps({"command": ["apply_patch", patch("s.txt")]})}, 15),
            x_rec("response_item", {"type": "function_call_output", "call_id": "c6", "output": "Done"}, 15)]
        self.prep(self.write_rollout(recs))
        self.assertEqual(self.counts().get("WRITE"), 5, self.counts())
        w = self.kitcmd("writes", self.dir).stdout
        self.assertIn("/fx/j.txt", w)
        self.assertIn("/fx/s.txt", w)

    def test_roots_split_as_pfm_splits_them(self):
        """PFM_CODEX_ROOT and PFM_OPENCODE_ROOT are path lists, as pfm reads them (filepath.SplitList)."""
        self.write_rollout()
        db = self.path("opencode", "opencode.db")
        os.makedirs(os.path.dirname(db))
        sid = oc_store(db)
        env = {"PFM_CODEX_ROOT": self.path("nowhere") + os.pathsep + self.path("codex"),
               "PFM_OPENCODE_ROOT": self.path("nowhere") + os.pathsep + self.path("opencode")}
        for target in ("00-01aa", sid):
            shutil.rmtree(self.path("run"), True)
            self.assertIn("DIGEST", self.prep(target, env=env), target)

    def test_unknown_record_is_counted_not_dropped(self):
        self.prep(self.write_rollout())
        head = "\n".join(self.digest()[:14])
        self.assertIn("some_future_record", head)

    def test_timeline_priced_per_call_from_last_usage(self):
        self.prep(self.write_rollout())
        with open(os.path.join(self.dir, "timeline.txt")) as f:
            tl = f.read().splitlines()
        self.assertIn("· 3 calls ·", tl[0])
        self.assertIn("gpt-5.5", tl[0])

        def usd(i, c, o):
            return ((i - c) * 5 + c * 0.5 + o * 30) / 1e6
        want = usd(1000, 200, 50) + usd(1500, 1000, 80) + usd(900, 300, 40)
        self.assertIn(f"${want:.4f}", tl[0])
        rows = [ln for ln in tl if ln.startswith("  #")]
        self.assertEqual(len(rows), 3)
        self.assertIn("exec_command: echo hi > /fx/out.txt", rows[0])
        self.assertIn("ERR", rows[1])

    def test_unpriced_model_is_marked_not_zero(self):
        recs = codex_records()
        recs[1]["payload"]["model"] = "gpt-unlisted"
        path = self.path("rollout-2026-01-01T10-00-00-01bb.jsonl")
        with open(path, "w") as f:
            f.write("\n".join(json.dumps(r) for r in recs) + "\n")
        self.prep(path)
        with open(os.path.join(self.dir, "timeline.txt")) as f:
            tl = f.read()
        self.assertIn('3 UNPRICED calls {"gpt-unlisted":3}', tl)
        self.assertIn("· n/a ·", tl.splitlines()[1])


# ---------------------------------------------------------------------------------------- OpenCode

def oc_store(path):
    con = sqlite3.connect(path)
    con.executescript("""
        CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, title TEXT, directory TEXT,
                              time_created INTEGER, time_updated INTEGER);
        CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT);
        CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, data TEXT);""")
    ms = lambda s: int((BASE + timedelta(seconds=s)).timestamp() * 1000)  # noqa: E731
    sid = "ses_fixture01"
    con.execute("INSERT INTO session VALUES (?,?,?,?,?,?)", (sid, None, "Invented store task", "/fx", ms(0), ms(30)))
    con.execute("INSERT INTO session VALUES (?,?,?,?,?,?)", ("ses_child01", sid, "child", "/fx", ms(5), ms(9)))

    def message(mid, t, **data):
        con.execute("INSERT INTO message VALUES (?,?,?,?)", (mid, sid, ms(t), json.dumps(data)))

    def part(pid, mid, t, **data):
        con.execute("INSERT INTO part VALUES (?,?,?,?,?)", (pid, mid, sid, ms(t), json.dumps(data)))

    message("m01", 0, role="user", agent="build")
    part("p01", "m01", 0, type="text", text="Do the store thing.")
    part("p01b", "m01", 0, type="subtask", agent="explore", description="scan")
    tokens = {"input": 1000, "output": 50, "reasoning": 10, "cache": {"read": 200, "write": 0}}
    message("m02", 2, role="assistant", providerID="openai", modelID="gpt-5.5", finish="tool-calls", tokens=tokens,
            time={"created": ms(2), "completed": ms(8)})
    part("p02", "m02", 3, type="tool", tool="bash", state={
        "status": "error", "input": {"command": "make test"}, "error": "exit 2\nboom: permission denied",
        "time": {"start": ms(3), "end": ms(4)}})
    part("p03", "m02", 5, type="tool", tool="task", state={
        "status": "completed", "input": {"subagent_type": "explore", "description": "scan"}, "output": "scanned",
        "time": {"start": ms(5), "end": ms(70)}})
    part("p05", "m02", 7, type="compaction")
    part("p06", "m02", 8, type="tool", tool="write", state={
        "status": "completed", "input": {"filePath": "/fx/w.txt", "content": "x"}, "output": "ok",
        "time": {"start": ms(8), "end": ms(8)}})
    message("m03", 10, role="assistant", providerID="openai", modelID="gpt-5.5", finish="stop",
            tokens={"input": 500, "output": 20, "reasoning": 0, "cache": {"read": 1500, "write": 0}},
            time={"created": ms(10), "completed": ms(12)})
    part("p07", "m03", 11, type="text", text="Store done.")
    con.commit()
    con.close()
    return sid


class OpenCodeKit(Fixture):
    def setUp(self):
        super().setUp()
        self.db = self.path("opencode", "opencode.db")
        os.makedirs(os.path.dirname(self.db))
        self.sid = oc_store(self.db)

    def test_labels(self):
        out = self.prep(f"{self.db}#{self.sid}")
        n = self.counts()
        for label, want in (("BRIEF", 1), ("FINAL", 1), ("COMPACTION", 1), ("ERROR", 1), ("WAIT", 1), ("DENIAL", 1),
                            ("SPAWN", 1), ("WRITE", 1), ("AFTER-ERROR", 1)):
            self.assertEqual(n.get(label), want, f"{label}: {n}")
        self.assertIn("task explore: scan", self.labelled("SPAWN")[0])
        self.assertIn("./w.txt", self.labelled("WRITE")[0])
        self.assertIn("NOTICE n/a (OpenCode has no task-notification record)", "\n".join(self.digest()))

    def test_compaction_summary_is_labelled(self):
        """OpenCode's compaction summary is an assistant message with `summary: true`: COMPACT-SUMMARY, not FINAL."""
        con = sqlite3.connect(self.db)
        t = int((BASE + timedelta(seconds=20)).timestamp() * 1000)
        con.execute("INSERT INTO message VALUES (?,?,?,?)", ("m04", self.sid, t, json.dumps(
            dict(role="assistant", summary=True, mode="compaction", providerID="openai", modelID="gpt-5.5",
                 finish="stop", tokens={"input": 10, "output": 5, "reasoning": 0, "cache": {"read": 0, "write": 0}},
                 time={"created": t, "completed": t}))))
        con.execute("INSERT INTO part VALUES (?,?,?,?,?)", ("p08", "m04", self.sid, t,
                                                         json.dumps(dict(type="text", text="Summary of the work."))))
        con.commit()
        con.close()
        self.prep(f"{self.db}#{self.sid}")
        n = self.counts()
        self.assertEqual((n.get("COMPACT-SUMMARY"), n.get("FINAL")), (1, 1), n)
        at = int(next(r[0] for r in self.mech() if r[1] == "COMPACT-SUMMARY")[1:])
        self.assertIn("Summary of the work.", "\n".join(self.digest()[at - 1:at + 1]))

    def add_rows(self, messages=(), parts=()):
        con = sqlite3.connect(self.db)
        t = int((BASE + timedelta(seconds=20)).timestamp() * 1000)
        for mid, data in messages:
            con.execute("INSERT INTO message VALUES (?,?,?,?)", (mid, self.sid, t, json.dumps(data)))
        for pid, mid, data in parts:
            con.execute("INSERT INTO part VALUES (?,?,?,?,?)", (pid, mid, self.sid, t, json.dumps(data)))
        con.commit()
        con.close()

    def test_parts_pfm_cannot_read_are_skipped(self):
        """As pfm transcript show: a message without a role hides its parts; a tool error that is no string is unparseable."""
        tool = dict(type="tool", tool="bash", state={"status": "completed", "input": {"command": "zz-unread"},
                                                     "output": "x", "time": {"start": 1, "end": 2}})
        bad = dict(type="tool", tool="bash", state={"status": "error", "input": {"command": "zz-bad"},
                                                    "error": {"code": 1}, "time": {"start": 1, "end": 2}})
        self.add_rows([("m05", {"time": {"created": 1}})], [("p09", "m05", tool), ("p10", "m03", bad)])
        self.prep(f"{self.db}#{self.sid}")
        dig = "\n".join(self.digest())
        self.assertNotIn("zz-unread", dig)
        self.assertNotIn("zz-bad", dig)
        self.assertEqual(self.counts().get("ERROR"), 1)

    def test_store_path_with_uri_characters(self):
        odd = self.path("store?mode=rw%41")
        os.makedirs(odd)
        shutil.copy(self.db, os.path.join(odd, "opencode.db"))
        self.assertIn("DIGEST", self.prep(f"{odd}/opencode.db#{self.sid}"))
        self.assertFalse(os.path.exists(self.path("store")))

    def test_timeline_priced_from_message_tokens(self):
        self.prep(f"{self.db}#{self.sid}")
        with open(os.path.join(self.dir, "timeline.txt")) as f:
            tl = f.read().splitlines()
        self.assertIn("· 2 calls ·", tl[0])

        def usd(inp, out, read):
            tot = inp + read
            return ((tot - read) * 5 + read * 0.5 + out * 30) / 1e6
        want = usd(1000, 60, 200) + usd(500, 20, 1500)
        self.assertIn(f"${want:.4f}", tl[0])

    def test_locate_by_id_title_and_locator(self):
        for target in (self.sid, "Invented store task", f"{self.db}#{self.sid}"):
            shutil.rmtree(self.path("run"), True)
            self.assertIn("DIGEST", self.prep(target), target)
        self.assertEqual(oc_engine.split_locator(f"{self.db}#{self.sid}"), (self.db, self.sid))


class Metrics(Fixture):
    def metrics(self, targets, gateway=None):
        manifest, out = self.path("manifest.json"), self.path("metrics.json")
        with open(manifest, "w") as f:
            json.dump(targets, f)
        args = ["metrics", "--manifest", manifest, "--prices", os.path.join(self.kit, "prices.json"), "--out", out]
        if gateway:
            args += ["--gateway", gateway]
        self.kitcmd(*args)
        with open(out) as f:
            return json.load(f)

    def test_opencode_locator_metrics(self):
        db = self.path("opencode.db")
        sid = oc_store(db)
        with sqlite3.connect(db) as con:
            for mid, session, model, tokens in (
                ("m04", sid, "claude-sonnet-4-6",
                 {"input": 100, "output": 20, "reasoning": 5, "cache": {"read": 300, "write": 400}}),
                ("child-message", "ses_child01", "gpt-5.5",
                 {"input": 900000, "output": 800000, "cache": {"read": 700000}}),
            ):
                con.execute("INSERT INTO message VALUES (?,?,?,?)", (mid, session, 1767261620000, json.dumps(
                    dict(role="assistant", providerID="anthropic" if model.startswith("claude") else "openai",
                         modelID=model, tokens=tokens))))
            con.execute("INSERT INTO part VALUES (?,?,?,?,?)", ("child-tool", "child-message", "ses_child01",
                        1767261620000, json.dumps(dict(type="tool", tool="bash"))))
        target = dict(id="manifest-label", path=f"{db}#{sid}")
        row, = self.metrics([target])
        self.assertNotIn("failed", row, row)
        self.assertEqual((row["id"], row["path"], row["engine"]), (target["id"], target["path"], "opencode"))
        self.assertEqual(row["model_requests_with_usage"], 3)
        self.assertEqual(row["tools"], {"bash": 1, "task": 1, "write": 1})
        self.assertEqual((row["tool_calls"], row["rendered_operations"]), (3, 3))
        self.assertEqual(row["tool_call_lines"], [5, 6, 8])
        self.assertEqual(row["batch_histogram_including_zero_tool_responses"], {"3": 1, "0": 2})
        self.assertEqual(row["tokens"], dict(input_tokens=4000, cached_input_tokens=2000,
                         cache_write_input_tokens=400, output_tokens=105, reasoning_tokens=15,
                         uncached_input_tokens=2000, cache_hit_fraction=2000 / 4000))
        self.assertEqual([u["response_id"] for u in row["per_request_usage"]], ["m02", "m03", "m04"])
        self.assertEqual(row["first_request"]["line"], 4)
        self.assertEqual(row["last_request"]["line"], 11)
        self.assertAlmostEqual(row["price_components_usd"]["uncached_input"], 0.0078)
        self.assertAlmostEqual(row["price_components_usd"]["cached_input"], 0.00094)
        self.assertAlmostEqual(row["price_components_usd"]["cache_write_input"], 0.0015)
        self.assertAlmostEqual(row["price_components_usd"]["output"], 0.002775)
        self.assertAlmostEqual(row["api_equivalent_usd"], 0.013015)
        self.assertTrue(any("5-minute" in gap for gap in row["gaps"]), row["gaps"])
        self.assertFalse(any("gateway" in gap for gap in row["gaps"]), row["gaps"])

    def test_metrics_preserve_claude_and_codex_accounting(self):
        claude, codex, gateway = self.path("claude.jsonl"), self.path("codex.jsonl"), self.path("gateway.db")
        usage = dict(input_tokens=1200, cached_input_tokens=200, output_tokens=50, reasoning_output_tokens=10)
        record = x_rec("token_usage_record", dict(response_id="response-one", usage=usage), 17)
        for path, records in ((claude, claude_records()), (codex, codex_records() + [record, record])):
            with open(path, "w") as f:
                f.write("\n".join(json.dumps(r) for r in records) + "\n")
        with sqlite3.connect(gateway) as con:
            con.execute("CREATE TABLE requests (id INTEGER, agent_id TEXT, started REAL, ended REAL, status TEXT, "
                        "attempts INTEGER, error_head TEXT, model TEXT, note TEXT, input_tokens INTEGER, "
                        "output_tokens INTEGER, cache_read_tokens INTEGER, reasoning_tokens INTEGER, request_id TEXT)")
            con.execute("INSERT INTO requests VALUES (1, 'claude-agent', 1, 2, '200', 1, NULL, "
                        "'claude-sonnet-4-6', NULL, 1200, 50, 200, 10, 'request-one')")
        c, x = self.metrics([dict(id="claude-agent", path=claude), dict(id="codex-agent", path=codex)], gateway)
        for row, engine, tools, usd in ((c, "claude", 6, 0.00381), (x, "codex", 4, 0.0066)):
            self.assertNotIn("failed", row, row)
            self.assertEqual((row["engine"], row["model_requests_with_usage"], row["tool_calls"]), (engine, 1, tools))
            self.assertEqual(row["tokens"], dict(input_tokens=1200, cached_input_tokens=200, output_tokens=50,
                             reasoning_tokens=10, uncached_input_tokens=1000, cache_hit_fraction=200 / 1200))
            self.assertAlmostEqual(row["api_equivalent_usd"], usd)
            self.assertEqual(row["gaps"], [])
        self.assertEqual(c["gateway_rows"], 1)
        self.assertEqual(x["last_request"]["response_id"], "response-one")
        self.assertEqual(x["batch_histogram_including_zero_tool_responses"], {"4": 1})

    def test_opencode_metrics_report_missing_usage_prices_and_sessions(self):
        db = self.path("opencode.db")
        sid = oc_store(db)
        with sqlite3.connect(db) as con:
            con.execute("UPDATE message SET data=? WHERE id='m02'", (json.dumps(dict(role="assistant")),))
            con.execute("UPDATE message SET data=? WHERE id='m03'", (json.dumps(dict(role="assistant",
                        modelID="unlisted-model", tokens={"input": 7, "output": 3})),))
        row, absent = self.metrics([dict(id=sid, path=f"{db}#{sid}"), dict(id="absent", path=f"{db}#ses_absent")])
        self.assertNotIn("failed", row, row)
        self.assertEqual(row["model_requests_with_usage"], 1)
        self.assertEqual(row["tokens"]["input_tokens"], 7)
        self.assertIsNone(row["api_equivalent_usd"])
        self.assertTrue(any("missing token usage" in gap for gap in row["gaps"]), row["gaps"])
        self.assertTrue(any("unlisted-model" in gap for gap in row["gaps"]), row["gaps"])
        self.assertIn("no such opencode session ses_absent", absent["failed"])

    def test_opencode_metrics_resolve_prices_as_the_timeline_does(self):
        db = self.path("opencode.db")
        sid = oc_store(db)
        with sqlite3.connect(db) as con:
            con.execute("UPDATE message SET data=? WHERE id='m03'", (json.dumps(dict(role="assistant",
                        providerID="openai", modelID="claude-sonnet-4-6", tokens={"input": 7, "output": 3})),))
        row, = self.metrics([dict(id=sid, path=f"{db}#{sid}")])
        self.assertNotIn("failed", row, row)
        self.assertIsNone(row["api_equivalent_usd"])
        self.assertTrue(any("openai/claude-sonnet-4-6" in gap for gap in row["gaps"]), row["gaps"])

    def test_opencode_metrics_see_a_write_to_the_store_log(self):
        from chatlogkit import metrics as kit_metrics
        db = self.path("opencode.db")
        sid = oc_store(db)
        real = oc_engine.session_rows

        def writer_lands(*loc):
            with open(db + "-wal", "ab") as f:
                f.write(b"row")
            return real(*loc)
        with open(os.path.join(self.kit, "prices.json")) as f:
            book = json.load(f)
        with mock.patch.object(kit_metrics.opencode, "session_rows", writer_lands):
            row = kit_metrics.measure(dict(id=sid, path=f"{db}#{sid}"), None, book)
        self.assertTrue(any("Source changed" in gap for gap in row["gaps"]), row["gaps"])


class DiscoveryEvidence(Fixture):
    def test_evidence_id_discovery_failure_cannot_certify_a_match_or_absence(self):
        for hits in ([self.path("match.jsonl")], [], [self.path("a.jsonl"), self.path("b.jsonl")]):
            with self.subTest(hits=hits), mock.patch.object(locate, "id_hits", return_value=(hits, ["list fixture-root: denied"])), \
                    mock.patch.object(locate, "claude_roots", return_value=[]), \
                    mock.patch.object(locate, "codex_homes", return_value=[]), \
                    mock.patch.object(locate, "opencode_roots", return_value=[]):
                with self.assertRaisesRegex(locate.NotFound, "INCOMPLETE.*fixture-root: denied"):
                    locate.resolve("abc123")

    def test_evidence_agent_path_discovery_failure_cannot_certify_uniqueness(self):
        with mock.patch.object(locate, "codex_homes", return_value=[self.tmp]), \
                mock.patch.object(locate, "walk_rollouts", return_value=(["rollout-match.jsonl"], ["walk denied"])), \
                mock.patch.object(locate, "rollout_agent_path", return_value="/root/worker"):
            with self.assertRaisesRegex(locate.NotFound, "INCOMPLETE.*walk denied"):
                locate.resolve("/root/worker")
        with mock.patch.object(locate, "codex_homes", return_value=[self.tmp]), \
                mock.patch.object(locate, "walk_rollouts", return_value=(["match", "unreadable"], [])), \
                mock.patch.object(locate, "rollout_agent_path", side_effect=["/root/worker", None]):
            with self.assertRaisesRegex(locate.NotFound, "INCOMPLETE.*unreadable"):
                locate.resolve("/root/worker")

    def test_evidence_subagent_walk_errors_reach_id_discovery(self):
        root = self.path("projects")
        folder = os.path.join(root, "p", "session", "subagents")
        os.makedirs(folder)

        def failed_walk(path, onerror):
            onerror(PermissionError(13, "fixture denied", path))
            return iter(())

        with mock.patch.object(locate, "claude_roots", return_value=[root]), \
                mock.patch.object(locate, "codex_homes", return_value=[]), \
                mock.patch.object(locate.os, "walk", side_effect=failed_walk):
            hits, failed = locate.id_hits("abc123")
        self.assertEqual(hits, [])
        self.assertTrue(any("fixture denied" in e and folder in e for e in failed), failed)


# -------------------------------------------------------------------------------- the reader's verbs

class Verbs(Fixture):
    def setUp(self):
        super().setUp()
        path = self.path("s1.jsonl")
        with open(path, "w") as f:
            for rec in claude_records():
                f.write(json.dumps(rec) + "\n")
        self.prep(path)

    def test_read_find_note_report_verify(self):
        self.assertIn("D1 ", self.kitcmd("read", self.dir, "1-3").stdout)
        self.assertIn("permission denied", self.kitcmd("find", self.dir, "PERMISSION").stdout)
        dig = self.digest()
        brief = next(i for i, ln in enumerate(dig, 1) if "Audit the invented thing" in ln)
        got = self.kitcmd("note", self.dir, f"D{brief} CLAIM").stdout
        self.assertIn("noted 1 · refused 0", got)
        bad = self.kitcmd("note", self.dir, "D9999 CLAIM", check=False)
        self.assertIn("REFUSED", bad.stdout + bad.stderr)
        report = self.path("report.tsv")
        self.kitcmd("report", self.dir, "--out", report)
        self.assertTrue(os.path.isfile(report))
        ok = self.kitcmd("verify", report, "--print", check=False)
        self.assertRegex(ok.stdout, r"· BAD 0\n")
        with open(report) as f:
            text = f.read()
        tampered = self.path("tampered.tsv")
        with open(tampered, "w") as f:
            f.write(text.replace("Audit the invented thing", "Something nobody wrote"))
        res = self.kitcmd("verify", tampered, check=False)
        self.assertNotIn("BAD 0", res.stdout)
        self.assertRegex(res.stdout, r"· BAD [1-9]")

    def test_evidence_report_requires_readable_timeline_and_retains_output(self):
        timeline = os.path.join(self.dir, "timeline.txt")
        with open(timeline, "rb") as f:
            original = f.read()
        for broken in ("missing", "directory", "empty", "truncated"):
            for prior in (False, True):
                with self.subTest(broken=broken, prior=prior):
                    out = self.path(f"{broken}-{prior}.tsv")
                    if prior:
                        with open(out, "w") as f:
                            f.write("previous valid report\n")
                    os.remove(timeline)
                    if broken == "directory":
                        os.mkdir(timeline)
                    elif broken != "missing":
                        with open(timeline, "w") as f:
                            f.write("" if broken == "empty" else "TIMELINE truncated\n")
                    try:
                        done = self.kitcmd("report", self.dir, "--out", out, check=False)
                        self.assertNotEqual(done.returncode, 0, done.stdout)
                        self.assertIn("KIT FAILED", done.stdout)
                        self.assertIn("timeline", done.stdout.lower())
                        self.assertNotIn("READ ", done.stdout)
                        if prior:
                            with open(out) as f:
                                self.assertEqual(f.read(), "previous valid report\n")
                        else:
                            self.assertFalse(os.path.exists(out))
                    finally:
                        if os.path.isdir(timeline):
                            os.rmdir(timeline)
                        with open(timeline, "wb") as f:
                            f.write(original)

    def test_missing_digest_is_failed(self):
        os.remove(os.path.join(self.dir, "digest.txt"))
        res = self.kitcmd("read", self.dir, "1", check=False)
        self.assertIn("FAILED", res.stdout + res.stderr)
        self.assertNotEqual(res.returncode, 0)

    def test_prompts_and_writes(self):
        self.assertIn("Now finish up.", self.kitcmd("prompts", self.dir).stdout)
        w = self.kitcmd("writes", self.dir).stdout
        self.assertIn("/fx/b.txt", w)
        self.assertIn("GONE", w)


class WritesProbe(Fixture):
    def test_git_probe_runs_no_repo_program(self):
        """`writes` asks git for each file's state; a repo's core.fsmonitor never runs, an inherited GIT_DIR never steers it."""
        repo, marker = self.path("repo"), self.path("ran")
        os.makedirs(repo)
        subprocess.run(["git", "init", "-q", repo], check=True)
        subprocess.run(["git", "-C", repo, "config", "core.fsmonitor", f"touch '{marker}'; false"], check=True)
        with open(os.path.join(repo, "b.txt"), "w") as f:
            f.write("x")
        recs = claude_records()
        recs[7]["message"]["content"][0]["input"]["file_path"] = os.path.join(repo, "b.txt")
        path = self.path("s1.jsonl")
        with open(path, "w") as f:
            f.write("\n".join(json.dumps(r) for r in recs) + "\n")
        self.prep(path)
        w = self.kitcmd("writes", self.dir, env={"GIT_DIR": self.path("nowhere")}).stdout
        self.assertIn("git ??", w)
        self.assertFalse(os.path.exists(marker))


class Stamps(unittest.TestCase):
    def test_parse_stamp_reads_as_pfm_reads(self):
        """pfm's ParseTimestamp: a day past the month's end rolls over, 24:00 is the next midnight, a number is no stamp."""
        self.assertEqual(parse_stamp("2026-02-30T10:00:00Z"), datetime(2026, 3, 2, 10, tzinfo=timezone.utc))
        self.assertEqual(parse_stamp("2026-01-01T24:00:00Z"), datetime(2026, 1, 2, tzinfo=timezone.utc))
        self.assertIsNone(parse_stamp("2026-01-01T24:00:01Z"))
        self.assertIsNone(parse_stamp("2026-13-01T10:00:00Z"))
        self.assertIsNone(parse_stamp(1767261600000))


class Entry(Fixture):
    def test_entry_runs_under_isolated_mode(self):
        done = self.kitcmd("nonsense", check=False)
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("prep TARGET", done.stdout + done.stderr)


class CleanLine(unittest.TestCase):
    def test_notification_collapses_but_a_quoting_prompt_keeps_its_text(self):
        from chatlogkit.digest import clean
        note = "L5 04:56:27 NOTE <task-notification><status>done</status><summary>s</summary></task-notification>"
        self.assertEqual(clean(note), "L5 04:56:27 NOTE task-notification · done · s")
        facts = "L6 04:56:39 PROMPT [forward-compaction: facts] Compacted. <task-notification><summary>x</summary>"
        self.assertTrue(clean(facts).startswith("L6 04:56:39 PROMPT [forward-compaction: facts]"))


class CodeModeWrites(unittest.TestCase):
    def test_exec_wrapper_cmd_literal_is_parsed(self):
        from types import SimpleNamespace
        from chatlogkit.writes import event_writes
        ev = SimpleNamespace(writes=None, cmd=False, name="exec",
                             text='const r = await tools.exec_command({cmd:"python x.py > /work/a.txt\\necho hi >> /work/b.log", yield_time_ms: 1000});')
        self.assertEqual(event_writes(ev), ["/work/a.txt", "/work/b.log"])


if __name__ == "__main__":
    unittest.main()
