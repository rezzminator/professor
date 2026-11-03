"""Real ledger fixtures for the checks that consume blocking-run evidence."""

import hashlib
import json
from functools import partial

from checks import check_registry
from plan_selftests import missing
from run_checks import CHECKS
from selftest_fixture import ID, ROW, judge, put, seed, write_run


def prepare(c, subject):
    if subject == "budgets":
        for name in ("first.tsv", "second.tsv", "third.tsv"):
            write_run(c, name)
        (c.suite / "budgets.tsv").write_text("sequence\t12\tfirst.tsv,second.tsv,third.tsv\tfixture\n")
        c.close = True
        return "1 budgets recomputed from named green runs"
    if subject == "collected":
        put(c.root, ".github/workflows/ci.yml", "jobs:\n  journey:\n    runs-on: ubuntu-latest\n    steps:\n      - run: infra/fence/journey/run.sh --root reuse\n      - run: scripts/suite-checks.sh --close\n")
        checks = "".join(f"CHECK {name} PASS fixture\n" for name in check_registry())
        put(c.root, c.suite / "runs/blocking.log", "COLLECT pfm/internal/chat/new_test.go\n" + checks)
        write_run(c, "clock.tsv")
        start = next(line for line in (c.suite / "runs/clock.tsv").read_text().splitlines() if line.startswith("start: "))
        put(c.root, c.suite / "runs/close.log", start + "\n" + checks)
        return "1 test files collected once; all checks and OS gates recorded"
    if subject == "self-tests":
        declared = [(name, case, f"test-{name}-{case}") for name in check_registry() for case in ("unreadable", "planted")]
        (c.suite / "self-tests.txt").write_text("".join("\t".join(row) + "\n" for row in declared))
        put(c.root, c.suite / "runs/census.txt", "".join(row[2] + "\n" for row in declared))
        put(c.root, c.suite / "runs/self-tests.log", f"tree: {c.subject()}\n" + "".join(f"SELFTEST {row[2]} PASS\n" for row in declared))
        return "52 self-tests collected and passing"
    if subject == "env-twice":
        write_run(c, "unit-base.tsv")
        write_run(c, "unit-perturbed.tsv", config="perturbed", perturbed_keys="backend")
        return "1 unit results equal under 1 perturbed keys"
    if subject == "tree":
        write_run(c, "sequence.tsv")
        write_run(c, "solo.tsv", mode="solo")
        return "1 canonical runs on subject; 1 lanes proven sequence and solo"
    if subject == "observed":
        write_run(c, "sequence.tsv")
        return "1 mapped assertions and operations observed"
    if subject == "bite":
        from shape_rules import SHAPES
        diff = '--- a/pfm/cmd/pfm/ops.go\n+++ b/pfm/cmd/pfm/ops.go\n@@ -2 +2 @@\n-return "scout"\n+return "wrong"\n'
        digest = hashlib.sha256(diff.encode()).hexdigest()
        put(c.root, c.suite / "runs" / f"{digest}.diff", diff)
        names = sorted(set(check_registry()) | set(SHAPES) | {ROW})
        (c.suite / "bites.tsv").write_text("".join(f"{name}\tpfm/cmd/pfm/ops.go:2\t{digest}\tred.tsv\n" for name in names))
        write_run(c, "red.tsv", rows=[[name, "FAIL", ID, "scout", "chat.new", "", "10", "0"] for name in names], dirty=digest)
        return "44 mutations watched red"
    if subject == "flakes":
        write_run(c, "sequence.tsv")
        for kind in ("unit", "hermetic"):
            write_run(c, kind + ".tsv", rows=[[f"tier-{kind}:new-chat", "PASS", ID, "scout", "chat.new", "", "10", "0"]], mode="tier", order="shuffle:17")
        return "3 attempts recomputed; no flaky row"
    raise AssertionError(subject)


