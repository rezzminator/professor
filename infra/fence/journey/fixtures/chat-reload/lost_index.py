"""Drive the real failed-seat retry while its indexed native tail is unavailable."""
from contextlib import ExitStack
import json
import os
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
sys.path.insert(0, str(Path(__file__).resolve().parent))
from bootstrap import wait
from activity import cursor, records
from chat import seats
from faults import deleted_cwd, interruptible, signalled_process, temporary_text, temporarily_missing_file
from continuation import indexed as cached_continuation
from observation import process_snapshot, terminal
from runtime import now_ms, read_json, write_json, write_text
from terminal import type_command
from projection import birth_presence


def writer_ended(birth, processes):
    return birth_presence(birth, processes) == "ended" or processes[birth["pid"]]["state"] == "Z"


def seat(chat):
    found = [row for row in seats() if row["seat_id"] == chat["seat_id"]]
    if len(found) != 1:
        raise ValueError(f"LOST-INDEX-SEAT: expected one owned seat; actual={found}")
    return found[0]


def complete_records(path, offset):
    # Native append may expose an unterminated suffix; JSON decodes committed lines only.
    try:
        data = path.read_bytes()[offset:]
    except FileNotFoundError:
        return []
    return [json.loads(line) for line in data.split(b"\n")[:-1] if line.strip()]


def held_replacement(journal, offset, original, previous):
    processes = process_snapshot()
    if birth_presence(original, processes) != "ended":
        return None
    found = {}
    for record in complete_records(journal, offset):
        pid = record["pid"]
        current = processes.get(pid)
        if current is None or (pid in previous and previous[pid]["start"] == current["start"]):
            continue
        found[pid] = {key: current[key] for key in ("pid", "start")}
    if len(found) > 1:
        raise ValueError(f"LOST-INDEX-HELD-AMBIGUOUS: {found}")
    return next(iter(found.values())) if found else None


def failed_worker(rows, note):
    # Diagnostic records carry no beat/run; the isolated cwd in this cause is run-owned.
    failures = [row for row in rows if row.get("msg") == "reload.failed" and note in row.get("err", "")]
    if not failures:
        return None
    expected = {"level": "ERROR", "exit": 1,
                "err": "pfm chat reload: native reload new engine failed: " + note}
    actual = [{key: row.get(key) for key in expected} for row in failures]
    if actual != [expected]:
        raise ValueError(f"LOST-INDEX-WORKER-FAILURE: expected={[expected]!r} actual={actual!r}")
    return failures[0]


