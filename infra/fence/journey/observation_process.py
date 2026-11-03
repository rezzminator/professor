#!/usr/bin/env python3
"""Read-only process, reporter and account paths; never credential values."""
import ctypes
from contextlib import contextmanager
import errno
import hashlib
import json
import os
from pathlib import Path
import shlex
import stat
import sys
import threading

from runtime import command
from observation_accounts import default_account_path, account_stats_cache

KEYS = {"CLAUDE_CONFIG_DIR", "CODEX_HOME", "OPENCODE_CONFIG", "OPENCODE_CONFIG_DIR",
        "MOCK_ENGINE_SCENARIO", "PFM_TEST_FRESH_SOCKET",
        "PFM_RUN", "PFM_BEAT_FILE", "HERDR_SOCKET_PATH", "HERDR_PANE_ID", "TMUX", "TMUX_PANE",
        "CODEX_TOOL_CALL_ID", "NO_COLOR", "PAGER", "GIT_PAGER", "GH_PAGER", "CODEX_CI", "CODEX_SESSION_ID",
        "CODEX_VERSION", "CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS", "HERDR_ENV", "HERDR_WORKSPACE_ID",
        "HERDR_TAB_ID", "HERDR_BIN_PATH", "PFM_CLAUDE_CONFIG_DIR_DEFAULT", "XPC_SERVICE_NAME",
        "PFM_COST_LEDGER", "PFM_COST_LEDGER_PID", "OTEL_EXPORTER_OTLP_ENDPOINT", "CLAUDE_CODE_ENABLE_TELEMETRY"}

_environment_denial_lock = threading.RLock()
_environment_denial_scope = None


def engine_births(observed, engine):
    rows = observed["process_births"] if "process_births" in observed else observed["terminal"]["births"]
    recipe = observed.get("stored", {}).get("recipe", {})
    binary = recipe.get("binary") if isinstance(recipe, dict) else None
    selected = {}
    for row in rows:
        command = shlex.split(row["chain"][0]["argv"])
        if command and (Path(command[0]).name in {engine, "mock-engine"} or (binary and command[0] == binary)):
            selected[(row["pid"], row["start"])] = {
                "pid": row["pid"], "start": row["start"], "argv": command,
                "environment": row["environment"], "terminal": observed["terminal"]["identity"]}
    return list(selected.values())


def process_metrics(pid):
    """Return cumulative kernel counters for one live process where exposed."""
    if sys.platform == "darwin":
        # Python has no libproc binding; layout: apple-oss-distributions/xnu bsd/sys/resource.h (rusage_info_v2).
        class RusageInfoV2(ctypes.Structure):
            _fields_ = [("ri_uuid", ctypes.c_uint8 * 16)] + [(name, ctypes.c_uint64) for name in (
                "ri_user_time", "ri_system_time", "ri_pkg_idle_wkups", "ri_interrupt_wkups", "ri_pageins",
                "ri_wired_size", "ri_resident_size", "ri_phys_footprint", "ri_proc_start_abstime",
                "ri_proc_exit_abstime", "ri_child_user_time", "ri_child_system_time", "ri_child_pkg_idle_wkups",
                "ri_child_interrupt_wkups", "ri_child_pageins", "ri_child_elapsed_abstime",
                "ri_diskio_bytesread", "ri_diskio_byteswritten")]
        read = ctypes.CDLL("/usr/lib/libproc.dylib", use_errno=True).proc_pid_rusage
        read.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_void_p]
        read.restype = ctypes.c_int
        usage = RusageInfoV2()
        if read(pid, 2, ctypes.byref(usage)) != 0:
            code = ctypes.get_errno()
            if code == errno.ESRCH:
                return {"cpu_seconds": "n/a:process-absent", "write_bytes": "n/a:process-absent"}
            if code in (errno.EPERM, errno.EACCES):
                return {"cpu_seconds": "n/a:process-permission", "write_bytes": "n/a:process-permission"}
            raise OSError(code, "read process resource usage", pid)
        class MachTimebaseInfo(ctypes.Structure):
            _fields_ = [("numer", ctypes.c_uint32), ("denom", ctypes.c_uint32)]
        # XNU tests/recount/recount_perf_tests.c converts rusage CPU ticks with mach_timebase_info.
        timebase_read = ctypes.CDLL(None).mach_timebase_info
        timebase_read.argtypes = [ctypes.POINTER(MachTimebaseInfo)]
        timebase_read.restype = ctypes.c_int
        timebase = MachTimebaseInfo()
        status = timebase_read(ctypes.byref(timebase))
        if status != 0 or timebase.numer == 0 or timebase.denom == 0:
            raise ValueError(f"OBSERVE-PROCESS-TIMEBASE: status={status} numer={timebase.numer} denom={timebase.denom}")
        return {"cpu_seconds": (usage.ri_user_time + usage.ri_system_time) * timebase.numer / timebase.denom / 1_000_000_000,
                "write_bytes": usage.ri_diskio_byteswritten}
    if not Path("/proc").is_dir():
        return {"cpu_seconds": "n/a:unsupported-platform", "write_bytes": "n/a:unsupported-platform"}
    try:
        fields = _linux_stat(pid)
        clock_tick = os.sysconf("SC_CLK_TCK")
        cpu_seconds = (int(fields[11]) + int(fields[12])) / clock_tick
    except FileNotFoundError:
        return {"cpu_seconds": "n/a:process-absent", "write_bytes": "n/a:process-absent"}
    except PermissionError:
        return {"cpu_seconds": "n/a:process-permission", "write_bytes": "n/a:process-permission"}
    except (OSError, ValueError, IndexError, TypeError):
        return {"cpu_seconds": "n/a:process-stat-malformed", "write_bytes": "n/a:process-stat-malformed"}
    writes = "n/a:write-bytes-unavailable"
    try:
        for line in (Path("/proc") / str(pid) / "io").read_text().splitlines():
            if line.startswith("write_bytes:"):
                writes = int(line.partition(":")[2].strip())
                break
    except FileNotFoundError:
        writes = "n/a:process-absent"
    except PermissionError:
        writes = "n/a:write-bytes-permission"
    except (OSError, ValueError):
        writes = "n/a:write-bytes-malformed"
    return {"cpu_seconds": cpu_seconds, "write_bytes": writes}


