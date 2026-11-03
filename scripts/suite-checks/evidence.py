"""Readers for recorded runs, assertion retirements and watched-red mutations."""

import hashlib
import fnmatch
import csv
import json
import re
import tempfile
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path

from core import Context, Unavailable, nonempty, require


PASS = {"PASS", "✓"}
FAIL = {"FAIL", "✗"}


def green(c, rows):
    gaps = [row[0] for row in c.policy_rows(c.suite / "gaps.tsv", 5, "row", excuses=True)]
    return all(record.get("verdict") in PASS or
               record.get("verdict") in {"KNOWN", "known"} and
               any(fnmatch.fnmatchcase(row, pattern) for pattern in gaps)
               for row, record in rows.items())


def ledger(c, name):
    path = c.runs / name
    require(path.resolve().is_relative_to(c.runs.resolve()), f"run path escapes runs/: {name}")
    text = c.read(path, nonempty=True)
    headers, rows = {}, {}
    columns = None
    for line in text.splitlines():
        if not line or line.startswith("#"):
            continue
        if "\t" in line:
            fields = next(csv.reader([line], delimiter="\t"))
            if fields[0] == "row":
                columns = fields
                continue
            if columns is None or len(fields) != len(columns):
                raise Unavailable(f"malformed run row in {path}: {line}")
            require(fields[0] not in rows, f"duplicate run row: {fields[0]}")
            rows[fields[0]] = dict(zip(columns[1:], fields[1:]))
        elif ": " in line:
            key, value = line.split(": ", 1)
            require(key not in headers, f"duplicate run header: {key}")
            headers[key] = value
        elif line.endswith(":"):
            headers[line[:-1]] = ""
    nonempty(rows, f"no run rows in {path}")
    expected = {"row", "verdict", "assertions-by-id", "expected", "ops", "goldens", "wall_ms", "t+s"}
    if columns is None or set(columns) != expected:
        raise Unavailable(f"run columns differ from contract: {path}")
    required = {"command", "mode", "boot", "runner_pid", "order", "config", "root", "tree", "dirty", "start", "load"}
    if required - set(headers):
        raise Unavailable(f"run headers missing in {path}: {','.join(sorted(required - set(headers)))}")
    return headers, rows


def runs(c, pattern="*.tsv"):
    directory = c.runs
    if not directory.is_dir():
        raise Unavailable(f"ledgers unreadable: {directory}")
    paths = sorted(directory.glob(pattern))
    nonempty(paths, f"no usable ledger matching {pattern}")
    return [(path.name, *ledger(c, path.name)) for path in paths]


def canonical(c):
    result = [(name, header, rows) for name, header, rows in runs(c) if header.get("mode") == "sequence"]
    return nonempty(result, "no canonical ledger")


def activity(c, name, header, allow_empty=False):
    path = Path(header.get("log", Path(name).stem + ".activity.jsonl"))
    path = c.runs / (path.name if path.is_absolute() else path)
    records = [json.loads(line) for line in c.lines(path)]
    run_id = header.get("run", Path(name).stem)
    selected = [record for record in records if record.get("run") == run_id]
    if not allow_empty:
        nonempty(selected, f"LOG-ABSENT: no attributable activity for {run_id}")
    require(all(all(record.get(field) for field in ("run", "beat", "op", "pid")) for record in selected),
            f"activity lacks run/beat/op/pid: {run_id}")
    return selected


