#!/usr/bin/env python3
"""Behaviour checks for the harness; run only through the repository fence."""
import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from journey_path import JOURNEY as HERE
import activity
import beatstate
import isolation
import observation
import terminal
import terminal_pty
from bootstrap import wait
from runtime import command, read_json, write_json


class JourneyHarness(unittest.TestCase):
    def owner_beat(self, root):
        log = root / "home/.local/state/pfm/log/pfm.jsonl"
        log.parent.mkdir(parents=True, exist_ok=True)
        log.write_text("")
        Path(str(log) + ".lock").touch()
        write_json(root / "header.json", {"start_ms": 0})
        (root / "ledger.tsv").write_text("")
        (root / "gaps.tsv").write_text("gap\tbeat\tstatus\n")
        target = {"backend": "tmux", "socket": str(root / "tmux.sock"), "pane": "%0"}
        write_json(root / "target.json", target)
        self.enterContext(patch.dict(os.environ, {"JOURNEY_ROOT": str(root), "JOURNEY_OUT": str(root),
            "JOURNEY_LANE": "fleet-reader", "JOURNEY_BRANCH": "linux+tmux", "JOURNEY_STATE_LANE": "fleet-reader",
            "JOURNEY_ACTIVITY": str(log), "JOURNEY_DESIGN": str(root), "PFM_RUN": "synthetic",
            "PFM_BEAT_FILE": str(root / "beat.json"), "PFM_BEAT": "fleet-reader/read-list@linux+tmux",
            "JOURNEY_TARGET": str(root / "target.json")}))
        beatstate.start("read-list", "chat.ls-cli")
        return target, Path(beatstate.load()["artifacts"]) / "owner"

    def test_owner_receipts_require_the_current_completed_dispatch(self):
        root = Path(self.enterContext(tempfile.TemporaryDirectory(prefix="owner-receipt-")))
        target, prefix = self.owner_beat(root)
        with patch.object(terminal, "text"), patch.object(terminal, "keys", side_effect=RuntimeError("send failed")):
            with self.assertRaisesRegex(RuntimeError, "send failed"):
                terminal.type_command(target, ["pfm", "ls"], str(prefix))
        self.assertEqual({}, beatstate.load().get("dispatches", {}))
        self.assertEqual(False, Path(str(prefix) + ".dispatch.json").exists())
        with patch.object(terminal, "text") as text, patch.object(terminal, "keys") as keys:
            terminal.type_command(target, ["pfm", "ls"], str(prefix))
            self.assertEqual(["enter"], keys.call_args.args[1])
        proof_path = Path(str(prefix) + ".dispatch.json")
        proof = read_json(proof_path)
        self.assertEqual(text.call_args.args[1], proof["line"])
        self.assertEqual(["pfm", "ls"], proof["argv"])
        self.assertEqual(0o400, proof_path.stat().st_mode & 0o777)
        output, status = Path(str(prefix) + ".out"), Path(str(prefix) + ".status")
        output.write_text("fixture-list\n")
        status.write_text("0\n")
        state = beatstate.load()
        for argv, accepted in ((["claude", "--version"], True), (["claude"], False),
                               (["claude", "--version", "extra"], False), (["claude", "--help"], False),
                               (["claude-other", "--version"], False)):
            with self.subTest(argv=argv):
                changed = {**proof, "argv": argv}
                write_json(proof_path, changed)
                altered = {**state, "dispatches": {str(prefix): changed}}
                literal = json.dumps(argv, separators=(",", ":"))
                if accepted:
                    self.assertEqual(argv, beatstate.owner_receipt(altered, str(prefix), literal)["dispatch"]["argv"])
                else:
                    with self.assertRaisesRegex(ValueError, "OWNER-INPUT-COMMAND"):
                        beatstate.owner_receipt(altered, str(prefix), literal)
        write_json(proof_path, proof)
        source = "owner-input:" + str(prefix)
        self.assertEqual(0, beatstate.assertion("chat.ls-cli", '["pfm","ls"]', '["pfm","ls"]', source))
        for field, value, error in (("run", "other-run", "CONTEXT"), ("beat", "fleet-reader/other", "CONTEXT"),
                ("invocation", "stale-entry", "CONTEXT"), ("target", {**target, "pane": "%99"}, "CONTEXT"),
                ("argv", ["pfm", "internal", "status"], "COMMAND")):
            with self.subTest(field=field):
                changed = {**proof, field: value}
                write_json(proof_path, changed)
                altered = {**state, "dispatches": {str(prefix): changed}}
                with self.assertRaisesRegex(ValueError, "OWNER-INPUT-" + error):
                    beatstate.owner_receipt(altered, str(prefix), '["pfm","ls"]')
        write_json(proof_path, {**proof, "argv": ["pfm", "chat", "status", "foreign"]})
        with self.assertRaisesRegex(ValueError, "OWNER-INPUT-DISPATCH"):
            beatstate.owner_receipt(state, str(prefix), '["pfm","ls"]')
        write_json(proof_path, proof)
        with self.assertRaisesRegex(ValueError, "OWNER-INPUT-MISMATCH"):
            beatstate.owner_receipt(state, str(prefix), '["pfm","chat","status"]')
        for key, value in (("PFM_RUN", "other-run"), ("PFM_BEAT", "fleet-reader/other")):
            with self.subTest(environment=key), patch.dict(os.environ, {key: value}):
                with self.assertRaisesRegex(ValueError, "OWNER-INPUT-CONTEXT"):
                    beatstate.owner_receipt(state, str(prefix), '["pfm","ls"]')
        for value, accepted in (("", False), ("pending\n", False), ("256\n", False), ("0\n", True), ("2\n", True), ("255\n", True)):
            with self.subTest(status=value):
                status.write_text(value)
                if accepted:
                    self.assertEqual(value, beatstate.owner_receipt(state, str(prefix), '["pfm","ls"]')["status"])
                else:
                    with self.assertRaisesRegex(ValueError, "OWNER-INPUT-STATUS"):
                        beatstate.owner_receipt(state, str(prefix), '["pfm","ls"]')
        status.write_text("0\n")
        for invalid in ("owner", str(root), str(prefix.parent), str(root / "foreign"), str(prefix.parent.parent / "other" / "owner"), str(root.parent / "foreign")):
            with self.subTest(path=invalid), self.assertRaises(ValueError):
                beatstate.receipt_path(invalid, state)
        output.unlink()
        (root / "foreign").write_text("fixture-list\n")
        output.symlink_to(root / "foreign")
        with self.assertRaisesRegex(ValueError, "OWNER-INPUT-PATH"):
            beatstate.owner_receipt(state, str(prefix), '["pfm","ls"]')
        output.unlink()
        output.write_text("fixture-list\n")
        with self.assertRaisesRegex(ValueError, "OWNER-INPUT-EXISTS"):
            terminal.type_command(target, ["pfm", "ls"], str(prefix))
        observation = prefix.parent / "observation.json"
        write_json(observation, {"activity": {}})
        state = beatstate.load()
        state["observation"] = str(observation)
        beatstate.save(state)
        before = json.loads(json.dumps(state))
        status.write_text("2\n")
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(1, beatstate.finish("pass"))
        self.assertEqual("OWNER-INPUT-STATUS", read_json(beatstate.paths()[1])["errors"][0].split(":")[0])
        status.write_text("0\n")
        beatstate.save(before)
        output.write_text("invented-list\n")
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(1, beatstate.finish("pass"))
        self.assertEqual("OWNER-INPUT-OUTPUT", read_json(beatstate.paths()[1])["errors"][0].split(":")[0])
        output.write_text("fixture-list\n")
        beatstate.save(before)
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(0, beatstate.finish("pass"))
        finished = read_json(beatstate.paths()[1])
        self.assertEqual(("pass", [], []), (finished["verdict"], finished["ops"], finished["errors"]))
        with patch.object(terminal, "text") as text, patch.object(terminal, "keys") as keys:
            sent = "sent"
            try:
                terminal.type_command(target, ["pfm", "ls"], str(root / "prelude"))
            except ValueError as error:
                sent = str(error)
            self.assertEqual("sent", sent)
            self.assertEqual(["enter"], keys.call_args.args[1])
            self.assertEqual(target, text.call_args.args[0])
        self.assertEqual(False, (root / "prelude.dispatch.json").exists())
        beatstate.paths(".failed")[1].unlink()
        beatstate.start("read-list", "chat.ls-cli")
        self.assertNotEqual(before["invocation"], beatstate.load()["invocation"])
        with self.assertRaisesRegex(ValueError, "OWNER-INPUT-DISPATCH"):
            beatstate.owner_receipt(beatstate.load(), str(prefix), '["pfm","ls"]')

    def test_owner_receipts_from_real_public_pfm_in_tmux_and_pty(self):
        root = Path(self.enterContext(tempfile.TemporaryDirectory(prefix="owner-public-")))
        home = root / "home"
        binary = home / "bin/pfm"
        binary.parent.mkdir(parents=True)
        command(["go", "build", "-o", str(binary), "./cmd/pfm"], cwd=HERE.parents[2] / "pfm", timeout=120)
        self.enterContext(patch.dict(os.environ, isolation.environment(str(root), "synthetic", 20001)))
        self.enterContext(patch.dict(os.environ, {"SHELL": "/bin/bash", "PATH": str(binary.parent) + ":" + os.environ["PATH"]}))
        (home / ".bash_profile").write_text("export PATH=" + str(binary.parent) + ":/usr/bin:/bin\nPS1='owned$ '\n")
        _, prefix = self.owner_beat(root)
        socket = str(root / "tmux.sock")
        pane, pane_pid = command(["tmux", "-S", socket, "-f", "/dev/null", "new-session", "-d", "-P", "-F", "#{pane_id}\t#{pane_pid}", "-s", "owned", "/bin/bash", "-l"], env=terminal_pty.plain_environment()).strip().split("\t")
        self.addCleanup(wait, "owned tmux shell exited", lambda: not command(["ps", "-p", pane_pid, "-o", "pid="], allowed_codes=(0, 1)).strip())
        self.addCleanup(command, ["tmux", "-S", socket, "kill-server"])
        pty = terminal_pty.create("/bin/bash", str(home))
        self.addCleanup(wait, "owned PTY shell exited", lambda: not command(["ps", "-p", str(pty["pid"]), "-o", "pid="], allowed_codes=(0, 1)).strip())
        self.addCleanup(terminal_pty.request, pty, "close")
        for backend, target in (("tmux", {"backend": "tmux", "socket": socket, "pane": pane}), ("pty", pty)):
            with self.subTest(backend=backend):
                write_json(root / "target.json", target)
                wait("owned shell ready", lambda: terminal.screen(target).rstrip().endswith("owned$"))
                for label, argv, invocation, expected, status in (
                        ("list", ["pfm", "chat", "schedule", "ls", "--json"], '["pfm","chat","schedule","ls","--json"]', "[]", "0\n"),
                        ("refusal", ["pfm", "chat", "ls", "--json"], '["pfm","chat","ls","--json"]', "usage: pfm chat ls [--all|--reap]  (--all: chats in every repo; --reap: end empty pfm tmux servers)", "2\n")):
                    receipt = str(prefix) + "-" + backend + "-" + label
                    terminal.type_command(target, argv, receipt)
                    wait("completed public receipt", lambda: Path(receipt + ".status").is_file() and Path(receipt + ".status").read_text() == status)
                    self.assertEqual(status, Path(receipt + ".status").read_text(), receipt)
                    actual = Path(receipt + ".out").read_text().rstrip("\n")
                    self.assertEqual(expected, actual, receipt)
                    actual_argv = json.dumps(read_json(receipt + ".dispatch.json")["argv"], separators=(",", ":"))
                    self.assertEqual(0, beatstate.assertion("chat.ls-cli", invocation, actual_argv, "owner-input:" + receipt))
                    self.assertEqual(argv, read_json(receipt + ".dispatch.json")["argv"])
        state = beatstate.load()
        logs = activity.sweep(os.environ["JOURNEY_ACTIVITY"], state["cursor"], "synthetic", state["row"], [])
        self.assertEqual(([], [], []), (logs["attributed"], logs["ops"], logs["unexpected"]))

    def test_known_gaps_require_a_current_owner_ruling(self):
        state = {"row": "terminal-open/plain-shell-stays-plain@linux+herdr-shield"}
        with tempfile.TemporaryDirectory() as scratch, patch.dict(os.environ, {"JOURNEY_DESIGN": scratch}):
            path = Path(scratch) / "gaps.tsv"
            for owner, expires, accepted in (("owner", "until-owner-decision", True),
                                             ("owner", "until-product-flight", True),
                                             ("owner", "2000-01-01", False), ("", "2099-01-01", False)):
                with self.subTest(owner=owner, expires=expires):
                    path.write_text("row\towner\texpires\n" + state["row"] + f"\t{owner}\t{expires}\n")
                    if accepted:
                        beatstate.policy("known", "listed gap", state)
                    else:
                        with self.assertRaisesRegex(ValueError, "KNOWN-EXPIRED"):
                            beatstate.policy("known", "listed gap", state)
                    with self.assertRaisesRegex(ValueError, "KNOWN-NOW-PASSES"):
                        beatstate.policy("pass", "", state)

    def test_run_resources_and_ambient_refusals(self):
        left = isolation.environment("/j/left", "synthetic-left", 20001)
        right = isolation.environment("/j/right", "synthetic-right", 20002)
        expected = {
            "HOME": "/j/left/home", "PFM_HOME": "/j/left/home",
            "PFM_STATE_DB": "/j/left/state/pfm.db", "PFM_CONFIG": "/j/left/pfm.config.json",
            "TMUX_TMPDIR": "/j/left/tmux", "JOURNEY_MCP_PORT": "20001",
        }
        self.assertEqual(expected, {key: left[key] for key in expected})
        for key in ("HOME", "PFM_STATE_DB", "HERDR_SOCKET_PATH", "TMUX_TMPDIR", "JOURNEY_MCP_PORT", "PFM_UNIT_PREFIX"):
            with self.subTest(key=key):
                self.assertNotEqual(left[key], right[key])
        for key in ("PFM_CONFIG", "PFM_HOME", "PFM_STATE_DB", "HERDR_SOCKET_PATH", "JOURNEY_ROOT", "CODEX_HOME", "JOURNEY_HOSTLESS"):
            for value in ("", "/outside"):
                with self.subTest(key=key, value=value):
                    with self.assertRaisesRegex(ValueError, "AMBIENT-RUN-ROOT: " + key):
                        isolation.refuse_ambient({key: value})
        isolation.refuse_ambient({"HOME": "/ambient", "PATH": "/ambient/bin"})
        for port, accepted in ((1023, False), (1024, True), (1025, True), (65534, True), (65535, True), (65536, False)):
            with self.subTest(port=port):
                if accepted:
                    self.assertEqual(str(port), isolation.environment("/j/bound", "synthetic", port)["JOURNEY_MCP_PORT"])
                else:
                    with self.assertRaisesRegex(ValueError, "PORT-REFUSED"):
                        isolation.environment("/j/bound", "synthetic", port)

    def test_native_environment_generator_removes_inherited_selectors(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            receipt = root / "generator.json"
            environment = {"JOURNEY_ROOT": scratch, "HOME": str(root / "home"), "PFM_DEV_FENCE": "1",
                           "CLAUDE_CONFIG_DIR": "wrong-account", "PFM_CLAUDE_CONFIG_DIR_DEFAULT": "wrong-default"}
            def generate(argv, *, env, capture):
                self.assertEqual(["/usr/lib/systemd/user-environment-generators/30-systemd-environment-d-generator"], argv)
                self.assertEqual({key: value for key, value in environment.items() if key not in
                                  {"CLAUDE_CONFIG_DIR", "PFM_CLAUDE_CONFIG_DIR_DEFAULT"}}, env)
                capture.update(argv=argv, stdout="CLAUDE_CONFIG_DIR=/fixture/account\n", stderr="", status=0)
            with patch.dict(os.environ, environment, clear=True), patch.object(isolation, "journey_os", return_value="linux"), \
                 patch.object(isolation, "command", side_effect=generate):
                actual = isolation.environment_d(receipt)
            self.assertEqual({"unset": ["CLAUDE_CONFIG_DIR", "PFM_CLAUDE_CONFIG_DIR_DEFAULT"],
                              "argv": ["/usr/lib/systemd/user-environment-generators/30-systemd-environment-d-generator"],
                              "stdout": "CLAUDE_CONFIG_DIR=/fixture/account\n", "stderr": "", "status": 0}, actual)
            self.assertEqual(actual, read_json(receipt))

    def test_log_window_rotation_and_read_refusals(self):
        record = read_json(HERE / "fixtures/synthetic-log/record.json")
        with tempfile.TemporaryDirectory() as scratch:
            path = Path(scratch) / "pfm.jsonl"
            Path(str(path) + ".lock").touch()
            path.write_text(json.dumps(record) + "\n")
            start = activity.cursor(path)
            with path.open("a") as output:
                output.write(json.dumps(record) + "\n")
            expected = {"records": [record], "attributed": [record], "ops": ["picker.open-typed"],
                        "unexpected": [], "missing_expected": []}
            self.assertEqual(expected, activity.sweep(path, start, "synthetic", record["beat"], []))
            rotated = path.with_suffix(".jsonl.1")
            path.rename(rotated)
            path.write_text("")
            self.assertEqual([record], activity.records(path, start))
            rotated.rename(path.with_suffix(".jsonl.2"))
            path.rename(rotated)
            path.write_text(json.dumps(record) + "\n")
            self.assertEqual([record, record], activity.records(path, start))
            path.with_suffix(".jsonl.2").unlink()
            rotated.unlink()
            with self.assertRaisesRegex(ValueError, "LOG-ABSENT"):
                activity.records(path, start)
            path.write_text("")
            start = activity.cursor(path)
            path.write_text("not-json\n")
            with self.assertRaises(json.JSONDecodeError):
                activity.records(path, start)

    def test_error_records_require_a_declared_match(self):
        record = read_json(HERE / "fixtures/synthetic-log/record.json")
        record.update(level="ERROR", msg="fixture-refusal")
        with tempfile.TemporaryDirectory() as scratch:
            path = Path(scratch) / "pfm.jsonl"
            Path(str(path) + ".lock").touch()
            path.write_text("")
            start = activity.cursor(path)
            path.write_text(json.dumps(record) + "\n")
            actual = activity.sweep(path, start, "synthetic", record["beat"], [])
            self.assertEqual([record], actual["unexpected"])
            actual = activity.sweep(path, start, "synthetic", record["beat"], ["fixture-refusal", "missing-refusal"])
            self.assertEqual([], actual["unexpected"])
            self.assertEqual(["missing-refusal"], actual["missing_expected"])

    def test_diagnostics_and_other_beats_do_not_credit_actions(self):
        action = read_json(HERE / "fixtures/synthetic-log/record.json")
        diagnostic = {key: value for key, value in action.items() if key != "action"}
        other = {**action, "beat": "terminal-open/other@linux+tmux"}
        with tempfile.TemporaryDirectory() as scratch:
            path = Path(scratch) / "pfm.jsonl"
            Path(str(path) + ".lock").touch()
            path.write_text("")
            start = activity.cursor(path)
            path.write_text("".join(json.dumps(row) + "\n" for row in (diagnostic, other, action)))
            actual = activity.sweep(path, start, "synthetic", action["beat"], [])
            self.assertEqual([action], actual["attributed"])
            self.assertEqual(["picker.open-typed"], actual["ops"])

    def test_fork_walks_depth_first_and_need_reports_unreadable(self):
        with tempfile.TemporaryDirectory() as scratch:
            program = Path(scratch) / "fork.sh"
            program.write_text('''source "$1/lib.sh"
export JOURNEY_LANE=terminal-open JOURNEY_BRANCH=linux
leaf() { printf '%s\n' "$JOURNEY_BRANCH"; }
branch() { fork engine 'claude codex' leaf; }
fork terminal 'herdr-shield tmux' branch
printf '%s\n' "$JOURNEY_BRANCH"
unreadable() { return 2; }
create() { printf 'UNEXPECTED-CREATION\n'; }
need store unreadable create
printf 'need-exit=%s\n' "$?"
''')
            result = command(["bash", str(program), str(HERE)])
            self.assertEqual("linux+herdr-shield+claude\nlinux+herdr-shield+codex\nlinux+tmux+claude\nlinux+tmux+codex\nlinux\nneed-exit=2\n", result)

    def test_blocked_before_input_retains_header_without_fresh_observation(self):
        for dispatched in (False, True):
            with self.subTest(dispatched=dispatched):
                root = Path(self.enterContext(tempfile.TemporaryDirectory(prefix="blocked-input-")))
                self.owner_beat(root)
                if dispatched:
                    state = beatstate.load()
                    state["dispatches"] = {"owner": {"argv": ["pfm", "ls"]}}
                    beatstate.save(state)
                with patch.object(beatstate, "observe") as observe:
                    self.assertEqual(1, beatstate.finish("blocked", "need:missing-role"))
                self.assertEqual(int(dispatched), observe.call_count)
                actual = beatstate.load(allow_closed=True)
                self.assertEqual("blocked", actual["verdict"])
                self.assertEqual([], actual["assertions"])
                if not dispatched:
                    self.assertEqual({"header": str(root / "header.json"), "reason": "need:missing-role",
                        "action": "n/a:blocked before input; beat not executed"}, actual["blocked_evidence"])

    def test_a_mismatch_cannot_pass_and_blocks_the_next_beat(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            log = root / "activity.jsonl"
            Path(str(log) + ".lock").touch()
            log.write_text("")
            write_json(root / "header.json", {"start_ms": 0})
            (root / "ledger.tsv").write_text("row\tverdict\tassertions-by-id\texpected\tops\tgoldens\twall_ms\tt+s\n")
            (root / "gaps.tsv").write_text("gap\tbeat\tstatus\n")
            env = {"JOURNEY_OUT": scratch, "JOURNEY_LANE": "terminal-open", "JOURNEY_BRANCH": "linux+tmux",
                   "JOURNEY_ACTIVITY": str(log), "PFM_RUN": "synthetic", "JOURNEY_DESIGN": scratch,
                   "JOURNEY_ROOT": scratch, "PFM_BEAT_FILE": str(root / "beat.json")}
            with patch.dict(os.environ, env, clear=True), contextlib.redirect_stdout(io.StringIO()):
                with patch.dict(os.environ, {"PFM_BEAT_FILE": "beat.json"}), self.assertRaisesRegex(ValueError, "TRACE-PATH"):
                    beatstate.start("open-picker", "picker.open-typed")
                self.assertEqual(0, beatstate.start("open-picker", "picker.open-typed"))
                self.assertEqual({"beat": "terminal-open/open-picker@linux+tmux", "run": "synthetic"}, read_json(root / "beat.json"))
                (root / "beat.json").chmod(0o600)
                golden = root / "goldens" / "typed.txt"
                golden.parent.mkdir()
                golden.write_text("/reload\n")
                missing = root / "typed-receipt.txt"
                stdout, stderr = io.StringIO(), io.StringIO()
                with patch.object(beatstate, "__file__", str(root / "beatstate.py")), self.assertRaises(FileNotFoundError) as rejected:
                    beatstate.golden("chat.reload-typed", "typed.txt", str(missing))
                with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                    beatstate.rejected(rejected.exception)
                self.assertEqual("", stdout.getvalue())
                self.assertEqual("BEAT-ERROR: " + str(rejected.exception) + "\n", stderr.getvalue())
                self.assertEqual(["BEAT-ERROR: " + str(rejected.exception)], beatstate.load()["errors"])
                receipt = root / "engine-input.txt"
                receipt.write_text("/stats\n")
                self.assertEqual(0, beatstate.assertion("engine.stats", "/stats", "/stats", "engine-input:" + str(receipt)))
                with self.assertRaisesRegex(ValueError, "ENGINE-INPUT-MISMATCH"):
                    beatstate.assertion("engine.stats", "/stats", "/wrong", "engine-input:" + str(receipt))
                self.assertEqual(1, beatstate.assertion("picker.open-typed", "atlas", "beacon"))
                self.assertEqual(1, beatstate.finish("pass"))
                actual = read_json(root / "terminal-open.state.json")
                self.assertEqual("fail", actual["verdict"])
                self.assertEqual({"id": "picker.open-typed", "expected": "atlas", "actual": "beacon", "source": "literal", "pass": False}, actual["assertions"][1])
                with patch.dict(os.environ, {"JOURNEY_TARGET": "unused-target"}), patch.object(beatstate, "observe", side_effect=AssertionError("blocked beat must not observe")):
                    self.assertEqual(1, beatstate.start("picker-quit", "picker.quit"))
                self.assertEqual({"beat": "terminal-open/picker-quit@linux+tmux", "run": "synthetic"}, read_json(root / "beat.json"))
                self.assertEqual(0o600, (root / "beat.json").stat().st_mode & 0o777)
                actual = read_json(root / "terminal-open.state.json")
                self.assertEqual("blocked", actual["verdict"])
                self.assertEqual("blocked-by terminal-open/open-picker@linux+tmux", actual["detail"])
                self.assertEqual({"result": str(root / "beats/terminal-open/open-picker@linux+tmux/result.json"),
                                  "observation": None, "action": "n/a:predecessor-failed; beat not executed"}, actual["blocked_evidence"])
                self.assertEqual([], actual["assertions"])
                self.assertEqual([], actual["ops"])
                with patch.dict(os.environ, {"JOURNEY_STATE_LANE": "machine-lifecycle-final"}):
                    self.assertEqual(0, beatstate.start("final-install", "lifecycle.install"))
                    self.assertEqual("terminal-open/final-install@linux+tmux", read_json(root / "machine-lifecycle-final.state.json")["row"])
                    self.assertEqual(root / "machine-lifecycle-final.failed", beatstate.paths(".failed")[1])


if __name__ == "__main__":
    unittest.main()
