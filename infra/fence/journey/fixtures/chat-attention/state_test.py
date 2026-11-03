import unittest
import json
import os
from pathlib import Path
import tempfile
from subprocess import PIPE

from state import (accounting, cadence, codex_hook, mark, native_projection,
                   new_agents, phases, tool_output)
from previous_settings import previous_settings
from indicator_live import selected
from unittest.mock import patch
from runtime import command, read_json, start, write_json
from bootstrap import wait


class IndicatorPhases(unittest.TestCase):
    maxDiff = None

    def test_engine_birth_receipt_includes_the_actual_shield_child(self):
        with tempfile.TemporaryDirectory(prefix="attention-births-") as directory:
            source, output = Path(directory) / "terminal.json", Path(directory) / "births.json"
            write_json(source, {"identity": {"pane": "fixture-pane"}, "births": [
                {"pid": 11, "start": "holder", "chain": [{"argv": "shield-holder"}], "environment": {}}],
                "shield": {"births": [{"pid": 12, "start": "engine", "chain": [{"argv": "mock-engine --resume actual"}],
                                         "environment": {"CLAUDE_CONFIG_DIR": "/fixture/account"}}]}})
            command(["python3", "-B", str(Path(__file__).with_name("state.py")), "engine-births", str(source), str(output)])
            self.assertEqual([{"pid": 12, "start": "engine", "argv": ["mock-engine", "--resume", "actual"],
                               "environment": {"CLAUDE_CONFIG_DIR": "/fixture/account"},
                               "terminal": {"pane": "fixture-pane"}}], read_json(output))

    def test_indicator_accounting_binds_birth_window_and_strict_limits(self):
        process = {"pid": 42, "start": "actual-start", "argv": "actual-painter"}
        with patch("indicator_live.process_snapshot", return_value={42: process, 43: {"pid": 43}}), \
                patch("indicator_live.indicator_daemon", return_value="actual-observer-receipt") as observer:
            self.assertEqual("actual-observer-receipt", selected(42))
            observer.assert_called_once_with(snapshot={42: process})
            observer.reset_mock()
            selected(44)
            observer.assert_called_once_with(snapshot={})
        before = {"pid": 42, "start": "actual-start", "cpu_seconds": 0, "write_bytes": 0}
        for cpu, written, expected in [
                (1.499, 4999999, {"same_birth": True, "cpu_under_limit": True, "write_under_limit": True}),
                (1.5, 5000000, {"same_birth": True, "cpu_under_limit": False, "write_under_limit": False}),
                (1.501, 5000001, {"same_birth": True, "cpu_under_limit": False, "write_under_limit": False})]:
            with self.subTest(cpu=cpu, written=written):
                self.assertEqual(expected, accounting(before, {**before, "cpu_seconds": cpu,
                                                               "write_bytes": written}, 15000))
        self.assertEqual({"same_birth": False, "cpu_under_limit": True, "write_under_limit": True},
                         accounting(before, {**before, "start": "reused-pid"}, 16000))
        for duration, exception, message in [(14999, ValueError, "ATTENTION-ACCOUNTING-WINDOW"),
                                              (16001, RuntimeError, "CONTENTION")]:
            with self.subTest(duration=duration), self.assertRaisesRegex(exception, message):
                accounting(before, before, duration)
        with self.assertRaisesRegex(ValueError, "ATTENTION-ACCOUNTING-UNREADABLE"):
            accounting(before, {**before, "cpu_seconds": "n/a:unsupported-platform"}, 15000)
        with self.assertRaisesRegex(ValueError, "ATTENTION-ACCOUNTING-COUNTER-RESET"):
            accounting(before, {**before, "write_bytes": -1}, 15000)

    def test_native_detector_preserves_actual_fields_and_working_state(self):
        observed = {"agent": "claude", "manifest_source": "bundled", "state": "working",
                    "matched_rule": {"id": "actual-rule"}, "visible_idle": False,
                    "screen_detection_skipped": True}
        self.assertEqual({"agent": "claude", "manifest_source": "bundled", "state": "working",
                          "matched_rule": {"id": "actual-rule"}, "visible_idle": False,
                          "screen_detection_skipped": True, "claude_working": True}, native_projection(observed))
        self.assertEqual({"agent": "claude", "manifest_source": "bundled", "state": "idle",
                          "matched_rule": {"id": "actual-rule"}, "visible_idle": False,
                          "screen_detection_skipped": True, "claude_working": False},
                         native_projection({**observed, "state": "idle"}))
        with self.assertRaises(KeyError):
            native_projection({"agent": "claude"})

    def test_previous_install_bytes_are_scoped_and_provenance_checked(self):
        with tempfile.TemporaryDirectory(prefix="attention-previous-") as directory:
            root = Path(directory)
            source = root / "home/.cc/1/settings.json"
            source.parent.mkdir(parents=True)
            source.write_text("current settings\n")
            source.chmod(0o600)
            artifact = root / "previous-install/1/settings.json"
            artifact.parent.mkdir(parents=True)
            artifact.write_text("a")
            artifact.chmod(0o400)
            provenance = root / "previous-install/provenance.json"
            digest = "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"
            write_json(provenance, {"pfm_version": "pfm 0.81.0", "pfm_sha256": "recorded-binary",
                                   "accounts": {"1": {"source": str(source), "artifact": str(artifact),
                                                       "sha256": digest, "mode": 0o600}}})
            with patch.dict(os.environ, {"JOURNEY_ROOT": str(root), "HOME": str(root / "home")}):
                for interrupted in (False, True):
                    with self.subTest(interrupted=interrupted):
                        try:
                            with previous_settings(root) as actual:
                                self.assertEqual({"account": "1", "source": str(source),
                                                  "artifact": str(artifact), "sha256": digest,
                                                  "pfm_version": "pfm 0.81.0", "pfm_sha256": "recorded-binary"}, actual)
                                self.assertEqual(b"a", source.read_bytes())
                                if interrupted:
                                    raise RuntimeError("interrupted fixture")
                        except RuntimeError as error:
                            self.assertEqual("interrupted fixture", str(error))
                        self.assertEqual(b"current settings\n", source.read_bytes())
                        self.assertEqual(0o600, source.stat().st_mode & 0o777)
                artifact.chmod(0o600)
                with self.assertRaisesRegex(ValueError, "ATTENTION-PREVIOUS-PROVENANCE"):
                    with previous_settings(root):
                        self.fail("writable prior-install artifact accepted")
                artifact.write_text("wrong prior bytes")
                artifact.chmod(0o400)
                with self.assertRaisesRegex(ValueError, "ATTENTION-PREVIOUS-INPUT"):
                    with previous_settings(root):
                        self.fail("changed prior-install artifact accepted")

    def test_actual_tool_receipt_distinguishes_pending_and_ambiguous_output(self):
        with tempfile.TemporaryDirectory(prefix="attention-tool-") as directory:
            path = Path(directory) / "shell.jsonl"
            self.assertIsNone(tool_output(path, "tool"))
            row = {"command": "tool", "cwd": "/fixture/atlas", "exit_code": 0, "stdout": "a", "stderr": ""}
            path.write_text(json.dumps(row))
            self.assertIsNone(tool_output(path, "tool"))
            path.write_text(json.dumps(row) + "\n")
            self.assertEqual({"command": "tool", "cwd": "/fixture/atlas", "exit_code": 0, "stderr": "",
                              "stdout_sha256": "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"},
                             tool_output(path, "tool"))
            path.write_text((json.dumps(row) + "\n") * 2)
            with self.assertRaisesRegex(ValueError, "ATTENTION-TOOL-RECEIPT: expected one command, got 2"):
                tool_output(path, "tool")

    def test_codex_hook_uses_the_actual_child_and_parent(self):
        child = {"id": "native-child", "path": "/fixture/rollout-child.jsonl", "parent_thread_id": "native-parent"}
        self.assertEqual([{"type": "hook", "event": "SessionStart", "payload": {
            "hook_event_name": "SessionStart", "session_id": "native-child",
            "transcript_path": "/fixture/rollout-child.jsonl", "cwd": "/fixture/atlas", "source": "startup"}},
            {"type": "question"}], codex_hook(child, "native-parent", "/fixture/atlas", [{"type": "question"}]))
        with self.assertRaisesRegex(ValueError, "ATTENTION-CODEX-CHILD: actual parent differs"):
            codex_hook(child, "different-parent", "/fixture/atlas", [])

    def test_action_error_cannot_be_reclassified_as_a_prior_blink_gap(self):
        lane = Path(__file__).resolve().parents[2] / "lanes/chat-attention.sh"
        capture = {}
        with tempfile.TemporaryDirectory(prefix="attention-control-") as directory:
            (Path(directory) / "branches/linux+tmux+project+codex/forecast-legacy-codex").mkdir(parents=True)
            result = command(["bash", "-c", '''source "$1"
lane_gap_only() { return 0; }
_attention_dispatch() { GAP_SOURCE=product-gap:working-blink; return 2; }
_attention_row action '' '' ''; rc=$?
printf '%s|%s\\n' "$rc" "$GAP_SOURCE"
_attention_dispatch() { GAP_SOURCE=product-gap:working-blink; return 0; }
_attention_row action '' '' ''; rc=$?
printf '%s|%s\\n' "$rc" "$GAP_SOURCE"
_attention_indicator_project() { return 2; }
_attention_indicator_cleanup() { printf 'cleanup\\n'; }
_attention_indicator_actions owned-setup; rc=$?
printf 'result=%s\\n' "$rc"
launch_receipt() {
  printf 'launch-argv='
  command python3 -c 'import json,sys; print(json.dumps(sys.argv[1:]))' "$@"
}
python3() { case "$2" in receipt|command) printf 'launch-door=%s\\n' "$2"; launch_receipt "${@:4}" ;; esac; }
poll() { return 0; }
JOURNEY_BRANCH=linux+herdr-shield+project+codex JOURNEY_ROOT="$ATTENTION_CONTROL_ROOT" PFM_RUN=fixture
_attention_prepare_legacy
poll() { return 1; }
python3() {
  case "$2" in
    receipt|command) printf 'launch-door=%s\\n' "$2"; launch_receipt "${@:4}" ;;
    read) printf '{"backend":"herdr","text":"actual fixture frame"}\\n' ;;
  esac
}
_attention_prepare_legacy; printf 'capture-result=%s\\n' "$?"
printf 'diagnostic='; cat "$JOURNEY_ROOT/branches/$LEGACY_BRANCH/forecast-legacy-codex/launch-display.json"
python3() {
  case "$2" in
    receipt|command) printf 'launch-door=%s\\n' "$2"; launch_receipt "${@:4}" ;;
    read) printf 'fixture viewport unreadable\\n' >&2; return 2 ;;
  esac
}
_attention_prepare_legacy; printf 'capture-result=%s\\n' "$?"
''', "attention-classification", str(lane)], capture=capture,
                             env={**os.environ, "JOURNEY_OS": "linux", "ATTENTION_CONTROL_ROOT": directory})
        launch = "launch-door=command\nlaunch-argv=" + json.dumps([
            "pfm", "chat", "new", "--engine", "cx", "--backend", "tmux", "--attach", "--account", "1",
            "--cwd", str(Path(os.environ["HOME"]) / "work/atlas"), "--name", "journey-legacy-codex-fixture"]) + "\n"
        self.assertEqual('2|\n1|product-gap:working-blink\ncleanup\nresult=2\n' + launch + launch
                         + 'capture-result=1\ndiagnostic={"backend":"herdr","text":"actual fixture frame"}\n'
                         + launch + 'capture-result=1\n', result)
        self.assertEqual("fixture viewport unreadable\nATTENTION-CAPTURE-DIAGNOSTIC-UNREADABLE: exit=2 artifact="
                         + directory + "/branches/linux+tmux+project+codex/forecast-legacy-codex/launch-display.json\n",
                         capture["stderr"])

    def test_generated_agent_comes_from_new_engine_sidechain(self):
        before = {"subagents": [{"agent_id": "previous-researcher"}]}
        after = {"subagents": [{"agent_id": "new-researcher"}, {"agent_id": "previous-researcher"}]}
        self.assertEqual(["new-researcher"], new_agents(before, after))
        self.assertEqual([], new_agents(before, before))

    def test_missing_phase_requires_bounded_observation_cadence(self):
        samples = [{"started_ms": value, "observed_ms": value} for value in range(0, 1001, 250)]
        self.assertEqual({"span_ms": 1000, "max_gap_ms": 250, "max_read_ms": 0}, cadence(samples))
        for actual in (samples[:-1], [samples[0], samples[-1]],
                       [{**sample, "observed_ms": sample["observed_ms"] + 251} for sample in samples]):
            with self.subTest(actual=actual), self.assertRaisesRegex(RuntimeError, "CONTENTION: indicator sampling"):
                cadence(actual)

    def test_nested_agent_prompt_binds_the_actual_hold_gate(self):
        journey = Path(__file__).resolve().parents[2]
        with tempfile.TemporaryDirectory(prefix="attention-bind-") as directory:
            chat, output = Path(directory) / "chat.json", Path(directory) / "bound.json"
            hold = Path(directory) / 'quoted " hold'
            write_json(chat, {"session_id": "fixture-session"})
            environment = {**os.environ, "CHAT": str(chat), "HOLD": str(hold), "MAIN_STOP_HOLD": ""}
            environment.pop("HERE", None)

            def bind(fixture):
                command(["bash", "-c", 'HERE="$1"; source "$2"; lane_bind "$3" "$4"',
                         "attention-bind", str(journey), str(journey / "fixtures/chat-reload/flow.sh"),
                         str(fixture), str(output)], env=environment)
                return read_json(output)

            bind(journey / "fixtures/chat-attention/agent.json")
            prompt = read_json(output)[0]["input"]["prompt"]
            self.assertEqual([{"type": "hold", "until_gone": str(hold)},
                              {"type": "turn", "reply": "research delivered", "busy_ms": 1}],
                             json.loads(prompt.split(" mock-engine: ", 1)[1]))
            main_stop = Path(directory) / "main-stop.hold"
            environment["MAIN_STOP_HOLD"] = str(main_stop)
            prompt = bind(journey / "fixtures/chat-attention/previous-agent.json")[0]["input"]["prompt"]
            child_steps = json.loads(prompt.split(" mock-engine: ", 1)[1])
            self.assertEqual({"type": "hold", "until_gone": str(main_stop)}, child_steps[0])
            self.assertEqual({"type": "hold", "until_gone": str(hold)}, child_steps[2])
            self.assertEqual({"file_path": "research.txt", "content": "research approved\n"}, child_steps[1]["input"])
            fixture = Path(directory) / "background.json"
            write_json(fixture, [{"type": "background_agent", "input": {"command": "$BACKGROUND_COMMAND"}}])
            held_command = bind(fixture)[0]["input"]["command"]
            hold.write_text("held\n")
            receipt = Path(directory) / "background.pid"
            with start(["bash", "-c", held_command], stdout=PIPE, stderr=PIPE, text=True) as child:
                try:
                    wait("actual background shell receipt", lambda: receipt.exists() and receipt.read_text(), 5)
                    self.assertEqual(str(child.pid) + "\n", receipt.read_text())
                    self.assertIsNone(child.poll())
                finally:
                    hold.unlink()
                self.assertEqual(("", ""), child.communicate(timeout=5))
                self.assertEqual(0, child.returncode)

    def test_tmux_name_cannot_be_credited_as_a_state_mark(self):
        for actual, name, expected in [("atlas", "atlas", "absent"),
                                       ("[W] atlas", "atlas", "unclassified:[W] atlas"),
                                       ("界" * 24, "界" * 25, "absent")]:
            with self.subTest(actual=actual):
                self.assertEqual({"window_name": actual, "mark": expected}, mark(actual, name))

    def test_phases_preserve_actual_state_and_unrelated_token(self):
        for samples, expected in [
            ([{"state": "blocked", "tokens": "pfm_blocked"}, {"state": "blocked", "tokens": "none"}],
             {"states": ["blocked"], "phases": ["none", "pfm_blocked"]}),
            ([{"state": "working", "tokens": "pfm_working"}],
             {"states": ["working"], "phases": ["pfm_working"]}),
            ([{"state": "working", "tokens": "pfm_working"}, {"state": "idle", "tokens": "pfm_blocked"}],
             {"states": ["idle", "working"], "phases": ["pfm_blocked", "pfm_working"]}),
        ]:
            with self.subTest(samples=samples):
                self.assertEqual(expected, phases(samples))
        with self.assertRaisesRegex(ValueError, "ATTENTION-PHASES: no terminal samples"):
            phases([])


if __name__ == "__main__":
    unittest.main()
