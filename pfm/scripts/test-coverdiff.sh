#!/usr/bin/env bash
# test-coverdiff.sh — the unit-isolation driver: per package, the statements only the jail tests
# hold. A move test (a jail test, kind real in scripts/test-kinds.sh) whose statements the journey or a
# unit test also covers is folded into an assertion and deleted; the statements only it holds need a
# journey step or a unit test first.
#
#   scripts/test-coverdiff.sh <journey-coverage.out> <out-dir> [pfm-dir]
#
# Runs in the fence (it runs go test): per package directory, its unit tests and its move tests as two
# `go test -covermode=set -coverpkg={module}/...` runs anchored on their names (PFM_COVERDIFF_JOBS at once,
# default 4), then merges the profiles with the journey's (the run's artifacts/coverage.out, its
# atomic counts read as set). A statement is held by a side when any of its blocks counted above zero.
# Coverage of a child process the test runs (a jailed pfm binary) is not collected: the move side is a
# lower bound, so the move-only list is too.
# Writes into <out-dir>:
#   raw/{unit|move}.{label}.{out|log}   each run's profile and output (label: the dir, `/` as `_`)
#   runs.tsv      side, dir, tests, rc (`killed` when its job died unrecorded), profile (yes|no) — a red
#                 or unrecorded run is named, never dropped
#   report.tsv    package, statements, journey, unit, move, move_only, move_only_lines — move_only is held
#                 by the move tests and by neither the journey nor a unit test; packages with move_only > 0
#                 first, most first, then by name
#   move-only.txt one `{file}:{start}-{end}\t{statements}` row per move-only block, by file and line
# and prints, last:
#   COVERDIFF {n} packages · move-only {s} statements on {l} lines · runs {r} ({f} red, {u} unrecorded) · journey {path}
# BROKEN STATE: a missing journey profile or one with no block, an out-dir whose raw/ already holds a
# run, a test-kinds.sh that cannot list, or no run
# that wrote a profile prints `COVERDIFF ERROR {why}` and exits 2; a red run exits 1 after the report.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
die() { echo "COVERDIFF ERROR $*"; exit 2; }
[ $# -ge 2 ] || die 'usage: test-coverdiff.sh <journey-coverage.out> <out-dir> [pfm-dir]'
journey=$1 out=$2 pfm=${3:-$HERE/..}
jobs=${PFM_COVERDIFF_JOBS:-4}
[[ $jobs =~ ^[1-9][0-9]*$ ]] || die "PFM_COVERDIFF_JOBS must be a positive integer (got '$jobs')"
[ -r "$journey" ] || die "journey profile $journey unreadable"
grep -q '^[^ ]*:[0-9]' "$journey" || die "journey profile $journey holds no coverage block"
command -v go >/dev/null 2>&1 || die 'go not on PATH'
module=$(awk '$1 == "module" {print $2; exit}' "$pfm/go.mod" 2>/dev/null) || true
[ -n "$module" ] || die "no module line in $pfm/go.mod"
mkdir -p "$out/raw" || die "cannot create $out/raw"
# The report merges every raw/*.out and raw/*.rc: an earlier run's files would count as this run's.
[ -z "$(ls -A "$out/raw")" ] || die "$out/raw already holds a run — name an empty out-dir"
kinds=$(bash "$HERE/test-kinds.sh" --list "$pfm") || die "test-kinds.sh --list failed: ${kinds%%$'\n'*}"

# One job row per side and directory: side, dir, label, test count, the anchored -run regex.
awk -F '\t' '{
  side = ($1 == "unit") ? "unit" : "move"; key = side "\t" $2
  if (!(key in names)) order[++n] = key
  names[key] = (key in names) ? names[key] "|" $4 : $4; count[key]++
} END { for (i = 1; i <= n; i++) { split(order[i], k, "\t"); label = k[2]; gsub("/", "_", label)
  printf "%s\t%s\t%s\t%d\t^(%s)$\n", k[1], k[2], label, count[order[i]], names[order[i]] } }' <<< "$kinds" > "$out/jobs.tsv" \
  || die 'cannot write the job list'

