"""The Opus plan checks, with conditional branches and imported forecast obligations."""

import fnmatch
import itertools
import re
from collections import Counter, defaultdict
from datetime import date

from core import Unavailable, nonempty, require, tier
from source_analysis import lane_beats


def map_ids(c):
    wanted, actual = set(c.landscape()), {row[0] for row in c.mapping()}
    require(wanted == actual, "unmatched: " + " ".join(sorted(wanted ^ actual)))
    return f"{len(wanted)} ids, each mapped"


def map_beats(c):
    beats, mapped = c.beats(), {row[1] for row in c.mapping()}
    pending = set(c.lines(c.suite / "pending.txt"))
    defined = set()
    for declaration in lane_beats(c):
        defined.update(row for row in mapped if declaration["row"] in (row, row.split("@", 1)[0]))
    for path in c.files():
        if path.endswith(("_test.go", ".txtar")):
            names = re.findall(r't\.Run\("([a-z][a-z0-9-]+)"|(?m:^# test ([a-z][a-z0-9-]+))', c.read(path))
            for pair in names:
                defined.update(f"{kind}:{next(part for part in pair if part)}" for kind in ("tier-unit", "tier-hermetic"))
    require(not (mapped - set(beats)), "map rows missing from beats.md: " + " ".join(sorted(mapped - set(beats))))
    require(not (set(beats) - mapped), "beats.md rows unmapped: " + " ".join(sorted(set(beats) - mapped)))
    require(not (mapped - defined - pending), "undefined: " + " ".join(sorted(mapped - defined - pending)))
    require(not (pending - mapped), "unknown pending rows: " + " ".join(sorted(pending - mapped)))
    require(not c.close or not pending, f"{len(pending)} rows still pending")
    return f"{len(mapped)} rows defined or pending ({len(pending)} pending)"


def map_names(c):
    landscape = c.landscape()
    expected = nonempty({row["op"] for row in landscape.values() if row.get("op")},
                        "DERIVE-FAILED: no op fields")
    actual = nonempty(set(c.command([str(c.pfm), "help", "--ops"]).splitlines()),
                      "DERIVE-FAILED: registry dump empty")
    missing_writers = []
    for key, row in landscape.items():
        location = row["writer"].rsplit(":", 1)
        if len(location) != 2 or not location[1].isdigit() or not c.path(location[0]).is_file():
            missing_writers.append(key)
        elif int(location[1]) > len(c.read(location[0]).splitlines()):
            missing_writers.append(key)
    require(expected == actual and not missing_writers,
            "built ops without a row: " + ",".join(sorted(actual - expected)) +
            "; rows the build lacks: " + ",".join(sorted(expected - actual)) +
            "; writer missing: " + ",".join(missing_writers))
    return f"{len(actual)} public operations, each in the landscape"


def map_dup(c):
    land, mapping = c.landscape(), c.mapping()
    beats = c.beats()
    modifiers = [line.split("\t")[0] for line in c.policy(c.suite / "dimensions.tsv")
                 if line.split("\t")[0] not in ("os", "terminal", "place", "engine", "peer", "none")]
    rows = {row for _, row, _ in mapping}
    ids, ops, crossings = Counter(), defaultdict(set), defaultdict(set)
    failures = []
    for key, row, reason in mapping:
        branch = row.partition("@")[2]
        context = beats.get(row, {})
        branch += "".join(f"+{name}={context[name]}" for name in modifiers if name in context)
        branch += "".join(f"+{name}={context[name]}" for name in ("caller-origin", "forecast-factors", "cycle")
                          if name in context and (context.get("fork-of") or name == "cycle"))
        kind = tier(row)
        if "crossing:" in reason:
            crossings[key].add(row.split("/")[0])
        else:
            ids[key, kind, branch] += 1
            if kind != "tier-unit":
                record = land.get(key, {})
                if record.get("op"):
                    ops[record["op"], context.get("entry", record.get("entry")), record.get("kind"), kind, branch].add(row)
        if kind == "live" and not any(token in reason for token in ("twin:", "live-only")):
            failures.append(f"live row without twin or live-only: {row}")
        match = re.search(r"twin:([^ ]+)", reason)
        if match and (match[1] not in rows or tier(match[1]) == kind):
            failures.append(f"bad twin: {row}")
    failures.extend(f"duplicate id/tier/branch: {key}" for key, count in ids.items() if count > 1)
    failures.extend(f"shared op/entry/kind/tier/branch: {key}" for key, values in ops.items() if len(values) > 1)
    failures.extend(f"one-lane crossing: {key}" for key, lanes in crossings.items() if len(lanes) < 2)
    require(not failures, "; ".join(failures))
    return f"{len(mapping)} map rows, no duplicate"


