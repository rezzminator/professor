import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.realpath(__file__))
# TRANSCRIPT_SCRIPT lets the watched-failure proof point this whole suite at a mutated
# copy of the script without ever touching transcript.py itself.
SCRIPT = os.environ.get("TRANSCRIPT_SCRIPT") or os.path.join(HERE, "transcript.py")

CLAUDE_SID = "11111111-2222-3333-4444-555555555555"
CLAUDE_AGENT = "a0123456789abcdef"
CODEX_SID = "01a0eee7-4d90-7e82-8340-2904c468c83f"


def ts(second):
    return f"2026-09-29T20:{second // 60:02d}:{second % 60:02d}.000Z"


def claude_records():
    base = {"sessionId": CLAUDE_SID, "agentId": CLAUDE_AGENT, "isSidechain": True, "cwd": "/work/repo"}

    def rec(kind, second, **fields):
        return dict(base, type=kind, timestamp=ts(second), **fields)

    return [
        rec("user", 0, message={"role": "user", "content": "Revise task 3-d after FAILED round 1."}),
        rec("attachment", 0, attachment={"type": "total_tokens_reminder", "text": "left"}),
        rec("assistant", 1, message={"model": "claude-opus-5-5", "content": [{"type": "thinking", "thinking": "hmm"}]}),
        rec("assistant", 2, message={"model": "claude-opus-5-5", "content": [
            {"type": "tool_use", "id": "t1", "name": "Bash", "input": {"command": "go test ./pkg/..."}}]}),
        rec("user", 30, message={"content": [{"type": "tool_result", "tool_use_id": "t1", "is_error": True,
                                              "content": "Exit code 1\n--- FAIL: TestFork\nfork reuses parent sid\nFAIL pkg"}]}),
        rec("assistant", 31, message={"content": [
            {"type": "tool_use", "id": "t2", "name": "Read", "input": {"file_path": "/work/repo/pkg/fork.go"}}]}),
        rec("user", 32, message={"content": [{"type": "tool_result", "tool_use_id": "t2", "content": "package pkg\n" * 50}]}),
        rec("attachment", 33, attachment={"type": "queued_command", "prompt": "Two more items from the orchestrator."}),
        rec("user", 34, isMeta=True, message={"content": "[SYSTEM NOTIFICATION - NOT USER INPUT] task done"}),
        rec("weird_future_type", 35),
        rec("assistant", 36, message={"content": [
            {"type": "tool_use", "id": "t3", "name": "Grep", "input": {"pattern": "fork", "path": "pkg"}}]}),
        rec("assistant", 40, message={"content": [{"type": "text", "text": "SPEC revised: 3-d cut smaller."}]}),
    ]


