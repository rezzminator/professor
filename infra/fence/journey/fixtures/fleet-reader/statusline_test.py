#!/usr/bin/env python3
"""The statusline reader refuses stale, missing and ambiguous callback evidence."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("reader_statusline", Path(__file__).with_name("statusline.py"))
statusline = importlib.util.module_from_spec(spec)
spec.loader.exec_module(statusline)
from runtime import write_json


class StatuslineTests(unittest.TestCase):
    def receipt(self):
        return {"input": {"session_id": "fixture-session", "model": {"id": "claude-sonnet-4-5", "display_name": "Sonnet"},
                          "effort": {"level": ""}, "cost": {"total_cost_usd": 0.1201},
                          "context_window": {"used_percentage": 60},
                          "rate_limits": {"five_hour": {"used_percentage": 89, "resets_at": 4102444800},
                                          "seven_day": {"used_percentage": 34, "resets_at": 4102531200}}},
                "stdout": "\x1b[31m🥈 ◇ Sonnet\x1b[0m·effort unknown │ atlas\n💰$0.12/120.0K/0/0 │ ⏳ 0s\n5h-used:89% │ 7d-used:34%\n",
                "stderr": "", "pid": 4242, "exit_code": 0, "config_dir": "/fixture/account2",
                "argv": ["/bin/sh", "-c", "/fixture/pfm-statusline"]}

    def test_native_receipt_projects_exact_cells_and_retains_bad_windows(self):
        row = self.receipt()
        self.assertEqual({"model": ["🥈 ◇ Sonnet·effort unknown"], "cost": ["$0.12"],
                          "windows": {"5h": "89%", "7d": "34%"}}, statusline.cells(row["stdout"]))
        changed = row["stdout"].replace("5h-used:89%", "5h-used:ERR").replace("7d-used:34%", "7d-used:—")
        self.assertEqual({"5h": "ERR", "7d": "—"}, statusline.cells(changed)["windows"])
        self.assertEqual({"7d": "34%"}, statusline.cells(row["stdout"].replace("5h-used:89% │ ", ""))["windows"])

    def test_each_receipt_assertion_rejects_its_wrong_projection(self):
        row = self.receipt()
        chat, resources = {"session_id": "fixture-session"}, {"config_dir": "/fixture/account2", "statusline_command": "/fixture/pfm-statusline"}
        checks = statusline.receipt_checks(row, chat, resources, {"5h": "89%", "7d": "34%"})
        self.assertEqual([], [item for item in checks if item["expected"] != item["actual"]])
        variants = [(("input", "session_id"), "foreign", "native-session"),
                    (("config_dir",), "/fixture/account1", "native-account-directory"),
                    (("input", "model"), {"id": "haiku", "display_name": "Haiku"}, "native-model"),
                    (("input", "effort"), {"level": "high"}, "native-effort"),
                    (("input", "cost", "total_cost_usd"), 0, "native-turn-cost"),
                    (("input", "context_window", "used_percentage"), 0, "native-context"),
                    (("input", "rate_limits"), {}, "native-rate-limits"),
                    (("argv",), ["/bin/bash", "-c", "/fixture/pfm-statusline"], "callback-shell"),
                    (("argv",), ["/bin/sh", "-c", "printf invented"], "callback-command"),
                    (("exit_code",), 1, "callback-exit"),
                    (("stdout",), row["stdout"].replace("🥈", "🥇"), "account-model-effort-cell"),
                    (("stdout",), row["stdout"].replace("$0.12", "$0.00"), "turn-cost-cell"),
                    (("stdout",), row["stdout"].replace("7d-used:34%", "7d-used:61%"), "quota-windows")]
        for path, replacement, field in variants:
            with self.subTest(field=field):
                broken, target = copy.deepcopy(row), None
                target = broken
                for key in path[:-1]:
                    target = target[key]
                target[path[-1]] = replacement
                projected = {item["field"]: item for item in statusline.receipt_checks(broken, chat, resources, {"5h": "89%", "7d": "34%"})}
                self.assertNotEqual(projected[field]["expected"], projected[field]["actual"])

    def test_callback_cursor_rejects_truncation_and_shape_without_inventing_completion(self):
        with tempfile.TemporaryDirectory(prefix="statusline-receipts-") as directory:
            path = Path(directory) / "callbacks.jsonl"
            with self.assertRaisesRegex(ValueError, "^STATUSLINE-RECEIPTS-ABSENT:"):
                statusline.callbacks(path)
            prior = json.dumps(self.receipt()) + "\n"
            path.write_text(prior + json.dumps(self.receipt()) + "\n" + '{"input":')
            self.assertEqual([self.receipt()], statusline.callbacks(path, len(prior)))
            self.assertEqual(len(prior) * 2, statusline.callback_cursor(path))
            with self.assertRaisesRegex(ValueError, "^STATUSLINE-RECEIPTS-TRUNCATED:"):
                statusline.callbacks(path, path.stat().st_size + 1)
            for key, value in (("pid", 0), ("exit_code", "0"), ("input", "native"), ("stdout", None)):
                row = {**self.receipt(), key: value}
                path.write_text(json.dumps(row) + "\n")
                with self.subTest(key=key), self.assertRaisesRegex(ValueError, "^STATUSLINE-RECEIPT-SHAPE:"):
                    statusline.callbacks(path)

    def test_refresh_window_keeps_every_first_callback_after_settlement(self):
        with tempfile.TemporaryDirectory(prefix="statusline-window-") as directory:
            path = Path(directory) / "callbacks.jsonl"
            rows = [dict(self.receipt(), pid=4242 + index) for index in range(11)]
            rows[0]["stdout"] = rows[0]["stdout"].replace("5h-used:89%", "5h-used:ERR")
            path.write_text("".join(json.dumps(row) + "\n" for row in rows))
            with patch.object(statusline, "wait", side_effect=lambda label, predicate, bound: predicate()):
                observed = statusline.ready_rows(path, 0, {"session_id": "fixture-session"}, {"5h": "89%", "7d": "34%"}, count=10, settle=False)
                self.assertEqual(list(range(4242, 4252)), [row["pid"] for row in observed])
                self.assertEqual({"5h": "ERR", "7d": "34%"}, statusline.cells(observed[0]["stdout"])["windows"])
                settled = statusline.ready_rows(path, 0, {"session_id": "fixture-session"}, {"5h": "89%", "7d": "34%"})
                self.assertEqual(4252, settled[0]["pid"])

    def test_ten_refreshes_prove_live_daemon_birth_and_real_child_reaping(self):
        before = {100: {"parent": 1, "start": "birth", "state": "S"}}
        after = {**before, 101: {"parent": 100, "start": "worker", "state": "S"}}
        daemon = {"pid": 100, "start": "birth"}
        rows = [dict(self.receipt(), pid=4242 + index) for index in range(10)]
        refreshes = [{"pid": 5000 + index, "parent": 100, "start": "refresh", "argv": "/fixture/pfm statusline --refresh-gpt"} for index in range(10)]
        checks = statusline.reaping_checks(rows, daemon, before, after, refreshes)
        self.assertEqual([], [item for item in checks if item["expected"] != item["actual"]])
        for count in (9, 10, 11):
            with self.subTest(count=count):
                checks = {item["field"]: item for item in statusline.reaping_checks((rows + rows)[:count], daemon, before, after, refreshes)}
                self.assertEqual(count, checks["ten-completed-callbacks"]["actual"])
        broken = {**after, 4242: {"parent": 100, "start": "callback", "state": "Z"}}
        checks = {item["field"]: item for item in statusline.reaping_checks(rows, daemon, before, broken, refreshes)}
        self.assertEqual([4242], checks["completed-callback-children-reaped"]["actual"])
        self.assertEqual([4242], checks["daemon-zombie-children"]["actual"])
        checks = {item["field"]: item for item in statusline.reaping_checks(rows, {"pid": 100, "start": "old"}, before, after, refreshes)}
        self.assertEqual("birth", checks["daemon-birth-after"]["actual"])
        bad_refreshes = [{**refreshes[0], "parent": 999}, *refreshes[1:]]
        checks = {item["field"]: item for item in statusline.reaping_checks(rows, daemon, before, after, bad_refreshes)}
        self.assertEqual([999] + [100] * 9, checks["refresh-child-parents"]["actual"])
        checks = {item["field"]: item for item in statusline.reaping_checks(rows, daemon, before, after, refreshes[:-1])}
        self.assertEqual(9, checks["ten-actual-refresh-children"]["actual"])
        bad_argv = [{**refreshes[0], "argv": "/fixture/pfm unrelated"}, *refreshes[1:]]
        checks = {item["field"]: item for item in statusline.reaping_checks(rows, daemon, before, after, bad_argv)}
        self.assertEqual(["/fixture/pfm", "unrelated"], checks["refresh-child-argv"]["actual"][0])
        repeated = [*refreshes[:-1], refreshes[0]]
        checks = {item["field"]: item for item in statusline.reaping_checks(rows, daemon, before, after, repeated)}
        self.assertEqual(9, checks["refresh-child-unique-births"]["actual"])
        alive = {**after, 5000: {**refreshes[0], "state": "Z"}}
        checks = {item["field"]: item for item in statusline.reaping_checks(rows, daemon, before, alive, refreshes)}
        self.assertEqual([5000], checks["refresh-children-reaped"]["actual"])
        with patch.object(statusline, "process_snapshot", side_effect=[alive, after]):
            self.assertEqual(after, statusline.reaped_snapshot(refreshes), "delayed OS reaping must settle before the final snapshot")
        with patch.object(statusline, "process_snapshot", return_value=alive):
            final = statusline.reaped_snapshot(refreshes, bound=0)
            self.assertEqual(alive, final, "a deadline must retain the actual zombie snapshot")
            checks = {item["field"]: item for item in statusline.reaping_checks(rows, daemon, before, final, refreshes)}
            self.assertEqual([5000], checks["refresh-children-reaped"]["actual"])
        with patch.object(statusline, "process_snapshot", side_effect=ValueError("OBSERVE-PROCESS-SHAPE: synthetic")):
            with self.assertRaisesRegex(ValueError, "^OBSERVE-PROCESS-SHAPE: synthetic$"):
                statusline.reaped_snapshot(refreshes)
        for missing in ({}, {100: before[100], 101: {"parent": 100, "start": "worker"}}):
            with self.subTest(snapshot=missing), self.assertRaisesRegex(ValueError, "^STATUSLINE-(DAEMON-ABSENT|PROCESS-STATE-UNAVAILABLE):"):
                statusline.reaping_checks(rows, daemon, before, missing, refreshes)
        with self.assertRaisesRegex(ValueError, "^STATUSLINE-CALLBACKS-EMPTY:"):
            statusline.reaping_checks([], daemon, before, after, refreshes)

    def test_warning_requires_the_exact_visible_cause_from_its_own_file(self):
        path = "/fixture/state.session.json"
        cause = 'statusline: auto-effort state /fixture/state.session.json (effort shows ERR): parse "{": unexpected end of JSON input'
        records = [{"level": "WARN", "msg": "statusline.warn", "err": cause}, {"level": "WARN", "msg": "statusline.warn", "err": "foreign cause"}]
        self.assertEqual([{ "field": "visible-ERR-cause", "expected": [["WARN", "statusline.warn", cause]], "actual": [["WARN", "statusline.warn", cause]]}], statusline.warning_checks(records, path))
        self.assertEqual([], statusline.warning_checks(records, "/foreign/state.json")[0]["actual"])
        self.assertEqual([], statusline.warning_checks([{**records[0], "msg": "other-warning"}], path)[0]["actual"])

    def test_fault_door_restores_the_real_effort_and_cache_files(self):
        with tempfile.TemporaryDirectory(prefix="statusline-inputs-") as directory:
            root = Path(directory)
            effort, cache = root / "state.json", root / "acct-2.json"
            effort.write_text("original-effort\n")
            cache.write_text("original-cache\n")
            with patch.dict(os.environ, {"JOURNEY_ROOT": directory, "HOME": str(root / "home")}):
                with self.assertRaisesRegex(RuntimeError, "fixture interruption"):
                    with statusline.faults.temporary_text(effort, "{"), statusline.faults.temporary_text(cache, '{"seven_day":{"utilization":89}}'):
                        self.assertEqual("{", effort.read_text())
                        self.assertEqual({"seven_day": {"utilization": 89}}, json.loads(cache.read_text()))
                        raise RuntimeError("fixture interruption")
                self.assertEqual("original-effort\n", effort.read_text())
                self.assertEqual("original-cache\n", cache.read_text())

    def test_seed_and_worker_selection_use_one_owned_native_scenario(self):
        with tempfile.TemporaryDirectory(prefix="statusline-seed-") as directory:
            root = Path(directory)
            with patch.dict(os.environ, {"JOURNEY_ROOT": directory, "HOME": str(root / "home"), "PFM_RUN": "fixture-run"}):
                prefix = str(root / "statusline")
                statusline.seed(prefix)
                self.assertEqual({"model": "claude-sonnet-4-5", "steps": [],
                                  "tokens": {"input": 120000, "output": 0, "cache_read": 0, "cache_creation": 0, "context_window": 200000},
                                  "idle_statusline_refresh": {"count": 1800, "interval_ms": 100, "stop_when_exists": prefix + ".idle-stop"},
                                  "app_server_stdout_hold": prefix + ".refresh-release"}, json.loads(Path(prefix + ".seed.json").read_text()))
                self.assertEqual("release\n", Path(prefix + ".refresh-release").read_text())
                processes = {10: {"pid": 10, "parent": 1, "start": "worker", "state": "S", "argv": "/fixture/pfm statusline --paced key"},
                             20: {"pid": 20, "parent": 1, "start": "foreign", "state": "S", "argv": "/fixture/pfm statusline --paced foreign"},
                             30: {"pid": 30, "parent": 1, "start": "other", "argv": "unrelated unclosed'"}}
                values = {10: {"PFM_RUN": "fixture-run", "MOCK_ENGINE_SCENARIO": "/fixture/scenario.json"},
                          20: {"PFM_RUN": "foreign-run", "MOCK_ENGINE_SCENARIO": "/fixture/scenario.json"}}
                with patch.object(statusline, "process_snapshot", return_value=processes), patch.object(statusline, "environment", side_effect=values.__getitem__):
                    self.assertEqual({**processes[10], "key": "key", "scenario": "/fixture/scenario.json"}, statusline.paced_worker({"scenario": "/fixture/scenario.json"}))
                    self.assertEqual(False, statusline.paced_worker({"scenario": "/fixture/other.json"}))
                    values[20]["PFM_RUN"] = "fixture-run"
                    with self.assertRaisesRegex(ValueError, "^STATUSLINE-PACED-WORKER-AMBIGUOUS:"):
                        statusline.paced_worker({"scenario": "/fixture/scenario.json"})

    def test_resources_bind_native_account_and_only_prepare_owned_input_directories(self):
        with tempfile.TemporaryDirectory(prefix="statusline-resources-") as directory:
            root = Path(directory)
            home, records, tmp = root / "home", root / "records", root / "tmp"
            for path in (home / ".local/state", home / ".cc/2", home / "tmp", records, tmp):
                path.mkdir(parents=True, exist_ok=True)
            session = "11111111-1111-4111-8111-111111111111"
            native = self.receipt()
            native["input"]["session_id"] = session
            native["config_dir"] = str(home / ".cc/2")
            (records / "statusline-callbacks.jsonl").write_text(json.dumps(native) + "\n")
            scenario = root / "scenario.json"
            write_json(scenario, {"app_server_stdout_hold": str(root / "refresh-release"),
                                  "idle_statusline_refresh": {"stop_when_exists": str(root / "idle-stop")}})
            (root / "refresh-release").write_text("release\n")
            chat = root / "chat.json"
            write_json(chat, {"engine": "claude", "account": 2, "session_id": session, "records": str(records), "scenario": str(scenario)})
            values = {"JOURNEY_ROOT": directory, "HOME": str(home), "TMPDIR": str(tmp),
                      "AUTO_EFFORT_STATE_FILE": "", "AUTO_EFFORT_CONTROL_STATE_FILE": ""}
            for jailed, cache in ((str(home), home / "tmp"), ("", tmp)):
                with self.subTest(jailed=jailed), patch.dict(os.environ, {**values, "PFM_HOME": jailed}), patch.object(statusline.os, "getuid", return_value=1000), \
                        patch.object(statusline, "account_stats_cache", return_value={"2": {"path": str(home / ".cc/2/stats-cache.json")}}):
                    prefix = str(root / "result")
                    statusline.resources(str(chat), prefix)
                    actual = json.loads(Path(prefix + ".resources.json").read_text())
                    self.assertEqual({"config_dir": str(home / ".cc/2"), "statusline_command": str(home / ".local/bin/pfm-statusline"),
                                      "effort_state": str(home / ".local/state/auto-effort" / ("state." + session + ".json")),
                                      "usage_cache": str(cache / "cc-usage-1000/acct-2.json"),
                                      "codex_cache": str(cache / "cc-gpt-usage-1000.json"),
                                      "refresh_gate": str(root / "refresh-release"), "idle_stop": str(root / "idle-stop")}, actual)
                    self.assertEqual([{"field": "native-account2-input", "expected": str(home / ".cc/2"), "actual": str(home / ".cc/2")}],
                                     json.loads(Path(prefix + ".resources-checks.json").read_text()))
                    self.assertEqual(True, Path(actual["effort_state"]).parent.is_dir())
                    self.assertEqual(False, Path(actual["effort_state"]).exists())
                    self.assertEqual(True, Path(actual["usage_cache"]).parent.is_dir())
                    self.assertEqual(False, Path(actual["usage_cache"]).exists())

    def test_refresh_child_scopes_age_real_inputs_and_release_before_waiting(self):
        with tempfile.TemporaryDirectory(prefix="statusline-refresh-") as directory:
            root = Path(directory)
            cache, gate = root / "codex.json", root / "release"
            attempt = root / "codex.json.attempt"
            for path in (cache, gate, attempt):
                path.write_text("original\n")
            before = {path: path.stat().st_mtime_ns for path in (cache, attempt)}
            worker = {"pid": 100, "start": "worker", "parent": 1, "state": "S", "argv": "/fixture/pfm statusline --paced key"}
            phase, count = "born", 0
            def snapshot():
                nonlocal phase, count
                if phase == "born":
                    self.assertEqual(False, gate.exists())
                    for path in (cache, attempt):
                        self.assertGreater(statusline.now_ms() - path.stat().st_mtime_ns / 1_000_000, 300000)
                    born = {"pid": 5000 + count, "parent": 100, "start": "refresh", "state": "S", "argv": "/fixture/pfm statusline --refresh-gpt"}
                    count += 1
                    phase = "completed"
                    return {100: worker, born["pid"]: born}
                self.assertEqual("original\n", gate.read_text())
                phase = "born"
                return {100: worker}
            def ready(label, predicate, bound):
                value = predicate()
                if not value:
                    self.fail("named condition unexpectedly pending: " + label)
                return value
            with patch.dict(os.environ, {"JOURNEY_ROOT": directory, "HOME": str(root / "home"), "PFM_RUN": "fixture-run"}), \
                    patch.object(statusline, "process_snapshot", side_effect=snapshot), \
                    patch.object(statusline, "paced_workers", return_value=[worker]), \
                    patch.object(statusline, "environment", return_value={"PFM_RUN": "fixture-run"}), patch.object(statusline, "wait", side_effect=ready):
                births = statusline.refresh_children({}, {"codex_cache": str(cache), "refresh_gate": str(gate)}, worker, str(root / "result"))
            self.assertEqual(list(range(5000, 5010)), [row["pid"] for row in births])
            self.assertEqual([100] * 10, [row["parent"] for row in births])
            self.assertEqual(births, json.loads((root / "result.refresh-births.json").read_text()))
            self.assertEqual(before, {path: path.stat().st_mtime_ns for path in (cache, attempt)})
            self.assertEqual("original\n", gate.read_text())
            phase, count = "born", 0
            with patch.dict(os.environ, {"JOURNEY_ROOT": directory, "HOME": str(root / "home"), "PFM_RUN": "fixture-run"}), \
                    patch.object(statusline, "process_snapshot", side_effect=snapshot), \
                    patch.object(statusline, "paced_workers", return_value=[worker]), \
                    patch.object(statusline, "environment", return_value={"PFM_RUN": "foreign-run"}), patch.object(statusline, "wait", side_effect=ready):
                with self.assertRaisesRegex(ValueError, "^STATUSLINE-REFRESH-CHILD-FOREIGN"):
                    statusline.refresh_children({}, {"codex_cache": str(cache), "refresh_gate": str(gate)}, worker, str(root / "foreign"))
            self.assertEqual(before, {path: path.stat().st_mtime_ns for path in (cache, attempt)})
            self.assertEqual("original\n", gate.read_text())

    def test_complete_run_neutralizes_prior_cache_and_restores_it_on_failure(self):
        with tempfile.TemporaryDirectory(prefix="statusline-run-") as directory:
            root = Path(directory)
            cache, inputs = root / "acct-2.json", root / "resources.json"
            cache.write_text("prior-account-reading\n")
            stop = root / "idle-stop"
            write_json(inputs, {"usage_cache": str(cache), "config_dir": "/fixture/account2", "idle_stop": str(stop)})
            def rendering(*arguments):
                self.assertEqual(("chat", "target", str(inputs), "prefix"), arguments)
                self.assertEqual({"config_dir": "/fixture/account2", "fetched_at": "2023-11-14T22:13:20+00:00",
                                  "five_hour": {"utilization": 89, "resets_at": "2100-01-01T00:00:00Z"},
                                  "seven_day": {"utilization": 34, "resets_at": "2100-01-02T00:00:00Z"}}, json.loads(cache.read_text()))
                raise RuntimeError("fixture interrupted")
            with patch.dict(os.environ, {"JOURNEY_ROOT": directory, "HOME": str(root / "home")}), \
                    patch.object(statusline, "now_ms", return_value=1700000000000), \
                    patch.object(statusline, "render_checks", side_effect=rendering):
                with self.assertRaisesRegex(RuntimeError, "fixture interrupted"):
                    statusline.run("chat", "target", str(inputs), "prefix")
            self.assertEqual("prior-account-reading\n", cache.read_text())
            self.assertEqual("complete\n", stop.read_text() if stop.exists() else "n/a:missing", str(stop))


if __name__ == "__main__":
    unittest.main()
