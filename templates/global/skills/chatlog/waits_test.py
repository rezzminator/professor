"""Tests for the chatlog kit's `waits` verb — run through `.claude/scripts/test.sh`.

The fixture is an invented Claude session: a main chat spawns a reviewer, the reviewer spawns a tracer and waits
400 s for it, then re-writes its cache; every number below is worked by hand from the fixture's stamps, usage
blocks and chatlog_test's price table. Read through the entry point (`python3 -I chatlog.py waits ...`) as an
agent does; only chatlog_test's helpers are imported, never its test classes, so no test runs twice."""
import json
import os
import sys
import unittest

HERE = os.path.dirname(os.path.realpath(__file__))
sys.path.insert(0, HERE)

from chatlog_test import Fixture, c_call, c_final, c_result, c_user, x_rec  # noqa: E402

COLS = ("transcript_path host parent_type reviewer_wall_s tracer_runs tracer_wall_s reviewer_longest_idle_s "
        "reviewer_idle_total_s parent_idle_s cache_rewrites_after_wait rewrite_tokens rewrite_usd usd_reviewer "
        "usd_tracer_tree usd_parent_during_wait findings_count parent_idle_total_s parent_rewrites "
        "parent_rewrite_tokens parent_rewrite_usd waits_over_gap_no_spike w5m_tokens w1h_tokens parent_w5m_tokens "
        "parent_w1h_tokens children_any concurrent_siblings reviewer_dormant_s rewrites_child_wait "
        "rewrite_usd_child_wait agent_type start_utc parent_path review_path").split()
ALL = "9999d"


def reply(text, t, tag, usage, model=None):
    """A final text reply with its own ids and usage (input, output, cache read, cache write)."""
    rec = c_final(text, t)
    rec["requestId"], rec["message"]["id"] = "rf" + tag, "mf" + tag
    i, o, cr, cw = usage
    rec["message"]["usage"] = {"input_tokens": i, "output_tokens": o, "cache_read_input_tokens": cr,
                               "cache_creation_input_tokens": cw}
    if model:
        rec["message"]["model"] = model
    return rec


class World(Fixture):
    def jsonl(self, path, records, tail=""):
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            for rec in records:
                f.write(json.dumps(rec) + "\n")
            f.write(tail)
        return path

    def sub(self, aid):
        return self.path("projects", "p", "sess0001", "subagents", f"agent-{aid}.jsonl")

    def meta(self, aid, meta):
        with open(self.sub(aid)[:-len(".jsonl")] + ".meta.json", "w") as f:
            f.write(meta if isinstance(meta, str) else json.dumps(meta))

    def session(self, child="tracer", model=None, tail=""):
        """Main: spawns the reviewer at 50 s, its result at 600 s, a reply at 700 s that re-writes 30,000 cached
        tokens. Reviewer (100-504 s): spawns a `child` at 101 s (writing 20,000), the child returns at 500 s,
        a Read at 502 s re-writes all 20,000, a final at 504 s reads them from the cache and names a review file
        holding two distinct findings. Tracer (110-490 s): one reply. child=None: the reviewer's first call is
        a Bash, no child is ever out."""
        review = self.path("review.md")
        with open(review, "w") as f:
            f.write("# review\nF1 one\n- F2 two\nF1 again\n")
        self.jsonl(self.path("projects", "p", "sess0001.jsonl"), [
            c_user("Review it.", 0),
            c_call("toolu_rev", "Agent", {"subagent_type": "reviewer"}, 50, usage=(100, 10, 0, 30000)),
            c_result("toolu_rev", "reviewed", 600), reply("Done.", 700, "root", (10, 10, 0, 30000))])
        first = c_call("toolu_tr", "Agent", {"subagent_type": child}, 101, usage=(10, 10, 0, 20000)) if child \
            else c_call("toolu_tr", "Bash", {"command": "sleep 399"}, 101, usage=(10, 10, 0, 20000))
        if model:
            first["message"]["model"] = model
        self.jsonl(self.sub("rev1"), [
            c_user("Review.", 100), first, c_result("toolu_tr", "traced", 500),
            c_call("r2", "Read", {"file_path": "/fx/a"}, 502, usage=(10, 10, 0, 20000)), c_result("r2", "x", 503),
            reply("Review at " + review, 504, "rev", (10, 5, 20010, 0))], tail)
        self.meta("rev1", {"agentType": "reviewer", "toolUseId": "toolu_rev"})
        if child:
            self.jsonl(self.sub("tr1"), [c_user("Trace.", 110), reply("traced", 490, "tr", (100, 20, 0, 0))])
            self.meta("tr1", {"agentType": child, "toolUseId": "toolu_tr"})
        return review

    def waits(self, *args, check=True):
        return self.kitcmd("waits", *args, check=check)

    def rows(self, out):
        """[row dict] of a waits stdout; the header must be the contract's."""
        got = [ln.split("\t") for ln in out.splitlines() if "\t" in ln]
        self.assertEqual(got[0], COLS, out)
        return [dict(zip(COLS, r)) for r in got[1:]]

    def pick(self, row, *cols):
        return {c: row[c] for c in cols}


