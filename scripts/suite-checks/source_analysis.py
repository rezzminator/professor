"""Syntax-based source observations shared by the suite's source checks."""

import json
import re
import tempfile
from pathlib import Path

from core import Unavailable, nonempty

_BINARY = None
_BUILD = None


def syntax_records(c, paths):
    global _BINARY, _BUILD
    nonempty(paths, "zero source files scanned")
    if _BINARY is None:
        _BUILD = tempfile.TemporaryDirectory(prefix="suite-syntax-")
        binary = Path(_BUILD.name) / "inspect"
        source = Path(__file__).parent / "go-inspect/main.go"
        c.command(["go", "build", "-o", str(binary), str(source)],
                  cwd=Path(__file__).resolve().parents[2] / "pfm")
        _BINARY = binary
    records = []
    for offset in range(0, len(paths), 300):
        batch = [str(c.root / path) for path in paths[offset:offset + 300]]
        output = c.command([str(_BINARY), *batch])
        try:
            records.extend(json.loads(output))
        except (ValueError, TypeError) as error:
            raise Unavailable(f"syntax extractor returned malformed JSON: {error}") from error
    for record in records:
        record["file"] = str(Path(record["file"]).relative_to(c.root))
    return records


def test_paths(c):
    return [p for p in c.files() if p.endswith("_test.go") or
            p.startswith("pfm/e2e/testdata/") and p.endswith(".txtar") or
            p.startswith("infra/fence/journey/lanes/") and p.endswith(".sh")]


def lane_paths(c):
    return [p for p in c.files() if p.startswith("infra/fence/journey/lanes/") and p.endswith(".sh")]


def lane_beats(c):
    paths = lane_paths(c)
    if not paths:
        return []
    declarations = [record for record in syntax_records(c, paths) if record["kind"] == "beat"]
    for record in declarations:
        record["row"] = record["name"] if "/" in record["name"] else Path(record["file"]).stem + "/" + record["name"]
        if not record["text"]:
            following = [other["line"] - 1 for other in declarations
                         if other["file"] == record["file"] and other["line"] > record["line"]]
            lines = c.read(record["file"]).splitlines()
            record["text"] = "\n".join(lines[record["line"] - 1:min(following, default=len(lines))])
    return declarations


def product_paths(c):
    return [p for p in c.files() if re.match(r"^pfm/(cmd|internal)/.*\.go$", p)
            and not p.endswith("_test.go")]


def patterns(c, filename):
    rows = c.policy(c.spell / filename)
    nonempty(rows, f"empty spellings: {filename}")
    try:
        return [re.compile(row) for row in rows]
    except re.error as error:
        raise Unavailable(f"bad spelling in {filename}: {error}") from error


def normalized(text):
    return " ".join(text.split())


def resolved(expr, bindings):
    visited = set()
    while expr in bindings and expr not in visited:
        visited.add(expr)
        expr = bindings[expr]
    return expr


def oracle_findings(records):
    findings = []
    for record in records:
        args = record.get("args", [])
        binding = record.get("bindings", {})
        name = record["name"]
        problem = ""
        if record["kind"] == "comparison" and len(args) == 2:
            left, right = (resolved(arg, binding) for arg in args)
            if left == right:
                problem = "tautological-oracle"
            elif re.search(r"(?:^want$|expected|golden)", args[1], re.I):
                if re.search(r"(?:ReadFile|ReadString|ReadAll)\([^)]*(?:\.go|\.sh|assets/|harness-prompts/|templates/)", right):
                    problem = "expected-from-source"
                elif "(" in right and not re.match(r'^(?:\[\]|map\[|string\(|int\(|fmt\.Sprintf\(|strings\.Join\()', right):
                    problem = "expected-from-call"
        elif record["kind"] == "call":
            if re.fullmatch(r"(?:assert|require)\.(?:True|False|Len|NotNil|NotEmpty|Nil)", name):
                problem = "weak-oracle"
            elif name.endswith((".Equal", ".EqualValues", ".Diff")) and len(args) >= 2:
                values = args[1:3] if name.startswith(("assert.", "require.")) else args[:2]
                if len(values) == 2 and resolved(values[0], binding) == resolved(values[1], binding):
                    problem = "tautological-oracle"
        if problem:
            findings.append((record, problem))
    return findings