def _linux_stat(pid):
    return (Path("/proc") / str(pid) / "stat").read_text().rsplit(")", 1)[1].split()


def process_state(pid):
    """Read the native process state without substituting an unreadable process."""
    birth = _kernel_start(pid)
    state = command(["ps", "-p", str(pid), "-o", "state="]).strip() if sys.platform == "darwin" else _linux_stat(pid)[0]
    if len(state) != 1 or state not in "RSDTtXZPIUW":
        raise ValueError(f"OBSERVE-PROCESS-STATE-SHAPE: pid={pid}: {state!r}")
    actual = _kernel_start(pid)
    if actual != birth:
        raise ValueError(f"OBSERVE-PROCESS-STATE-BIRTH: pid={pid}: expected={birth} actual={actual}")
    return {"pid": pid, "start": birth, "state": state}


def process_cwd(pid):
    """Read the native working directory, bound to the same process birth."""
    birth = _kernel_start(pid)
    if sys.platform == "darwin":
        capture = {}
        output = command(["/usr/sbin/lsof", "-a", "-p", str(pid), "-d", "cwd", "-Fpnf0"],
                         allowed_codes=(0, 1), capture=capture)
        if capture["status"] != 0 or capture["stderr"]:
            raise RuntimeError(f"OBSERVE-PROCESS-CWD-READ: pid={pid}: exit={capture['status']} stderr={capture['stderr']!r}")
        # Python has no lsof field decoder; NUL fields retain whitespace in the native cwd name.
        fields = output.split("\0")
        if len(fields) != 4 or fields[:2] != [f"p{pid}", "\nfcwd"] or fields[3] != "\n" or not fields[2].startswith("n/"):
            raise ValueError(f"OBSERVE-PROCESS-CWD-SHAPE: pid={pid}: {output!r}")
        cwd = fields[2][1:]
    else:
        cwd = os.readlink(f"/proc/{pid}/cwd")
    actual_start = _kernel_start(pid)
    if actual_start != birth:
        raise ValueError(f"OBSERVE-PROCESS-CWD-BIRTH: pid={pid}: expected={birth} actual={actual_start}")
    return cwd


def is_indicator_process(argv):
    try:
        words = shlex.split(argv) if isinstance(argv, str) else list(argv)
    except ValueError:
        return False
    return any(words[index:index + 2] == ["internal", "herdr-indicators"] for index in range(len(words) - 1))


def _activity_path(session):
    digest = hashlib.sha256(session.encode()).hexdigest()
    return Path(os.environ["HOME"]) / ".local/state/pfm/herdr" / ("activity-" + digest + ".json")


def _blocked_owner(tool):
    parts = tool.split()
    if not parts:
        return None
    agent_id = None
    if parts[0] == "agent":
        if len(parts) < 3:
            raise ValueError(f"OBSERVE-REPORTER-ACTIVITY-OWNER: {tool!r}")
        agent_id, parts = parts[1], parts[2:]
    if not parts:
        raise ValueError(f"OBSERVE-REPORTER-ACTIVITY-OWNER: {tool!r}")
    return {"tool": parts[0], "input_fingerprint": parts[1] if len(parts) > 1 else None,
            "agent_id": agent_id}