def codex_records():
    def rec(kind, second, payload):
        return {"timestamp": ts(second), "type": kind, "payload": payload}

    def done(second, item):
        return rec("event_msg", second, {"type": "item_completed", "item": item})

    return [
        rec("session_meta", 0, {"id": CODEX_SID, "cwd": "/work/repo", "cli_version": "0.159.0"}),
        rec("turn_context", 0, {"model": "gpt-6-sol", "collaboration_mode": {"settings": {"reasoning_effort": "high"}}}),
        rec("response_item", 0, {"type": "message", "role": "developer", "content": [{"type": "input_text", "text": "rules"}]}),
        rec("response_item", 0, {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "# AGENTS.md instructions for /work/repo"}]}),
        rec("response_item", 1, {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "# Brief — task 3-c round 4"}]}),
        done(1, {"type": "UserMessage", "content": [{"type": "text", "text": "# Brief — task 3-c round 4"}]}),
        rec("response_item", 2, {"type": "reasoning", "summary": [], "encrypted_content": "gAAA"}),
        rec("response_item", 3, {"type": "message", "role": "assistant", "phase": "commentary", "content": [{"type": "output_text", "text": "Reading the task first."}]}),
        rec("response_item", 4, {"type": "custom_tool_call", "call_id": "c1", "name": "exec", "input": "const r = await tools.exec_command({cmd: 'cat task.md'})"}),
        done(5, {"type": "CommandExecution", "command": ["/bin/zsh", "-lc", "cat /work/repo/task.md"],
                 "parsed_cmd": [{"type": "read"}], "aggregated_output": "task text\n", "exit_code": 0, "status": "completed"}),
        done(6, {"type": "CommandExecution", "command": ["/bin/zsh", "-lc", "infra/run.sh --lanes E1"],
                 "parsed_cmd": [{"type": "unknown"}], "aggregated_output": "E1 ok one\nE1 FAIL E1.22-handoff no live row\nrun: 1 failed\n",
                 "exit_code": 1, "status": "failed", "duration": {"secs": 541, "nanos": 0}}),
        rec("response_item", 7, {"type": "custom_tool_call_output", "call_id": "c1", "output": [{"type": "input_text", "text": "Script completed"}]}),
        rec("response_item", 8, {"type": "custom_tool_call", "call_id": "c2", "name": "exec", "input": "throw new Error('boom')"}),
        rec("response_item", 9, {"type": "custom_tool_call_output", "call_id": "c2", "output": [{"type": "input_text", "text": "Script failed\nError: boom"}]}),
        rec("response_item", 10, {"type": "function_call", "call_id": "c3", "name": "exec_command", "arguments": json.dumps({"cmd": "go vet ./..."})}),
        rec("response_item", 11, {"type": "function_call_output", "call_id": "c3", "output": "Chunk ID: 1\nProcess exited with code 2\nOutput:\nvet: bad"}),
        done(12, {"type": "FileChange", "status": "completed", "changes": {
            "/work/repo/infra/E1.sh": {"type": "update", "unified_diff": "@@ -1,3 +1,2 @@\n-old\n-older\n+new\n"}}}),
        done(13, {"type": "McpToolCall", "server": "professor", "tool": "chat_inject", "arguments": {"target": "PFM", "file": "/work/repo/r.md"},
                  "status": "failed", "result": {"content": [{"type": "text", "text": "unexpected additional properties [\"file\"]"}], "isError": True}}),
        rec("event_msg", 14, {"type": "token_count", "info": {}}),
        {"timestamp": ts(15), "type": "brand_new_record", "payload": {"type": "mystery"}},
        rec("response_item", 16, {"type": "message", "role": "assistant", "phase": "final_answer", "content": [{"type": "output_text", "text": "FAILED 3-c: mock ignores --fork-session."}]}),
        rec("event_msg", 17, {"type": "task_complete", "last_agent_message": "FAILED 3-c: mock ignores --fork-session.\n<citation/>"}),
    ]


def write_jsonl(path, records, bad_lines=0):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as handle:
        for record in records:
            handle.write(json.dumps(record) + "\n")
        for _ in range(bad_lines):
            handle.write("{not json\n")


class Fixture(unittest.TestCase):
    def setUp(self):
        self.home = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.home, ignore_errors=True)
        self.claude = os.path.join(self.home, ".claude/projects/-work-repo", CLAUDE_SID, "subagents", f"agent-{CLAUDE_AGENT}.jsonl")
        write_jsonl(self.claude, claude_records(), bad_lines=1)
        with open(self.claude.replace(".jsonl", ".meta.json"), "w") as handle:
            json.dump({"agentType": "flights-foreman", "description": "Build unit be"}, handle)
        self.codex = os.path.join(self.home, ".codex/sessions/2026/09/29", f"rollout-2026-09-29T22-42-11-{CODEX_SID}.jsonl")
        write_jsonl(self.codex, codex_records())

    def run_tp(self, *args):
        env = {k: v for k, v in os.environ.items() if k not in ("CLAUDE_CONFIG_DIR", "CODEX_HOME")}
        env["HOME"] = self.home
        return subprocess.run([sys.executable, SCRIPT] + list(args), capture_output=True, text=True, env=env)

    def ok(self, *args):
        proc = self.run_tp(*args)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        return proc.stdout


