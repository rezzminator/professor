"""The shared plugin-log library: level files under one directory per plugin, rows bounded and keyed in one order,
rotation past a cap, and rows held in memory across a failed write. It imports the standard library only.

This file is a vendored copy of the plugin-log canonical source, kept byte-identical in every plugin by a sync
check: change the canonical source, then re-vendor every copy.
"""
from datetime import datetime, timedelta, timezone
import json
import os
import sys
import threading
import time

CAP_BYTES = 20971520
"""A level file past this size moves aside to .1, older generations shifting up."""
KEEP = 5
"""Rotated generations each level file keeps (.1 newest ... .5)."""
ROW_MAX_BYTES = 4096
"""One row's bound in bytes, its newline included."""
HOLD_ROWS = 2000
"""Unwritten rows held for the next write; past it the oldest go, counted."""
LEVEL_FILES = {"error": "errors.jsonl", "warning": "warnings.jsonl", "info": "info.jsonl", "debug": "debug.jsonl"}
_EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


def log_dir(override, home, plugin):
    """$PLUGIN_LOG_DIR/{plugin} when override is set, else {home}/.local/state/{plugin}/logs; None is logging off."""
    if override:
        return override.rstrip("/") + "/" + plugin
    if home:
        return home.rstrip("/") + "/.local/state/" + plugin + "/logs"
    return None


def on_screen(level):
    """The screen shows error rows only."""
    return level == "error"


def stamp(now_ms):
    """ISO 8601 UTC with milliseconds, from an integer epoch in milliseconds."""
    return (_EPOCH + timedelta(milliseconds=int(now_ms))).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def level_row(fields, now_ms):
    """One JSON row with its newline, keys in order ts, plugin, level, event, msg, where, session, detail, at most
    ROW_MAX_BYTES: an oversized row's detail becomes {truncated, bytes} (bytes: the whole row's), then msg, session,
    where, event and plugin shorten in that order. `fields["ts"]`, when given, is the row's own time."""
    level = fields.get("level")
    if level not in LEVEL_FILES:
        raise ValueError("plugin log invalid level: %r" % (level,))
    row = {"ts": fields.get("ts") or stamp(now_ms), "plugin": fields["plugin"], "level": level, "event": fields["event"],
           "msg": fields["msg"], "where": fields["where"]}
    if fields.get("session"):
        row["session"] = fields["session"]
    if fields.get("detail") is not None:
        row["detail"] = fields["detail"]

    def encoded():
        # A lone surrogate becomes its JSON escape, as JSON.stringify writes it.
        return (json.dumps(row, ensure_ascii=False, separators=(",", ":"), allow_nan=False) + "\n").encode("utf-8", "backslashreplace")

    data = encoded()
    if len(data) <= ROW_MAX_BYTES:
        return data
    row["detail"] = {"truncated": True, "bytes": len(data)}
    for key in ("msg", "session", "where", "event", "plugin"):
        if key not in row:
            continue
        whole = row[key]
        low, high = 0, len(whole)
        while low < high:
            middle = (low + high + 1) // 2
            row[key] = whole[:middle]
            if len(encoded()) <= ROW_MAX_BYTES:
                low = middle
            else:
                high = middle - 1
        row[key] = whole[:low]
        if key == "session" and not row[key]:
            del row["session"]
        data = encoded()
        if len(data) <= ROW_MAX_BYTES:
            return data
    return data


def _rotate(path):
    try:
        size = os.stat(path).st_size
    except FileNotFoundError:
        return False
    if size <= CAP_BYTES:
        return False
    for n in range(KEEP - 1, 0, -1):
        try:
            os.replace("%s.%d" % (path, n), "%s.%d" % (path, n + 1))
        except FileNotFoundError:
            pass
    try:
        os.replace(path, path + ".1")
    except FileNotFoundError:
        # Another writer moved it aside first.
        return False
    return True


def _append(path, data):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
    try:
        written = os.write(fd, data)
        if written != len(data):
            raise OSError("short write to %s: %d of %d bytes" % (path, written, len(data)))
    finally:
        os.close(fd)