def exercise(root, subject, case):
    c = seed(root)
    detail = prepare(c, subject)
    check = CHECKS[subject]
    judge(c, subject, check, "PASS", detail)
    absent = {
        ("budgets", "budgets-unreadable"): c.suite / "budgets.tsv",
        ("budgets", "named-run-unreadable"): c.suite / "runs/first.tsv",
        ("collected", "ci-config-unreadable"): c.root / ".github/workflows/ci.yml",
        ("self-tests", "census-unreadable"): c.suite / "runs/census.txt",
        ("env-twice", "leg-missing"): c.suite / "runs/unit-perturbed.tsv",
        ("bite", "bites-unreadable"): c.suite / "bites.tsv",
    }
    if (subject, case) in absent:
        path = absent[subject, case]
        path.unlink()
        return judge(c, subject, check, "ERROR", missing(path))
    if case in ("no-ledger", "no-canonical-ledger", "ledgers-unreadable"):
        for path in (c.suite / "runs").glob("*.tsv"):
            path.unlink()
        return judge(c, subject, check, "ERROR", "no usable ledger matching *.tsv")
    if case == "tier-ledger-missing":
        path = c.suite / "map.tsv"
        path.write_text(path.read_text() + f"{ID}\ttier-unit:new-chat\ttwin:{ROW}\n")
        return judge(c, subject, check, "ERROR", missing(c.suite / "runs/tier-unit.tsv"))
    if case == "diff-unreadable":
        path = next((c.suite / "runs").glob("*.diff"))
        path.unlink()
        return judge(c, subject, check, "ERROR", missing(path))
    if case == "no-close-job":
        path = c.root / ".github/workflows/ci.yml"
        path.write_text(path.read_text().replace("--close", ""))
        return judge(c, subject, check, "ERROR", "no CI job invokes suite-checks.sh --close")
    if case == "live-os-undeclared":
        path = c.suite / "design.md"
        path.write_text(path.read_text().replace("live-os: linux\n", ""))
        return judge(c, subject, check, "ERROR", "live-os not declared")
    if case == "leg-older-than-window":
        write_run(c, "unit-perturbed.tsv", config="perturbed", perturbed_keys="backend", start="2000-01-01T00:00:00+00:00")
        return judge(c, subject, check, "ERROR", "last perturbed leg older than schedule window")
    assert case == "planted", (subject, case)
    if subject == "budgets":
        (c.suite / "budgets.tsv").write_text("sequence\t11\tfirst.tsv,second.tsv,third.tsv\tfixture\n")
        detail = "budget sequence: pin 11 below recomputed 12"
    elif subject == "collected":
        path = c.suite / "runs/blocking.log"
        path.write_text(path.read_text() + "COLLECT pfm/internal/chat/new_test.go\n")
        detail = "test files not collected exactly once: pfm/internal/chat/new_test.go"
    elif subject == "self-tests":
        path = c.suite / "runs/self-tests.log"
        path.write_text(path.read_text().replace("SELFTEST test-map-ids-planted PASS", "SELFTEST test-map-ids-planted FAIL"))
        detail = "self-tests absent or not passing: test-map-ids-planted"
    elif subject == "env-twice":
        write_run(c, "unit-perturbed.tsv", rows=[[ROW, "PASS", ID, "wrong", "chat.new", "", "10", "0"]], config="perturbed", perturbed_keys="backend")
        detail = "base and perturbed outcomes differ"
    elif subject == "tree":
        write_run(c, "sequence.tsv", tree="wrong")
        detail = "wrong subject: sequence.tsv"
    elif subject == "observed":
        write_run(c, "sequence.tsv", rows=[[ROW, "PASS", ID, "scout", "chat.wrong", "", "10", "0"]])
        detail = f"CLAIMED-NOT-INVOKED: {ID}/{ROW}"
    elif subject == "bite":
        path = next((c.suite / "runs").glob("*.diff"))
        path.write_text(path.read_text().replace('return "wrong"', "exit 1"))
        detail = "diff hash mismatch: " + (c.suite / "bites.tsv").read_text().split("\t")[0]
    elif subject == "flakes":
        write_run(c, "failed.tsv", rows=[[ROW, "FAIL", ID, "scout", "chat.new", "", "10", "0"]])
        detail = "row failed and passed on identical subject/configuration/root"
    else:
        raise AssertionError(subject)
    judge(c, subject, check, "FAIL", detail)


