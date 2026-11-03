"""Each check is first green, then receives the named unreadable input or violation."""

from functools import partial

from evidence import escapes, retired
from plan_checks import CHECKS
from selftest_fixture import ID, LANE, ROW, judge, put, seed


def missing(path):
    return f"unreadable {path}: [Errno 2] No such file or directory: '{path}'"


def owner_gap(root):
    c = seed(root)
    reason = "pfm offers no plain-shell entry in Herdr; a new pane always arms the picker"
    beats = c.suite / "beats.md"
    beats.write_text(beats.read_text().replace(" · do:", f" · gap-owner:owner · gap-reason:{reason} · do:"))
    gaps = c.suite / "gaps.tsv"
    gaps.write_text(f"{ROW}\t{ID}\t{reason}\towner\tuntil-owner-decision\n")
    judge(c, "gaps", CHECKS["gaps"], "PASS", "1 gap entries in force")
    gaps.write_text(f"{ROW}\t{ID}\tan unrelated reason\towner\tuntil-owner-decision\n")
    judge(c, "gaps", CHECKS["gaps"], "FAIL", f"unratified owner decision gap: {ROW}")


def branch_declaration(root):
    c = seed(root)
    (c.suite / "pending.txt").write_text("")
    put(root, LANE, 'if beat new-from-cli chat.new; then\n assert chat.new scout "$actual"\nfi\nif beat other chat.new; then\n assert chat.new "$actual" "$actual"\nfi\n')
    judge(c, "map-beats", CHECKS["map-beats"], "PASS", "1 rows defined or pending (0 pending)")
    from source_checks import oracle
    put(root, c.spell / "locator.sh", "generated-id\n")
    put(root, c.spell / "refused-shape.go", "weak-oracle\n")
    judge(c, "oracle", oracle, "PASS", "1 planned beat oracle fields; 0 mapped Go files and 1 live beat bodies inspected")
    path = c.root / LANE
    path.write_text(path.read_text().replace("beat new-from-cli", "beat missing-entry"))
    judge(c, "map-beats", CHECKS["map-beats"], "FAIL", f"undefined: {ROW}")


def forecast_holders(root):
    c = seed(root)
    ledger = c.suite / "forecast.tsv"
    original = ledger.read_text()
    for replacement, expected in (
        ("engine=codex", "forecast cells differ from imported source; fixture/C1: imported=[('1', 'engine=claude', 'wrong-state', 'UNHELD')] ledger=[('1', 'engine=codex', 'wrong-state', 'UNHELD')]"),
        (None, "forecast cells differ from imported source; fixture/C1: imported=[('1', 'engine=claude', 'wrong-state', 'UNHELD')] ledger=[('1', 'engine=claude', 'wrong-state', 'UNHELD'), ('1', 'engine=claude', 'wrong-state', 'UNHELD')]"),
    ):
        ledger.write_text(original.replace("engine=claude", replacement) if replacement else original + original.splitlines()[1] + "\n")
        judge(c, "forecast", CHECKS["forecast"], "FAIL", expected)
    ledger.write_text(original.replace("fixture\tC1", "fixture\tC2"))
    judge(c, "forecast", CHECKS["forecast"], "FAIL",
          "forecast cells differ from imported source; fixture/C1: imported=[('1', 'engine=claude', 'wrong-state', 'UNHELD')] ledger=[]; fixture/C2: imported=[] ledger=[('1', 'engine=claude', 'wrong-state', 'UNHELD')]")
    ledger.write_text(original)
    unit = "tier-unit:chat-birth-unit-holder"
    for name in ("beats.md", "forecast.tsv", "map.tsv", "pending.txt"):
        path = c.suite / name
        path.write_text(path.read_text().replace(ROW, unit))
    judge(c, "forecast", CHECKS["forecast"], "PASS", "1 cells: 0 journey beats, 1 unit holders, 0 cut; 1 UNHELD cells retained")
    beats = c.suite / "beats.md"
    beats.write_text(beats.read_text().replace("forecast-factors:engine=claude", "forecast-cells:other/C9,fixture/C1"))
    judge(c, "forecast", CHECKS["forecast"], "PASS", "1 cells: 0 journey beats, 1 unit holders, 0 cut; 1 UNHELD cells retained")
    beats.write_text(beats.read_text().replace("forecast-cells:other/C9,fixture/C1", "forecast-cells:fixture/C2"))
    judge(c, "forecast", CHECKS["forecast"], "FAIL", "forecast factors not carried by beat: fixture/C1")
    c = seed(root)
    beats = c.suite / "beats.md"
    beats.write_text(beats.read_text().replace(ROW, ROW.replace("+claude", "+codex")))
    (c.suite / "cuts.tsv").write_text("chat-birth/*@*+{tmux,herdr}+*\t1000\tfixture\n")
    judge(c, "forecast", CHECKS["forecast"], "PASS", "1 cells: 0 journey beats, 0 unit holders, 1 cut; 1 UNHELD cells retained")
    (c.suite / "cuts.tsv").write_text("chat-ending/*\t1000\tfixture\n")
    judge(c, "forecast", CHECKS["forecast"], "FAIL", f"forecast cell fixture/C1 has no journey beat: {ROW}")