def engine_input(c, header, row, identity, result, expected_input, actions):
    require(not result.get("ops") and not actions,
            f"ENGINE-INPUT-OPS: {row} must not credit a pfm action")
    assertions = json.loads(result.get("expected", "[]"))
    require(isinstance(assertions, list), f"ENGINE-INPUT-ABSENT: {row}")
    assertions = [item for item in assertions if item.get("id") == identity]
    inputs = [item for item in assertions if item.get("source", "").startswith("engine-input:")]
    require(len(inputs) == 1, f"ENGINE-INPUT-ABSENT: {row}")
    item = inputs[0]
    source = Path(item["source"].removeprefix("engine-input:"))
    origin = Path(header.get("log", str(c.runs / "activity.jsonl"))).parent
    require(not source.is_absolute() or source.is_relative_to(origin),
            f"ENGINE-INPUT-FOREIGN: {row}: {source}")
    relative = source.relative_to(origin) if source.is_absolute() else source
    beat_dir = Path("beats") / row
    require(relative.parent == beat_dir and not source.is_symlink(),
            f"ENGINE-INPUT-FOREIGN: {row}: {source}")
    artifacts = Path(header.get("artifacts", "."))
    require(not artifacts.is_absolute() and ".." not in artifacts.parts,
            f"ENGINE-ARTIFACTS-FOREIGN: {row}: {artifacts}")
    artifact_root = c.runs / artifacts
    require(artifact_root.resolve().is_relative_to(c.runs.resolve()),
            f"ENGINE-ARTIFACTS-FOREIGN: {row}: {artifacts}")
    receipt = artifact_root / relative
    require(not receipt.is_symlink() and receipt.resolve().is_relative_to(artifact_root.resolve())
            and receipt.resolve().parent == (artifact_root / beat_dir).resolve(),
            f"ENGINE-INPUT-FOREIGN: {row}: {source}")
    recorded = c.read(receipt, True).rstrip("\n")
    require(item.get("pass") is True and item.get("expected") == item.get("actual") == recorded == expected_input,
            f"ENGINE-INPUT-MISMATCH: {row}: expected {expected_input!r}, recorded {recorded!r}")
    require(any(item.get("source") == "engine-state:regular-file" and item.get("pass") is True
                and item.get("expected") == item.get("actual") == "regular-file" for item in assertions),
            f"ENGINE-STATE-ABSENT: {row}: regular-file")
    goldens = json.loads(result.get("goldens", "[]"))
    require(any(item.get("pass") is True and isinstance(item.get("expected"), str)
                and item.get("expected") == item.get("actual")
                and any(golden.get("path") == item.get("source")
                        and golden.get("sha256") == hashlib.sha256(item["expected"].encode()).hexdigest()
                        for golden in goldens) for item in assertions),
            f"ENGINE-STATE-ABSENT: {row}: exact golden bytes")


def age_days(header):
    if not header.get("start"):
        raise Unavailable("run start timestamp absent")
    moment = datetime.fromisoformat(header["start"].replace("Z", "+00:00"))
    if moment.tzinfo is None:
        raise Unavailable("run start timestamp has no timezone")
    return (datetime.now(timezone.utc) - moment).total_seconds() / 86400


def red_proof(c, row, digest, run, mutation_path=None):
    require(bool(digest and run), f"no reverted-fix diff/red run: {row}")
    header, rows = ledger(c, run)
    diff_path = mutation_path or c.runs / f"{digest}.diff"
    diff = c.read(diff_path, nonempty=True)
    require(hashlib.sha256(diff.encode()).hexdigest() == digest, f"diff hash mismatch: {row}")
    require(header.get("dirty") == digest, f"red run dirty hash mismatch: {row}")
    require(rows.get(row, {}).get("verdict") in FAIL, f"run did not show {row} red")
    return header, rows, diff


def escapes(c):
    nonempty(c.policy(c.spell / "assertion.sh"), "assertion spellings empty")
    rows = c.policy_rows(c.suite / "escapes.tsv", 5, "defect")
    beats, pending = c.beats(), set(c.lines(c.suite / "pending.txt"))
    failures, proof_pending = [], 0
    for defect, row, signature, digest, run in rows:
        if row not in beats or not signature:
            failures.append(f"escape has no beat/assertion: {defect}")
            continue
        if not digest or not run:
            # A plan lists obligations honestly; a close requires the recorded failure.
            if not c.close and row in pending:
                proof_pending += 1
                continue
            failures.append(f"escape has no reverted-fix proof: {defect}")
            continue
        red_proof(c, row, digest, run)
        require(signature in c.body(row), f"escape signature absent from its beat: {row}: {signature}")
    require(not failures, "; ".join(failures))
    return f"{len(rows)} escapes, {len(rows) - proof_pending} watched red, {proof_pending} pending proof"


