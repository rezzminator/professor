"""Tests for the chatlog reader kit — run through `.claude/scripts/test-templates.sh`.

Every transcript here is invented: a Claude jsonl, a Codex rollout and an OpenCode store the tests build in a
temp directory, then read through the entry point (`python3 -I chatlog.py {verb}`) exactly as an agent does.
Prices come from a fixture table seeded in the kit directory, so no test calls pfm."""
import json
import os
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from datetime import datetime, timedelta, timezone

HERE = os.path.dirname(os.path.realpath(__file__))
ENTRY = os.path.join(HERE, "chatlog.py")
sys.path.insert(0, HERE)

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


class ClaudeKit(Fixture):
    def write_claude(self, records=None, name="s1.jsonl"):
        path = self.path("projects", "p", name)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            for rec in records or claude_records():
                f.write(json.dumps(rec) + "\n")
            f.write("this line is not json\n")
        return path

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


if __name__ == "__main__":
    unittest.main()