class ResolveTest(Fixture):
    def test_agent_id_resolves_to_the_subagent_file(self):
        self.assertEqual(self.ok("locate", CLAUDE_AGENT).strip(), os.path.realpath(self.claude))
        self.assertEqual(self.ok("locate", "agent-" + CLAUDE_AGENT).strip(), os.path.realpath(self.claude))

    def test_codex_sid_prefix_resolves_to_its_rollout(self):
        self.assertEqual(self.ok("locate", CODEX_SID[:8]).strip(), os.path.realpath(self.codex))

    def test_unknown_id_fails_naming_where_it_searched(self):
        proc = self.run_tp("show", "deadbeef00")
        self.assertEqual(proc.returncode, 2)
        self.assertIn("TRANSCRIPT FAILED — NOT FOUND deadbeef00 — searched", proc.stderr)
        self.assertIn(".codex", proc.stderr)

    def test_ambiguous_prefix_fails_listing_every_match(self):
        twin = self.codex.replace("22-42-11", "23-00-00").replace(CODEX_SID, CODEX_SID[:8] + "-ffff-0000-0000-000000000000")
        write_jsonl(twin, codex_records())
        proc = self.run_tp("show", CODEX_SID[:8])
        self.assertEqual(proc.returncode, 2)
        self.assertIn("AMBIGUOUS", proc.stderr)
        self.assertEqual(proc.stderr.count("rollout-"), 2)

    def test_codex_agent_path_resolves_through_session_meta(self):
        rollouts = {}
        for name, meta in (("fix_1a", {"agent_path": "/root/fix_1a"}),
                           ("fix_1p", {"source": {"subagent": {"thread_spawn": {"agent_path": "/root/fix_1p"}}}})):
            records = codex_records()
            records[0]["payload"] = dict(records[0]["payload"], id=f"cx-{name}", **meta)
            rollouts[name] = os.path.join(self.home, ".codex/sessions/2026/09/29", f"rollout-2026-09-29T23-00-00-cx-{name}.jsonl")
            write_jsonl(rollouts[name], records)
        for name, path in rollouts.items():
            self.assertEqual(self.ok("locate", f"/root/{name}").strip(), os.path.realpath(path))
        proc = self.run_tp("show", "/root/fix_9z")
        self.assertEqual(proc.returncode, 2)
        self.assertIn("NOT FOUND /root/fix_9z — searched", proc.stderr)

    def test_a_seat_name_points_at_pfm_chat_resolve(self):
        proc = self.run_tp("show", "seat.2-a")
        self.assertEqual(proc.returncode, 2)
        self.assertIn("`pfm chat resolve seat.2-a`, third column", proc.stderr)

    def test_missing_path_and_empty_file_fail_differently(self):
        proc = self.run_tp("show", os.path.join(self.home, "nope.jsonl"))
        self.assertIn("no such transcript file", proc.stderr)
        empty = os.path.join(self.home, "empty.jsonl")
        open(empty, "w").close()
        proc = self.run_tp("show", empty)
        self.assertEqual(proc.returncode, 2)
        self.assertIn("empty transcript", proc.stderr)


class ClaudeTest(Fixture):
    def test_digest_renders_prompt_calls_error_tail_queued_prompt_and_final(self):
        out = self.ok("show", self.claude)
        self.assertIn("TRANSCRIPT claude · agent a0123456789abcdef of session", out)
        self.assertIn("flights-foreman · Build unit be", out)
        self.assertRegex(out, r"L1 20:00:00 PROMPT Revise task 3-d")
        self.assertRegex(out, r"L4 20:00:02 Bash go test \./pkg/\.\.\. → ERR \d+B · 28s")
        self.assertIn("    | fork reuses parent sid", out)
        self.assertRegex(out, r"Read ./pkg/fork.go → ok 600B\n")  # a read shows its size, never its last line
        self.assertIn("PROMPT(queued) Two more items from the orchestrator.", out)
        self.assertIn("NOTE [SYSTEM NOTIFICATION", out)
        self.assertIn("Grep fork pkg → NO RESULT", out)
        self.assertRegex(out, r"L12 20:00:40 FINAL\n  SPEC revised: 3-d cut smaller.")
        self.assertIn("CALLS 3, 1 err, 1 without result", out)

    def test_every_unrendered_record_is_counted_by_type(self):
        out = self.ok("show", self.claude)
        skipped = out.strip().splitlines()[-1]
        self.assertTrue(skipped.startswith("SKIPPED 4 records: "), skipped)
        for label in ("attachment.total_tokens_reminder 1", "assistant.thinking 1", "weird_future_type 1", "unparseable line 1"):
            self.assertIn(label, skipped)

    def test_types_accounts_for_every_record(self):
        out = self.ok("types", self.claude)
        total = int(re.search(r"RECORDS (\d+)", out).group(1))
        parts = sum(int(n) for n in re.findall(r"^(?:rendered|header|skipped) (\d+):", out, re.M))
        self.assertEqual(total, 13)
        self.assertEqual(parts, total)


