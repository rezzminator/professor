#!/usr/bin/env python3
"""Forecast assembly proves actual terminal input and fault restoration."""
import importlib.util
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("reader_forecast", Path(__file__).with_name("forecast.py"))
forecast = importlib.util.module_from_spec(spec)
spec.loader.exec_module(forecast)
from runtime import command, write_json
import terminal_pty


class ForecastTests(unittest.TestCase):
    def test_plain_pty_accepts_only_the_explicit_same_birth_denial(self):
        owner = {"backend": "pty", "socket": "/synthetic/owned/io.sock", "pane": "/dev/pts/9"}
        denial = {"state": "denied", "pid": 21, "start": "actual-owned-birth", "errno": 13,
                  "receipt": "/synthetic/owned/caller.environ.json"}
        observed = {"births": [{"pid": 21, "start": "actual-owned-birth", "environment": denial},
                               {"pid": 22, "start": "actual-shell", "environment": {"PFM_RUN": "owned"}}]}
        with patch.dict(os.environ, {"PFM_RUN": "owned"}), patch.object(forecast.faults, "owned_path"), \
                patch.object(forecast, "terminal", return_value=observed):
            try:
                actual = forecast.checked_owner(owner, expected_denial=denial)
            except ValueError as error:
                self.fail(f"valid explicit native denial refused: {error}")
            self.assertEqual(observed, actual)
            for expected in (None, {**denial, "pid": 99}, {**denial, "start": "reused-birth"},
                             {**denial, "errno": 1}):
                with self.subTest(expected=expected), self.assertRaisesRegex(ValueError, "^FORECAST-OWNER-FOREIGN:"):
                    forecast.checked_owner(owner, expected_denial=expected)
            observed["births"][1]["environment"]["PFM_RUN"] = "foreign"
            with self.assertRaisesRegex(ValueError, "^FORECAST-OWNER-FOREIGN:"):
                forecast.checked_owner(owner, expected_denial=denial)

    def test_assignment_parser_preserves_values_and_refuses_duplicate_axis(self):
        self.assertEqual({"engine": "codex", "multiplexer": "herdr"}, forecast.factors("engine=codex,multiplexer=herdr"))
        for value in ("engine=codex,engine=claude", "engine", "engine=", "=claude"):
            with self.subTest(value=value), self.assertRaisesRegex(ValueError, "^FORECAST-FACTORS:"):
                forecast.factors(value)

    def test_dispatch_never_credits_an_unevidenced_factor(self):
        self.assertEqual("herdr-reader", forecast.operation({"engine": "codex", "multiplexer": "herdr"}))
        self.assertEqual("outside-stopped", forecast.operation({"process_state": "stopped", "tmux_server": "job_control_stopped", "caller_context": "outside_pane"}))
        self.assertEqual("stopped-probe", forecast.operation({"tmux_server": "sigstopped", "probe_site": "gather_probe_without_deadline", "client_pipe": "held_by_stopped_server"}))
        for values in ({"engine": "codex", "multiplexer": "herdr", "pane_state": "blocked"},
                       {"process_table": "empty_kinfo_or_eio"}):
            with self.subTest(values=values), self.assertRaisesRegex(ValueError, "^FORECAST-UNSUPPORTED:"):
                forecast.operation(values)

    def test_probe_entry_targets_its_server_and_retains_bound_edges(self):
        with tempfile.TemporaryDirectory(prefix="reader-probe-") as directory:
            root = Path(directory)
            socket = str(root / "cc-owned")
            carry = root / "carry.json"
            write_json(carry, {"chats": [{"terminal": {"backend": "tmux", "socket": socket}}]})
            with patch.dict(os.environ, {"JOURNEY_CARRY": str(carry), "PFM_TMUX_DIR": directory}):
                forecast.targeted_entry(["pfm", "chat", "status", "cc-owned", "--json"], socket)
                forecast.targeted_entry(["pfm", "ls", "--all", "--tsv"], socket)
                with self.assertRaisesRegex(ValueError, "^FORECAST-PROBE-TARGET:"):
                    forecast.targeted_entry(["pfm", "chat", "status", "cc-neighbour", "--json"], socket)
        for elapsed, outcome in ((999, "bounded"), (1000, "bounded"), (1001, "overrun")):
            with self.subTest(elapsed=elapsed):
                self.assertEqual({"field": "probe-wall", "expected": "bounded", "actual": outcome,
                                  "elapsed_ms": elapsed, "maximum_ms": 1000}, forecast.bounded_check(elapsed, 1000))

    def test_receipt_refuses_overwriting_an_earlier_action(self):
        with tempfile.TemporaryDirectory(prefix="reader-receipt-") as directory:
            prefix = Path(directory) / "command"
            prefix.with_suffix(".out").write_text("earlier-answer")
            with self.assertRaisesRegex(ValueError, "^FORECAST-RECEIPT-EXISTS:"):
                forecast.action({}, ["printf", "new-answer"], prefix)
            self.assertEqual("earlier-answer", prefix.with_suffix(".out").read_text())
            prefix.with_suffix(".out").unlink()
            prefix.with_suffix(".out").symlink_to(Path(directory) / "not-created")
            with self.assertRaisesRegex(ValueError, "^FORECAST-RECEIPT-EXISTS:"):
                forecast.action({}, ["printf", "new-answer"], prefix)
            self.assertEqual(False, (Path(directory) / "not-created").exists())

    def test_cli_list_argv_reaches_the_owner_boundary(self):
        with tempfile.TemporaryDirectory(prefix="reader-argv-") as directory:
            root = Path(directory)
            write_json(root / "chat.json", {"terminal": {}})
            write_json(root / "owner.json", {})
            write_json(root / "resources.json", {})
            write_json(root / "carry.json", {"chats": [], "foreign": {"terminal": {"backend": "herdr", "pane": "foreign"}}})
            with patch.dict(os.environ, {"JOURNEY_ROOT": directory, "JOURNEY_CARRY": str(root / "carry.json")}):
                with self.assertRaisesRegex(ValueError, "^FAULT-TARGET-NOT-CARRIED:"):
                    forecast.main("run", "engine=codex,multiplexer=herdr", str(root / "chat.json"), str(root / "owner.json"),
                                  str(root / "action"), str(root / "resources.json"), "--", "pfm", "ls", "--all", "--tsv")

    def test_plain_pty_action_completes_while_auxiliary_server_is_stopped(self):
        directory = self.enterContext(tempfile.TemporaryDirectory(prefix="reader-forecast-"))
        root = Path(directory)
        home = root / "home"
        home.mkdir()
        carry = root / "carry.json"
        variables = {"JOURNEY_ROOT": str(root), "HOME": str(home), "PFM_RUN": "synthetic",
                     "PFM_BEAT": "fleet-reader/forecast-test", "JOURNEY_CARRY": str(carry), "SHELL": "/bin/sh"}
        self.enterContext(patch.dict(os.environ, variables))
        owner = terminal_pty.create("/bin/sh", str(home))
        self.addCleanup(terminal_pty.request, owner, "close")
        socket = str(root / "auxiliary")
        command(["tmux", "-S", socket, "new-session", "-d", "-s", "auxiliary", "/bin/sh"])
        self.addCleanup(command, ["tmux", "-S", socket, "kill-server"])
        write_json(carry, {"owner": owner, "chats": [], "foreign": {"terminal": {"backend": "herdr", "pane": "foreign"}}})
        values = {"process_state": "stopped", "tmux_server": "job_control_stopped", "caller_context": "outside_pane"}
        original = command(["tmux", "-S", socket, "display-message", "-p", "#{pid}"]).strip()
        with forecast.scope(values, {}, owner, {"tmux_socket": socket}) as evidence:
            self.assertEqual([], [row for row in evidence["checks"] if row["expected"] != row["actual"]])
            receipt = forecast.action(owner, ["printf", "forecast-answer"], root / "command", bound=5)
            self.assertEqual({"argv": ["printf", "forecast-answer"], "exit_code": 0, "output": "forecast-answer",
                              "status_file": str(root / "command.status"), "output_file": str(root / "command.out")}, receipt)
        self.assertEqual(original, command(["tmux", "-S", socket, "display-message", "-p", "#{pid}"]).strip())
        with self.assertRaisesRegex(ValueError, "^FORECAST-OWNER-STOPPED:"):
            forecast.responsive_owner({"backend": "tmux", "socket": socket}, socket)
        with self.assertRaisesRegex(ValueError, "^FAULT-TARGET-NOT-CARRIED:"):
            forecast.checked_owner({"backend": "tmux", "socket": str(root / "foreign"), "pane": "%99"})


if __name__ == "__main__":
    unittest.main()
