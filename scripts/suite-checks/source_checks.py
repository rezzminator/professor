"""Checks over names, expected values, parsed owner actions and source ratchets."""

import json
import re
import shlex
import tempfile
from collections import Counter
from pathlib import Path

from core import Unavailable, Violation, nonempty, require
from source_analysis import (lane_paths, normalized, oracle_findings, patterns,
                             product_paths, syntax_records, test_paths)
from shape_rules import COMPILED, SHAPES, in_scope, line_shapes

SOURCE_CHECKS = ("names", "oracle", "copy", "refused-shapes", "bare-doors", "clones", "size", "internal-entry")


def names(c):
    refused = patterns(c, "refused-name.sh")
    actions = patterns(c, "violating-action.sh")
    allow = set(c.policy(c.spell / "name-allowlist.txt", True))
    beats, landscape = c.beats(), c.landscape()
    paths = nonempty(test_paths(c), "zero test or lane files read")
    tokens = set(landscape)
    for row in beats:
        stem, _, branch = row.partition("@")
        tokens.update(stem.split("/"))
        tokens.update(branch.split("+"))
    tokens.update(Path(path).stem for path in paths)
    bad = [name for name in tokens if name and name not in allow and any(p.search(name) for p in refused)]
    bad.extend("tier-lane:" + Path(path).stem for path in lane_paths(c) if Path(path).stem.startswith("tier-"))
    for row, beat in beats.items():
        if re.search(r"(?:durable|recovers|idempotent|exclusive|retries)", row):
            if not any(p.search(beat.get("do", "")) for p in actions):
                bad.append("property-without-violating-action:" + row)
    require(not bad, "refused names: " + "; ".join(sorted(bad)[:20]))
    return f"{len(tokens)} names and {len(paths)} source files checked"


def oracle(c):
    refs = c.references()
    locators = patterns(c, "locator.sh")
    patterns(c, "refused-shape.go")
    beats, landscape = c.beats(), c.landscape()
    files = nonempty(test_paths(c), "zero mapped test files scanned for expected side")
    bad = []
    for row, beat in beats.items():
        source = beat.get("source", "")
        if not source:
            bad.append("no-source:" + row)
        if not row.startswith("tier-unit:") and not beat.get("live-because"):
            bad.append("no-live-because:" + row)
        ruled = any(landscape.get(key, {}).get("ruled") == "yes" for key in beat.get("ids", []))
        if ruled and source not in refs:
            bad.append("ruled-source-not-a-reference:" + row)
        if ruled and "weak-oracle:" in beat.get("assert", ""):
            bad.append("ruled-weak-oracle:" + row)
    pending = set(c.lines(c.suite / "pending.txt"))
    implemented = {row: beat for row, beat in beats.items() if row not in pending}
    tier_rows = [row.split(":", 1)[1] for row in implemented if row.startswith("tier-")]
    go_inspected = 0
    live_inspected = 0
    go = [path for path in files if path.endswith(".go")]
    if tier_rows:
        nonempty(go, "no Go source for implemented tier oracles")
        keys = {re.sub(r"[^a-z0-9]", "", slug.lower()) for slug in tier_rows}
        selected_files = []
        for path in go:
            text = c.read(path)
            functions = re.findall(r"func (Test[A-Za-z0-9_]+)\s*\(", text)
            if any(re.sub(r"[^a-z0-9]", "", name.removeprefix("Test").lower()) in keys for name in functions) or any(f'"{slug}"' in text for slug in tier_rows):
                selected_files.append(path)
        nonempty(selected_files, "no source matched implemented tier oracle rows")
        records = syntax_records(c, selected_files)
        go_inspected = len(selected_files)
        selected = []
        for slug in tier_rows:
            key = re.sub(r"[^a-z0-9]", "", slug.lower())
            matching = [r for r in records if key == re.sub(r"[^a-z0-9]", "", r["function"].removeprefix("Test").lower())
                        or slug in r.get("labels", [])]
            nonempty(matching, f"no assertion syntax found for mapped tier row {slug}")
            selected.extend(matching)
        for record, problem in oracle_findings(selected):
            bad.append(f"{problem}:{record['file']}:{record['line']}:{record['text']}")
    for row in implemented:
        if row.startswith("tier-"):
            continue
        live_inspected += 1
        for number, line in enumerate(c.body(row).splitlines(), 1):
            if re.search(r"(?:expected|want)\s*=\s*\$\((?:pfm|\$\{?PFM)", line):
                bad.append(f"expected-from-product:{row}:{number}:{line.strip()}")
            if re.search(r"\b(?:assert|test)\b.*(?:\$actual.*\$actual|\$got.*\$got)", line):
                bad.append(f"tautological-oracle:{row}:{number}:{line.strip()}")
            if re.search(r"(?:PFM_UPDATE_GOLDENS|UPDATE_SNAPSHOTS)=1", line):
                bad.append(f"snapshot-update:{row}:{number}")
    # Locator spellings are an exemption only for selecting the actual subject; never for the expected value.
    require(bool(locators), "empty locator policy")
    require(not bad, "oracle violations: " + "; ".join(bad[:20]))
    return f"{len(beats)} planned beat oracle fields; {go_inspected} mapped Go files and {live_inspected} live beat bodies inspected"