def assertion_signatures(c, path, text, spellings):
    if path.endswith(".sh"):
        from source_analysis import syntax_records
        with tempfile.TemporaryDirectory(prefix="suite-retired-") as temporary:
            snapshot = Path(temporary) / "snapshot.sh"
            snapshot.write_text(text)
            records = syntax_records(Context(temporary), ["snapshot.sh"])
        found = {}
        for record in records:
            if record["kind"] == "assertion":
                found[record["line"]] = f"{record.get('beat', '')} :: {record['text'].strip()}"
        return list(found.values())
    patterns = [re.compile(pattern.replace("[[:space:]]", r"\s")) for pattern in spellings]
    beat, signatures = "", []
    for line in text.splitlines():
        match = re.match(r"\s*beat\s+['\"]?([^\s'\"]+)", line)
        if match:
            beat = match[1]
        if any(pattern.search(line) for pattern in patterns):
            signatures.append(f"{beat} :: {line.strip()}")
    return signatures


def retired(c):
    c.base()
    spellings = nonempty(c.policy(c.spell / "assertion.sh"), "assertion spellings empty")
    rows = c.tsv(c.suite / "retired.tsv", 4, "file")
    changes = c.command(["git", "diff", "--no-renames", "--name-status", c.base(), "--", "infra/fence", "pfm"]).splitlines()
    removed, new_signatures = Counter(), Counter()
    for change in changes:
        status, path = change.split("\t", 1)
        if not (path.endswith(("_test.go", ".txtar")) or "/lanes/" in path and path.endswith(".sh")):
            continue
        before = "" if status == "A" else c.command(["git", "show", f"{c.base()}:{path}"])
        after = "" if status == "D" else c.read(path)
        old = Counter(assertion_signatures(c, path, before, spellings))
        new = Counter(assertion_signatures(c, path, after, spellings))
        removed.update({(path, sig): count for sig, count in (old - new).items()})
        new_signatures.update(new - old)
    for key in list(removed):
        moved = min(removed[key], new_signatures[key[1]])
        removed[key] -= moved
        new_signatures[key[1]] -= moved
    if removed and not rows:
        raise Unavailable("deleted assertions with an empty retirement ledger")
    expected = Counter((path, signature) for path, signature, disposition, _ in rows if disposition)
    # Planned retirement rows are allowed while their source assertions still exist.
    planned, kept = 0, {}
    for path, signature, disposition, run in rows:
        if not disposition:
            require(not c.close and c.path(path).is_file(), f"retirement disposition missing: {path}: {signature}")
            if path not in kept:
                kept[path] = set(assertion_signatures(c, path, c.read(path), spellings))
            require(signature in kept[path], f"retirement signature absent from kept source: {path}: {signature}")
            planned += 1
        elif disposition.startswith("successor:"):
            row = disposition[10:]
            require(row in c.beats(), f"successor absent: {row}")
            header, result = ledger(c, run)
            require(header.get("tree") == c.subject() and result.get(row, {}).get("verdict") in PASS,
                    f"successor unproved on subject: {row}")
            require(signature.split(" :: ", 1)[-1] in c.body(row), f"successor changed subject/expected value: {row}")
        elif disposition.startswith("ruling:"):
            require(disposition[7:] in c.references(), f"retirement ruling absent: {disposition}")
        elif disposition.startswith("dropped:"):
            patterns = c.policy(c.spell / "refused-shape.go")
            require(any(re.search(pattern.replace("[[:space:]]", r"\s"), signature) for pattern in patterns),
                    f"retired assertion does not match refused shape: {signature}")
        else:
            require(False, f"unknown retirement disposition: {disposition}")
    require(+removed == expected, f"retirement/deletion mismatch: missing {list((removed - expected).elements())}; extra {list((expected - removed).elements())}")
    return f"{sum(expected.values())} retired assertions verified; {planned} planned in kept lanes"
