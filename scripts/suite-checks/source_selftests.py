"""Executable green controls and planted violations for the source checks."""

import json
import os
import re
from pathlib import Path

from selftest_fixture import LANE, ROW, judge, put, seed
from core import signature_key
from shape_rules import SHAPES
from source_checks import CHECKS, corpora, scan_paths, signatures


SPELLINGS = ("refused-name.sh", "violating-action.sh", "name-allowlist.txt", "locator.sh",
             "refused-shape.go", "doors.go", "internal-entry.sh", "corpus-shapes.tsv")


def source_seed(root):
    c = seed(root)
    assertions = "\n".join(f' if names[{n}] != "name-{n}" {{ t.Errorf("name-{n}: %s", names[{n}]) }}' for n in range(12))
    put(root, "pfm/internal/chat/new_test.go", "package chat\nfunc TestNew(t *testing.T) {\n" + assertions + "\n}\n")
    for name in SPELLINGS:
        put(root, c.spell / name, (Path(__file__).parent / name).read_text())
    for name in ("bare-doors", "clones", "size", "refused-shapes"):
        put(root, c.spell / f"{name}.baseline", "# ceiling 2000-01-01 0\n")
    return c


def refresh(c):
    c._files = None
    return c


def expect(c, subject, verdict, detail=None):
    return judge(refresh(c), subject, CHECKS[subject], verdict, detail)


def baseline(c, name):
    rows, _ = signatures(c, name)
    put(c.root, c.spell / f"{name}.baseline", f"# ceiling 2000-01-01 {len(rows)}\n" + "\n".join(signature_key(row) for row in rows) + ("\n" if rows else ""))
    refresh(c)


def case(subject, mutation, expected, detail=None):
    def run(root):
        c = source_seed(root)
        if subject in ("bare-doors", "clones", "size", "refused-shapes"):
            baseline(c, subject)
        expect(c, subject, "PASS")
        mutation(c)
        result = expect(c, subject, expected, detail)
        if detail is None and expected == "FAIL" and subject == "clones":
            label, separator, evidence = result[1].partition(": ")
            assert (label, separator) == ("beyond baseline", ": "), f"expected clone ratchet failure, actual {result!r}"
            paths = evidence.split("\t", 2)[:2]
            assert paths == ["pfm/internal/chat/alpha_test.go", "pfm/internal/chat/beta_test.go"], f"expected planted clone pair, actual {paths!r}"
        if detail is None and expected == "ERROR":
            diagnostic = result[1]
            if diagnostic.startswith("unreadable "):
                match = re.fullmatch(r"unreadable (.+): \[Errno 2\] No such file or directory: '(.+)'", diagnostic)
                assert match is not None and match[1] == match[2], f"expected a named missing-file error, actual {diagnostic!r}"
            else:
                assert diagnostic in {
                    "DERIVE-FAILED: no rows read from beats.md", "empty spellings: locator.sh",
                    "empty spellings: refused-shape.go", "zero files scanned", "no planted-violation corpus",
                    "door spellings empty or unreadable", "zero product source files scanned",
                    "clone detector matched no files", "no lane source read for internal-entry",
                    f"empty {c.suite / 'ruled-copy.tsv'}", f"size-ceiling not declared in {c.suite / 'design.md'}",
                }, f"expected a named input failure, actual {diagnostic!r}"
    return run


def empty(c, name):
    put(c.root, c.spell / name, "")


def no_sources(c):
    for path in c.files():
        if path.endswith(("_test.go", ".txtar")) or path.startswith("infra/fence/journey/"):
            (c.root / path).unlink()


def no_scanned_sources(c):
    for path in scan_paths(c):
        (c.root / path).unlink()


def no_product(c):
    for path in c.files():
        if path.startswith(("pfm/cmd/", "pfm/internal/")) and path.endswith(".go") and not path.endswith("_test.go"):
            (c.root / path).unlink()