def copy(c):
    c.read(c.suite / "ruled-copy.tsv", nonempty=True)
    declared = c.policy_rows(c.suite / "ruled-copy.tsv")
    rows = [row for row in declared if row != ["none"]]
    if not rows and ["none"] in declared:
        return "none ruled"
    nonempty(rows, "ruled-copy registry empty without none")
    if any(len(row) != 4 for row in rows):
        raise Unavailable("ruled-copy registry must have four fields")
    refs = c.references()
    paths = nonempty([p for p in test_paths(c) if p.endswith((".go", ".sh"))], "zero assertion files scanned")
    records = syntax_records(c, paths)
    bad = []
    for key, locale, literal, ruling in rows:
        if not all((key, locale, literal)) or ruling not in refs:
            bad.append(f"invalid-copy-row:{key}/{locale}")
            continue
        pins = set()
        for record in records:
            if record["kind"] == "assertion":
                args = record.get("args", [])
                if record["name"] == "assert" and len(args) >= 2:
                    expected = shlex.split(args[-2])
                    if expected == [literal]:
                        pins.add((record["file"], record.get("beat") or record["function"]))
                continue
            if record["kind"] != "comparison" and not record["name"].endswith((".Equal", ".Diff")):
                continue
            if json.dumps(literal, ensure_ascii=False) in record.get("args", []):
                pins.add((record["file"], record["function"]))
        if len(pins) != 1:
            bad.append(f"{key}/{locale}:expected-one-pin:actual-{len(pins)}")
    require(not bad, "copy violations: " + "; ".join(bad))
    return f"{len(rows)} ruled key/locale literals pinned exactly once"


def corpora(c):
    paths = nonempty(sorted(c.spell.glob("corpus-*.tsv")), "no planted-violation corpus")
    seen = Counter()
    for path in paths:
        for shape, language, form, encoded in c.tsv(path, 4):
            if shape not in SHAPES or language not in ("go", "sh"):
                raise Unavailable(f"unknown corpus shape/language: {shape}/{language}")
            code = json.loads(encoded)
            require(bool(COMPILED[shape].search(code)), f"corpus misses {shape}/{language}/{form}")
            seen[shape, language] += 1
    missing = [f"{shape}/{language}" for shape in SHAPES for language in ("go", "sh") if seen[shape, language] < 3]
    require(not missing, "corpus requires three syntactic forms: " + ",".join(missing))
    return seen


def scan_paths(c):
    source_suffixes = (".go", ".sh", ".py", ".yml", ".yaml", ".txtar")
    fixture_suffixes = (".txt", ".json", ".jsonl", ".sql", ".csv", ".tsv", ".golden", ".result", ".ini", ".conf", ".config")
    paths = []
    for path in c.files():
        if in_scope("allowlist", path):
            paths.append(path)
        elif in_scope("fixture", path) and path.endswith(fixture_suffixes):
            paths.append(path)
        elif path.endswith(source_suffixes) and (not path.startswith("scripts/suite-checks/") or path.endswith(".py") or "/go-inspect/" in path):
            paths.append(path)
    return paths


def refused_signatures(c):
    legacy = patterns(c, "refused-shape.go")
    paths = nonempty(scan_paths(c), "zero files scanned")
    corpora(c)
    c.read(c.suite / "skip-reasons.txt")
    classes = set(c.lines(c.suite / "unobservable.txt"))
    skips = set(c.lines(c.suite / "skip-reasons.txt"))
    tests = set(test_paths(c))
    signatures = []
    for path in paths:
        text = c.read(path)
        for _, shape, statement in line_shapes(path, text):
            if shape == "internal-entry-as-action":
                continue
            if "weak-oracle:" in statement:
                reason = statement.split("weak-oracle:", 1)[1].split()[0]
                if shape == "weak-oracle" and reason in classes:
                    continue
            if shape == "self-skip" and "skip-because:" in statement:
                reason = statement.split("skip-because:", 1)[1].split()[0]
                if reason in skips:
                    continue
            signatures.append(f"{shape}\t{path}\t{statement}")
        if path in tests:
            for line in text.splitlines():
                if line.lstrip().startswith(("#", "//")) or "pfm internal " in line:
                    continue
                if any(pattern.search(line) for pattern in legacy):
                    signatures.append(f"refused-spelling\t{path}\t{normalized(line)}")
    lanes = lane_paths(c)
    if lanes:
        for record in syntax_records(c, lanes):
            if record["kind"] == "internal-action":
                signatures.append(f"internal-entry-as-action\t{record['file']}\t{normalized(record['text'])}")
    return signatures, len(paths)