def reporter_activity(chat):
    """Read the product Herdr activity record for one carried native Claude chat."""
    if chat.get("engine") not in {"claude", "cc"} or chat.get("terminal", {}).get("backend") != "herdr":
        return "n/a:reporter-activity"
    session = chat["session_id"]
    path = _activity_path(session)
    try:
        metadata = path.lstat()
    except FileNotFoundError:
        return "n/a:reporter-activity-absent"
    except OSError as error:
        raise ValueError(f"OBSERVE-REPORTER-ACTIVITY-STAT: {session}: {error}") from error
    if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISREG(metadata.st_mode):
        raise ValueError(f"OBSERVE-REPORTER-ACTIVITY-SHAPE: {session}")
    try:
        payload = json.loads(path.read_text())
    except (OSError, ValueError) as error:
        raise ValueError(f"OBSERVE-REPORTER-ACTIVITY-READ: {session}: {error}") from error
    if not isinstance(payload, dict) or not isinstance(payload.get("stored"), dict):
        raise ValueError(f"OBSERVE-REPORTER-ACTIVITY-SHAPE: {session}")
    stored = payload["stored"]
    if stored.get("session_id") not in {None, session}:
        raise ValueError(f"OBSERVE-REPORTER-ACTIVITY-SESSION: {session}: {stored.get('session_id')!r}")
    agents = stored.get("agents", {})
    if not isinstance(agents, dict):
        raise ValueError(f"OBSERVE-REPORTER-ACTIVITY-AGENTS: {session}")
    active = sorted(agent for agent, running in agents.items() if running)
    owner = _blocked_owner(stored["tool"]) if stored.get("state") == "blocked" and stored.get("tool") else None
    sequence = stored.get("seq", 0)
    if type(sequence) is not int or sequence < 0:
        raise ValueError(f"OBSERVE-REPORTER-ACTIVITY-SEQ: {session}: {sequence!r}")
    stopped = stored.get("main_stopped_at", 0)
    if type(stopped) is not int or stopped < 0:
        raise ValueError(f"OBSERVE-REPORTER-MAIN-STOPPED-AT: {session}: {stopped!r}")
    return {"session_id": session, "state": stored.get("state"), "blocked_owner": owner,
            "active_child_ids": active, "active_child_count": len(active), "event_sequence": sequence,
            "main_stopped_at": stopped}


def _owned_receipt(value):
    if not isinstance(value, (str, os.PathLike)):
        raise ValueError(f"OBSERVE-ENV-DENIAL-RECEIPT-PATH: absolute run-owned file required: {value!r}")
    path = Path(value)
    if not path.is_absolute():
        raise ValueError(f"OBSERVE-ENV-DENIAL-RECEIPT-PATH: absolute run-owned file required: {path}")
    try:
        root = Path(os.environ["JOURNEY_ROOT"]).resolve(strict=True)
        actual = path.resolve(strict=True)
        actual.relative_to(root)
    except (KeyError, FileNotFoundError, OSError, RuntimeError, ValueError) as error:
        raise ValueError(f"OBSERVE-ENV-DENIAL-RECEIPT-PATH: outside run or unreadable: {path}") from error
    try:
        metadata = path.lstat()
    except OSError as error:
        raise ValueError(f"OBSERVE-ENV-DENIAL-RECEIPT-READ: {path}: {error}") from error
    if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISREG(metadata.st_mode) or actual != path:
        raise ValueError(f"OBSERVE-ENV-DENIAL-RECEIPT-SHAPE: {path}")
    return path


def _receipt_evidence(path):
    try:
        receipt = json.loads(path.read_text())
    except (OSError, ValueError) as error:
        raise ValueError(f"OBSERVE-ENV-DENIAL-RECEIPT-READ: {path}: {error}") from error
    if not isinstance(receipt, dict) or set(receipt) != {"birth", "before", "denied"}:
        raise ValueError(f"OBSERVE-ENV-DENIAL-RECEIPT-SHAPE: {path}: {receipt!r}")
    birth = receipt["birth"]
    if (not isinstance(birth, dict) or type(birth.get("pid")) is not int or birth["pid"] <= 1 or
            not isinstance(birth.get("start"), str) or not birth["start"]):
        raise ValueError(f"OBSERVE-ENV-DENIAL-RECEIPT-BIRTH: {path}: {birth!r}")
    for label, expected_errno in (("before", 0), ("denied", errno.EACCES)):
        row = receipt[label]
        if (not isinstance(row, dict) or type(row.get("pid")) is not int or row["pid"] <= 1 or
                type(row.get("parent_pid")) is not int or row["parent_pid"] != birth["pid"] or
                type(row.get("errno")) is not int or row["errno"] != expected_errno):
            raise ValueError(f"OBSERVE-ENV-DENIAL-RECEIPT-{label.upper()}: {path}: {row!r}")
        if "capabilities" in row and (not isinstance(row["capabilities"], list) or
                                       not all(isinstance(capability, str) for capability in row["capabilities"])):
            raise ValueError(f"OBSERVE-ENV-DENIAL-RECEIPT-{label.upper()}: {path}: {row!r}")
    return receipt