class CodexTest(Fixture):
    def test_exec_wrapper_folds_into_its_commands_and_a_childless_one_renders(self):
        out = self.ok("show", CODEX_SID[:8])
        self.assertIn("TRANSCRIPT codex · " + CODEX_SID + " · gpt-6-sol high", out)
        self.assertNotIn("const r = await", out)
        self.assertRegex(out, r"exec cat ./task.md → ok 10B\n")
        self.assertRegex(out, r"exec infra/run.sh --lanes E1 → ERR EXIT 1 \d+B · 541s")
        self.assertIn("    | E1 FAIL E1.22-handoff no live row", out)
        self.assertIn("exec throw new Error('boom') → ERR", out)
        self.assertIn("exec_command go vet ./... → ERR", out)
        self.assertIn("edit ./infra/E1.sh (update +1 -2) → ok", out)
        self.assertIn("mcp__professor__chat_inject", out)
        self.assertIn("unexpected additional properties", out)

    def test_prompts_skip_injected_context_and_final_prints_once(self):
        out = self.ok("show", CODEX_SID[:8])
        self.assertEqual(out.count("PROMPT"), 1)
        self.assertIn("PROMPT # Brief — task 3-c round 4", out)
        self.assertIn("SAY Reading the task first.", out)
        self.assertEqual(out.count("FINAL"), 1)
        skipped = out.strip().splitlines()[-1]
        for label in ("response_item/message:developer 1", "response_item/message:user (injected context) 1",
                      "event_msg/item_completed:UserMessage (duplicate of its response_item) 1",
                      "response_item/reasoning 1", "event_msg/token_count 1", "brand_new_record/mystery 1",
                      "exec script (shown as its actions) 1", "event_msg/task_complete (its message is the reply above) 1"):
            self.assertIn(label, skipped)
        self.assertIn("HEADER 2: session_meta 1 · turn_context 1", skipped)

    def test_counts_tabulates_per_tool(self):
        out = self.ok("counts", CODEX_SID[:8])
        self.assertIn("exec | 3 | 2 |", out)
        self.assertIn("mcp__professor__chat_inject | 1 | 1 |", out)


class FilterTest(Fixture):
    def events(self, out):
        return [ln for ln in out.splitlines() if re.match(r"L\d+ ", ln)]

    def test_only_error_keeps_failed_calls_alone(self):
        out = self.ok("show", CODEX_SID[:8], "--only", "error")
        names = [ln.split()[2] for ln in self.events(out)]
        self.assertEqual(names, ["exec", "exec", "exec_command", "mcp__professor__chat_inject"])

    def test_tool_filter_is_case_insensitive_and_keeps_prose(self):
        out = self.ok("show", self.claude, "--tool", "bash")
        self.assertIn("Bash go test", out)
        self.assertNotIn("Read ./pkg", out)
        self.assertIn("FINAL", out)

    def test_grep_searches_results_and_prints_the_matching_line(self):
        out = self.ok("show", CODEX_SID[:8], "--grep", "E1\\.22")
        events = self.events(out)
        self.assertEqual(len(events), 1)
        self.assertIn("    ~ E1 FAIL E1.22-handoff no live row", out)

    def test_time_window_clock_and_relative(self):
        out = self.ok("show", CODEX_SID[:8], "--since", "20:00:10", "--until", "20:00:13")
        self.assertEqual([ln.split()[0] for ln in self.events(out)], ["L15", "L17", "L18"])
        out = self.ok("show", CODEX_SID[:8], "--since", "-2s")
        self.assertEqual(len(self.events(out)), 1)

    def test_last_counts_events_after_filters(self):
        out = self.ok("show", self.claude, "--last", "2")
        self.assertEqual([ln.split()[2] for ln in self.events(out)], ["Grep", "FINAL"])

    def test_bad_filter_values_fail_loudly(self):
        for args, reason in ((["--only", "chats"], "unknown kind"), (["--results", "some"], "--results"),
                             (["--since", "noon"], "unreadable time"), (["--grep", "("], "--grep"), (["--lines", "a-b"], "--lines")):
            proc = self.run_tp("show", self.claude, *args)
            self.assertEqual(proc.returncode, 2, args)
            self.assertIn(reason, proc.stderr)

    def test_lines_selects_the_events_of_a_record_range(self):
        out = self.ok("show", self.claude, "--lines", "4-8")
        self.assertEqual([ln.split()[0] for ln in self.events(out)], ["L4", "L6", "L8"])
        self.assertEqual([ln.split()[0] for ln in self.events(self.ok("show", self.claude, "--lines", "11-"))], ["L11", "L12"])

    def test_final_reply_prints_whole_by_default(self):
        records = claude_records()
        records[-1]["message"]["content"][0]["text"] = "RETURN " + "x" * 9000 + " END-OF-RETURN"
        write_jsonl(self.claude, records)
        out = self.ok("show", self.claude)
        self.assertIn("END-OF-RETURN", out)
        self.assertNotIn("…[+", out.split("FINAL", 1)[1])

    def test_out_writes_the_digest_and_reports_its_size(self):
        target = os.path.join(self.home, "d", "digest.txt")
        out = self.ok("show", self.claude, "--out", target)
        self.assertRegex(out, r"^WROTE .*digest.txt · \d+ bytes · \d+ lines$")
        with open(target) as handle:
            self.assertIn("FINAL", handle.read())


