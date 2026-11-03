"""Run every declared check, retaining ERROR over FAIL while continuing after either."""

import argparse
import sys
import tempfile
from datetime import date
from pathlib import Path

from core import Context, Unavailable, evaluate, signature_key


CLOSE = ("tree", "observed", "bite", "flakes")


def check_registry():
    from evidence import escapes, retired
    from plan_checks import CHECKS as plan
    from run_checks import CHECKS as run
    from source_checks import CHECKS as source
    return plan | source | run | {"retired": retired, "escapes": escapes}


def self_test_cases():
    from plan_selftests import CASES as plan
    from run_selftests import CASES as run
    from source_selftests import CASES as source
    return plan | run | source


def declared_checks(c):
    reference = c.tsv(c.suite / "checks-codex.tsv", 6, "check")
    return [row[0] for row in reference] + ["forecast", "internal-entry"]


def case_name(subject, case):
    if case == "corpus-three-forms":
        return f"suite-checks-{subject}-detects-three-forms-per-language"
    if case == "contexts":
        return f"suite-checks-{subject}-distinguishes-actions-from-observations"
    suffix = "fails-on-planted-violation" if case == "planted" else f"errors-on-{case}"
    return f"suite-checks-{subject}-{suffix}"


def self_test(c, output=None, census_only=False):
    cases = self_test_cases()
    declarations = c.tsv(c.suite / "self-tests.txt", 3)
    registry = check_registry()
    required = {(subject, case): name for subject, case, name in declarations if subject in registry}
    for key in cases:
        required.setdefault(key, case_name(*key))
    if census_only:
        for key in sorted(cases):
            print(required[key])
        return 0
    lines, failures = [], 0
    for key, name in required.items():
        try:
            if key not in cases:
                raise AssertionError(f"self-test not implemented: {key}")
            with tempfile.TemporaryDirectory(prefix="suite-check-") as temporary:
                cases[key](Path(temporary))
            line = f"SELFTEST {name} PASS"
        except Exception as error:
            failures += 1
            line = f"SELFTEST {name} FAIL {type(error).__name__}: {error}"
        lines.append(line)
        print(line, flush=True)
    if output:
        directory = Path(output)
        directory.mkdir(parents=True, exist_ok=True)
        (directory / "census.txt").write_text("\n".join(required[key] for key in sorted(cases)) + "\n")
        (directory / "self-tests.log").write_text(f"tree: {c.subject()}\n" + "\n".join(lines) + "\n")
    print(f"SELFTESTS {'FAIL' if failures else 'PASS'} {len(required)} tests, {failures} failed")
    return int(bool(failures))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--close", action="store_true")
    parser.add_argument("--check", action="append", default=[])
    parser.add_argument("--pfm", type=Path)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--suite", type=Path)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--list-self-tests", action="store_true")
    parser.add_argument("--output", type=Path)
    parser.add_argument("--runs", type=Path, help="recorded run evidence (defaults to the design's runs directory)")
    parser.add_argument("--subject", action="store_true")
    parser.add_argument("--dirty", action="store_true")
    parser.add_argument("--baseline-signatures", choices=("bare-doors", "refused-shapes", "clones", "size"),
                        help="print an initial measured baseline for review; never write or raise a baseline")
    args = parser.parse_args()
    c = Context(args.root, args.suite, args.pfm, args.close)
    if args.runs:
        c.runs = args.runs.resolve()
    try:
        if args.subject:
            print(c.subject())
            return 0
        if args.dirty:
            print(c.dirty())
            return 0
        if args.baseline_signatures:
            from source_checks import signatures
            rows, scanned = signatures(c, args.baseline_signatures)
            print(f"# measured {scanned} source files")
            print(f"# ceiling {date.today().isoformat()} {len(rows)}")
            for row in sorted(rows):
                print(signature_key(row))
            return 0
        if args.self_test or args.list_self_tests:
            return self_test(c, args.output, args.list_self_tests)
        registry = check_registry()
        selected = args.check or [name for name in declared_checks(c) if args.close or name not in CLOSE]
        code = 0
        for name in selected:
            if name not in registry:
                verdict, detail = "ERROR", "declared check has no implementation"
            else:
                verdict, detail = evaluate(c, name, registry[name])
            print(f"CHECK {name} {verdict} {' '.join(detail.splitlines())}", flush=True)
            code = max(code, {"PASS": 0, "FAIL": 1, "ERROR": 2}[verdict])
        return code
    except (Unavailable, OSError) as error:
        print(f"CHECK setup ERROR {error}")
        return 2


if __name__ == "__main__":
    sys.exit(main())