def cut_patterns(pattern):
    # The ledger's brace alternatives extend shell globs; fnmatch owns glob matching.
    match = re.search(r"\{([^{}]+)\}", pattern)
    if not match:
        return [pattern]
    return [result for value in match[1].split(",")
            for result in cut_patterns(pattern[:match.start()] + value + pattern[match.end():])]


def ruled_cuts(c):
    cuts, refs = [], c.references()
    for line in c.policy(c.suite / "cuts.tsv", True):
        parts = line.split("\t")
        require(len(parts) == 3 and parts[1].isdigit(), f"cut without cost: {line}")
        require(parts[2] in refs, f"cut ruling not a reference: {parts[2]}")
        cuts.extend(cut_patterns(parts[0]))
    return cuts


def branches(c):
    lines = nonempty(c.policy(c.suite / "dimensions.tsv"), "dimensions.tsv empty without none")
    beats = c.beats()
    if lines == ["none"]:
        require(not any("@" in key for key in beats), "dimensions none, yet beats carry branch keys")
        return "none: no dimension"
    dims = [line.split("\t") for line in lines]
    if any(len(row) != 3 or not row[1] for row in dims):
        raise Unavailable("malformed dimensions.tsv")
    dimensions = {}
    for name, values, _ in dims:
        dimensions[name] = list(dict.fromkeys(dimensions.get(name, []) + values.split(",")))
    positional = [name for name in dimensions if name in ("os", "terminal", "place", "engine", "peer")]
    cuts = ruled_cuts(c)
    families, covered, forks = defaultdict(set), set(), defaultdict(list)
    for row, record in beats.items():
        if "@" not in row:
            continue
        stem, key = row.split("@", 1)
        values = key.split("+")
        require(len(values) <= len(positional), f"unrecognised branch depth: {row}")
        for name, value in zip(positional, values):
            require(value in dimensions[name], f"unknown {name} value {value}: {row}")
            covered.add(name)
        if record.get("fork-of"):
            parent = record["fork-of"]
            require(parent in beats, f"fork parent absent: {row}: {parent}")
            require(parent.partition("@")[2] == key, f"fork changed positional branch: {row}")
            if not record.get("forecast-factors"):
                forks[parent].append((row, record))
            continue
        families[stem, len(values)].add(key)
    nonempty(families, "DERIVE-FAILED: no forked beat read")
    missing, total, cut_count = [], 0, 0
    for (stem, depth), walked in families.items():
        for values in itertools.product(*(dimensions[name] for name in positional[:depth])):
            total += 1
            key = "+".join(values)
            if key in walked:
                continue
            candidate = f"{stem}@{key}"
            if any(fnmatch.fnmatchcase(candidate, pattern) for pattern in cuts):
                cut_count += 1
            else:
                missing.append(candidate)
    for name, values in dimensions.items():
        if name in positional:
            continue
        walked = {record.get(name) for record in beats.values() if record.get(name)}
        unknown = walked - set(values)
        require(not unknown, f"unknown {name}: {sorted(unknown)}")
        for value in values:
            if value not in walked and not any(f"{name}={value}" in cut for cut in cuts):
                missing.append(f"{name}={value}")
        if walked:
            covered.add(name)
    modifiers = [name for name in dimensions if name not in positional]
    for parent, children in forks.items():
        axes = [name for name in modifiers if any(record.get(name) for _, record in children)]
        tuples = {tuple(record.get(name, "") for name in axes) for _, record in children}
        for values in itertools.product(*(dimensions[name] for name in axes)):
            if values in tuples:
                continue
            candidate = parent + "+" + "+".join(f"{name}={value}" for name, value in zip(axes, values))
            if not any(fnmatch.fnmatchcase(candidate, pattern) for pattern in cuts):
                missing.append(candidate)
    require(not missing, f"{len(missing)} combinations neither walked nor cut: " + " ".join(missing[:16]))
    for name in dimensions:
        require(name in covered or any(name in cut for cut in cuts), f"unforked-uncut dimension: {name}")
    return f"{total} combinations: {total - cut_count} walked, {cut_count} cut; {len(dimensions)} dimensions"