def session_records(sid, *titles, second=0):
    """A top-level Claude session: one prompt, then each (kind, name) title record as Claude Code appends it."""
    out = [{"type": "user", "sessionId": sid, "cwd": "/work/repo", "timestamp": ts(second),
            "message": {"role": "user", "content": "Open the audit desk."}}]
    for kind, name in titles:
        key = "customTitle" if kind == "custom-title" else "agentName"
        out.append({"type": kind, key: name, "sessionId": sid})
    return out


SID_A = "aaaaaaaa-1111-2222-3333-444444444444"
SID_B = "bbbbbbbb-1111-2222-3333-444444444444"


class NameTest(Fixture):
    def session(self, sid, *titles, second=0):
        path = os.path.join(self.home, ".claude/projects/-work-repo", f"{sid}.jsonl")
        write_jsonl(path, session_records(sid, *titles, second=second))
        return path

    def test_a_chat_name_resolves_case_insensitively_and_heads_the_digest(self):
        path = self.session(SID_A, ("agent-name", "AUDIT DESK"), ("custom-title", "Audit Desk"))
        self.assertEqual(self.ok("locate", "audit desk").strip(), os.path.realpath(path))
        self.assertIn("\nNAME Audit Desk\n", self.ok("show", "AUDIT DESK"))
        self.assertIn("\nNAME Audit Desk\n", self.ok("counts", SID_A[:8]))
        only_agent = self.session(SID_B, ("agent-name", "Night Shift"))
        self.assertEqual(self.ok("locate", "night shift").strip(), os.path.realpath(only_agent))

    def test_a_renamed_chat_matches_only_its_last_title(self):
        path = self.session(SID_A, ("custom-title", "Old Desk"), ("agent-name", "Old Desk"), ("custom-title", "New Desk"))
        self.assertEqual(self.ok("locate", "New Desk").strip(), os.path.realpath(path))
        proc = self.run_tp("locate", "Old Desk")
        self.assertEqual(proc.returncode, 2)
        self.assertIn("NOT FOUND Old Desk", proc.stderr)

    def test_two_chats_sharing_a_name_fail_ambiguous_listing_both(self):
        first = self.session(SID_A, ("custom-title", "Twin"), second=5)
        second = self.session(SID_B, ("custom-title", "Twin"), second=9)
        proc = self.run_tp("show", "twin")
        self.assertEqual(proc.returncode, 2)
        self.assertIn("AMBIGUOUS twin — 2 transcripts", proc.stderr)
        for path, second_ in ((first, 5), (second, 9)):
            size_ = os.path.getsize(path)
            self.assertIn(f"{os.path.realpath(path)} ({size_} bytes, last record {ts(second_)})", proc.stderr)

    def test_an_unknown_name_fails_naming_the_lookup_and_its_scope(self):
        self.session(SID_A, ("custom-title", "Audit Desk"))
        self.session(SID_B)
        proc = self.run_tp("show", "Nobody Here")
        self.assertEqual(proc.returncode, 2)
        self.assertIn("NOT FOUND Nobody Here", proc.stderr)
        self.assertIn("name lookup over 2 session files", proc.stderr)

    def test_a_symlinked_root_is_searched_once(self):
        path = self.session(SID_A, ("custom-title", "Audit Desk"))
        os.makedirs(os.path.join(self.home, ".cc/1"))
        os.symlink(os.path.join(self.home, ".claude/projects"), os.path.join(self.home, ".cc/1/projects"))
        self.assertEqual(self.ok("locate", "Audit Desk").strip(), os.path.realpath(path))
        proc = self.run_tp("show", "Nobody Here")
        self.assertIn("name lookup over 1 session files", proc.stderr)


