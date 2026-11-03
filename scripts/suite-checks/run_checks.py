"""Checks whose evidence is a recorded blocking run, never a coverage declaration."""

import math
import re
import statistics
from collections import Counter, defaultdict

from core import Unavailable, nonempty, require, tier
from evidence import FAIL, PASS, activity, age_days, canonical, engine_input, green, ledger, red_proof, runs


def budgets(c):
    target = c.value("target-sequence-ms")
    pins = c.tsv(c.suite / "budgets.tsv", 4, "key")
    costs = defaultdict(int)
    for row, record in c.beats().items():
        if "@" in row:
            require(record.get("cost_ms", "").isdigit(), f"missing paper cost: {row}")
            costs[row.split("@", 1)[1].split("+")[0]] += int(record["cost_ms"])
    nonempty(costs, "no paper costs derived")
    require(max(costs.values()) <= target, f"paper critical path {max(costs.values())} ms exceeds {target} ms")
    if not c.close:
        return f"paper critical path {max(costs.values())} ms <= {target} ms; {len(pins)} pinned budgets"
    require(bool(pins), "BUDGET-UNPINNED: budgets.tsv empty")
    headroom = c.value("budget-headroom-pct")
    previous = {key: int(budget) for key, budget, _, _ in
                c.tsv(c.suite / "budgets.tsv", 4, "key", c.at(c.suite / "budgets.tsv"))}
    previous_rows = {row for _, row, _ in c.tsv(c.suite / "map.tsv", 3, "landscape_id", c.at(c.suite / "map.tsv"))}
    added = {row for _, row, _ in c.mapping()} - previous_rows
    for key, budget, names, load in pins:
        names = names.split(",")
        require(len(set(names)) >= 3, f"budget {key} needs three distinct green runs")
        walls, subjects, roots, maxima = [], set(), set(), defaultdict(int)
        for name in names:
            header, rows = ledger(c, name)
            require(header.get("mode") == ("tier" if key.startswith("tier-") else "sequence"), f"budget run mode: {name}")
            require(header.get("load") == load, f"budget load differs: {name}")
            require(green(c, rows), f"budget run not green: {name}")
            selected = [row for row_id, row in rows.items() if key == "sequence" or row_id.startswith(key + "/") or row_id.startswith(key + ":")]
            nonempty(selected, f"budget run lacks {key}: {name}")
            walls.append(sum(int(row["wall_ms"]) for row in selected))
            for row_id, result in rows.items():
                if key == "sequence" or row_id.startswith(key + "/") or row_id.startswith(key + ":"):
                    maxima[row_id] = max(maxima[row_id], int(result["wall_ms"]))
            subjects.add(header.get("tree"))
            roots.add(header.get("root"))
        require(len(subjects if key.startswith("tier-") else roots) == 1, f"budget {key} mixes subjects or roots")
        required = math.ceil(max(walls) * (100 + headroom) / 100)
        require(int(budget) >= required, f"budget {key}: pin {budget} below recomputed {required}")
        require(max(walls) <= 2 * statistics.median(walls), f"budget {key} slowest > twice median")
        require(key != "sequence" or int(budget) <= target, f"sequence pin above target: {budget}")
        if key in previous:
            require(int(budget) <= previous[key] + sum(maxima[row] for row in added),
                    f"budget {key} raised beyond added rows' measured cost")
    return f"{len(pins)} budgets recomputed from named green runs"


def collected(c):
    ci = c.read(".github/workflows/ci.yml", nonempty=True)
    if not re.search(r"suite-checks\.sh[^\n]*--close", ci):
        raise Unavailable("no CI job invokes suite-checks.sh --close")
    operating_systems = [value.strip() for value in c.setting("live-os").split(",")]
    patterns = nonempty(c.policy(c.spell / "test-patterns.txt"), "test-name patterns empty")
    blocking = c.read(c.runs / "blocking.log", nonempty=True)
    close = c.read(c.runs / "close.log", nonempty=True)
    from checks import check_registry, CLOSE
    required_checks = set(check_registry())
    observed_checks = set(re.findall(r"(?m)^CHECK ([^ ]+) (?:PASS|FAIL|ERROR) ", blocking + "\n" + close))
    require(required_checks <= observed_checks, "blocking logs missing checks: " + ",".join(sorted(required_checks - observed_checks)))
    require(set(CLOSE) <= set(re.findall(r"(?m)^CHECK ([^ ]+) PASS ", close)), "close checks have no passing blocking result")
    start = re.search(r"(?m)^start: (.+)$", close)
    if not start:
        raise Unavailable("close log has no start timestamp")
    require(age_days({"start": start[1]}) <= c.value("schedule-window-days"), "close job outside schedule window")
    census = Counter(re.findall(r"(?m)^COLLECT (.+)$", blocking))
    expected = [path for path in c.files() if any(re.search(pattern, path) for pattern in patterns)]
    nonempty(expected, "test patterns matched no files")
    require(all(census[path] == 1 for path in expected), "test files not collected exactly once: " + ",".join(path for path in expected if census[path] != 1))
    for os_name in operating_systems:
        # Job blocks keep runner and invocation together; unrelated names cannot satisfy a gate.
        jobs = re.split(r"(?m)(?=^  [a-zA-Z0-9_-]+:\s*$)", ci)
        require(any(re.search(rf"runs-on:.*{'ubuntu|linux' if os_name == 'linux' else 'macos'}", job)
                    and c.setting("blocking-command-live") in job for job in jobs), f"no blocking live job for {os_name}")
    return f"{len(expected)} test files collected once; all checks and OS gates recorded"