def landscape_scope(c):
    patterns = nonempty(c.policy(c.spell / "exclusion.go"), "exclusion spellings empty")
    refs, failures = c.references(), []
    for key, record in c.landscape().items():
        if any(not record.get(field) for field in ("kind", "ruled", "surface")):
            failures.append(f"missing field: {key}")
        if any(re.search(pattern, record.get("op", "")) for pattern in patterns):
            failures.append(f"excluded op: {key}")
        if record.get("kind") not in ("success", "refusal"):
            failures.append(f"invalid kind: {key}")
        ruled_surface = set(record.get("surface", "").split("+")) & {"identity", "money", "consent", "safety", "data-lifecycle"}
        ruling = re.search(r"ruling:([^ ]+)", record.get("ruled", ""))
        if ruled_surface and record.get("ruled") != "yes" and not (ruling and ruling[1] in refs):
            failures.append(f"unruled surface: {key}")
    require(not failures, "; ".join(failures))
    return f"{len(c.landscape())} rows scoped"


def gaps(c):
    roster = set(nonempty(c.policy(c.suite / "roster.txt", True), "roster empty"))
    own = nonempty(c.policy(c.spell / "own-identifiers.txt"), "own identifiers empty")
    refs, beats = c.references(), c.beats()
    rows = c.policy_rows(c.suite / "gaps.tsv", 5, "row", excuses=True)
    for row, key, why, owner, expiry in rows:
        require(owner in roster, f"owner off roster: {owner}")
        require(any(fnmatch.fnmatchcase(beat, row) for beat in beats), f"gap names no beat: {row}")
        if expiry == "until-owner-decision":
            matching = [record for beat, record in beats.items() if fnmatch.fnmatchcase(beat, row)]
            require(all(record.get("gap-owner") == owner and record.get("gap-reason") == why for record in matching),
                    f"unratified owner decision gap: {row}")
        elif why.startswith("ruling:"):
            # MERGE expressly admits these product gaps; the cited ruling is the exception.
            require(why[7:] in refs and why[7:].startswith("ruling-gap-"), f"unratified product gap: {why}")
            require(expiry == "until-product-flight", f"invalid product gap lifetime: {row}")
        else:
            require(bool(re.fullmatch(r"\d{4}-\d{2}-\d{2}", expiry)) and expiry >= date.today().isoformat(), f"expired gap: {row}")
            require(re.search(r"https?://", why), f"no external reference: {row}")
            require(not any(re.search(pattern, why) for pattern in own), f"internal gap: {row}")
    return f"{len(rows)} gap entries in force"


def forecast(c):
    rows = c.tsv(c.suite / "forecast.tsv", 7, "forecast")
    source = c.policy_rows(c.suite / "forecast-src.tsv", 6, "forecast")
    nonempty(rows, "forecast.tsv empty")
    nonempty(source, "forecast source empty")
    imported, ledger = Counter(tuple(row) for row in source), Counter(tuple(row[:6]) for row in rows)
    differences = []
    for cell in sorted({row[:2] for row in imported.keys() | ledger.keys()}):
        expected = sorted(row[2:] for row in imported.elements() if row[:2] == cell)
        actual = sorted(row[2:] for row in ledger.elements() if row[:2] == cell)
        if expected != actual:
            differences.append(f"{'/'.join(cell)}: imported={expected!r} ledger={actual!r}")
    require(not differences, "forecast cells differ from imported source; " + "; ".join(differences))
    require(len({tuple(row[:2]) for row in rows}) == len(rows), "duplicate forecast cell")
    beats, cuts, held = c.beats(), ruled_cuts(c), Counter()
    for row in rows:
        # A cell is held by a live journey beat, a named unit-tier holder beat, or a ruled cut of its beat.
        if row[6] not in beats and any(fnmatch.fnmatchcase(row[6], pattern) for pattern in cuts):
            held["cut"] += 1
            continue
        require(row[6] in beats and tier(row[6]) in ("live", "tier-unit"),
                f"forecast cell {row[0]}/{row[1]} has no journey beat: {row[6]}")
        record = beats[row[6]]
        # One unit holder may hold several cells, naming each in forecast-cells instead of one factor set.
        named = f"{row[0]}/{row[1]}" in record.get("forecast-cells", "").split(",") and tier(row[6]) == "tier-unit"
        require(named or record.get("forecast-factors") == row[3], f"forecast factors not carried by beat: {row[0]}/{row[1]}")
        require(record.get("forecast-scope", "product") == "product", f"forecast cell outside product journey scope: {row[0]}/{row[1]}")
        held[tier(row[6])] += 1
    return (f"{len(rows)} cells: {held['live']} journey beats, {held['tier-unit']} unit holders, {held['cut']} cut; "
            f"{sum(row[5] == 'UNHELD' for row in rows)} UNHELD cells retained")


CHECKS = {
    "map-ids": map_ids, "map-beats": map_beats, "map-names": map_names,
    "map-dup": map_dup, "branches": branches, "landscape-scope": landscape_scope,
    "gaps": gaps, "forecast": forecast,
}