def _kernel_start(pid):
    # The process table has one birth reader; use it instead of a second /proc stat parser.
    from observation import process_snapshot
    process = process_snapshot().get(pid)
    if process is None or not isinstance(process.get("start"), str):
        raise ValueError(f"OBSERVE-ENV-DENIAL-BIRTH: pid={pid}: process absent")
    return process["start"]


def _scope_for(pid):
    with _environment_denial_lock:
        scope = _environment_denial_scope
    return scope if scope is not None and scope["birth"]["pid"] == pid else None


def _denied_environment(pid, error):
    scope = _scope_for(pid)
    if scope is None or error.errno != errno.EACCES:
        raise error
    actual_start = _kernel_start(pid)
    expected_start = scope["birth"]["start"]
    if actual_start != expected_start:
        raise ValueError(f"OBSERVE-ENV-DENIAL-BIRTH: pid={pid}: expected={expected_start} actual={actual_start}")
    return dict(scope["evidence"])


@contextmanager
def expected_environment_denial(receipt_path):
    """Allow one live, receipt-proven native EACCES from the process environment door."""
    path = _owned_receipt(receipt_path)
    receipt = _receipt_evidence(path)
    birth = receipt["birth"]
    actual_start = _kernel_start(birth["pid"])
    if actual_start != birth["start"]:
        raise ValueError(f"OBSERVE-ENV-DENIAL-BIRTH: pid={birth['pid']}: expected={birth['start']} actual={actual_start}")
    evidence = {"state": "denied", "pid": birth["pid"], "start": birth["start"],
                "errno": receipt["denied"]["errno"], "receipt": str(path)}
    scope = {"birth": birth, "receipt": receipt, "evidence": evidence}
    global _environment_denial_scope
    with _environment_denial_lock:
        if _environment_denial_scope is not None:
            raise ValueError("OBSERVE-ENV-DENIAL-SCOPE: nested scope is not allowed")
        _environment_denial_scope = scope
    try:
        yield dict(evidence)
    finally:
        with _environment_denial_lock:
            if _environment_denial_scope is scope:
                _environment_denial_scope = None


def environment(pid):
    try:
        if sys.platform == "darwin":
            # Python has no KERN_PROCARGS2 decoder; ctypes reads Darwin's argc/NUL-framed ABI.
            libc = ctypes.CDLL(None, use_errno=True)
            names = (ctypes.c_int * 3)(1, 49, pid)
            size = ctypes.c_size_t()
            if libc.sysctl(names, 3, None, ctypes.byref(size), None, 0) != 0:
                raise OSError(ctypes.get_errno(), "read process arguments size", pid)
            if size.value < ctypes.sizeof(ctypes.c_int) or size.value > 16 * 1024 * 1024:
                raise ValueError(f"OBSERVE-PROCESS-ENV-SIZE: {pid}: {size.value}")
            buffer = ctypes.create_string_buffer(size.value)
            if libc.sysctl(names, 3, buffer, ctypes.byref(size), None, 0) != 0:
                raise OSError(ctypes.get_errno(), "read process arguments", pid)
            argc = ctypes.c_int.from_buffer_copy(buffer.raw).value
            _, separator, content = buffer.raw[ctypes.sizeof(ctypes.c_int):size.value].partition(b"\0")
            values = content.lstrip(b"\0").split(b"\0")
            if not separator or argc < 1 or argc > len(values):
                raise ValueError(f"OBSERVE-PROCESS-ENV-SHAPE: {pid}")
            content = b"\0".join(values[argc:])
        else:
            content = Path(f"/proc/{pid}/environ").read_bytes()
    except PermissionError as error:
        return _denied_environment(pid, error)
    scope = _scope_for(pid)
    if scope is not None:
        actual_start = _kernel_start(pid)
        expected_start = scope["birth"]["start"]
        if actual_start != expected_start:
            raise ValueError(f"OBSERVE-ENV-DENIAL-BIRTH: pid={pid}: expected={expected_start} actual={actual_start}")
        raise ValueError(f"OBSERVE-ENV-DENIAL-NOT-PROVEN: pid={pid}: native environment read succeeded")
    result = {}
    for entry in content.split(b"\0"):
        name, separator, value = entry.partition(b"=")
        key = os.fsdecode(name)
        if separator and key in KEYS:
            if key in result:
                raise ValueError(f"OBSERVE-PROCESS-ENV-DUPLICATE: {pid}: {key}")
            result[key] = os.fsdecode(value)
    return result