class WaitsSweep(World):
    def test_child_span_idle_and_rewrite_after_the_wait(self):
        review = self.session()
        out = self.waits("--since", ALL).stdout
        [row] = self.rows(out)
        # reviewer wall 504-100; tracer out 101-500 (its result, after its own last stamp 490), and the reviewer
        # idle all of that span (response 101 → next send 500); the main chat idle from the spawn (50) to the
        # reviewer's end (504). Rewrite: 20,000 tokens at 3.75 instead of 0.3 per million = $0.0690; the parent's
        # 30,000 = $0.1035. Reviewer: 2 × (10·3 + 10·15 + 20,000·3.75) + (10·3 + 5·15 + 20,010·0.3) µ$ = $0.1565;
        # tracer 100·3 + 20·15 µ$ = $0.0006
        self.assertEqual(self.pick(row, *COLS[2:22], "children_any", "concurrent_siblings", "reviewer_dormant_s",
                                   "rewrites_child_wait", "rewrite_usd_child_wait", "agent_type", "start_utc",
                                   "review_path"), dict(
            parent_type="main", reviewer_wall_s="404", tracer_runs="1", tracer_wall_s="399",
            reviewer_longest_idle_s="399", reviewer_idle_total_s="399", parent_idle_s="454",
            cache_rewrites_after_wait="1", rewrite_tokens="20000", rewrite_usd="0.0690", usd_reviewer="0.1565",
            usd_tracer_tree="0.0006", usd_parent_during_wait="0.0000", findings_count="2", parent_idle_total_s="454",
            parent_rewrites="1", parent_rewrite_tokens="30000", parent_rewrite_usd="0.1035",
            waits_over_gap_no_spike="0", w5m_tokens="40000", children_any="1", concurrent_siblings="0",
            reviewer_dormant_s="0", rewrites_child_wait="1", rewrite_usd_child_wait="0.0690", agent_type="reviewer",
            start_utc="2026-01-01T10:01:40Z", review_path=review))
        self.assertEqual(row["transcript_path"], self.sub("rev1"))
        self.assertEqual(row["parent_path"], self.path("projects", "p", "sess0001.jsonl"))
        self.assertIn("RUNS 1 · ", out)
        self.assertIn("LOST reviewer rewrite excess $0.0690 · parent rewrite excess (each parent call once) $0.1035 "
                      "over 1 calls, 30000 tokens", out)
        self.assertIn("IDLE reviewer idle while a child was out 399 s of 404 s wall", out)
        self.assertNotIn("FAILED /", out)
        self.assertNotIn("GAPS", out)
        self.assertTrue(out.rstrip().splitlines()[-1].startswith("# waits 1 runs · 0 FAILED"), out)

    def test_the_gap_threshold_splits_rewrites_from_held_waits(self):
        self.session()
        # --gap 2: the 400 s wait re-wrote (spiked), the 3 s one read its cache back (held)
        [row] = self.rows(self.waits("--since", ALL, "--gap", "2").stdout)
        self.assertEqual(self.pick(row, "cache_rewrites_after_wait", "waits_over_gap_no_spike", "parent_rewrites"),
                         dict(cache_rewrites_after_wait="1", waits_over_gap_no_spike="1", parent_rewrites="1"))
        # --gap 500: no reviewer gap passes it; the parent's 600 s one still does
        [row] = self.rows(self.waits("--since", ALL, "--gap", "500").stdout)
        self.assertEqual(self.pick(row, "cache_rewrites_after_wait", "rewrite_tokens", "rewrite_usd",
                                   "waits_over_gap_no_spike", "parent_rewrites", "parent_rewrite_tokens"),
                         dict(cache_rewrites_after_wait="0", rewrite_tokens="0", rewrite_usd="0.0000",
                              waits_over_gap_no_spike="0", parent_rewrites="1", parent_rewrite_tokens="30000"))

    def test_a_wait_with_no_child_out_is_dormant_not_child_idle(self):
        self.session(child=None)
        [row] = self.rows(self.waits("--since", ALL).stdout)
        self.assertEqual(self.pick(row, "tracer_runs", "children_any", "reviewer_idle_total_s", "reviewer_dormant_s",
                                   "cache_rewrites_after_wait", "rewrites_child_wait", "rewrite_usd_child_wait"),
                         dict(tracer_runs="0", children_any="0", reviewer_idle_total_s="0", reviewer_dormant_s="399",
                              cache_rewrites_after_wait="1", rewrites_child_wait="0", rewrite_usd_child_wait="0.0000"))

    def test_a_child_outside_the_child_pattern_is_out_but_no_tracer(self):
        self.session(child="explorer")
        [row] = self.rows(self.waits("--since", ALL).stdout)
        self.assertEqual(self.pick(row, "tracer_runs", "tracer_wall_s", "usd_tracer_tree", "children_any",
                                   "reviewer_idle_total_s"),
                         dict(tracer_runs="0", tracer_wall_s="0", usd_tracer_tree="0.0000", children_any="1",
                              reviewer_idle_total_s="399"))

    def test_a_run_outside_the_window_is_not_counted(self):
        self.session()
        out = self.waits("--since", "1d").stdout
        self.assertEqual(self.rows(out), [])
        self.assertIn("RUNS 0 · ", out)

    def test_unpriced_calls_are_named_not_zero(self):
        self.session(model="claude-invented-9")
        out = self.waits("--since", ALL).stdout
        self.assertIn("UNPRICED 1 calls (reviewer or parent) carry no rate: their dollars count as $0", out)