def self_tests(c):
    declarations = [line.split("\t") for line in c.policy(c.suite / "self-tests.txt")]
    census = set(c.lines(c.runs / "census.txt", True))
    results = c.read(c.runs / "self-tests.log", True)
    subject = re.search(r"(?m)^tree: (.+)$", results)
    require(subject is not None and subject[1] == c.subject(), "self-tests have no passing result on this subject")
    passed = set(re.findall(r"(?m)^SELFTEST ([^ ]+) PASS$", results))
    from checks import check_registry
    for check in check_registry():
        cases = {row[1] for row in declarations if row[0] == check}
        require("planted" in cases and bool(cases - {"planted"}), f"self-tests missing error/planted input: {check}")
    missing = [row[2] for row in declarations if len(row) != 3 or row[2] not in census or row[2] not in passed]
    require(not missing, "self-tests absent or not passing: " + ",".join(missing))
    return f"{len(declarations)} self-tests collected and passing"


def env_twice(c):
    c.setting("double-run-paths")
    keys = nonempty(c.lines(c.spell / "config-keys.txt"), "config key set unreadable or empty")
    base, original = ledger(c, "unit-base.tsv")
    changed, perturbed = ledger(c, "unit-perturbed.tsv")
    if max(age_days(base), age_days(changed)) > c.value("schedule-window-days"):
        raise Unavailable("last perturbed leg older than schedule window")
    require(base.get("tree") == changed.get("tree") == c.subject(), "configuration legs name different subjects")
    require(set(changed.get("perturbed_keys", "").split(",")) == set(keys), "not every configuration key was perturbed")
    normalize = lambda rows: {key: (row.get("verdict"), row.get("expected")) for key, row in rows.items()}
    require(normalize(original) == normalize(perturbed), "base and perturbed outcomes differ")
    require(base.get("config") != changed.get("config"), "configuration was not perturbed")
    return f"{len(original)} unit results equal under {len(keys)} perturbed keys"


def tree(c):
    attempts = runs(c)
    candidates = canonical(c)
    subject = c.subject()
    require(not c.dirty(), "working tree differs from the recorded clean subject")
    sequences = [(name, header, rows) for name, header, rows in candidates
                 if header.get("tree") == subject and not header.get("dirty") and green(c, rows)]
    if not sequences:
        name, header, _ = candidates[0]
        require(header.get("tree") == subject, f"wrong subject: {name}")
        require(not header.get("dirty"), f"dirty canonical subject: {name}")
        require(False, "no green canonical run on the subject")
    solos = [(name, header, rows) for name, header, rows in attempts
             if header.get("mode") == "solo" and header.get("tree") == subject
             and not header.get("dirty") and green(c, rows)]
    lanes = {row.split("/")[0] for row in c.beats() if tier(row) == "live"}
    green_lanes, solo_lanes = set(), set()
    for name, header, rows in sequences + solos:
        require(header.get("tree") == subject, f"wrong subject: {name}")
        require(header.get("tree_end") == header.get("tree") and header.get("dirty_end") == header.get("dirty"), f"subject changed during run: {name}")
        require(not header.get("dirty"), f"dirty canonical subject: {name}")
        require(header.get("mode") in ("sequence", "solo", "tier"), f"noncanonical run mode: {name}")
        require(header.get("boot") == "process", f"unproved in-process boot: {name}")
        records = activity(c, name, header)
        require(all(str(record.get("pid", "")) != header.get("runner_pid") for record in records),
                f"product executed in runner process: {name}")
        intervals = []
        for row, result in rows.items():
            lane = row.split("/")[0]
            if header.get("mode") == "sequence":
                green_lanes.add(lane)
                start = float(result["t+s"])
                intervals.append((start, start + int(result["wall_ms"]) / 1000, lane))
            if header.get("mode") == "solo":
                solo_lanes.add(lane)
        intervals.sort()
        for left, right in zip(intervals, intervals[1:]):
            require(left[2] == right[2] or left[1] <= right[0], f"overlapping lanes: {name}")
    require(lanes <= green_lanes and lanes <= solo_lanes, "lane lacks green sequence/solo: " + ",".join(sorted(lanes - green_lanes | lanes - solo_lanes)))
    return f"{len(sequences)} canonical runs on subject; {len(lanes)} lanes proven sequence and solo"