class PluginLog:
    """One plugin's rows under one directory (None is logging off). A row that cannot be written is held and written
    first by the next record; past HOLD_ROWS the oldest go, and the next write that succeeds carries a
    plugin-log-rows-dropped error row counting them. The screen gets one line per failing streak, re-armed by a
    success, and never a row. Writers in other processes take no lock with it: a rotation they race at worst shifts
    one generation more."""

    def __init__(self, plugin, directory, screen=None, now_ms=None):
        self.plugin = plugin
        self.dir = directory
        self.screen = screen
        self.now_ms = now_ms or (lambda: time.time_ns() // 1000000)
        self._lock = threading.Lock()
        self._pending = []
        self._lost = 0
        self._failing = False

    @property
    def held(self):
        with self._lock:
            return len(self._pending)

    def record(self, level, event, msg, where, session=None, detail=None, ts=None):
        """Write one row after every row held before it; never raises to its caller."""
        with self._lock:
            if self.dir is None:
                if not self._failing:
                    self._failing = True
                    self._say("plugin log off: no PLUGIN_LOG_DIR and no HOME")
                return
            fields = {"ts": ts or stamp(self.now_ms()), "plugin": self.plugin, "level": level, "event": event, "msg": msg,
                      "where": where, "session": session, "detail": detail}
            try:
                data = level_row(fields, 0)
            except (ValueError, TypeError, OverflowError) as error:
                # A row that cannot be encoded never can be: it is said, not held.
                self._fail(error)
                return
            self._pending.append((level, data, fields["ts"], session))
            over = len(self._pending) - HOLD_ROWS
            if over > 0:
                del self._pending[:over]
                self._lost += over
            self._flush()

    def _flush(self):
        if self._lost and self._pending:
            lost = self._lost
            text = "row was" if lost == 1 else "rows were"
            try:
                data = level_row({"ts": self._pending[0][2], "plugin": self.plugin, "level": "error", "event": "plugin-log-rows-dropped",
                                  "msg": "%d unwritten %s dropped past the %d-row hold" % (lost, text, HOLD_ROWS),
                                  "where": "plugin_log.py#PluginLog._flush", "detail": {"rows": lost}}, 0)
                self._write("error", data, self._pending[0][2], None)
            except (OSError, ValueError) as error:
                self._fail(error)
                return
            # Counted until a write takes its row: the count is never itself a held row the hold could drop.
            self._lost = 0
            self._failing = False
        while self._pending:
            try:
                self._write(*self._pending[0])
            except (OSError, ValueError) as error:
                self._fail(error)
                return
            del self._pending[0]
            self._failing = False

    def _write(self, level, data, ts, session):
        os.makedirs(self.dir, mode=0o700, exist_ok=True)
        name = LEVEL_FILES[level]
        path = os.path.join(self.dir, name)
        if _rotate(path):
            if name != "warnings.jsonl" and _rotate(os.path.join(self.dir, "warnings.jsonl")):
                self._capped("warnings.jsonl", ts, session)
            self._capped(name, ts, session)
        _append(path, data)

    def _capped(self, name, ts, session):
        data = level_row({"ts": ts, "plugin": self.plugin, "level": "warning", "event": "log-capped",
                          "msg": "level file exceeded %d bytes; moved aside" % CAP_BYTES, "where": "plugin_log.py#PluginLog._write",
                          "session": session, "detail": {"file": name}}, 0)
        _append(os.path.join(self.dir, "warnings.jsonl"), data)

    def _fail(self, error):
        if not self._failing:
            n = len(self._pending)
            cause = str(error).replace("\r", " ").replace("\n", "; ") or type(error).__name__
            self._say("plugin log %s failed: %s; %d %s kept for the next write" % (self.dir, cause, n, "row is" if n == 1 else "rows are"))
        self._failing = True

    def _say(self, line):
        screen = self.screen if self.screen is not None else sys.stderr
        try:
            print("%s: %s" % (self.plugin, line), file=screen, flush=True)
        except (OSError, ValueError):
            # The screen is the last place left to tell; a write it refuses has nowhere else to go.
            pass