run_one() { # run_one <jobs.tsv row>: one go test run of a side's tests in one dir, its rc into raw/
  local side dir label count regex profile rc have=no
  IFS=$'\t' read -r side dir label count regex <<< "$1"
  profile="$out/raw/$side.$label.out"
  go -C "$pfm" test -tags jail -count=1 -covermode=set -coverpkg="$module/..." -run "$regex" -coverprofile="$profile" "./$dir" \
    > "$out/raw/$side.$label.log" 2>&1
  rc=$?
  [ -s "$profile" ] && have=yes
  printf '%s\t%s\t%s\t%s\t%s\n' "$side" "$dir" "$count" "$rc" "$have" > "$out/raw/$side.$label.rc"
}
export -f run_one
export out pfm module
tr '\n' '\0' < "$out/jobs.tsv" | xargs -0 -n 1 -P "$jobs" bash -c 'run_one "$1"' _
# A job whose shell died before writing its row (a kill; xargs then starts no more) is named, never dropped.
while IFS=$'\t' read -r side dir label count _; do
  [ -f "$out/raw/$side.$label.rc" ] ||
    printf '%s\t%s\t%s\tkilled\tno\n' "$side" "$dir" "$count" > "$out/raw/$side.$label.rc" || die "cannot record $side $dir"
done < "$out/jobs.tsv"
printf 'side\tdir\ttests\trc\tprofile\n' > "$out/runs.tsv"
cat "$out"/raw/*.rc >> "$out/runs.tsv" 2>/dev/null || die "no run recorded under $out/raw"

summary=$(python3 - "$out" "$journey" <<'PY'
import glob
import os
import sys

out, journey = sys.argv[1], sys.argv[2]


def read(paths):
    """{block: (statements, covered)} merged over every profile: covered when any count > 0."""
    blocks = {}
    for path in paths:
        with open(path, encoding='utf-8') as handle:
            for line in handle:
                if line.startswith('mode:') or not line.strip():
                    continue
                try:
                    block, stmts, count = line.rsplit(' ', 2)
                    stmts, count = int(stmts), int(count)
                except ValueError:
                    print(f'COVERDIFF ERROR {path}: not a coverage profile line: {line.rstrip()}')
                    sys.exit(2)
                old = blocks.get(block, (stmts, False))
                blocks[block] = (stmts, old[1] or count > 0)
    return blocks


raw = f'{out}/raw'
unit = read(sorted(glob.glob(f'{raw}/unit.*.out')))
move = read(sorted(glob.glob(f'{raw}/move.*.out')))
jour = read([journey])
if not unit and not move:
    print(f'COVERDIFF ERROR no run under {raw} wrote a profile')
    sys.exit(2)
rows, only = {}, []
for block in sorted(set(unit) | set(move) | set(jour)):
    stmts = (move.get(block) or unit.get(block) or jour[block])[0]
    path = block.split(':', 1)[0]
    package = os.path.dirname(path)
    held = {side: blocks.get(block, (0, False))[1] for side, blocks in (('journey', jour), ('unit', unit), ('move', move))}
    row = rows.setdefault(package, {'statements': 0, 'journey': 0, 'unit': 0, 'move': 0, 'move_only': 0, 'lines': set()})
    row['statements'] += stmts
    for side, yes in held.items():
        row[side] += stmts if yes else 0
    if held['move'] and not held['journey'] and not held['unit']:
        row['move_only'] += stmts
        start, end = block.split(':', 1)[1].split(',')
        first, last = int(start.split('.')[0]), int(end.split('.')[0])
        row['lines'].update((path, number) for number in range(first, last + 1))
        only.append((path, first, last, stmts))
with open(f'{out}/report.tsv', 'w', encoding='utf-8') as handle:
    handle.write('package\tstatements\tjourney\tunit\tmove\tmove_only\tmove_only_lines\n')
    for package, row in sorted(rows.items(), key=lambda item: (-item[1]['move_only'], item[0])):
        handle.write(f"{package}\t{row['statements']}\t{row['journey']}\t{row['unit']}\t{row['move']}"
                     f"\t{row['move_only']}\t{len(row['lines'])}\n")
with open(f'{out}/move-only.txt', 'w', encoding='utf-8') as handle:
    for path, first, last, stmts in sorted(only):
        handle.write(f'{path}:{first}-{last}\t{stmts}\n')
packages = sum(1 for row in rows.values() if row['statements'])
statements = sum(row['move_only'] for row in rows.values())
lines = sum(len(row['lines']) for row in rows.values())
print(f'{packages}\t{statements}\t{lines}')
PY
) || { echo "$summary"; exit 2; }
IFS=$'\t' read -r packages statements lines <<< "$summary"
runs=$(awk 'NR > 1' "$out/runs.tsv" | wc -l)
red=$(awk -F '\t' 'NR > 1 && $4 != 0' "$out/runs.tsv" | wc -l)
unrecorded=$(awk -F '\t' 'NR > 1 && $5 == "no"' "$out/runs.tsv" | wc -l)
echo "COVERDIFF $packages packages · move-only $statements statements on $lines lines · runs $runs ($red red, $unrecorded unrecorded) · journey $journey"
[ "$red" -eq 0 ] && [ "$unrecorded" -eq 0 ]