def observed(c):
    sequences = [(name, header, rows) for name, header, rows in canonical(c)
                 if header.get("tree") == c.subject() and not header.get("dirty") and green(c, rows)]
    nonempty(sequences, "no canonical ledger on this subject")
    land, mapping, beats = c.landscape(), c.mapping(), c.beats()
    nonempty([row.get("op") for row in land.values() if row.get("op")], "op fields empty")
    all_rows, operations, headers = {}, defaultdict(set), {}
    for kind in {tier(row) for _, row, _ in mapping} - {"live"}:
        header, rows = ledger(c, kind + ".tsv")
        sequences.append((kind + ".tsv", header, rows))
    for name, header, rows in sequences:
        records = activity(c, name, header, allow_empty=True)
        for row, result in rows.items():
            all_rows[row], headers[row] = result, header
            operations[row] = {record["action"] for record in records
                               if record.get("beat") == row and record.get("action")}
    failures = []
    for key, row, _ in mapping:
        result = all_rows.get(row, {})
        if not result:
            failures.append(f"UNOBSERVED: {row}")
        if key not in result.get("assertions-by-id", "").split(","):
            failures.append(f"NO-ASSERTION: {key}/{row}")
        operation = land.get(key, {}).get("op")
        evidence = beats.get(row, {}).get("evidence", "")
        if not operation and evidence.startswith("engine-input:") and result:
            engine_input(c, headers[row], row, key, result, evidence.removeprefix("engine-input:"), operations[row])
        elif operation not in operations[row]:
            failures.append(f"CLAIMED-NOT-INVOKED: {key}/{row}")
    require(not failures, "; ".join(failures))
    return f"{len(mapping)} mapped assertions and operations observed"


def bite(c):
    rows = c.tsv(c.suite / "bites.tsv", 4, "row")
    rules = c.lines(c.spell / "third-party-rules.txt")
    from checks import check_registry
    from shape_rules import SHAPES
    owed = {row for _, row, _ in c.mapping()} | set(check_registry()) | set(SHAPES) | {line.split("\t")[0] for line in rules}
    counts = Counter(row[0] for row in rows)
    require(owed <= set(counts), "missing bite rows: " + ",".join(sorted(owed - set(counts))))
    for row, location, digest, run in rows:
        _, _, diff = red_proof(c, row, digest, run)
        removed = [line for line in diff.splitlines() if line.startswith("-") and not line.startswith("---") and line[1:].strip()]
        added = [line for line in diff.splitlines() if line.startswith("+") and not line.startswith("+++")]
        require(len(removed) == 1 and not any(re.search(r"\b(exit|return false|panic\()", line) for line in added), f"unconditional/oversized mutation: {row}")
        require(location.rsplit(":", 1)[0] in diff, f"mutation outside named file: {row}")
    return f"{len(rows)} mutations watched red"


def flakes(c):
    attempts, outcomes, shuffled = runs(c), defaultdict(set), set()
    for name, header, rows in attempts:
        if header.get("tree") != c.subject():
            continue
        for row, result in rows.items():
            verdict = result.get("verdict")
            outcomes[row, header.get("dirty"), header.get("root"), header.get("config")].add("PASS" if verdict in PASS else "FAIL")
        if header.get("order", "").startswith("shuffle") and age_days(header) <= c.value("schedule-window-days"):
            shuffled.update(tier(row) for row in rows)
    require(not any(len(values) > 1 for values in outcomes.values()), "row failed and passed on identical subject/configuration/root")
    require({"tier-unit", "tier-hermetic"} <= shuffled, "unit or hermetic shuffled leg missing within schedule window")
    return f"{len(attempts)} attempts recomputed; no flaky row"


CHECKS = {"budgets": budgets, "collected": collected, "self-tests": self_tests,
          "env-twice": env_twice, "tree": tree, "observed": observed, "bite": bite, "flakes": flakes}