def engine_input_case(root, case):
    c = seed(root)
    install = "machine-lifecycle/install-over-live-chat@linux"
    landscape = c.suite / "landscape.md"
    landscape.write_text(landscape.read_text().replace("op:chat.new", "op:") +
                         "lifecycle.install · installs · op:lifecycle.install · pfm/cmd/pfm/ops.go:1\n")
    beats = c.suite / "beats.md"
    beats.write_text(beats.read_text().replace(" · do:", " · evidence:engine-input:/stats · do:"))
    mapping = c.suite / "map.tsv"
    mapping.write_text(mapping.read_text() + f"lifecycle.install\t{install}\tlive-only\n")
    source = f"beats/{ROW}/engine-input.txt"
    receipt = put(root, c.runs / source, "/stats\n")
    golden = {"path": "goldens/stats-cache.json", "sha256": hashlib.sha256(b'{"count":7}\n').hexdigest()}
    assertions = [
        {"id": ID, "expected": "/stats", "actual": "/stats", "pass": True,
         "source": "engine-input:/original/run/" + source},
        {"id": ID, "expected": "regular-file", "actual": "regular-file", "pass": True,
         "source": "engine-state:regular-file"},
        {"id": ID, "expected": '{"count":7}\n', "actual": '{"count":7}\n', "pass": True,
         "source": golden["path"]},
    ]
    rows = [[ROW, "PASS", ID, json.dumps(assertions), "", json.dumps([golden]), "10", "0"],
            [install, "PASS", "lifecycle.install", "installed", "lifecycle.install", "", "10", "1"]]
    headers = {}
    def save():
        rows[0][3] = json.dumps(assertions)
        write_run(c, "sequence.tsv", rows=rows, log="/original/run/sequence.activity.jsonl", **headers)
    save()
    judge(c, "observed", CHECKS["observed"], "PASS", "2 mapped assertions and operations observed")
    headers["artifacts"] = "sequence"
    receipt.unlink()
    receipt = put(root, c.runs / "sequence" / source, "/stats\n")
    put(root, c.runs / "another-run" / source, "/foreign\n")
    save()
    judge(c, "observed", CHECKS["observed"], "PASS", "2 mapped assertions and operations observed")
    verdict = "FAIL"
    if case == "input-record-unreadable":
        receipt.unlink()
        verdict, detail = "ERROR", missing(receipt)
    elif case == "input-record-absent":
        assertions.pop(0)
        save()
        detail = f"ENGINE-INPUT-ABSENT: {ROW}"
    elif case == "input-record-mismatch":
        receipt.write_text("/other\n")
        detail = f"ENGINE-INPUT-MISMATCH: {ROW}: expected '/stats', recorded '/other'"
    elif case == "input-record-foreign":
        assertions[0]["source"] = "engine-input:/original/run/beats/another/engine-input.txt"
        save()
        detail = f"ENGINE-INPUT-FOREIGN: {ROW}: /original/run/beats/another/engine-input.txt"
    elif case == "input-row-claims-op":
        rows[0][4] = "lifecycle.install"
        save()
        detail = f"ENGINE-INPUT-OPS: {ROW} must not credit a pfm action"
    elif case in ("input-borrowed-action", "input-relabelled-action", "diagnostic-action"):
        path = c.runs / "sequence.activity.jsonl"
        event = json.loads(path.read_text())
        if case == "diagnostic-action":
            event["op"] = event.pop("action")
            detail = f"CLAIMED-NOT-INVOKED: lifecycle.install/{install}"
        else:
            event["beat"] = ROW
            if case == "input-relabelled-action":
                event["action"] = "engine.stats"
            detail = f"ENGINE-INPUT-OPS: {ROW} must not credit a pfm action"
        path.write_text(json.dumps(event) + "\n")
    elif case == "input-state-absent":
        assertions.pop(1)
        save()
        detail = f"ENGINE-STATE-ABSENT: {ROW}: regular-file"
    elif case == "input-bytes-mismatch":
        assertions[-1]["actual"] = '{"count":8}\n'
        save()
        detail = f"ENGINE-STATE-ABSENT: {ROW}: exact golden bytes"
    elif case in ("artifacts-traversal", "artifacts-absolute"):
        headers["artifacts"] = "../foreign" if case == "artifacts-traversal" else "/foreign"
        save()
        detail = f"ENGINE-ARTIFACTS-FOREIGN: {ROW}: {headers['artifacts']}"
    else:
        raise AssertionError(case)
    judge(c, "observed", CHECKS["observed"], verdict, detail)


INPUTS = {
    "budgets": ("budgets-unreadable", "named-run-unreadable"),
    "collected": ("ci-config-unreadable", "no-close-job", "live-os-undeclared"),
    "self-tests": ("census-unreadable",), "env-twice": ("leg-missing", "leg-older-than-window"),
    "tree": ("no-ledger",), "observed": ("no-canonical-ledger", "tier-ledger-missing"),
    "bite": ("bites-unreadable", "diff-unreadable"), "flakes": ("ledgers-unreadable",),
}
CASES = {(subject, case): partial(exercise, subject=subject, case=case)
         for subject, inputs in INPUTS.items() for case in (*inputs, "planted")}
CASES.update({("observed", case): partial(engine_input_case, case=case) for case in (
    "input-record-unreadable", "input-record-absent", "input-record-mismatch", "input-record-foreign",
    "input-row-claims-op", "input-borrowed-action", "input-relabelled-action", "diagnostic-action",
    "input-state-absent", "input-bytes-mismatch",
    "artifacts-traversal", "artifacts-absolute",
)})