def duplicate_files(c):
    body = '\n'.join(f' if values[{n}] != {n} {{ t.Fatalf("value at {n}: %d", values[{n}]) }}' for n in range(12))
    for name in ("alpha", "beta"):
        put(c.root, f"pfm/internal/chat/{name}_test.go", f"package chat\nfunc Test{name.title()}(t *testing.T) {{\n{body}\n}}\n")


def clone_detector_absent(root):
    c = source_seed(root)
    baseline(c, "clones")
    expect(c, "clones", "PASS")
    scratch = c.root / "no-detector"
    scratch.mkdir()
    for command in ("bash", "dirname"):
        import shutil
        target = shutil.which(command)
        assert target is not None, f"required command {command} absent"
        (scratch / command).symlink_to(target)
    before_path = os.environ.get("PATH")
    before_bin = os.environ.get("TOOLS_BIN")
    try:
        os.environ["PATH"] = str(scratch)
        os.environ["TOOLS_BIN"] = str(scratch)
        expect(c, "clones", "ERROR")
    finally:
        if before_path is None:
            os.environ.pop("PATH", None)
        else:
            os.environ["PATH"] = before_path
        if before_bin is None:
            os.environ.pop("TOOLS_BIN", None)
        else:
            os.environ["TOOLS_BIN"] = before_bin


def corpus_forms(root):
    c = source_seed(root)
    counts = corpora(c)
    expected = {(shape, language): 3 for shape in SHAPES for language in ("go", "sh")}
    assert dict(counts) == expected, f"expected {expected!r}, actual {dict(counts)!r}"
    rows = c.tsv(c.spell / "corpus-shapes.tsv", 4)
    for shape, language, form, encoded in rows:
        if shape == "internal-entry-as-action":
            continue  # Its applicable language is shell; contexts() parses all action spellings.
        c2 = source_seed(c.root / f"{shape}-{language}-{form}")
        baseline(c2, "refused-shapes")
        code = json.loads(encoded)
        scope = SHAPES[shape][0]
        paths = {
            "verdict": "scripts/check.sh", "test": "pfm/internal/chat/planted_test.go",
            "shared": "infra/fence/journey/lanes/planted.sh",
            "test-harness": "infra/fence/journey/lanes/planted.sh",
            "harness": "infra/fence/journey/lanes/planted.sh",
            "production-test": "pfm/internal/chat/planted.go", "production": "pfm/internal/chat/planted.go",
            "logging": "pfm/internal/chat/planted.go", "allowlist": "scripts/name-allowlist.txt",
            "fixture": "pfm/internal/chat/testdata/planted.txt", "lane": "infra/fence/journey/lanes/planted.sh",
        }
        path = paths[scope]
        if scope in ("shared", "harness", "test-harness"):
            path = "infra/fence/journey/probe.py" if language == "go" else path
        put(c2.root, path, code + "\n")
        actual, _ = signatures(refresh(c2), "refused-shapes")
        found = [row.split("\t", 1)[0] for row in actual]
        assert shape in found, f"expected shape {shape!r}, actual {found!r} for {language}/{form}"
        expect(c2, "refused-shapes", "FAIL")


def internal_contexts(root):
    c = source_seed(root)
    allowed = (f"beat {ROW}\n"
               'need prepare "pfm internal launch"\n'
               'need_seed() { pfm internal launch; }\n'
               'observe_state() { pfm internal state; }\n'
               'observe "pfm internal state"\n'
               'assert chat.new state ready "$(pfm internal state)"\n')
    put(root, LANE, allowed)
    expect(c, "internal-entry", "PASS")
    forms = ('pfm internal launch', 'act "pfm internal launch"', 'pane_input pane "pfm internal launch"',
             'value=$(pfm internal launch)', '${PFM_BIN} internal launch', 'bash -c "pfm internal launch"')
    for form in forms:
        put(root, LANE, allowed + form + "\n")
        expect(c, "internal-entry", "FAIL")