class WaitsFailures(World):
    """Every input waits could not read prints a FAILED or GAPS line naming it: a broken input never renders as a
    clean row with no waits."""

    def test_malformed_records_are_a_named_gap(self):
        self.session(tail="this line is not json\n")
        out = self.waits("--since", ALL).stdout
        self.assertEqual(len(self.rows(out)), 1, out)
        self.assertIn(f"GAPS {self.sub('rev1')} — 1 malformed records skipped", out)

    def test_an_unreadable_meta_fails_the_sweep(self):
        self.session()
        self.meta("rev1", "{not json")
        done = self.waits("--since", ALL, check=False)
        self.assertEqual(done.returncode, 1, done.stdout)
        self.assertIn("FAILED " + self.sub("rev1")[:-len(".jsonl")] + ".meta.json — ", done.stdout)
        self.assertIn("# waits 0 runs · 1 FAILED", done.stdout)

    def test_an_unreadable_sibling_meta_is_named_once(self):
        self.session()
        self.jsonl(self.sub("zz9"), [c_user("Other.", 120)])
        self.meta("zz9", "{not json")
        done = self.waits("--since", ALL, check=False)
        self.assertEqual(done.returncode, 1, done.stdout)
        self.assertEqual(len(self.rows(done.stdout)), 1, done.stdout)
        self.assertEqual(done.stdout.count("FAILED " + self.sub("zz9")[:-len(".jsonl")] + ".meta.json — "), 1,
                         done.stdout)

    def test_a_missing_reviewer_transcript_is_named_missing(self):
        self.session()
        os.unlink(self.sub("rev1"))
        done = self.waits("--since", ALL, check=False)
        self.assertEqual(done.returncode, 1, done.stdout)
        self.assertIn(f"FAILED {self.sub('rev1')} — no transcript beside its .meta.json", done.stdout)
        self.assertEqual(done.stdout.count("FAILED /"), 1, done.stdout)

    def test_a_missing_child_transcript_fails_the_row_it_undercounts(self):
        self.session()
        os.unlink(self.sub("tr1"))
        done = self.waits("--since", ALL, check=False)
        self.assertEqual(done.returncode, 1, done.stdout)
        self.assertIn(f"FAILED {self.sub('tr1')} — no transcript beside its .meta.json", done.stdout)

    def test_a_missing_root_fails(self):
        done = self.waits("--since", ALL, "--root", self.path("nowhere"), check=False)
        self.assertEqual(done.returncode, 1, done.stdout)
        self.assertIn(f"FAILED {self.path('nowhere')} — ", done.stdout)
        self.assertIn("RUNS 0 · ", done.stdout)

    def test_a_bad_gap_or_a_stray_argument_is_refused(self):
        for args in (("--gap", "abc"), ("stray",), ("--calls", "x", "stray")):
            done = self.waits(*args, check=False)
            self.assertNotEqual(done.returncode, 0, args)
            self.assertNotIn("RUNS", done.stdout, args)


