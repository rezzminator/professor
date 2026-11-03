"""Native STOP, move, KILL and reopen lifecycle faults."""
import os
import signal
from pathlib import Path
import unittest
from unittest.mock import patch

import faults_test
import faults
from bootstrap import tmux_server, wait
from observation import process_snapshot, process_state, terminal
from runtime import command, read_json, write_json
from terminal import text, keys


class ProcessMoveFaultTests(unittest.TestCase):
    setUp = faults_test.FaultTests.setUp

    def _targets(self, suffix):
        variables = patch.dict(os.environ, {"PFM_TMUX_DIR": str(self.root / "sockets"), "JOURNEY_CARRY": str(self.root / (suffix + ".json"))})
        variables.start()
        self.addCleanup(variables.stop)
        target = tmux_server("move-" + suffix, "source-" + suffix)
        other = tmux_server("other-" + suffix, "destination-" + suffix)
        foreign = tmux_server("foreign-" + suffix, "foreign-" + suffix)
        for value in (target, other, foreign):
            self.addCleanup(lambda value=value: command(["tmux", "-S", value["socket"], "kill-server"], allowed_codes=(0, 1)))
        write_json(os.environ["JOURNEY_CARRY"], {"chats": [{"terminal": target}, {"terminal": other}], "foreign": {"terminal": foreign}})
        return target, other

    def test_fresh_terminal_scope_checks_native_birth_and_restores_authority(self):
        target, other = self._targets("fresh")
        carry = read_json(os.environ["JOURNEY_CARRY"])
        carry["chats"] = [{"terminal": other}]
        write_json(os.environ["JOURNEY_CARRY"], carry)
        subject = self._sleep_birth(target)
        before = terminal(target)
        for protected in (other, carry["foreign"]["terminal"]):
            with self.assertRaisesRegex(ValueError, "^FAULT-TARGET-NOT-FRESH:"):
                with faults.fresh_target(protected):
                    self.fail("protected target accepted")
        alias = dict(other, pane=terminal(other)["identity"]["pane"])
        with self.assertRaisesRegex(ValueError, "^FAULT-TARGET-NOT-FRESH:"):
            with faults.fresh_target(alias):
                self.fail("carried physical alias accepted")
        with self.assertRaisesRegex(ValueError, "^FAULT-TARGET-NOT-CARRIED:"):
            faults.owned_target(target)
        with self.assertRaisesRegex(RuntimeError, "owner failure"):
            with faults.fresh_target(target):
                self.assertEqual(before["identity"], faults.owned_target(target)["identity"])
                alias = dict(target, pane=before["identity"]["pane"])
                self.assertEqual(before["births"][0]["start"], faults.owned_target(alias)["births"][0]["start"])
                with faults.signalled_process(alias, subject, "STOP"):
                    wait("fresh subject stopped", lambda: process_state(subject["pid"])["state"] == "T")
                    self.assertEqual("T", process_state(subject["pid"])["state"])
                command(["tmux", "-S", target["socket"], "respawn-pane", "-k", "-t", alias["pane"], "sleep 600"])
                with self.assertRaisesRegex(ValueError, "^FAULT-TARGET-BIRTH:"):
                    faults.owned_target(alias)
                raise RuntimeError("owner failure")
        with self.assertRaisesRegex(ValueError, "^FAULT-TARGET-NOT-CARRIED:"):
            faults.owned_target(target)
        self.assertEqual(carry, read_json(os.environ["JOURNEY_CARRY"]))
        with patch.dict(os.environ, {"PFM_RUN": "another-run"}):
            with self.assertRaisesRegex(ValueError, "^FAULT-PROCESS-FOREIGN:"):
                with faults.fresh_target(target):
                    self.fail("foreign process accepted")

    def test_cli_authorizes_fresh_restore_owner_until_reopen_dispatch(self):
        target, owner = self._targets("restore-owner")
        carry = read_json(os.environ["JOURNEY_CARRY"])
        carry["chats"] = [{"terminal": target}]
        write_json(os.environ["JOURNEY_CARRY"], carry)
        birth = self._sleep_birth(target)
        paths = [self.root / name for name in ("chat.json", "birth.json", "owner.json")]
        for path, value in zip(paths, ({"terminal": target, "session_id": "actual-role"}, birth, owner)):
            write_json(path, value)
        observed = terminal(owner)["identity"]
        calls = []
        def reopen(actual, argv):
            calls.append((faults.owned_target(actual)["identity"], argv))
        failure = None
        with patch("terminal.type_command", side_effect=reopen):
            try:
                faults.main("killed-process", *map(str, paths), "--", "true")
            except ValueError as error:
                failure = str(error)
        self.assertEqual(None, failure)
        self.assertEqual([(observed, ["pfm", "chat", "open", "actual-role"])], calls)
        with self.assertRaisesRegex(ValueError, "^FAULT-TARGET-NOT-CARRIED:"):
            faults.owned_target(owner)
        carry["owner"] = owner
        write_json(os.environ["JOURNEY_CARRY"], carry)
        with faults.restoration_owner(owner):
            self.assertEqual(observed, faults.owned_target(owner)["identity"])
        write_json(paths[2], carry["foreign"]["terminal"])
        with self.assertRaisesRegex(ValueError, "^FAULT-FOREIGN-SENTINEL$"):
            faults.main("killed-process", *map(str, paths), "--", "true")

    def _sleep_birth(self, target):
        pane_birth = terminal(target)["births"][0]
        text(target, "sleep 600")
        keys(target, ["enter"])
        process = wait("native sleep subject", lambda: next((row for row in process_snapshot().values()
                       if row["parent"] == pane_birth["pid"] and row["argv"] == "sleep 600"), None))
        return {key: process[key] for key in ("pid", "start")}

    def test_stopped_move_kills_exact_birth_and_restores_once(self):
        target, other = self._targets("success")
        target["pane"] = command(["tmux", "-S", target["socket"], "display-message", "-p", "-t", target["pane"], "#{pane_id}"]).strip()
        carry = read_json(os.environ["JOURNEY_CARRY"])
        carry["chats"][0]["terminal"] = target
        write_json(os.environ["JOURNEY_CARRY"], carry)
        birth = self._sleep_birth(target)
        destination = tmux_server("move-success", "moved-success")
        restored = []

        def move(value):
            self.assertEqual("T", process_state(birth["pid"])["state"])
            command(["tmux", "-S", value["socket"], "join-pane", "-s", value["pane"], "-t", destination["pane"]])
            self.assertEqual("moved-success\n", command(["tmux", "-S", value["socket"], "display-message", "-p", "-t", value["pane"], "#{session_name}"]))
            return value

        def restore():
            restored.append(tmux_server("move-success", "source-success"))

        receipt = self.root / "move.json"
        with faults.stopped_for_move(target, birth, move, restore, receipt=receipt) as moved:
            self.assertEqual(target, moved)
            self.assertEqual(True, faults.birth_exited(birth))
            captured = read_json(receipt)
            self.assertEqual({"run", "source", "target", "supervisor", "before", "prekill"}, set(captured))
            self.assertEqual("synthetic", captured["run"])
            self.assertEqual(target, captured["source"])
            self.assertEqual(moved, captured["target"])
            self.assertEqual(birth, captured["supervisor"])
            self.assertEqual("source-success", captured["before"]["identity"]["session"])
            self.assertEqual("moved-success", captured["prekill"]["identity"]["session"])
            self.assertEqual(target["pane"], captured["prekill"]["identity"]["pane"])
            held = next(node for row in captured["prekill"]["births"] for node in row["chain"] if node["pid"] == birth["pid"])
            self.assertEqual((birth["start"], "T"), (held["start"], held["state"]))
        self.assertEqual(1, len(restored))
        self.assertEqual("source-success\n", command(["tmux", "-S", restored[0]["socket"], "display-message", "-p", "-t", restored[0]["pane"], "#{session_name}"]))
        self.assertNotEqual(birth["pid"], terminal(restored[0])["births"][0]["pid"])
        self.assertEqual(True, faults.birth_exited(birth))
        with self.assertRaisesRegex(ValueError, "^FAULT-MOVE-RECEIPT-EXISTS:"):
            with faults.stopped_for_move(target, birth, move, restore, receipt=receipt):
                self.fail("existing move receipt overwritten")
        self.assertEqual(1, len(restored))

    def test_move_refuses_target_without_stopped_original_birth_and_continues_it(self):
        for mode, failure in (("wrong-target", "FAULT-PROCESS-NOT-IN-TARGET"), ("resumed", "FAULT-MOVE-NOT-STOPPED")):
            with self.subTest(mode=mode):
                target, other = self._targets(mode)
                birth = self._sleep_birth(target)
                restored = []

                def refuse(value):
                    self.assertEqual("T", process_state(birth["pid"])["state"])
                    if mode == "resumed":
                        faults.signal_birth(birth, signal.SIGCONT)
                        wait("resumed before move validation", lambda: process_state(birth["pid"])["state"] == "S")
                        return value
                    return other

                with self.assertRaisesRegex(ValueError, "^" + failure + ":"):
                    with faults.stopped_for_move(target, birth, refuse, lambda: restored.append("done")):
                        self.fail("unreachable")
                self.assertEqual(["done"], restored)
                self.assertEqual(birth["start"], process_snapshot()[birth["pid"]]["start"])
                label = "continued native sleep"
                try:
                    wait(label, lambda: process_state(birth["pid"])["state"] == "S", bound=2)
                except ValueError as error:
                    if str(error) != "ROOT-TIMEOUT: " + label:
                        raise
                self.assertEqual("S", process_state(birth["pid"])["state"])


if __name__ == "__main__":
    unittest.main()