class PromptKindTest(Fixture):
    def digest(self, *records):
        write_jsonl(self.claude, claude_records() + [dict(r, sessionId=CLAUDE_SID, timestamp=ts(50)) for r in records])
        return self.ok("show", self.claude)

    def test_a_queued_task_notification_is_a_note(self):
        out = self.digest({"type": "attachment", "attachment": {"type": "queued_command",
                                                                "prompt": "<task-notification>\n<task-id>ab1</task-id>"}})
        self.assertIn("NOTE <task-notification> <task-id>ab1</task-id>", out)
        self.assertNotIn("PROMPT(queued) <task-notification>", out)
        self.assertIn("PROMPT(queued) Two more items from the orchestrator.", out)

    def test_a_plugin_message_is_a_plugin_prompt(self):
        out = self.digest({"type": "user", "message": {"role": "user", "content": "The buddy plugin sent a message: ping"}})
        self.assertIn("PROMPT(plugin) The buddy plugin sent a message: ping", out)


class AgentsTest(Fixture):
    def setUp(self):
        super().setUp()
        self.session = os.path.join(self.home, ".claude/projects/-work-repo", f"{SID_A}.jsonl")
        write_jsonl(self.session, session_records(SID_A, ("custom-title", "Audit Desk")))
        self.subagents = os.path.join(self.home, ".claude/projects/-work-repo", SID_A, "subagents")

    def agent(self, aid, start, calls, meta):
        path = os.path.join(self.subagents, f"agent-{aid}.jsonl")
        records = [{"type": "user", "sessionId": SID_A, "agentId": aid, "timestamp": ts(start), "message": {"content": "go"}}]
        for i in range(calls):
            records.append({"type": "assistant", "sessionId": SID_A, "agentId": aid, "timestamp": ts(start + 1 + i), "message": {
                "content": [{"type": "tool_use", "id": f"{aid}{i}", "name": "Bash", "input": {"command": "ls"}}]}})
        write_jsonl(path, records)
        if meta is not None:
            with open(path.replace(".jsonl", ".meta.json"), "w") as handle:
                handle.write(meta if isinstance(meta, str) else json.dumps(meta))
        return path

    def rows(self, out):
        return [ln for ln in out.splitlines() if ln.startswith("a")]

    def test_agents_lists_each_subagent_sorted_by_first_timestamp(self):
        late = self.agent("a2late", 30, 3, {"agentType": "gitter", "description": "Commit the docs"})
        self.agent("a1early", 10, 1, {"agentType": "tracer", "description": "Map the doors"})
        out = self.ok("agents", "Audit Desk")
        self.assertIn(f"SESSION {SID_A} · {os.path.realpath(self.session)}", out)
        self.assertIn("\nAGENTS 2\n", out)
        rows = self.rows(out)
        self.assertEqual([r.split()[0] for r in rows], ["a1early", "a2late"])
        self.assertEqual(rows[1], f"a2late · gitter · Commit the docs · {ts(30)[:19].replace('T', ' ')}Z → "
                                  f"{ts(33)[:19].replace('T', ' ')}Z · 4 records · {os.path.getsize(late)}B · 3 calls")

    def test_a_missing_or_broken_meta_prints_meta_error(self):
        self.agent("a1nometa", 10, 0, None)
        self.agent("a2badmeta", 20, 0, "{not json")
        rows = self.rows(self.ok("agents", SID_A[:8]))
        self.assertTrue(rows[0].startswith("a1nometa · META ERROR missing "), rows[0])
        self.assertTrue(rows[1].startswith("a2badmeta · META ERROR "), rows[1])

    def test_no_subagents_directory_prints_agents_zero(self):
        out = self.ok("agents", SID_A)
        self.assertIn(f"AGENTS 0 — no subagents directory at {os.path.realpath(self.subagents)}", out)


if __name__ == "__main__":
    unittest.main()