class WaitsCalls(World):
    def test_calls_table_per_model_call(self):
        self.session()
        out = self.waits("--calls", self.sub("rev1")).stdout.splitlines()
        self.assertEqual(out[0].split("\t")[:3], ["n", "response_utc", "sent_utc"])
        got = [ln.split("\t") for ln in out[1:-1]]
        # n, gap_s, prev_ctx, lost, spiked, lost_usd
        self.assertEqual([(r[0], r[3], r[9], r[10], r[11], r[13]) for r in got],
                         [("1", "0", "0", "0", "-", "0.0000"), ("2", "400", "20010", "20000", "Y", "0.0690"),
                          ("3", "3", "20010", "0", "-", "0.0000")])
        self.assertEqual(out[-1], f"# 3 calls · 0 unpriced · 0 malformed records · {self.sub('rev1')}")

    def test_calls_counts_malformed_records(self):
        self.session(tail="this line is not json\n")
        out = self.waits("--calls", self.sub("rev1")).stdout
        self.assertIn("# 3 calls · 0 unpriced · 1 malformed records", out)

    def test_calls_refuses_what_it_cannot_read(self):
        empty = self.jsonl(self.path("empty.jsonl"), [])
        rollout = self.jsonl(self.path("codex", "rollout.jsonl"),
                             [x_rec("session_meta", {"id": "t1", "cwd": "/fx"}, 0)])
        for target, why in ((self.path("missing.jsonl"), "no such transcript file"), (empty, "empty transcript"),
                            (rollout, "a codex transcript; waits reads Claude transcripts only")):
            done = self.waits("--calls", target, check=False)
            self.assertEqual(done.returncode, 1, done.stdout)
            self.assertTrue(done.stdout.startswith(f"FAILED {target} — "), done.stdout)
            self.assertIn(why, done.stdout)
            self.assertNotIn("calls ·", done.stdout)


if __name__ == "__main__":
    unittest.main()