def oracle_ast(root):
    c = source_seed(root)
    row = "tier-unit:new"
    put(root, c.suite / "beats.md", c.read(c.suite / "beats.md") +
        f"{row} · do:open a chat · assert:name equals scout · source:fixture · cost_ms:1 · spends:none · chat.new\n")
    expect(c, "oracle", "PASS")
    put(root, "pfm/internal/chat/new_test.go", 'package chat\nfunc TestNew(t *testing.T) { if got != got { t.Fatal(got) } }\n')
    expect(c, "oracle", "FAIL", "oracle violations: tautological-oracle:pfm/internal/chat/new_test.go:2:got != got")


CASES = {
    ("names", "no-ids"): case("names", lambda c: put(c.root, c.suite / "beats.md", ""), "ERROR"),
    ("names", "allowlist-unreadable"): case("names", lambda c: (c.spell / "name-allowlist.txt").unlink(), "ERROR"),
    ("names", "planted"): case("names", lambda c: put(c.root, c.suite / "beats.md", c.read(c.suite / "beats.md").replace("new-from-cli", "bug-123")), "FAIL", "refused names: bug-123"),
    ("oracle", "references-unreadable"): case("oracle", lambda c: (c.suite / "references.tsv").unlink(), "ERROR"),
    ("oracle", "locator-spellings-empty"): case("oracle", lambda c: empty(c, "locator.sh"), "ERROR"),
    ("oracle", "planted"): oracle_ast,
    ("copy", "registry-empty"): case("copy", lambda c: put(c.root, c.suite / "ruled-copy.tsv", ""), "ERROR"),
    ("copy", "planted"): case("copy", lambda c: put(c.root, c.suite / "ruled-copy.tsv", "welcome\ten\tWelcome aboard\tfixture\n"), "FAIL", "copy violations: welcome/en:expected-one-pin:actual-0"),
    ("refused-shapes", "spelling-empty"): case("refused-shapes", lambda c: empty(c, "refused-shape.go"), "ERROR"),
    ("refused-shapes", "zero-files"): case("refused-shapes", no_scanned_sources, "ERROR"),
    ("refused-shapes", "no-corpus"): case("refused-shapes", lambda c: (c.spell / "corpus-shapes.tsv").unlink(), "ERROR"),
    ("refused-shapes", "planted"): case("refused-shapes", lambda c: put(c.root, LANE, c.read(LANE) + "sleep 1\n"), "FAIL", f"beyond baseline: wall-clock-wait\t{LANE}\tsleep 1 (+1); refused-spelling\t{LANE}\tsleep 1 (+1)"),
    ("refused-shapes", "corpus-three-forms"): corpus_forms,
    ("bare-doors", "door-spellings-empty"): case("bare-doors", lambda c: empty(c, "doors.go"), "ERROR"),
    ("bare-doors", "zero-files"): case("bare-doors", no_product, "ERROR"),
    ("bare-doors", "planted"): case("bare-doors", lambda c: put(c.root, "pfm/internal/chat/open.go", 'package chat\nfunc Open() { os.Getenv("TOKEN") }\n'), "FAIL", 'beyond baseline: pfm/internal/chat/open.go:Open\tos.Getenv("TOKEN") (+1)'),
    ("clones", "detector-absent"): clone_detector_absent,
    ("clones", "no-files-matched"): case("clones", no_sources, "ERROR"),
    ("clones", "planted"): case("clones", duplicate_files, "FAIL"),
    ("size", "ceiling-undeclared"): case("size", lambda c: put(c.root, c.suite / "design.md", c.read(c.suite / "design.md").replace("size-ceiling: 400\n", "")), "ERROR"),
    ("size", "planted"): case("size", lambda c: put(c.root, LANE, c.read(LANE) + "\n" * 400), "FAIL", f"beyond baseline: {LANE} (+1)"),
    ("internal-entry", "lanes-unreadable"): case("internal-entry", lambda c: (c.root / LANE).unlink(), "ERROR"),
    ("internal-entry", "planted"): case("internal-entry", lambda c: put(c.root, LANE, c.read(LANE) + "pfm internal launch\n"), "FAIL", f"internal entry used as action: {LANE}:3:pfm internal launch"),
    ("internal-entry", "contexts"): internal_contexts,
}
