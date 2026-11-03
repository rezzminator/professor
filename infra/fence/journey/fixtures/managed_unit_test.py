#!/usr/bin/env python3
"""Managed-unit protocol projections and restoration, without claiming native Mac execution."""
import os
from pathlib import Path
import plistlib
import shutil
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch

import journey_path
import managed_unit
import observation
from runtime import command, read_json, write_json, write_text


class ManagedUnit(unittest.TestCase):
    def setUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory(prefix="managed-unit-")))
        home = self.root / "home"
        agents = home / "Library/LaunchAgents"
        agents.mkdir(parents=True)
        self.enterContext(patch.dict(os.environ, {"JOURNEY_ROOT": str(self.root), "HOME": str(home), "PFM_UNIT_PREFIX": "pfm-owned"}))
        self.enterContext(patch.object(managed_unit, "journey_os", return_value="macos"))
        self.label = "com.professor.pfm-owned.theme"
        self.target = f"gui/{os.getuid()}/{self.label}"
        self.path = agents / (self.label + ".plist")
        self.definition = {"Label": self.label, "ProgramArguments": ["/fixture/pfm", "theme", "sync"],
                           "WatchPaths": ["/fixture/settings.json", "/fixture/themes"], "EnvironmentVariables": {"PFM_UNIT_PREFIX": "pfm-owned"}}
        self.original = plistlib.dumps(self.definition)
        self.path.write_bytes(self.original)
        self.path.chmod(0o640)
        self.birth = {"pid": 42, "parent": 1, "start": "fixture birth", "argv": "/fixture/pfm theme sync", "state": "S"}

    def manager_reply(self, stdout="", status=0, stderr=""):
        def command(arguments, *, capture=None, **options):
            self.assertEqual(["launchctl", "print", self.target], arguments)
            capture.update(argv=arguments, stdout=stdout, stderr=stderr, status=status)
            return stdout
        return command

    def test_observer_separates_native_manager_disk_and_kernel(self):
        output = "job = {\n\tstate = running\n\tpid = 42\n\tlast exit code = 7\n}\n"
        with patch.object(managed_unit, "command", side_effect=self.manager_reply(output)), \
                patch.object(observation, "process_snapshot", return_value={42: self.birth}):
            actual = managed_unit.observe("theme")
        self.assertEqual({"platform": "darwin", "label": self.label, "target": self.target, "state": "running", "pid": 42,
                          "last_exit_code": 7, "receipt": {"argv": ["launchctl", "print", self.target], "stdout": output, "stderr": "", "status": 0},
                          "start": "fixture birth", "argv": "/fixture/pfm theme sync",
                          "definition": {"path": str(self.path), **{key: self.definition[key] for key in ("ProgramArguments", "WatchPaths", "EnvironmentVariables")}}}, actual)
        for output, expected in (("state = waiting\n", ("waiting", None, None)), ("state = not running\nlast exit code = 0\n", ("not running", None, 0)),
                                 ("state = waiting\nlast exit code = (never exited)\n", ("waiting", None, None))):
            with self.subTest(output=output), patch.object(managed_unit, "command", side_effect=self.manager_reply(output)):
                actual = managed_unit.observe("theme")
                self.assertEqual(expected, (actual["state"], actual["pid"], actual["last_exit_code"]))
                self.assertEqual(self.definition["WatchPaths"], actual["definition"]["WatchPaths"])
        self.path.unlink()
        with patch.object(managed_unit, "command", side_effect=self.manager_reply(status=113, stderr='Could not find service "owned"')):
            actual = managed_unit.observe("theme")
            self.assertEqual(("absent", None, None), (actual["state"], actual["definition"], actual["start"]))

    def test_observer_refuses_unreadable_and_changed_native_identity(self):
        for output, status, stderr, error in (("", 0, "", "UNREADABLE"), ("", 113, "permission denied", "UNREADABLE"),
                ("state = running\n", 0, "", "PID-STATE"), ("state = waiting\npid = 42\n", 0, "", "PID-STATE"),
                ("state = running\npid = nope\n", 0, "", "NUMBER"), ("state = waiting\nstate = running\n", 0, "", "AMBIGUOUS")):
            with self.subTest(output=output), patch.object(managed_unit, "command", side_effect=self.manager_reply(output, status, stderr)), \
                    self.assertRaisesRegex(ValueError, "^MANAGED-UNIT-" + error + ":"):
                managed_unit.observe("theme")
        with patch.object(managed_unit, "command", side_effect=self.manager_reply("state = running\npid = 42\n")):
            for snapshots, error in (([{}], "KERNEL-ABSENT"), ([{42: self.birth}, {42: {**self.birth, "start": "replacement"}}], "BIRTH-CHANGED")):
                with self.subTest(error=error), patch.object(observation, "process_snapshot", side_effect=snapshots), \
                        self.assertRaisesRegex(ValueError, "^MANAGED-UNIT-" + error + ":"):
                    managed_unit.observe("theme")
        self.path.write_bytes(plistlib.dumps({"Label": "foreign"}))
        with self.assertRaisesRegex(ValueError, "^MANAGED-UNIT-DEFINITION:"):
            managed_unit.observe("theme")

    def test_factory_retains_native_receipts_and_restores_unloaded_definition_after_stale_callback(self):
        import bootstrap
        for failure in (False, True, "birth", "xpc", "ancestry"):
            with self.subTest(failure=failure):
                prefix = self.root / ("case-" + str(failure).lower())
                calls = []
                loaded = [False]
                completed = []
                job = {"pid": 42, "start": "fixture birth", "state": "running"}
                ready = {"birth": {"pid": 42, "start": "wrong birth" if failure == "birth" else "fixture birth",
                                   "chain": [{**self.birth, "parent": 99 if failure == "ancestry" else 1}]},
                         "xpc_service_name": "foreign" if failure == "xpc" else self.label}
                result = {"stdout": "sync report\n", "stderr": "native warning\n", "status": 3, "birth": {"pid": 43, "start": "public birth"}}
                def command(arguments):
                    calls.append(arguments)
                    loaded[0] = arguments[1] == "bootstrap"
                    if loaded[0]:
                        definition = plistlib.loads(self.path.read_bytes())
                        self.assertEqual(self.label, definition["Label"])
                        self.assertEqual(False, "XPC_SERVICE_NAME" in definition["EnvironmentVariables"])
                        write_json(str(prefix) + ".ready.json", ready)
                def wait(label, predicate, bound):
                    if label == "managed unit command completion":
                        self.assertEqual("release\n", Path(str(prefix) + ".release").read_text())
                        write_json(str(prefix) + ".result.json", result)
                    actual = predicate()
                    self.assertNotEqual(False, actual, label)
                    return actual
                def before(actual):
                    self.assertEqual(job, actual)
                    write_text(self.path, "stale plist written after native ready\n")
                    if failure is True:
                        raise RuntimeError("owner failed")
                def after(actual, receipt):
                    self.assertEqual(job, actual)
                    self.assertEqual(result, {key: receipt[key] for key in result})
                    self.assertEqual(True, loaded[0])
                    self.assertEqual("stale plist written after native ready\n", self.path.read_text())
                    completed.append(receipt["status"])
                with patch.object(managed_unit, "observe", side_effect=[{"state": "absent"}, job]), \
                        patch.object(managed_unit, "manager", side_effect=lambda target: {"state": "running" if loaded[0] else "absent"}), \
                        patch.object(managed_unit, "command", side_effect=command), patch.object(bootstrap, "wait", side_effect=wait):
                    if failure in ("birth", "xpc", "ancestry"):
                        with self.assertRaisesRegex(ValueError, "^MANAGED-UNIT-ANCESTRY:"):
                            managed_unit.run("theme", ["pfm", "theme", "sync"], prefix, before=before)
                    elif failure:
                        with self.assertRaisesRegex(RuntimeError, "^owner failed$"):
                            managed_unit.run("theme", ["pfm", "theme", "sync"], prefix, before=before)
                    else:
                        actual = managed_unit.run("theme", ["pfm", "theme", "sync"], prefix, before=before, after=after)
                        self.assertEqual([3], completed)
                        self.assertEqual(result, {key: actual[key] for key in result})
                        self.assertEqual({"before": {"state": "absent"}, "held": job, "native": ready}, read_json(str(prefix) + ".job.json"))
                self.assertEqual([["launchctl", "bootstrap", f"gui/{os.getuid()}", str(self.path)], ["launchctl", "bootout", self.target]], calls)
                self.assertEqual(self.original, self.path.read_bytes())
                self.assertEqual(0o640, self.path.stat().st_mode & 0o777)
                self.assertEqual(False, loaded[0])

    def test_factory_refuses_foreign_loaded_or_reused_artifacts(self):
        with patch.object(managed_unit, "observe", return_value={"state": "waiting"}), \
                patch.object(managed_unit, "command", side_effect=AssertionError("loaded unit reached native transport")), \
                self.assertRaisesRegex(ValueError, "^MANAGED-UNIT-LOADED:"):
            managed_unit.run("theme", ["pfm", "theme", "sync"], self.root / "loaded")
        for variables, name in (({"PFM_UNIT_PREFIX": "pfm"}, "theme"), ({"PFM_UNIT_PREFIX": "../escape"}, "theme"), ({}, "mcp")):
            with self.subTest(variables=variables, name=name), patch.dict(os.environ, variables), self.assertRaisesRegex(ValueError, "^MANAGED-UNIT-OWNERSHIP:"):
                managed_unit.observe(name)
        with patch.object(managed_unit, "journey_os", return_value="linux"), self.assertRaisesRegex(ValueError, "^MANAGED-UNIT-PLATFORM:"):
            managed_unit.observe("theme")
        write_text(self.root / "reuse.out", "prior result\n")
        with self.assertRaisesRegex(ValueError, "^MANAGED-UNIT-ARTIFACT-EXISTS:"):
            managed_unit.run("theme", ["pfm", "theme", "sync"], self.root / "reuse")
        self.assertEqual("prior result\n", (self.root / "reuse.out").read_text())

    def test_supervisor_retains_child_receipt_and_refuses_missing_identity(self):
        import bootstrap
        for invalid in (False, True, "missing"):
            with self.subTest(invalid=invalid):
                prefix = self.root / ("missing-child" if invalid == "missing" else "wrong-child" if invalid else "public-child")
                write_json(str(prefix) + ".request.json", {"argv": ["pfm", "theme", "sync"], "label": self.label, "bound": 30})
                write_text(str(prefix) + ".release", "release\n")
                child = Mock(pid=43, returncode=3)
                parent = {"pid": 42, "start": "supervisor", "chain": [self.birth]}
                birth = {"pid": 43, "start": "public birth", "chain": [{"parent": os.getpid() + (1 if invalid else 0)}],
                         "environment": {"XPC_SERVICE_NAME": self.label}}
                evidence = ValueError("OBSERVE-PROCESS-ABSENT: pid=43 origin=43") if invalid == "missing" else [birth]
                def native_command(arguments, *, capture, started, allowed_codes, timeout, env):
                    self.assertEqual(["pfm", "theme", "sync"], arguments)
                    self.assertEqual({**os.environ, "PFM_DOOR": "cli"}, env)
                    self.assertEqual((True, True, True, 30), (-9 in allowed_codes, 0 in allowed_codes, 3 in allowed_codes, timeout))
                    started(child)
                    capture.update(argv=arguments, stdout="native report\n", stderr="native warning\n", status=3)
                with patch.object(managed_unit, "command", side_effect=native_command), \
                        patch.object(observation, "process_tree", side_effect=[[parent], evidence]), \
                        patch.object(bootstrap, "wait", side_effect=lambda label, predicate, bound: predicate()):
                    managed_unit.invoke(prefix)
                result = read_json(str(prefix) + ".result.json")
                self.assertEqual({"birth": parent, "xpc_service_name": os.environ.get("XPC_SERVICE_NAME")}, read_json(str(prefix) + ".ready.json"))
                if invalid:
                    expected = str(evidence) if invalid == "missing" else "MANAGED-UNIT-CHILD-ANCESTRY: " + repr(birth)
                    self.assertEqual({"error": expected}, result)
                else:
                    self.assertEqual({"argv": ["pfm", "theme", "sync"], "stdout": "native report\n", "stderr": "native warning\n", "status": 3, "birth": birth}, result)
                    self.assertEqual(("native report\n", "native warning\n", "3\n"), tuple(Path(str(prefix) + suffix).read_text() for suffix in (".out", ".err", ".status")))

    def test_private_mutations_fail_on_assertions(self):
        source = Path(managed_unit.__file__).read_text()
        mutations = (
            ('if after is not None:', 'if False:', 'test_factory_retains_native_receipts_and_restores_unloaded_definition_after_stale_callback'),
            ('"PFM_DOOR": "cli"', '"PFM_DOOR": "prelude"', 'test_supervisor_retains_child_receipt_and_refuses_missing_identity'),
            ('content.get(key) for key in', '([] if key == "WatchPaths" else content.get(key)) for key in', 'test_observer_separates_native_manager_disk_and_kernel'),
            ('current["start"] != birth["start"]', 'False', 'test_observer_refuses_unreadable_and_changed_native_identity'),
            ('with interruptible(), preserved_file(path):', 'if True:', 'test_factory_retains_native_receipts_and_restores_unloaded_definition_after_stale_callback'),
            ('original["state"] != "absent"', 'False', 'test_factory_refuses_foreign_loaded_or_reused_artifacts'),
            ('str(result["status"]) + "\\n"', 'str(0) + "\\n"', 'test_supervisor_retains_child_receipt_and_refuses_missing_identity'),
            ('if birth["chain"][0]["parent"] != os.getpid() or birth["environment"].get("XPC_SERVICE_NAME") != request["label"]:',
             'if False:', 'test_supervisor_retains_child_receipt_and_refuses_missing_identity'),
        )
        private = self.root / "private"
        (private / "fixtures").mkdir(parents=True)
        for path in journey_path.JOURNEY.glob("*.py"):
            shutil.copy2(path, private / path.name)
        for name in ("journey_path.py", "managed_unit_test.py"):
            shutil.copy2(Path(__file__).parent / name, private / "fixtures" / name)
        for old, new, test in mutations:
            with self.subTest(test=test):
                self.assertEqual(1, source.count(old), old)
                write_text(private / "managed_unit.py", source.replace(old, new))
                receipt = {}
                command([sys.executable, "-B", str(private / "fixtures/managed_unit_test.py"), "ManagedUnit." + test],
                        allowed_codes=(0, 1), capture=receipt)
                errors = [line for line in receipt["stderr"].splitlines() if line.startswith(("AssertionError:", "ERROR:"))]
                self.assertEqual((1, True, False), (receipt["status"], any(line.startswith("AssertionError:") for line in errors), any(line.startswith("ERROR:") for line in errors)), receipt)
                print("WATCHED ASSERTION-RED", test, errors[0])
        write_text(private / "managed_unit.py", source)
        receipt = {}
        command([sys.executable, "-B", str(private / "fixtures/managed_unit_test.py"),
                 *("ManagedUnit." + test for test in dict.fromkeys(row[2] for row in mutations))], allowed_codes=(0, 1), capture=receipt)
        self.assertEqual(0, receipt["status"], receipt)


if __name__ == "__main__":
    unittest.main()