def refused_shapes(c):
    signatures, scanned = refused_signatures(c)
    return c.ratchet("refused-shapes", signatures, scanned)


def door_signatures(c):
    spellings = c.policy(c.spell / "doors.go")
    nonempty(spellings, "door spellings empty or unreadable")
    paths = nonempty(product_paths(c), "zero product source files scanned")
    signatures = []
    for record in syntax_records(c, paths):
        if record["kind"] != "call":
            continue
        def matches(spelling):
            if spelling.endswith("("):
                return record["name"] == spelling[:-1]
            if spelling.startswith('"'):
                return any(spelling in arg for arg in record.get("args", []))
            return record["text"].startswith(spelling) or record["name"].startswith(spelling + ".")
        if any(matches(spelling) for spelling in spellings):
            signatures.append(f"{record['file']}:{record['function']}\t{normalized(record['text'])}")
    return signatures, len(paths)


def bare_doors(c):
    signatures, scanned = door_signatures(c)
    return c.ratchet("bare-doors", signatures, scanned)


def clone_signatures(c):
    threshold = c.value("clone-threshold-tokens")
    paths = nonempty([p for p in test_paths(c) if p.endswith("_test.go")], "clone detector matched no files")
    try:
        detector = c.command(["bash", str(Path(__file__).resolve().parents[1] / "clone-check.sh"), "--resolve"]).strip()
    except Unavailable as error:
        raise Unavailable(f"clone detector absent or unreadable: {error}") from error
    nonempty(detector, "clone detector absent")
    with tempfile.TemporaryDirectory(prefix="suite-clones-") as scratch:
        config = Path(scratch) / "config.json"
        config.write_text("{}\n")
        c.command([detector, "--config", str(config), "--absolute", "--format", "go", "--min-tokens", str(threshold), "--min-lines", "1",
                   "--reporters", "json", "--output", scratch, "--silent", *[str(c.root / path) for path in paths]])
        report = json.loads(c.read(Path(scratch) / "jscpd-report.json"))
    if not report.get("statistics", {}).get("total", {}).get("sources"):
        raise Unavailable("clone detector matched no files")
    signatures = []
    for duplicate in report.get("duplicates", []):
        pair = []
        for field in ("firstFile", "secondFile"):
            filename = Path(duplicate[field]["name"])
            if filename.is_absolute():
                filename = filename.relative_to(c.root)
            pair.append(str(filename))
        fragment = duplicate.get("fragment", "")
        nonempty(fragment, "clone report lacks fragment")
        signatures.append("\t".join(sorted(pair)) + "\t" + normalized(fragment))
    return signatures, len(paths)


def clones(c):
    signatures, scanned = clone_signatures(c)
    return c.ratchet("clones", signatures, scanned)


def size_signatures(c):
    ceiling = c.value("size-ceiling")
    paths = nonempty(test_paths(c), "zero test or lane files scanned")
    signatures = [path for path in paths if len(c.read(path).splitlines()) > ceiling]
    return signatures, len(paths)


def size(c):
    signatures, scanned = size_signatures(c)
    return c.ratchet("size", signatures, scanned)


def internal_entry(c):
    patterns(c, "internal-entry.sh")
    paths = nonempty(lane_paths(c), "no lane source read for internal-entry")
    findings = [r for r in syntax_records(c, paths) if r["kind"] == "internal-action"]
    for row, beat in c.beats().items():
        action = beat.get("do", "")
        if COMPILED["internal-entry-as-action"].search(action):
            findings.append({"file": "beats.md", "line": row, "text": action})
    require(not findings, "internal entry used as action: " + "; ".join(
        f"{r['file']}:{r['line']}:{r['text']}" for r in findings[:20]))
    return f"{len(paths)} lane files parsed; internal entries only in explicit need/observation contexts"


CHECKS = dict(zip(SOURCE_CHECKS, (names, oracle, copy, refused_shapes, bare_doors, clones, size, internal_entry)))
SIGNATURES = {"refused-shapes": refused_signatures, "bare-doors": door_signatures, "clones": clone_signatures, "size": size_signatures}


def signatures(c, name):
    return SIGNATURES[name](c)