def exercise(root, subject, case):
    c = seed(root)
    check = {**CHECKS, "retired": retired, "escapes": escapes}[subject]
    green = {
        "map-ids": "1 ids, each mapped", "map-beats": "1 rows defined or pending (1 pending)",
        "map-names": "1 public operations, each in the landscape", "map-dup": "1 map rows, no duplicate",
        "branches": "1 combinations: 1 walked, 0 cut; 4 dimensions", "landscape-scope": "1 rows scoped",
        "gaps": "0 gap entries in force", "forecast": "1 cells: 1 journey beats, 0 unit holders, 0 cut; 1 UNHELD cells retained",
        "retired": "0 retired assertions verified; 0 planned in kept lanes",
        "escapes": "0 escapes, 0 watched red, 0 pending proof",
    }
    judge(c, subject, check, "PASS", green[subject])
    absent = {
        ("map-ids", "landscape-absent"): c.suite / "landscape.md",
        ("map-beats", "pending-absent"): c.suite / "pending.txt",
        ("gaps", "roster-unreadable"): c.suite / "roster.txt",
        ("forecast", "forecast-unreadable"): c.suite / "forecast.tsv",
    }
    empty = {
        ("map-ids", "map-empty"): (c.suite / "map.tsv", "DERIVE-FAILED: empty map.tsv"),
        ("map-dup", "map-empty"): (c.suite / "map.tsv", "DERIVE-FAILED: empty map.tsv"),
        ("map-beats", "beats-empty"): (c.suite / "beats.md", "DERIVE-FAILED: no rows read from beats.md"),
        ("branches", "dimensions-empty"): (c.suite / "dimensions.tsv", "dimensions.tsv empty without none"),
        ("landscape-scope", "exclusion-spellings-empty"): (c.spell / "exclusion.go", "exclusion spellings empty"),
        ("retired", "assertion-spellings-empty"): (c.spell / "assertion.sh", "assertion spellings empty"),
    }
    if (subject, case) in absent:
        path = absent[subject, case]
        path.unlink()
        return judge(c, subject, check, "ERROR", missing(path))
    if (subject, case) in empty:
        path, detail = empty[subject, case]
        path.write_text("")
        return judge(c, subject, check, "ERROR", detail)
    if case == "dimensions-unreadable":
        path = c.suite / "dimensions.tsv"
        path.unlink()
        return judge(c, subject, check, "ERROR", missing(path))
    if case == "merge-base-unreadable":
        c._base = None
        c.git_unreadable = True
        return judge(c, subject, check, "ERROR", "merge base unreadable")
    if case == "registry-dump-empty":
        c.pfm.write_text("#!/bin/sh\nexit 0\n")
        return judge(c, subject, check, "ERROR", "DERIVE-FAILED: registry dump empty")
    if case == "op-fields-empty":
        path = c.suite / "landscape.md"
        path.write_text(path.read_text().replace("op:chat.new", "op:"))
        return judge(c, subject, check, "ERROR", "DERIVE-FAILED: no op fields")
    if case == "named-run-unreadable":
        (c.suite / "escapes.tsv").write_text(f"wrong chat\t{ROW}\tname equals scout\t{'a' * 64}\tmissing.tsv\n")
        return judge(c, subject, check, "ERROR", missing(c.suite / "runs/missing.tsv"))
    assert case == "planted", (subject, case)
    if subject == "map-ids":
        path = c.suite / "map.tsv"
        path.write_text(path.read_text().replace(ID, "chat.unknown"))
        detail = "unmatched: chat.new chat.unknown"
    elif subject == "map-beats":
        (c.suite / "map.tsv").write_text(f"landscape_id\trow\treason\n{ID}\tchat-birth/missing@linux\tlive-only\n")
        detail = "map rows missing from beats.md: chat-birth/missing@linux"
    elif subject == "map-names":
        c.pfm.write_text("#!/bin/sh\nprintf 'chat.unknown\\n'\n")
        detail = "built ops without a row: chat.unknown; rows the build lacks: chat.new; writer missing: "
    elif subject == "map-dup":
        path = c.suite / "map.tsv"
        path.write_text(path.read_text() + f"{ID}\t{ROW}\tlive-only\n")
        detail = "duplicate id/tier/branch: ('chat.new', 'live', 'linux+tmux+project+claude')"
    elif subject == "branches":
        path = c.suite / "dimensions.tsv"
        path.write_text(path.read_text().replace("os\tlinux\t", "os\tlinux,macos\t"))
        detail = "1 combinations neither walked nor cut: chat-birth/new-from-cli@macos+tmux+project+claude"
    elif subject == "landscape-scope":
        path = c.suite / "landscape.md"
        path.write_text(path.read_text().replace("surface:chat", "surface:identity"))
        detail = "unruled surface: chat.new"
    elif subject == "gaps":
        (c.suite / "gaps.tsv").write_text(f"{ROW}\t{ID}\thttps://upstream.invalid/defect\toutsider\t2999-01-01\n")
        detail = "owner off roster: outsider"
    elif subject == "forecast":
        path = c.suite / "forecast.tsv"
        path.write_text(path.read_text().replace(ROW, "missing"))
        detail = "forecast cell fixture/C1 has no journey beat: missing"
    elif subject == "escapes":
        (c.suite / "escapes.tsv").write_text("wrong chat\tmissing\tname equals scout\t\t\n")
        detail = "escape has no beat/assertion: wrong chat"
    elif subject == "retired":
        (c.suite / "retired.tsv").write_text(f"{LANE}\tmissing :: assert chat.new wrong literal\t\t\n")
        detail = f"retirement signature absent from kept source: {LANE}: missing :: assert chat.new wrong literal"
    else:
        raise AssertionError(subject)
    judge(c, subject, check, "FAIL", detail)


INPUTS = {
    "map-ids": ("landscape-absent", "map-empty"), "map-beats": ("pending-absent", "beats-empty"),
    "map-names": ("registry-dump-empty", "op-fields-empty"), "map-dup": ("map-empty",),
    "branches": ("dimensions-unreadable", "dimensions-empty"), "landscape-scope": ("exclusion-spellings-empty",),
    "gaps": ("roster-unreadable",), "forecast": ("forecast-unreadable",),
    "retired": ("merge-base-unreadable", "assertion-spellings-empty"), "escapes": ("named-run-unreadable",),
}
CASES = {(subject, case): partial(exercise, subject=subject, case=case)
         for subject, inputs in INPUTS.items() for case in (*inputs, "planted")}
CASES["gaps", "owner-decision"] = owner_gap
CASES["forecast", "holders"] = forecast_holders
CASES["map-beats", "branch-declaration"] = branch_declaration