def drive(chat, owner, indexed, original_birth, directory):
    started = now_ms()
    arguments = ["pfm", "chat", "reload", indexed["launch_id"]]
    failure_cursor = cursor(os.environ["JOURNEY_ACTIVITY"])
    actual = seat(chat)
    host_pid = actual["seat_host_pid"]
    initial = process_snapshot()
    if host_pid not in initial:
        raise ValueError(f"LOST-INDEX-HOST-ABSENT: {host_pid}")
    host = {key: initial[host_pid][key] for key in ("pid", "start")}
    native = read_json(directory / "continued-presence.json")
    cwd = native["cwd"]
    scenario = Path(chat["scenario"])
    original = read_json(scenario)
    journal = Path(original["record_dir"]) / "birth.json"
    if not journal.is_file():
        raise ValueError(f"LOST-INDEX-BIRTH-JOURNAL-ABSENT: {journal}")
    preparation_offset = journal.stat().st_size
    if (directory / "owner-cwd.status").read_text() != "0\n":
        raise ValueError("LOST-INDEX-OWNER-CWD: control shell pwd probe failed")
    owner_cwd = (directory / "owner-cwd.out").read_text().rstrip("\n")
    if owner_cwd == cwd:
        raise ValueError("LOST-INDEX-OWNER-CWD: control shell must survive the isolated cwd fault")

    def writer_stopped_and_pending():
        return writer_ended(original_birth, process_snapshot()) and seat(chat)["reload_pending"] == 1

    def failed():
        current = process_snapshot()
        row = seat(chat)
        if birth_presence(original_birth, current) != "ended" or row["state"] != "failed":
            return None
        text = terminal(chat["terminal"])["text"]
        if "press Enter to retry, q to close" not in text:
            return None
        expected_note = f"start foreground engine: chdir {cwd}: no such file or directory"
        if row["lost_note"] != expected_note:
            raise ValueError(f"LOST-INDEX-FAILURE-CAUSE: expected={expected_note!r} actual={row['lost_note']!r}")
        return {"seat": row, "screen": text, "original_birth": original_birth,
                "original_writer": "ended", "replacement_birth": "n/a:not-started"}

    # The stopped supervisor cannot start a replacement until its recorded cwd is missing.
    with ExitStack() as cwd_scope:
        with signalled_process(chat["terminal"], host, "STOP"):
            wait("owned supervisor stopped", lambda: process_snapshot()[host_pid]["state"] in {"T", "t"}, 15)
            type_command(owner, arguments, receipt=str(directory / "prepare-command"))
            wait("original writer ended and actual reload pending", writer_stopped_and_pending, 30)
            cwd_scope.enter_context(deleted_cwd(cwd))
        resumed = process_snapshot().get(host_pid)
        if resumed is None or resumed["start"] != host["start"]:
            raise ValueError("LOST-INDEX-HOST-CHANGED: supervisor birth changed before CONT")
        wait("owned supervisor CONT acknowledged", lambda: process_snapshot()[host_pid]["state"] not in {"T", "t"}, 15)
        evidence = wait("actual OS spawn failure and native retry UI", failed, 30)
        evidence["host_birth"] = host
        evidence["host_continued"] = True
        evidence["isolated_cwd"] = cwd
    evidence["cwd_restored"] = Path(cwd).is_dir()
    if complete_records(journal, preparation_offset):
        raise ValueError("LOST-INDEX-FAILED-START-CREATED-ENGINE: expected OS exec refusal before native birth")
    evidence["replacement_receipts"] = []

    # The outer worker records failure only after native reload has released its mutex.
    evidence["worker_failure"] = wait("failed reload worker completed", lambda: failed_worker(
        records(os.environ["JOURNEY_ACTIVITY"], failure_cursor), evidence["seat"]["lost_note"]), 30)
    evidence["wall_ms"] = now_ms() - started
    write_json(directory / "failed-seat-preparation.json", evidence)

    # No native writer exists here: the failed startup never created an engine process.
    if failed() is None:
        raise ValueError("LOST-INDEX-FAILED-PRECONDITION: retryable failed seat changed")
    replacement = dict(original)
    replacement.pop("startup_exit_code", None)
    gate = directory / "replacement.ready"
    if gate.exists():
        raise ValueError(f"LOST-INDEX-GATE-EXISTS: {gate}")
    replacement["ready_hold"] = str(gate)
    replacement["ready_timeout_ms"] = 30000
    offset = journal.stat().st_size if journal.exists() else 0
    previous = process_snapshot()
    retry_started = now_ms()
    try:
        with temporary_text(scenario, json.dumps(replacement)):
            with temporarily_missing_file(indexed["path"]):
                if Path(indexed["path"]).exists():
                    raise ValueError("LOST-INDEX-PATH-PRESENT: fault did not remove cached tail")
                during = cached_continuation(chat)
                write_json(directory / "cached-index-during-fault.json", during)
                if during["tail_id"] != indexed["tail_id"] or during["path"] != indexed["path"]:
                    raise ValueError(f"LOST-INDEX-CACHE-CHANGED: expected={indexed!r} actual={during!r}")
                type_command(owner, arguments, receipt=str(directory / "retry-command"))
                held = wait("new actual replacement held before native startup",
                            lambda: held_replacement(journal, offset, original_birth, previous), 30)
                write_json(directory / "held-replacement.json", held)
        if not Path(indexed["path"]).is_file():
            raise ValueError("LOST-INDEX-PATH-NOT-RESTORED: cached tail missing before release")
    finally:
        write_text(gate, "release\n")
    write_json(directory / "lost-index-timing.json", {"preparation_ms": evidence["wall_ms"],
               "retry_ms": now_ms() - retry_started, "controller_ms": now_ms() - started})


if __name__ == "__main__":
    try:
        with interruptible():
            drive(read_json(sys.argv[1]), read_json(sys.argv[2]), read_json(sys.argv[3]),
                  json.loads(sys.argv[4]), Path(sys.argv[5]))
    except (OSError, ValueError, RuntimeError, KeyError, TypeError) as error:
        print(f"LOST-INDEX: {error}", file=sys.stderr)
        sys.exit(2)
