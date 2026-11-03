#!/usr/bin/env python3
"""Follower oracles use the public native-unit seams, without claiming Darwin execution."""
from contextlib import contextmanager
import copy
import errno
import json
import os
from pathlib import Path
import plistlib
import tempfile
import unittest
from unittest.mock import patch

import native_macos
from runtime import read_json, write_json, write_text


class NativeMacOS(unittest.TestCase):
    def setUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory(prefix="reader-macos-")))
        self.home = self.root / "home"
        self.accounts = [self.home / ".claude/settings.json", *[self.home / ".cc" / str(number) / "settings.json" for number in (1, 2, 3)]]
        for settings in self.accounts:
            write_json(settings, {"theme": "dark"})
            write_text(settings.parent / "themes/fixture.txt", "Owned theme fixture.\n")
        self.settings = self.accounts[2]
        self.target = self.settings.with_name("reader-settings-target.json")
        write_text(self.target, self.settings.read_text())
        self.label = "com.professor.pfm-reader.theme"
        self.path = self.home / "Library/LaunchAgents" / (self.label + ".plist")
        self.program = [str(self.home / ".local/lib/pfm/agent-launcher"), "theme", "sync"]
        self.watches = [part for settings in sorted(self.accounts) for part in (str(settings), str(settings.parent / "themes"))]
        self.watches.append(str(self.home / "Library/Preferences/.GlobalPreferences.plist"))
        self.definition = {"path": str(self.path), "ProgramArguments": self.program, "WatchPaths": self.watches,
                           "EnvironmentVariables": {"PFM_UNIT_PREFIX": "pfm-reader"}}
        self.unit = {"platform": "darwin", "label": self.label, "state": "running", "pid": 42, "start": "native-job-birth",
                     "definition": self.definition, "last_exit_code": None}
        self.inputs = {"settings": str(self.settings), "settings_target": str(self.target), "account_directory": str(self.home / ".cc"),
                       "account_settings": list(map(str, self.accounts)), "follower_program": self.program, "expected_watches": self.watches}
        self.owner_file = self.root / "owner.json"
        write_json(self.owner_file, {"backend": "herdr", "pane": "actual-owner"})
        write_text(self.root / "artifacts/fixture.txt", "Owned reader artifact directory.\n")
        self.enterContext(patch.dict(os.environ, {"HOME": str(self.home), "JOURNEY_ROOT": str(self.root), "PFM_UNIT_PREFIX": "pfm-reader",
                                               "HERDR_SOCKET_PATH": "/fixture/herdr.sock"}))

    def failures(self, checks):
        return [row["field"] for row in checks if row["expected"] != row["actual"]]

    def report(self, verdict="IN-SYNC", detail=""):
        return {"enabled": True, "surfaces": [{"surface": "follower", "verdict": verdict, "generation": 0, "path": str(self.path), "detail": detail}]}

    def test_native_definition_checks_exact_watches_program_namespace_and_loaded_state(self):
        self.assertEqual([], self.failures(native_macos.watches_checks(self.unit, self.inputs, self.watches)))
        for group, key, value, expected in ((None, "platform", "linux", "native-follower-platform"),
                (None, "label", "foreign", "native-follower-label"), (None, "state", "absent", "native-follower-loaded"),
                ("definition", "path", "/fixture/foreign.plist", "native-follower-path"),
                ("definition", "ProgramArguments", ["pfm", "theme", "status"], "native-follower-program"),
                ("definition", "WatchPaths", [], "native-follower-watch-paths"),
                ("definition", "EnvironmentVariables", {"PFM_UNIT_PREFIX": "foreign"}, "native-follower-prefix")):
            with self.subTest(field=key):
                actual = copy.deepcopy(self.unit)
                (actual[group] if group else actual)[key] = value
                self.assertEqual([expected], self.failures(native_macos.watches_checks(actual, self.inputs, self.watches)))

    def test_own_job_receipt_keeps_actual_child_job_birth_xpc_and_rewritten_disk(self):
        receipt = {"argv": ["pfm", "theme", "sync", "--json"], "status": 1, "stderr": "",
                   "birth": {"environment": {"XPC_SERVICE_NAME": self.label}, "chain": [
                       {"pid": 43, "parent": 42, "start": "public-child-birth", "argv": "/fixture/pfm theme sync --json"},
                       {"pid": 42, "parent": 1, "start": "native-job-birth"}]}}
        self.assertEqual([], self.failures(native_macos.own_job_checks(self.unit, receipt, self.unit, self.definition)))
        cases = (("argv", ["pfm", "theme", "off"], "native-job-public-argv"), ("status", -15, "native-job-public-status"),
                 ("stderr", "unrelated failure", "native-job-public-stderr"),
                 ("parent", 99, "native-job-public-parent"), ("start", "reused-job", "native-job-parent-birth"),
                 ("xpc", "foreign", "native-job-xpc-label"), ("kernel", "pfm theme off", "native-job-public-kernel-argv"),
                 ("after-label", "foreign", "native-job-after-label"), ("state", "absent", "native-job-still-loaded"),
                 ("definition", {"path": "stale"}, "native-job-rewritten-definition"))
        for field, value, expected in cases:
            with self.subTest(field=field):
                observed, after = copy.deepcopy(receipt), copy.deepcopy(self.unit)
                if field in {"argv", "status", "stderr"}:
                    observed[field] = value
                elif field == "parent":
                    observed["birth"]["chain"][0]["parent"] = value
                elif field == "start":
                    observed["birth"]["chain"][1]["start"] = value
                elif field == "xpc":
                    observed["birth"]["environment"]["XPC_SERVICE_NAME"] = value
                elif field == "kernel":
                    observed["birth"]["chain"][0]["argv"] = value
                else:
                    after["label" if field == "after-label" else field] = value
                self.assertEqual([expected], self.failures(native_macos.own_job_checks(self.unit, observed, after, self.definition)))

    def test_native_symlink_and_unlistable_inputs_keep_all_settings_and_watches(self):
        for plan, interrupted in (("macos-watch-failure", False), ("macos-watch-unlistable", False), ("macos-watch-symlink", False), ("macos-watch-failure", True)):
            with self.subTest(plan=plan, interrupted=interrupted):
                enabled, calls = [False], []
                before = [(path.read_bytes(), path.stat().st_ino) for path in self.accounts]
                def observe(name):
                    self.assertEqual("theme", name)
                    value = copy.deepcopy(self.unit)
                    value["state"] = "running" if enabled[0] else "absent"
                    if plan == "macos-watch-symlink" and self.settings.is_symlink():
                        value["definition"]["WatchPaths"] = [str(self.target) if item == str(self.settings) else item for item in self.watches]
                    return value
                def execute(owner, argv, prefix):
                    label = prefix.split(".")[-1]
                    calls.append((label, argv))
                    if label == "theme-before":
                        return {"output": json.dumps({"enabled": False, "surfaces": []}), "exit_code": 0}
                    if label == "theme-on":
                        enabled[0] = True
                    if label == "theme-off":
                        enabled[0] = False
                        return {"output": json.dumps({"enabled": False, "surfaces": []}), "exit_code": 0}
                    if label.startswith("failure-"):
                        if interrupted:
                            for settings in self.accounts:
                                if settings != self.settings:
                                    write_json(settings, {"fixture": "changed by the failing owned entry"})
                            raise RuntimeError("owner input failed during native fault")
                        subject = self.home / ".cc" if plan == "macos-watch-unlistable" else self.settings
                        with self.assertRaises(OSError) as denied:
                            subject.stat()
                        self.assertEqual(errno.ELOOP, denied.exception.errno)
                        cause = (f"themesync: list account settings: open {subject}: too many levels of symbolic links" if plan == "macos-watch-unlistable" else
                                 f"themesync: resolve {subject}: EvalSymlinks: too many links")
                        source = {"surface": "claude", "verdict": "ERROR", "generation": 0, "path": "", "detail": cause}
                        rows = [source]
                        if "status" in label:
                            rows += self.report("ERROR", "themesync: discover follower watches: " + cause)["surfaces"]
                        return {"output": json.dumps({"enabled": True, "surfaces": rows}), "exit_code": 1}
                    return {"output": json.dumps(self.report()), "exit_code": 0}
                prefix = self.root / "artifacts" / plan
                with patch.object(native_macos, "checked_owner"), patch.object(native_macos, "managed_unit", side_effect=observe), \
                        patch.object(native_macos, "bundle", return_value={"chats": [{"session": "same-carried"}]}), \
                        patch.object(native_macos, "action", side_effect=execute):
                    if interrupted:
                        with self.assertRaisesRegex(RuntimeError, "^owner input failed during native fault$"):
                            native_macos.run(plan, str(self.owner_file), self.inputs, prefix)
                    else:
                        result = native_macos.run(plan, str(self.owner_file), self.inputs, prefix)
                if not interrupted:
                    self.assertEqual([], self.failures(result["checks"]))
                    self.assertEqual(result["checks"], read_json(str(prefix) + ".checks.json"))
                self.assertEqual(before, [(path.read_bytes(), path.stat().st_ino) for path in self.accounts])
                self.assertEqual(("theme-off", ["pfm", "theme", "off", "--json"]), calls[-1])
                self.assertEqual(4 if interrupted else 5 if plan == "macos-watch-symlink" else 8, len(calls))

    def test_own_job_callbacks_observe_before_factory_restoration_and_outside_sync(self):
        evidence = {"checks": [], "units": []}
        write_text(self.path, plistlib.dumps({"Label": self.label, **{key: value for key, value in self.definition.items() if key != "path"}}).decode())
        original = self.path.read_bytes()
        phases = []
        receipt = {"argv": ["pfm", "theme", "sync", "--json"], "status": 1, "stderr": "", "stdout": json.dumps(self.report("ERROR", native_macos.SELF_RELOAD)),
                   "birth": {"environment": {"XPC_SERVICE_NAME": self.label}, "chain": [
                       {"pid": 43, "parent": 42, "start": "public-child-birth", "argv": "pfm theme sync --json"},
                       {"pid": 42, "parent": 1, "start": "native-job-birth"}]}}
        @contextmanager
        def unloaded(label, active):
            self.assertEqual((self.label, False), (label, active))
            phases.append("unloaded")
            yield
            phases.append("original job restored")
        def factory(name, argv, prefix, *, before, after):
            self.assertEqual(("theme", ["pfm", "theme", "sync", "--json"]), (name, argv))
            phases.append("native ready")
            before(self.unit)
            self.assertEqual({"Label": self.label, "ProgramArguments": ["/fixture/stale-follower"]}, plistlib.loads(self.path.read_bytes()))
            phases.append("native completed")
            after(self.unit, receipt)
            self.assertEqual(receipt, evidence["job_receipt"])
            write_text(self.path, original.decode())
            phases.append("factory disk restored")
            return receipt
        def outside(label, argv, expected):
            self.assertEqual(("outside-sync", ["pfm", "theme", "sync", "--json"], 0), (label, argv, expected))
            self.assertEqual(original, self.path.read_bytes())
            phases.append("outside sync")
            return {"output": json.dumps(self.report())}
        with patch.object(native_macos, "managed_unit", return_value=self.unit), patch.object(native_macos.faults, "unit_state", side_effect=unloaded), \
                patch.object(native_macos, "run_managed_unit", side_effect=factory):
            native_macos.own_job({}, self.inputs, str(self.root / "own-job"), evidence, outside)
        self.assertEqual([], self.failures(evidence["checks"]))
        self.assertEqual(["unloaded", "native ready", "native completed", "factory disk restored", "original job restored", "outside sync"], phases)
        with patch.object(native_macos, "managed_unit", return_value=self.unit), patch.object(native_macos.faults, "unit_state", side_effect=unloaded), \
                patch.object(native_macos, "run_managed_unit", return_value=receipt):
            with self.assertRaisesRegex(ValueError, "^MACOS-READER-JOB-OBSERVATION: native after callback did not run$"):
                native_macos.own_job({}, self.inputs, str(self.root / "missing-after"), {"checks": [], "units": []}, outside)

    def mcp_inputs(self):
        child = {"key": "reader-child", "session_id": "native-child", "engine": "claude", "account": 3,
                 "name": "reader-native", "project": "atlas", "workspace": "w2",
                 "terminal": {"backend": "herdr", "pane": "w2:p3"}}
        environment = {"HERDR_PANE_ID": "w2:p3", "HERDR_WORKSPACE_ID": "w2", "HERDR_SOCKET_PATH": "/fixture/herdr.sock"}
        return {"child": child, "child_observed": {"status": {"session_id": "native-child"}, "stored": {"recipe": {"env": ["HOME=/fixture/home"]}},
                    "terminal": {"identity": child["terminal"]}, "process_births": [{"pid": 71, "start": "child-birth", "environment": environment,
                        "chain": [{"pid": 71, "parent": 70, "start": "child-birth", "argv": "claude --session-id native-child"}]}]},
                "producer": {"pid": 53, "start": "daemon-birth", "chain": [{"pid": 53, "parent": 1, "start": "daemon-birth", "argv": "/fixture/pfm mcp serve"}],
                             "environment": {"XPC_SERVICE_NAME": "com.professor.pfm-reader.mcp"}},
                "expected_context": {"run": "owned-run", "beat": "owned-beat"},
                "action_records": [{"run": "owned-run", "beat": "owned-beat", "pid": 53, "action": "chat.new", "door": "mcp"}],
                "tool_records": [{"run": "owned-run", "beat": "owned-beat", "pid": 53, "msg": "mcp.call", "tool": "new"}],
                "creation_result": {"status": "ok", "code": 0, "message": "native creation output"},
                "expected_child": {"engine": "claude", "account": 3, "name": "reader-native"}}

    def test_mcp_producer_requires_current_native_daemon_child_and_clean_recipe(self):
        inputs = self.mcp_inputs()
        cases = (("action", "native-mcp-action-producer"), ("tool", "native-mcp-tool-producer"),
                 ("argv", "native-mcp-daemon-argv"), ("birth", "native-mcp-daemon-birth"),
                 ("xpc", "native-mcp-daemon-xpc"), ("result", "native-mcp-creation-result"),
                 ("child", "native-mcp-requested-child"), ("backend", "native-mcp-child-backend"),
                 ("session", "native-mcp-child-session"), ("environment", "native-mcp-child-pane-environment"),
                 ("missing-engine", "native-mcp-child-pane-environment"), ("capture", "native-mcp-recipe-terminal-captures"))
        with patch.object(native_macos, "journey_os", return_value="macos"):
            for name in ("macos-mcp-recipe", "macos-mcp-terminal"):
                self.assertEqual([], self.failures(native_macos.mcp_checks(name, inputs)))
                for broken, field in cases:
                    with self.subTest(plan=name, broken=broken):
                        actual = copy.deepcopy(inputs)
                        if broken in {"action", "tool"}:
                            actual[broken + "_records"][0]["pid"] = 99
                        elif broken == "argv":
                            actual["producer"]["chain"][0]["argv"] = "pfm mcp stdio"
                        elif broken == "birth":
                            actual["producer"]["chain"][0]["start"] = "reused-daemon"
                        elif broken == "xpc":
                            actual["producer"]["environment"]["XPC_SERVICE_NAME"] = "com.professor.foreign.mcp"
                        elif broken == "result":
                            actual["creation_result"]["code"] = 1
                        elif broken == "child":
                            actual["child"]["name"] = "other-child"
                        elif broken == "backend":
                            actual["child"]["terminal"]["backend"] = "tmux"
                        elif broken == "session":
                            actual["child_observed"]["status"]["session_id"] = "other-session"
                        elif broken == "environment":
                            actual["child_observed"]["process_births"][0]["environment"]["HERDR_PANE_ID"] = "w1:p1"
                        elif broken == "missing-engine":
                            actual["child_observed"]["process_births"] = []
                        else:
                            actual["child_observed"]["stored"]["recipe"]["env"].append("HERDR_ENV=1")
                        self.assertEqual([field], self.failures(native_macos.mcp_checks(name, actual)))
        with patch.object(native_macos, "journey_os", return_value="linux"):
            self.assertEqual(["native-mcp-platform"], self.failures(native_macos.mcp_checks("macos-mcp-recipe", inputs)))

    def test_mcp_run_uses_public_text_roster_receipt_and_full_observations(self):
        inputs = self.mcp_inputs()
        carry = self.root / "carry.json"
        write_json(carry, {"chats": [inputs["child"]]})
        output = "kind\tid\tname\taccount\tproject\tcwd\tsocket\tkilled\nclaude\tnative-child\treader-native\t3\tatlas\t/fixture/atlas\t/fixture/herdr.sock\tfalse\n"
        prefix = self.root / "artifacts/mcp"
        for status, warning, failed in ((0, "", []), (1, "", ["native-mcp-roster-exit"]),
                (0, "unexpected native warning\n", ["native-mcp-roster-warnings"])):
            with self.subTest(status=status, warning=warning):
                with patch.dict(os.environ, {"JOURNEY_CARRY": str(carry)}), patch.object(native_macos, "journey_os", return_value="macos"), \
                        patch.object(native_macos, "checked_owner"), patch.object(native_macos, "bundle", return_value={"full": "observed"}) as observe, \
                        patch.object(native_macos, "action", return_value={"exit_code": status, "output": warning + output}) as execute:
                    result = native_macos.run("macos-mcp-recipe", str(self.owner_file), inputs, prefix)
                self.assertEqual(failed, self.failures(result["checks"]))
                self.assertEqual(3, observe.call_count)
                self.assertEqual([{"prefix": str(prefix) + ".native-list", "argv": ["pfm", "ls", "--all", "--tsv"]}], result["receipts"])
                self.assertEqual(["pfm", "ls", "--all", "--tsv"], execute.call_args.args[1])
                self.assertEqual(result["checks"], read_json(str(prefix) + ".checks.json"))


if __name__ == "__main__":
    unittest.main()
