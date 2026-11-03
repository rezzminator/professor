"""The actual reload receipt reader isolates the newly delivered engine input."""
import json
import os
from pathlib import Path
import tempfile
import unittest
from importlib import import_module
from contextlib import contextmanager
from unittest.mock import patch

from infra.fence.journey.runtime import command
continuation_records = import_module("infra.fence.journey.fixtures.chat-reload.handoff").continuation_records
lost_index = import_module("infra.fence.journey.fixtures.chat-reload.lost_index")


@contextmanager
def indexed_tail_fixture():
    with tempfile.TemporaryDirectory(prefix="reload-indexed-tail-") as folder:
        root = Path(folder)
        cwd = root / "isolated"
        cwd.mkdir()
        (cwd / "preserved").write_text("retained cwd\n")
        scenario = {"record_dir": folder, "steps": [], "startup_exit_code": 2}
        (root / "scenario.json").write_text(json.dumps(scenario))
        (root / "birth.json").write_text('{"pid":42}\n')
        (root / "continued-presence.json").write_text(json.dumps({"cwd": str(cwd)}))
        (root / "owner-cwd.status").write_text("0\n")
        (root / "owner-cwd.out").write_text(folder + "\n")
        (root / "activity.jsonl").write_text("")
        (root / "activity.jsonl.lock").write_text("")
        with patch.dict(os.environ, {"JOURNEY_ROOT": folder, "HOME": str(root / "home"),
                                    "JOURNEY_ACTIVITY": str(root / "activity.jsonl"),
                                    "PFM_RUN": "fixture-run", "PFM_BEAT": "fixture-beat"}):
            yield root, cwd, scenario


