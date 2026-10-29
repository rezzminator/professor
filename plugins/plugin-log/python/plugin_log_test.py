"""Conformance of the Python plugin-log against ../spec.json."""
import io
import json
import os
import re
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import plugin_log  # noqa: E402

with open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "spec.json"), encoding="utf-8") as f:
    SPEC = json.load(f)
NOW = 1791991231123


def rows(path):
    if not os.path.exists(path):
        return []
    with open(path, encoding="utf-8") as f:
        return [json.loads(line) for line in f.read().splitlines()]


def fill(path):
    with open(path, "wb"):
        pass
    os.truncate(path, SPEC["capBytes"] + 1)


def capped(name):
    return {"ts": "2026-10-14T15:20:31.123Z", "plugin": "demo", **SPEC["capped"], "where": "plugin_log.py#PluginLog._write",
            "session": "s1", "detail": {"file": name}}


class Conformance(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.screen = io.StringIO()

    def logger(self, directory):
        return plugin_log.PluginLog("demo", directory, self.screen, lambda: NOW)

    def record(self, logger, msg, level="error"):
        logger.record(level, "write-failed", msg, "src/demo.py#write", session="s1")

    def test_constants(self):
        self.assertEqual([plugin_log.CAP_BYTES, plugin_log.KEEP, plugin_log.ROW_MAX_BYTES, plugin_log.HOLD_ROWS],
                         [SPEC["capBytes"], SPEC["keep"], SPEC["rowMaxBytes"], SPEC["holdRows"]])
        self.assertEqual(plugin_log.LEVEL_FILES, SPEC["files"])
        for level, shown in SPEC["onScreen"].items():
            self.assertEqual(plugin_log.on_screen(level), shown)

    def test_dirs(self):
        for v in SPEC["dirs"]:
            self.assertEqual(plugin_log.log_dir(v["override"], v["home"], v["plugin"]), v["dir"], v)

    def test_spec_rows(self):
        for v in SPEC["rows"]:
            with self.subTest(v["name"]):
                fields = dict(v["fields"])
                for key, (text, n) in (v.get("repeat") or {}).items():
                    fields[key] = text * n
                if v.get("repeatDetail"):
                    fields["detail"] = {key: text * n for key, (text, n) in v["repeatDetail"].items()}
                self.assertEqual(plugin_log.level_row(fields, v["nowMs"]).decode("utf-8"), v["row"])
                directory = os.path.join(self.tmp.name, "rows", str(SPEC["rows"].index(v)))
                logger = plugin_log.PluginLog(fields["plugin"], directory, self.screen, lambda: v["nowMs"])
                logger.record(fields["level"], fields["event"], fields["msg"], fields["where"], fields.get("session"), fields.get("detail"), fields.get("ts"))
                with open(os.path.join(directory, SPEC["files"][fields["level"]]), encoding="utf-8") as f:
                    self.assertEqual(f.read(), v["row"])
        self.assertEqual(self.screen.getvalue(), "")

    def test_levels_and_modes(self):
        directory = os.path.join(self.tmp.name, "logs", "demo")
        logger = self.logger(directory)
        for level in SPEC["files"]:
            self.record(logger, "x", level)
        self.assertEqual(os.stat(directory).st_mode & 0o777, SPEC["dirMode"])
        for level, name in SPEC["files"].items():
            got = rows(os.path.join(directory, name))
            self.assertEqual([(r["level"], r["plugin"]) for r in got], [(level, "demo")])
            self.assertEqual(os.stat(os.path.join(directory, name)).st_mode & 0o777, SPEC["fileMode"])

    def test_rotation(self):
        big = os.path.join(self.tmp.name, "errors.jsonl")
        for n in range(1, 6):
            with open("%s.%d" % (big, n), "w") as f:
                f.write("generation %d\n" % n)
        fill(big)
        self.record(self.logger(self.tmp.name), "new")
        self.assertEqual(os.stat(big + ".1").st_size, SPEC["capBytes"] + 1)
        for n in range(2, 6):
            with open("%s.%d" % (big, n)) as f:
                self.assertEqual(f.read(), "generation %d\n" % (n - 1))
        self.assertFalse(os.path.exists(big + ".6"))
        self.assertEqual([r["msg"] for r in rows(big)], ["new"])
        self.assertEqual(rows(os.path.join(self.tmp.name, "warnings.jsonl")), [capped("errors.jsonl")])

    def test_warnings_capped_first(self):
        fill(os.path.join(self.tmp.name, "errors.jsonl"))
        fill(os.path.join(self.tmp.name, "warnings.jsonl"))
        self.record(self.logger(self.tmp.name), "new")
        self.assertEqual(rows(os.path.join(self.tmp.name, "warnings.jsonl")), [capped("warnings.jsonl"), capped("errors.jsonl")])

    def test_streak_hold_and_recovery(self):
        directory = os.path.join(self.tmp.name, "logs")
        with open(directory, "w") as f:
            f.write("a file where the directory goes")
        logger = self.logger(directory)
        self.record(logger, "a")
        self.record(logger, "b", "info")
        line = re.compile("^demo: plugin log %s failed: .+; 1 row is kept for the next write\n$" % re.escape(directory))
        self.assertRegex(self.screen.getvalue(), line)
        self.assertEqual(logger.held, 2)
        os.remove(directory)
        self.record(logger, "c")
        self.assertEqual(logger.held, 0)
        self.assertEqual([r["msg"] for r in rows(os.path.join(directory, "errors.jsonl"))], ["a", "c"])
        self.assertEqual([r["msg"] for r in rows(os.path.join(directory, "info.jsonl"))], ["b"])
        for name in os.listdir(directory):
            os.remove(os.path.join(directory, name))
        os.rmdir(directory)
        open(directory, "w").close()
        self.screen.seek(0)
        self.screen.truncate()
        self.record(logger, "d")
        self.assertRegex(self.screen.getvalue(), line)

    def test_hold_drop(self):
        directory = os.path.join(self.tmp.name, "logs")
        open(directory, "w").close()
        logger = self.logger(directory)
        for n in range(SPEC["holdRows"] + 3):
            self.record(logger, str(n))
        self.assertEqual(logger.held, SPEC["holdRows"])
        os.remove(directory)
        self.record(logger, "last")
        got = rows(os.path.join(directory, "errors.jsonl"))
        drop = {"ts": "2026-10-14T15:20:31.123Z", "plugin": "demo", "level": SPEC["dropped"]["level"], "event": SPEC["dropped"]["event"],
                "msg": SPEC["dropped"]["msg"].replace("{rows}", "4"), "where": "plugin_log.py#PluginLog._flush", "detail": {"rows": 4}}
        self.assertEqual(len(got), SPEC["holdRows"] + 1)
        self.assertEqual(got[0], drop)
        self.assertEqual((got[1]["msg"], got[-1]["msg"]), ("4", "last"))

    def test_off(self):
        logger = self.logger(None)
        self.record(logger, "a")
        self.record(logger, "b")
        self.assertEqual(self.screen.getvalue(), SPEC["screen"]["off"].replace("{plugin}", "demo") + "\n")

    def test_invalid_level_is_said_not_held(self):
        logger = self.logger(self.tmp.name)
        self.record(logger, "x", "fatal")
        self.assertIn("invalid level", self.screen.getvalue())
        self.assertEqual(logger.held, 0)


if __name__ == "__main__":
    unittest.main()