class ReloadInput(unittest.TestCase):
    def test_reload_readiness_requires_the_actual_successor_engine_birth(self):
        lane = Path(__file__).resolve().parents[2] / "lanes/chat-reload.sh"
        engine = {"pid": 42, "start": "original", "chain": [{"argv": "claude --resume actual-session"}],
                  "environment": {"CLAUDE_CONFIG_DIR": "/fixture/.cc/1"}}
        hook = {"pid": 43, "start": "hook", "chain": [{"argv": "pfm internal herdr-state"}], "environment": {}}
        terminal = {"identity": {"pane": "owned-pane"}, "births": [hook], "shield": {"births": [engine]}}
        chat = {"status": {"session_id": "actual-session"}, "terminal": terminal,
                "process_births": [hook, engine]}
        with tempfile.TemporaryDirectory(prefix="reload-readiness-") as folder:
            root = Path(folder)
            (root / "before.json").write_text(json.dumps({"chats": [chat], "terminal": terminal}))
            (root / "before-chat.json").write_text(json.dumps({"session_id": "actual-session", "engine": "claude"}))
            environment = {**os.environ, "ARTIFACT": folder, "JOURNEY_OS": "linux", "DOOR": "cli",
                           "JOURNEY_TARGET": str(root / "target.json")}
            for engines, expected in [([engine], 1), ([{**engine, "pid": 44, "start": "successor"}], 0),
                                      ([{**engine, "start": "reused-pid"}], 0), ([], 1),
                                      ([engine, {**engine, "pid": 44, "start": "successor"}], 1)]:
                current = {**terminal, "births": [{**hook, "pid": 45}], "shield": {"births": engines}}
                (root / "current.json").write_text(json.dumps(current))
                actual = command(["bash", "-c", '''source "$1"
lane_refresh() { :; }
lane_state() { :; }
python3() { if [ "$1" = "$HERE/terminal.py" ]; then cat "$ARTIFACT/current.json"; else command python3 "$@"; fi; }
BEFORE_BIRTHS="$(_reload_births "$ARTIFACT/before.json")"
_reload_ready; printf 'ready=%s\\n' "$?"
''', "reload-readiness", str(lane)], env=environment)
                with self.subTest(engines=engines):
                    self.assertEqual(f"ready={expected}\n", actual)
            (root / "current.json").write_text(json.dumps({**terminal, "shield": {"births": [{**engine, "pid": 44}]}}))
            message = "reload scheduled — this chat reboots when the current turn ends"
            shown = "⎿ Blocked by hook: " + message + " (pfm chat reload: reload scheduled in place (log /j\n/run/sid/reload-owned.log))\n\n❯ /exit\n"
            environment["DOOR"] = "typed"
            for frame, expected in [(shown, f"ready=0\n{message}\n"), ("❯\n", "ready=1\n"),
                                    (shown.replace("scheduled in place", "failed"), "ready=2\n"),
                                    (shown.replace("reload-owned.log", "reload-owned.pid"), "ready=2\n")]:
                (root / "screen.txt").write_text(frame)
                receipt = root / "typed-receipt.txt"
                receipt.unlink(missing_ok=True)
                actual = command(["bash", "-c", '''source "$1"
lane_refresh() { :; }
lane_state() { :; }
python3() { if [ "$1" = "$HERE/terminal.py" ]; then
  if [ "$2" = screen ]; then cat "$ARTIFACT/screen.txt"; else cat "$ARTIFACT/current.json"; fi
else command python3 "$@"; fi; }
_reload_ready; printf 'ready=%s\\n' "$?"
if [ -f "$ARTIFACT/typed-receipt.txt" ]; then cat "$ARTIFACT/typed-receipt.txt"; fi
''', "reload-typed-receipt", str(lane)], env=environment)
                with self.subTest(frame=frame):
                    self.assertEqual(expected, actual)
            projection = import_module("infra.fence.journey.fixtures.chat-reload.projection")
            try:
                receipt = projection.typed_receipt(shown)
            except ValueError as error:
                self.fail(f"expected valid wrapped receipt, actual={error}; frame={shown!r}")
            self.assertEqual({"text": message, "log": "/j/run/sid/reload-owned.log"}, receipt)
            for records, directory, expected in [({"reload-owned.log": "respawned"}, "/j/run/sid", "/j/run/sid/reload-owned.log"),
                                                  ({"reload-owned.log": "respawned"}, "/foreign/sid", "/foreign/sid/reload-owned.log"),
                                                  ({"reload-owned.log": "absent"}, "/j/run/sid", "n/a:missing-reload-log"),
                                                  ({}, "/j/run/sid", "n/a:missing-reload-log")]:
                with self.subTest(records=records, directory=directory):
                    bundle = {"chats": [{"status": {"session_id": "actual-session"}, "stored": {"reload_records": records}}]}
                    self.assertEqual([{"field": "typed-receipt-log", "expected": "/j/run/sid/reload-owned.log", "actual": expected}],
                                     projection.typed_receipt_log(receipt, bundle, "actual-session", directory))

    def test_shared_check_rows_aggregate_failures_and_reject_malformed_inputs(self):
        flow = Path(__file__).resolve().parent / "flow.sh"
        rows = [{"field": "first", "expected": "left", "actual": "wrong"},
                {"field": "last", "expected": 7, "actual": 7}]
        cases = [(json.dumps(rows), "observed", 'chat.reload|"left"|"wrong"|observed:first\nchat.reload|7|7|observed:last\nRESULT:1\n'),
                 (json.dumps([rows[1]]), "observed", "chat.reload|7|7|observed:last\nRESULT:0\n"),
                 ('[{"field":"nullable","expected":null,"actual":null}]', "", None),
                 ("[]", "observed", "RESULT:2\n"), ("{}", "observed", "RESULT:2\n"),
                 ("{broken", "observed", "RESULT:2\n"), ("[{}]", "observed", "RESULT:2\n"),
                 ('["malformed"]', "observed", "RESULT:2\n"),
                 ('[{"field":"missing-actual","expected":null}]', "observed", "RESULT:2\n"),
                 ('[{"field":"missing-expected","actual":null}]', "observed", "RESULT:2\n"),
                 ('[{"field":"","expected":true,"actual":true}]', "observed", "RESULT:2\n"),
                 ('[{"field":42,"expected":true,"actual":true}]', "observed", "RESULT:2\n")]
        with tempfile.TemporaryDirectory(prefix="reload-check-rows-") as folder:
            path = Path(folder) / "checks.json"
            for payload, source, expected in cases:
                path.write_text(payload)
                actual = command(["bash", "-c", 'source "$1"; ID=chat.reload; assert() { printf "%s|%s|%s|%s\\n" "$1" "$2" "$3" "$4"; [ "$2" = "$3" ]; }; lane_assert_checks "$2" "$3"; rc=$?; printf "RESULT:%s\\n" "$rc"',
                                  "reload-check-rows", str(flow), str(path), source])
                if expected is None:
                    expected = f"chat.reload|null|null|{path}:nullable\nRESULT:0\n"
                with self.subTest(payload=payload, source=source, artifact=str(path)):
                    self.assertEqual(expected, actual)

    def test_engine_cli_input_scopes_its_door_and_preserves_shell_arguments(self):
        lane = Path(__file__).resolve().parents[2] / "lanes/chat-reload.sh"
        with tempfile.TemporaryDirectory(prefix="reload-cli-input-") as folder:
            root = Path(folder)
            executable = root / "pfm"
            executable.write_text('#!/bin/sh\nprintf "%s\\n" "$PFM_DOOR" "$@"\n')
            executable.chmod(0o700)
            for engine, tool, argument in [("claude", "Bash", "command"), ("codex", "exec_command", "cmd"),
                                           ("opencode", "bash", "command")]:
                with self.subTest(engine=engine):
                    environment = {**os.environ, "ARTIFACT": folder, "JOURNEY_OS": "linux", "ENGINE": engine,
                                   "PFM_DOOR": "typed", "PATH": folder + os.pathsep + os.environ["PATH"]}
                    command(["bash", "-c", 'source "$1"; lane_input() { :; }; _reload_shell pfm chat reload "quoted argument"',
                             "reload-cli", str(lane)], env=environment)
                    step = json.loads((root / "command.json").read_text())[0]
                    self.assertEqual(tool, step["tool"])
                    self.assertEqual([argument], list(step["input"]))
                    actual = command(["bash", "-c", step["input"][argument] + '; printf "%s\\n" "$PFM_DOOR"'], env=environment)
                    self.assertEqual("typed\n", actual)
                    self.assertEqual("cli\nchat\nreload\nquoted argument\n", (root / "command.out").read_text())
                    self.assertEqual("0\n", (root / "command.status").read_text())
            environment.update(ARTIFACT=folder, JOURNEY_TARGET=str(root / "target.json"),
                               PICKER=str(root / "picker.json"), CHAT=str(root / "chat.json"))
            settled = {"name": "settled-atlas", "model": "settled-model"}
            script = '''source "$1"
CHAT_BRANCH=fixture-branch CHAT_ORIGIN=fixture-origin
_reload_settle() { printf 'settle\\n' >>"$ARTIFACT/events"; cp "$ARTIFACT/settled.json" "$ARTIFACT/current.json"; return "$2"; }
_reload_births() { printf birth; }
python3() {
  case "$2" in
    annotate) printf 'annotate\\n' >>"$ARTIFACT/events"; cat "$ARTIFACT/current.json" ;;
    pick) printf 'pick\\n' >>"$ARTIFACT/events" ;;
    snapshot) printf 'snapshot\\n' >>"$ARTIFACT/events"; cp "$ARTIFACT/current.json" "$4" ;;
  esac
}
_reload_before; printf 'result=%s\\n' "$?"
'''
            cases = [(0, "settle\nannotate\npick\nsnapshot\n"), (2, "settle\n")]
            for engine, result, events in [(engine, result, events) for engine in ("claude", "codex", "opencode")
                                          for result, events in cases]:
                environment["ENGINE"] = engine
                (root / "settled.json").write_text(json.dumps(settled))
                (root / "current.json").write_text('{"name":"","model":""}')
                (root / "events").write_text("")
                actual = command(["bash", "-c", script.replace('return "$2"', f'return {result}'),
                                  "reload-metadata", str(lane)], env=environment)
                self.assertEqual(f"result={result}\n", actual)
                self.assertEqual(events, (root / "events").read_text())
                if result == 0:
                    self.assertEqual(settled, json.loads((root / "before-chat.json").read_text()))
                    self.assertEqual(settled, json.loads((root / "before.json").read_text()))

    def test_failed_worker_receipt_names_only_the_owned_exec_refusal(self):
        row = {"msg": "reload.failed", "level": "ERROR", "exit": 1,
               "err": "pfm chat reload: native reload new engine failed: actual cwd absent"}
        for rows in ([], [{**row, "err": "foreign cwd absent"}], [{**row, "msg": "unrelated.error"}]):
            self.assertIsNone(lost_index.failed_worker(rows, "actual cwd absent"))
        self.assertEqual(row, lost_index.failed_worker([row], "actual cwd absent"))
        for rows in ([{**row, "exit": 2}], [{**row, "err": "different wrapper: actual cwd absent"}], [row, row]):
            with self.subTest(rows=rows), self.assertRaisesRegex(ValueError, "LOST-INDEX-WORKER-FAILURE"):
                lost_index.failed_worker(rows, "actual cwd absent")

    def test_failed_seat_retries_only_after_dead_writer_and_restores_tail_before_release(self):
        host = {"pid": 41, "start": "host-start", "state": "R"}
        original = {"pid": 42, "start": "writer-start"}
        replacement = {"pid": 43, "start": "replacement-start", "state": "R"}
        processes = {41: host, 42: {**original, "state": "R"}}
        events = []
        @contextmanager
        def stopped(target, birth, signal):
            host["state"] = "T"
            try:
                yield birth
            finally:
                host["state"] = "R"
                processes.pop(42)
                events.append("CONT")
                (root / "activity.jsonl").write_text(json.dumps({"msg": "reload.failed",
                    "level": "ERROR", "exit": 1, "err": "pfm chat reload: native reload new engine failed: " + row["lost_note"]}) + "\n")
        with indexed_tail_fixture() as (root, cwd, scenario):
            tail = root / "tail.jsonl"
            tail.write_text("actual indexed tail bytes\n")
            indexed = {"launch_id": "old-launch", "tail_id": "native-tail", "path": str(tail)}
            row = {"seat_host_pid": 41, "reload_pending": 1, "state": "failed",
                   "lost_note": f"start foreground engine: chdir {cwd}: no such file or directory"}
            def dispatch(target, arguments, receipt):
                self.assertEqual(["pfm", "chat", "reload", "old-launch"], arguments)
                self.assertTrue(cwd.is_dir())
                events.append(Path(receipt).name)
                if receipt.endswith("prepare-command"):
                    processes[42]["state"] = "Z"
                else:
                    self.assertFalse(tail.exists())
                    self.assertFalse((root / "replacement.ready").exists())
                    staged = json.loads((root / "scenario.json").read_text())
                    self.assertEqual({"record_dir": str(root), "steps": [], "ready_hold": str(root / "replacement.ready"),
                                      "ready_timeout_ms": 30000}, staged)
                    processes[43] = replacement
                    with (root / "birth.json").open("a") as journal:
                        journal.write('{"pid":43}\n')
            with patch.object(lost_index, "signalled_process", stopped), \
                    patch.object(lost_index, "seat", return_value=row), \
                    patch.object(lost_index, "terminal", return_value={"text": "press Enter to retry, q to close"}), \
                    patch.object(lost_index, "cached_continuation", return_value=indexed), \
                    patch.object(lost_index, "process_snapshot", side_effect=lambda: dict(processes)), \
                    patch.object(lost_index, "type_command", dispatch):
                lost_index.drive({"terminal": {}, "scenario": str(root / "scenario.json")}, {}, indexed, original, root)
            self.assertEqual(["prepare-command", "CONT", "retry-command"], events)
            self.assertEqual("retained cwd\n", (cwd / "preserved").read_text())
            self.assertEqual("actual indexed tail bytes\n", tail.read_text())
            self.assertEqual(scenario, json.loads((root / "scenario.json").read_text()))
            self.assertEqual("release\n", (root / "replacement.ready").read_text())
            self.assertEqual({"pid": 43, "start": "replacement-start"}, json.loads((root / "held-replacement.json").read_text()))
            evidence = json.loads((root / "failed-seat-preparation.json").read_text())
            self.assertEqual(("ended", "n/a:not-started", [], True, True),
                             (evidence["original_writer"], evidence["replacement_birth"], evidence["replacement_receipts"],
                              evidence["host_continued"], evidence["cwd_restored"]))

    def test_indexed_tail_fault_waits_for_writer_death_and_restores_after_failure(self):
        events = []
        host = {"pid": 41, "start": "host-start", "state": "R"}
        original = {"pid": 42, "start": "writer-start"}
        @contextmanager
        def stopped(target, birth, signal):
            self.assertEqual(({}, {"pid": 41, "start": "host-start"}, "STOP"), (target, birth, signal))
            events.append("STOP")
            host["state"] = "T"
            try:
                yield birth
            finally:
                host["state"] = "R"
                events.append("CONT")
        with indexed_tail_fixture() as (root, cwd, scenario):
            chat = {"terminal": {}, "scenario": str(root / "scenario.json")}
            def wait(label, predicate, bound):
                self.assertTrue(predicate() if "OS spawn failure" not in label else not cwd.exists(), label)
                events.append(label)
                if "OS spawn failure" in label:
                    raise RuntimeError("injected observation failure")
                return True
            def dispatch(target, arguments, receipt):
                self.assertTrue(cwd.is_dir())
                self.assertEqual(["pfm", "chat", "reload", "old-launch"], arguments)
                self.assertEqual(str(root / "prepare-command"), receipt)
                events.append("reload original launch")
            with patch.object(lost_index, "signalled_process", stopped), \
                    patch.object(lost_index, "seat", return_value={"seat_host_pid": 41, "reload_pending": 1}), \
                    patch.object(lost_index, "process_snapshot", side_effect=lambda: {41: dict(host), 42: {**original, "state": "Z"}}), \
                    patch.object(lost_index, "wait", wait), patch.object(lost_index, "type_command", dispatch):
                with self.assertRaisesRegex(RuntimeError, "injected observation failure"):
                    lost_index.drive(chat, {}, {"launch_id": "old-launch"}, original, root)
            self.assertEqual(["STOP", "owned supervisor stopped", "reload original launch",
                              "original writer ended and actual reload pending", "CONT",
                              "owned supervisor CONT acknowledged", "actual OS spawn failure and native retry UI"], events)
            self.assertEqual("retained cwd\n", (cwd / "preserved").read_text())

    def test_writer_and_held_replacement_use_actual_kernel_births(self):
        original = {"pid": 42, "start": "writer-start"}
        for processes, expected in [({}, True), ({42: {**original, "state": "Z"}}, True),
                                    ({42: {**original, "state": "R"}}, False),
                                    ({42: {"pid": 42, "start": "reused", "state": "R"}}, True)]:
            with self.subTest(processes=processes):
                self.assertEqual(expected, lost_index.writer_ended(original, processes))
        with tempfile.TemporaryDirectory(prefix="reload-birth-journal-") as folder:
            journal = Path(folder) / "birth.json"
            self.assertEqual([], lost_index.complete_records(journal, 0))
            journal.write_text('{"pid":42}\n{"pid":43}\n{"pid":')
            self.assertEqual([{"pid": 43}], lost_index.complete_records(journal, len('{"pid":42}\n')))
            replacement = {"pid": 43, "start": "replacement-start", "state": "R"}
            with patch.object(lost_index, "process_snapshot", return_value={42: {**original, "state": "Z"}, 43: replacement}):
                self.assertIsNone(lost_index.held_replacement(journal, 0, original, {}))
            with patch.object(lost_index, "process_snapshot", return_value={43: replacement}):
                self.assertEqual({"pid": 43, "start": "replacement-start"}, lost_index.held_replacement(journal, 0, original, {}))
                self.assertIsNone(lost_index.held_replacement(journal, 0, original, {43: replacement}))
            journal.write_text('{"pid":43}\n{"pid":44}\n')
            with patch.object(lost_index, "process_snapshot", return_value={43: replacement, 44: {"pid": 44, "start": "ambiguous"}}):
                with self.assertRaisesRegex(ValueError, "LOST-INDEX-HELD-AMBIGUOUS"):
                    lost_index.held_replacement(journal, 0, original, {})
            journal.write_text('{"pid":\n')
            with self.assertRaises(json.JSONDecodeError):
                lost_index.complete_records(journal, 0)

    def test_runtime_failure_cannot_inherit_a_known_gap(self):
        lane = Path(__file__).resolve().parents[2] / "lanes/chat-reload.sh"
        for completed, result, expected in [(0, 1, ":1"), (1, 2, ":2"), (1, 0, ":0"), (1, 1, "gap:1")]:
            output = command(["bash", "-c", 'source "$1"; _reload_row_action() { GAP_SOURCE=gap; GAP_REASON=gap; RELOAD_GAP_COMPLETE="$2"; return "$3"; }; _reload_row row "$2" "$3"; rc=$?; printf "%s:%s" "$GAP_SOURCE" "$rc"',
                              "reload-gap", str(lane), str(completed), str(result)], env={**os.environ, "JOURNEY_OS": "linux"})
            with self.subTest(completed=completed, result=result):
                self.assertEqual(expected, output)

    def test_native_handoff_record_preserves_the_observed_edge(self):
        record = {"type": "continued-in", "sessionId": "old", "continuedInSessionId": "new",
                  "timestamp": "2026-09-18T08:50:25.188Z"}
        expected = [{"type": "continued-in", "sessionId": "old", "continuedInSessionId": "new"}]
        self.assertEqual(expected, continuation_records(json.dumps(record), "old"))
        self.assertEqual(expected, continuation_records("\n" + json.dumps(record) + "\n\n", "old"))
        self.assertEqual([], continuation_records(json.dumps(record), "foreign"))
        self.assertEqual([], continuation_records('{"type":"user"}', "old"))
        with self.assertRaisesRegex(ValueError, "HANDOFF-RECORD"):
            continuation_records("[]", "old")
        with self.assertRaisesRegex(ValueError, "HANDOFF-TIMESTAMP"):
            continuation_records(json.dumps({**record, "timestamp": "2026-09-18T08:50:25"}), "old")

    def test_native_prompt_slice_distinguishes_pending_wrong_and_delivered_input(self):
        lane = Path(__file__).resolve().parents[2] / "lanes/chat-reload.sh"
        with tempfile.TemporaryDirectory(prefix="reload-input-") as folder:
            root = Path(folder)
            records = root / "records"
            records.mkdir()
            (root / "chat.json").write_text(json.dumps({"records": str(records)}))
            environment = {**os.environ, "CHAT": str(root / "chat.json"), "ARTIFACT": str(root),
                           "JOURNEY_OS": "linux", "PROMPT_OFFSET": "9"}
            def read(phase, codes=(0,)):
                return command(["bash", "-c", 'source "$1"; _reload_prompt "$2"',
                                "reload-input", str(lane), phase], env=environment, allowed_codes=codes)
            self.assertEqual("0\n", read("before"))
            (records / "prompts").write_text("old turn\n")
            self.assertEqual("9\n", read("before"))
            self.assertEqual("", read("after", (1,)))
            (records / "prompts").write_text("old turn\n/reload\n")
            self.assertEqual("", read("after"))
            self.assertEqual("/reload\n", (root / "reload-input.txt").read_text())
            (records / "prompts").write_text("old turn\nwrong target\n")
            with self.assertRaisesRegex(RuntimeError, "RELOAD-INPUT: expected /reload, actual='wrong target'"):
                read("after")


if __name__ == "__main__":
    unittest.main()
