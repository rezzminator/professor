#!/usr/bin/env bash
# pfm test timing ratchet — `make test` (part of `make gate`), `make timing`,
# and the pinned Tier U/A budget in pfm/.testtiming.yml. Design:
# docs/dev/testing/timing.md.
#
# Reads `go test -json` (stdin, or FILE) and always emits a TSV:
#   package  wall_s  tests  parallel_tests  slowest_test  slowest_s  status
# plus a trailing SUITE row reusing the same seven columns as
#   SUITE  wall_s  package_count  serial_sum  slowest_package  slowest_package_s  PASS|FAIL
# (serial_sum / wall_s is the achieved parallelism — read it off the row, this
# script does not print the ratio itself).
#
# --check   compares every package + the suite against --yml (default
#           pfm/.testtiming.yml) at budget * tolerance; exits 1 naming every
#           offender (FAIL) or unlisted package (UNBUDGETED, always red — a
#           package the tree ships that this file does not list is never
#           silently allowed). A run carrying ANY `fail` status is never given
#           a timing verdict at all — TIMING TESTS-FAILED, exit 1, before any
#           budget is even consulted: a broken suite has nothing to measure,
#           and its wall time (often inflated by retries/timeouts the failure
#           itself causes) is not a number to gate on.
# --measure --suite NAME F1 F2 F3   three separate `go test -json` runs of the
#           SAME suite; takes the median wall_s per package and ratchets
#           pfm/.testtiming.yml DOWN only — a package already in that suite's
#           block whose median beats its current budget gets the lower
#           number. It never adds a package the file does not already list
#           and never raises a budget: same law as scripts/arch-check.sh
#           (`--measure` locks in a shrink; raising a number, or budgeting a
#           brand-new package, is a hand edit named in the commit). With no
#           budget file at all, --measure bootstraps one from everything the
#           three runs saw, for the ONE suite named. Refuses to start while
#           another fence test run is live (same `docker ps` guard as
#           scripts/test-sweep.sh) — a budget measured beside a second fence
#           container is exactly the contention this wave's own baseline hit.
# --self-test   runs the sibling fixture suite (test-timing_test.sh) and
#           reports its verdict; every other flag is ignored.
#
# What THIS script reports when it is itself broken: unparsable JSON (or input
# with no package summaries in it at all) is TIMING-UNREADABLE, exit 2 —
# never a green. A missing --yml file under --check/--measure is the same:
# ERROR to stderr, exit 2, never treated as "nothing budgeted, so nothing to
# fail". A missing jq/python3+PyYAML is TOOLCHAIN-MISSING, exit 2.
set -uo pipefail
export LC_ALL=C

PFM="${PFM:-$(cd "$(dirname "$0")/.." && pwd)}"
REPO="${REPO:-$(cd "$PFM/.." && pwd)}"
YML="$PFM/.testtiming.yml"
SUITE_NAME="unit"
OUT=""
MODE="parse"
MEASURE_FILES=()
POSITIONAL=()

usage() {
  cat >&2 <<'EOF'
usage: test-timing.sh [--suite NAME] [--out FILE] [--yml FILE] [FILE]
       test-timing.sh [--suite NAME] [--out FILE] [--yml FILE] --check [FILE]
       test-timing.sh --suite NAME [--yml FILE] --measure FILE1 FILE2 FILE3
       test-timing.sh --self-test
FILE defaults to stdin (a `go test -json` stream). With no FILE and no pipe,
this blocks reading a terminal — pass '-' explicitly only if you mean stdin.
EOF
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --suite) SUITE_NAME="${2:?--suite needs NAME}"; shift 2 ;;
    --out) OUT="${2:?--out needs FILE}"; shift 2 ;;
    --yml) YML="${2:?--yml needs FILE}"; shift 2 ;;
    --check) MODE="check"; shift ;;
    --measure) MODE="measure"; shift ;;
    --self-test) MODE="self-test"; shift ;;
    -h|--help) usage ;;
    --) shift; while [ $# -gt 0 ]; do POSITIONAL+=("$1"); shift; done ;;
    -) POSITIONAL+=("-"); shift ;;
    -*) echo "test-timing: unknown flag $1" >&2; usage ;;
    *) POSITIONAL+=("$1"); shift ;;
  esac
done

if [ "$MODE" = self-test ]; then
  exec bash "$PFM/scripts/test-timing_test.sh"
fi

if [ "$MODE" = measure ]; then
  MEASURE_FILES=("${POSITIONAL[@]}")
  [ "${#MEASURE_FILES[@]}" -eq 3 ] || { echo "test-timing: --measure needs exactly 3 FILE args (median-of-3), got ${#MEASURE_FILES[@]}" >&2; usage; }
else
  [ "${#POSITIONAL[@]}" -le 1 ] || { echo "test-timing: at most one FILE in this mode" >&2; usage; }
fi

command -v jq >/dev/null 2>&1 || { echo "test-timing: TOOLCHAIN-MISSING — jq not found" >&2; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "test-timing: TOOLCHAIN-MISSING — python3 not found" >&2; exit 2; }
if [ "$MODE" = check ] || [ "$MODE" = measure ]; then
  python3 -c 'import yaml' >/dev/null 2>&1 || { echo "test-timing: TOOLCHAIN-MISSING — python3 + PyYAML not found" >&2; exit 2; }
fi

T="$(mktemp -d "${TMPDIR:-/tmp}/pfm-test-timing.XXXXXX")" || { echo "test-timing: mktemp failed" >&2; exit 2; }
trap 'rm -rf "$T"' EXIT

# check_fence_free — same guard as scripts/test-sweep.sh: refuse a --measure
# run while another pfm-dev fence container is already live. Skipped (not
# failed) when docker itself is not on PATH — true inside the fence
# container, which has no docker socket.
check_fence_free() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "test-timing: docker not on PATH — skipping the concurrency guard (expected inside the fence container itself)"
    return 0
  fi
  local running probe_rc
  if running="$(docker ps --filter 'ancestor=professor-pfm-dev' --format '{{.Names}}' 2>&1)"; then
    :
  else
    probe_rc=$?
    echo "TIMING-ERROR: docker ps probe failed (exit $probe_rc): $running" >&2
    return 2
  fi
  if [ -z "$running" ]; then
    if running="$(docker ps --format '{{.Names}}' 2>&1)"; then
      :
    else
      probe_rc=$?
      echo "TIMING-ERROR: docker ps fallback probe failed (exit $probe_rc): $running" >&2
      return 2
    fi
    running="$(printf '%s\n' "$running" | awk 'tolower($0) ~ /pfm-dev/')"
  fi
  if [ -n "$running" ]; then
    echo "test-timing: REFUSING to --measure — another fence run is already live: $running (a budget measured beside it would be contention, not a suite's real cost)" >&2
    return 1
  fi
  return 0
}

# The exact jq(1) program that turns one `go test -json` stream into the
# aggregate this script builds every TSV row from. `map` inside `map` reruns
# against the current group (jq's `.` inside a nested map is the OUTER map's
# element, i.e. one package's event array) — this is deliberate, not a bug:
# each derived value below is scoped to ONE package's events.
JQ_AGG='
  {
    packages: (
      group_by(.Package) | map(
        (map(select(.Test == null and (.Action=="pass" or .Action=="fail" or .Action=="skip")))) as $pkgsum |
        ($pkgsum[-1] // {}) as $summary |
        (map(select(.Test != null and (.Action=="pass" or .Action=="fail" or .Action=="skip")))) as $testfinals |
        ($testfinals | map(select((.Test|contains("/"))|not))) as $topfinals |
        (map(select(.Action=="pause" and .Test != null and ((.Test|contains("/"))|not))) | map(.Test) | unique) as $parallel |
        ($testfinals | sort_by(-(.Elapsed // 0)) | (.[0] // {})) as $slow |
        {
          package: (.[0].Package),
          terminal_summaries: ($pkgsum | length),
          wall_s: ($summary.Elapsed // null),
          test_elapsed_valid: (all($testfinals[]; (.Elapsed|type)=="number" and .Elapsed >= 0)),
          tests: ($topfinals | length),
          parallel_tests: ($parallel | length),
          slowest_test: ($slow.Test // "-"),
          slowest_s: ($slow.Elapsed // 0),
          status: ($summary.Action // "unknown")
        }
      )
    ),
    times: (map(.Time)),
    invalid_time_events: (map(select((.Time|type) != "string")) | length)
  }
'

# parse_agg <input-file-or-empty-for-stdin> <agg-out> — TIMING-UNREADABLE on
# anything jq cannot parse, or on a stream with no package summary in it at
# all (an enumerator that found nothing is an error here, never a silent
# empty pass — CLAUDE.md: absence and failure-to-look must read differently).
parse_agg() {
  local src="$1" agg="$2" raw="$T/raw.jsonl" clean="$T/clean.jsonl" jqerr="$T/jq.err" total kept
  if [ -n "$src" ] && [ "$src" != "-" ]; then
    cat -- "$src" > "$raw" 2>"$T/cat.err" || { echo "TIMING-UNREADABLE: cannot read $src" >&2; cat "$T/cat.err" >&2; return 2; }
  else
    if ! cat > "$raw"; then
      echo "TIMING-UNREADABLE: could not read the go test -json stream from stdin" >&2
      return 2
    fi
  fi
  total=$(awk 'NF {n++} END {print n+0}' "$raw")
  # Every nonblank line must be one complete JSON event. A stray print or
  # container/build chatter is not part of a trustworthy timing stream:
  # accepting a complete-looking subset could manufacture a green result.
  jq -R -c 'fromjson? // empty' "$raw" > "$clean" 2>"$jqerr"
  if [ $? -ne 0 ]; then
    echo "TIMING-UNREADABLE: jq could not read the stream at all" >&2
    sed 's/^/  /' "$jqerr" >&2
    return 2
  fi
  kept=$(wc -l < "$clean" | tr -d ' ')
  if [ "$kept" -lt "$total" ]; then
    echo "TIMING-UNREADABLE: $((total - kept)) non-JSON or malformed JSON line(s) in the stream" >&2
    return 2
  fi
  jq -s "$JQ_AGG" "$clean" > "$agg" 2>"$jqerr"
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "TIMING-UNREADABLE: jq could not aggregate the go test -json stream" >&2
    sed 's/^/  /' "$jqerr" >&2
    return 2
  fi
  local n
  if ! n=$(jq -r '.packages | length' "$agg" 2>"$jqerr"); then
    echo "TIMING-UNREADABLE: jq could not count package summaries" >&2
    sed 's/^/  /' "$jqerr" >&2
    return 2
  fi
  if [ "${n:-0}" -eq 0 ]; then
    echo "TIMING-UNREADABLE: 0 package summaries parsed — the input was not a go test -json stream, or the run produced none" >&2
    return 2
  fi
  if jq -e '
      (.times | type) != "array" or (.times | length) < 1 or .invalid_time_events > 0 or
      any(.packages[];
        (.package | type) != "string" or .package == "" or
        (.terminal_summaries | type) != "number" or .terminal_summaries != 1 or
        (.status != "pass" and .status != "fail" and .status != "skip") or
        (.wall_s | type) != "number" or .wall_s < 0 or
        (.test_elapsed_valid != true)
      )
    ' "$agg" >/dev/null; then
    echo "TIMING-INCOMPLETE: one or more packages has no terminal go test summary (or a valid timestamp/wall value)" >&2
    return 2
  fi
  return 0
}

# emit_tsv <agg> <out-file>
emit_tsv() {
  local agg="$1" out="$2"
  mkdir -p "$(dirname "$out")" || return 1
  {
    printf 'package\twall_s\ttests\tparallel_tests\tslowest_test\tslowest_s\tstatus\n'
    jq -r '.packages | sort_by(.package)[] | [.package, (.wall_s|tostring), (.tests|tostring), (.parallel_tests|tostring), .slowest_test, (.slowest_s|tostring), .status] | @tsv' "$agg"
    local wall pkgcount serial slowpkg slowwall overall
    if ! wall=$(python3 - "$agg" 2>"$T/time.err" <<'PY'
import datetime
import json
import sys

path = sys.argv[1]
with open(path) as fh:
    data = json.load(fh)

def parse(value, index):
    if not isinstance(value, str):
        raise SystemExit(f"timestamp {index} is not a string: {value!r}")
    try:
        parsed = datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise SystemExit(f"invalid RFC3339 timestamp {index} {value!r}: {exc}")
    if parsed.tzinfo is None:
        raise SystemExit(f"timestamp {index} {value!r} has no timezone")
    return parsed.timestamp()

times = data.get("times")
if not isinstance(times, list) or not times:
    raise SystemExit("the stream has no timestamps")
parsed = [parse(value, index) for index, value in enumerate(times)]
delta = max(parsed) - min(parsed)
print(f"{delta:.3f}")
PY
    ); then
      echo "TIMING-UNREADABLE: could not compute suite wall time" >&2
      sed 's/^/  /' "$T/time.err" >&2
      return 2
    fi
    pkgcount=$(jq -r '.packages | length' "$agg")
    serial=$(jq -r '([.packages[].wall_s] | add // 0)' "$agg")
    slowpkg=$(jq -r '(.packages | sort_by(-.wall_s) | .[0].package) // "-"' "$agg")
    slowwall=$(jq -r '(.packages | sort_by(-.wall_s) | .[0].wall_s) // 0' "$agg")
    overall=$(jq -r 'if any(.packages[]; .status == "fail" or (.status == "skip" and .tests > 0)) then "FAIL" else "PASS" end' "$agg")
    printf 'SUITE\t%s\t%s\t%s\t%s\t%s\t%s\n' "$wall" "$pkgcount" "$serial" "$slowpkg" "$slowwall" "$overall"
  } > "$out"
}

# yml_fields <yml-file> — prints "tolerance\tX", one "suite\tNAME\tWALL_S"
# line per Tier under `suites:` (unit, e2e, ...), and one
# "pkg\tNAME\tPATH\tBUDGET" line per package budgeted WITHIN that Tier —
# budgets are scoped per suite (an e2e run never sees the 63 unit packages,
# so its own completeness check must never require them). Errors (missing
# file, unparsable YAML) go to stderr and this returns non-zero — never a
# silent empty budget table, which --check would otherwise read as "nothing
# to fail".
yml_fields() {
  local yml="$1"
  [ -f "$yml" ] || { echo "TIMING-CONFIG-INVALID: budget file $yml missing" >&2; return 1; }
  python3 - "$yml" <<'PY'
import math
import sys

import yaml

path = sys.argv[1]

def fail(message):
    print(f"TIMING-CONFIG-INVALID: {path} {message}", file=sys.stderr)
    raise SystemExit(1)

try:
    with open(path) as fh:
        data = yaml.safe_load(fh) or {}
except Exception as exc:
    fail(f"could not parse YAML: {exc}")

if not isinstance(data, dict):
    fail("did not parse to a mapping")

def positive_number(value, field):
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        fail(f"{field} must be a finite positive number, got {value!r}")
    value = float(value)
    if not math.isfinite(value) or value <= 0:
        fail(f"{field} must be a finite positive number, got {value!r}")
    return value

if "tolerance" not in data:
    fail("is missing tolerance")
tol = positive_number(data["tolerance"], "tolerance")
print(f"tolerance\t{tol:g}")

suites = data.get("suites")
if not isinstance(suites, dict) or not suites:
    fail("suites: must be a nonempty mapping")
if any(not isinstance(name, str) or not name for name in suites):
    fail("suites contains an empty or non-string name")
for name in sorted(suites):
    entry = suites[name]
    if not isinstance(entry, dict):
        fail(f"suites.{name} must be a mapping")
    if "wall_s" not in entry:
        fail(f"suites.{name} is missing wall_s")
    wall = positive_number(entry["wall_s"], f"suites.{name}.wall_s")
    packages = entry.get("packages")
    if not isinstance(packages, dict) or not packages:
        fail(f"suites.{name}.packages must be a nonempty mapping")
    if any(not isinstance(pkg, str) or not pkg for pkg in packages):
        fail(f"suites.{name}.packages contains an empty or non-string package name")
    print(f"suite\t{name}\t{wall:g}")
    for pkg in sorted(packages):
        budget = positive_number(packages[pkg], f"suites.{name}.packages.{pkg}")
        print(f"pkg\t{name}\t{pkg}\t{budget:g}")
PY
}

# fail_report <timing-tsv> — prints every failing or skipped package (or a
# non-PASS SUITE row) to stderr and returns 1 if the run is not complete;
# returns 0 on a clean run. A broken suite gets NO timing verdict — checked
# before budgets are even loaded.
fail_report() {
  local timing="$1"
  awk -F'\t' '
    NR==1 { next }
    $1=="SUITE" { if ($7 != "PASS") { print "SUITE: " $7; n++ }; next }
    $7=="fail" || ($7=="skip" && ($3+0)>0) { print $1 " (" $7 ")"; n++ }
    END { exit (n>0) ? 0 : 1 }
  ' "$timing" > "$T/failed.list"
  if [ -s "$T/failed.list" ]; then
    printf 'TIMING TESTS-FAILED: %d package or suite row(s) failed/skipped — a broken run has no timing verdict, fix the tests first:\n' "$(wc -l < "$T/failed.list" | tr -d ' ')" >&2
    sed 's/^/  /' "$T/failed.list" >&2
    return 1
  fi
  return 0
}

# check_timing <timing-tsv> <budgets-tsv> <suite-name> — the awk(1) comparator.
# Deliberately POSIX awk (no gawk-only length(array)/asort): the fence
# image's /usr/bin/awk is not guaranteed to be gawk. <suite-name> selects
# which `suites:` entry (and ONLY that entry's own packages) gates this run —
# pfm/.testtiming.yml carries one budget block per Tier (unit, e2e, ...),
# since dev.sh checks each tier separately and an e2e run never sees the unit
# package set.
check_timing() {
  local timing="$1" budgets="$2" want="$3"
  awk -F'\t' -v OFS='\t' -v want="$want" '
    FNR==NR {
      if ($1=="tolerance") tol=$2
      else if ($1=="suite" && $2==want) suite_budget=$3
      else if ($1=="suite") suite_seen[$2]=1
      else if ($1=="pkg" && $2==want) { budget[$3]=$4; nbudget++ }
      next
    }
    FNR==1 { next }
    $1=="SUITE" { suite_wall=$2; next }
    {
      pkg=$1; wall=$2; measured++
      if (pkg in budget) {
        if (!(pkg in seen)) { seen[pkg]=1; seen_count++ }
        threshold = budget[pkg]*tol
        if (wall+0 > threshold+0) {
          pct = (wall-budget[pkg])/budget[pkg]*100
          printf "TIMING FAIL %s budget=%ss measured=%ss over=%.0f%%\n", pkg, budget[pkg], wall, pct > "/dev/stderr"
          bad++
        }
      } else {
        printf "TIMING UNBUDGETED %s measured=%ss\n", pkg, wall > "/dev/stderr"
        bad++
      }
    }
    END {
      full = 1
      for (p in budget) if (!(p in seen)) full = 0
      complete = (full && nbudget > 0 && suite_wall != "")
      if (!complete) {
        printf "TIMING-INCOMPLETE: suite %s is partial (%d/%d budgeted packages present, suite summary=%s) — no timing verdict\n", want, seen_count, nbudget, (suite_wall != "" ? "present" : "missing") > "/dev/stderr"
        bad++
      }
      if (complete) {
        if (suite_budget == "") {
          printf "TIMING SUITE-UNBUDGETED %s measured=%ss — no suites.%s in the budget file\n", want, suite_wall, want > "/dev/stderr"
          bad++
        } else {
          threshold = suite_budget*tol
          if (suite_wall+0 > threshold+0) {
            pct = (suite_wall-suite_budget)/suite_budget*100
            printf "TIMING FAIL SUITE(%s) budget=%ss measured=%ss over=%.0f%%\n", want, suite_budget, suite_wall, pct > "/dev/stderr"
            bad++
          } else {
            printf "TIMING: SUITE(%s) within budget (%.3fs <= %.3fs x%s)\n", want, suite_wall, suite_budget, tol
          }
        }
      }
      if (bad>0) { printf "TIMING: %d offender(s)\n", bad > "/dev/stderr"; exit 1 }
      printf "TIMING: %d package(s) measured, all within budget (tolerance x%s)\n", measured, tol
      exit 0
    }
  ' "$budgets" "$timing"
}

default_out() {
  local stamp; stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  echo "$REPO/tmp/timing/${SUITE_NAME}-${stamp}.tsv"
}

INPUT="${POSITIONAL[0]:-}"
if [ "$MODE" != measure ]; then
  AGG="$T/agg.json"
  parse_agg "$INPUT" "$AGG" || exit 2
  [ -n "$OUT" ] || OUT="$(default_out)"
  emit_tsv "$AGG" "$OUT" || { echo "test-timing: could not write $OUT" >&2; exit 2; }
  echo "test-timing: wrote $OUT"
fi

case "$MODE" in
  parse)
    exit 0
    ;;
  check)
    fail_report "$OUT" || exit 1
    BUDGETS="$T/budgets.tsv"
    yml_fields "$YML" > "$BUDGETS" || exit 2
    check_timing "$OUT" "$BUDGETS" "$SUITE_NAME"
    exit $?
    ;;
  measure)
    check_fence_free || exit $?
    # Parse each of the 3 runs to its own TSV.
    TSVS=()
    measure_failed=0
    i=0
    for f in "${MEASURE_FILES[@]}"; do
      i=$((i+1))
      agg="$T/agg-$i.json"
      parse_agg "$f" "$agg" || exit 2
      tsv="$T/run-$i.tsv"
      emit_tsv "$agg" "$tsv" || { echo "test-timing: could not write $tsv" >&2; exit 2; }
      if ! fail_report "$tsv"; then
        measure_failed=1
      fi
      TSVS+=("$tsv")
    done
    if [ "$measure_failed" -ne 0 ]; then
      echo "TIMING MEASURE-REFUSED: one or more captures contains failed tests; budgets were not changed" >&2
      exit 1
    fi
    MEDIANS="$T/medians.tsv"
    if ! python3 - "${TSVS[@]}" > "$MEDIANS" 2>"$T/measure.err" <<'PY'
import math
import statistics
import sys

HEADER = "package\twall_s\ttests\tparallel_tests\tslowest_test\tslowest_s\tstatus"

def fail(message):
    print(f"TIMING-INCOMPLETE: {message}", file=sys.stderr)
    raise SystemExit(1)

def finite_nonnegative(value, label):
    try:
        number = float(value)
    except ValueError:
        fail(f"{label} has a nonnumeric wall value {value!r}")
    if not math.isfinite(number) or number < 0:
        fail(f"{label} has an invalid wall value {value!r}")
    return number

def read_tsv(path):
    pkgs = {}
    suite_wall = None
    with open(path) as fh:
        lines = fh.read().splitlines()
    if not lines or lines[0] != HEADER:
        fail(f"{path} has no timing TSV header")
    for line_no, line in enumerate(lines[1:], 2):
        if not line:
            continue
        cols = line.split("\t")
        if cols[0] == "SUITE":
            if len(cols) != 7 or suite_wall is not None:
                fail(f"{path}:{line_no} has a duplicate or malformed SUITE row")
            suite_wall = finite_nonnegative(cols[1], f"{path}:{line_no}")
            continue
        if len(cols) != 7 or not cols[0]:
            fail(f"{path}:{line_no} has a malformed package row")
        if cols[0] in pkgs:
            fail(f"{path}:{line_no} repeats package {cols[0]}")
        pkgs[cols[0]] = finite_nonnegative(cols[1], f"{path}:{line_no}")
    if suite_wall is None:
        fail(f"{path} has no SUITE row")
    if not pkgs:
        fail(f"{path} has no package rows")
    return pkgs, suite_wall

runs = [read_tsv(p) for p in sys.argv[1:4]]
first_pkgs = set(runs[0][0])
for index, (pkgs, _) in enumerate(runs[1:], 2):
    current = set(pkgs)
    if current != first_pkgs:
        missing = sorted(first_pkgs - current)
        extra = sorted(current - first_pkgs)
        fail(f"capture {index} package set differs (missing={missing}, extra={extra})")

for pkg in sorted(first_pkgs):
    vals = [pkgs[pkg] for pkgs, _ in runs]
    print(f"PKG\t{pkg}\t{statistics.median(vals):.3f}")

suite_vals = [suite for _, suite in runs]
print(f"SUITE\t{statistics.median(suite_vals):.3f}")
PY
    then
      sed 's/^/test-timing: /' "$T/measure.err" >&2
      exit 1
    fi

    CEIL() { awk -v m="$1" 'BEGIN{c=int(m); if (c<m) c++; if (c<1) c=1; print c}'; }

    if [ ! -f "$YML" ]; then
      {
        echo "# pfm test timing budgets — ratchets DOWN only (same law as .arch/:"
        echo "# \`--measure\` locks in a shrink; raising a budget, or budgeting a brand-new"
        echo "# package, is a hand edit, named in the commit). Full design and the meaning"
        echo "# of every field: docs/dev/testing/timing.md."
        echo "#"
        echo "# tolerance: a measured wall may exceed a package's budget by up to this"
        echo "# fraction before \`scripts/test-timing.sh --check\` reports it FAIL — it"
        echo "# absorbs ordinary run-to-run noise on a shared fence, nothing more."
        echo "#"
        echo "# suites: one wall_s + packages block per Tier (unit ./..., e2e ./e2e/..."
        echo "# -tags e2e) — dev.sh checks each Tier separately, on its own package set."
        echo "#"
        echo "# A package the tree ships that its suite does not list is UNBUDGETED —"
        echo "# always red, never silently allowed."
        echo "tolerance: 1.25"
        suite_med=$(awk -F'\t' '$1=="SUITE"{print $2}' "$MEDIANS")
        echo "suites:"
        printf '  %s:\n' "$SUITE_NAME"
        printf '    wall_s: %s\n' "$(CEIL "${suite_med:-0}")"
        echo "    packages:"
        while IFS=$'\t' read -r tag pkg med; do
          [ "$tag" = PKG ] || continue
          printf '      %s: %s\n' "$pkg" "$(CEIL "$med")"
        done < "$MEDIANS"
      } > "$YML"
      echo "TIMING MEASURE: bootstrapped $YML from 3 runs (suite '$SUITE_NAME' only — bootstrap other suites into separate files, then combine their measured blocks explicitly)"
      exit 0
    fi

    BUDGETS="$T/budgets.tsv"
    yml_fields "$YML" > "$BUDGETS" || exit 2
    cur_suite=$(awk -F'\t' -v n="$SUITE_NAME" '$1=="suite" && $2==n {print $3}' "$BUDGETS")
    if [ -z "$cur_suite" ]; then
      echo "TIMING-CONFIG-INVALID: $YML has no suites.$SUITE_NAME entry to measure" >&2
      exit 2
    fi
    expected_pkgs="$T/expected-packages"
    measured_pkgs="$T/measured-packages"
    awk -F'\t' -v n="$SUITE_NAME" '$1=="pkg" && $2==n {print $3}' "$BUDGETS" | sort > "$expected_pkgs"
    awk -F'\t' '$1=="PKG" {print $2}' "$MEDIANS" | sort > "$measured_pkgs"
    suite_complete=1
    if [ -n "$(comm -3 "$expected_pkgs" "$measured_pkgs")" ]; then
      suite_complete=0
      echo "TIMING MEASURE: suite wall unchanged — capture package set is partial for suites.$SUITE_NAME" >&2
    fi
    NEW="$T/new-packages.tsv"
    : > "$NEW"
    lowered=0
    unchanged=0
    while IFS=$'\t' read -r tag pkg med; do
      [ "$tag" = PKG ] || continue
      cur=$(awk -F'\t' -v s="$SUITE_NAME" -v p="$pkg" '$1=="pkg" && $2==s && $3==p {print $4}' "$BUDGETS")
      [ -n "$cur" ] || continue # measured but not yet budgeted in this suite — never auto-added
      new=$(CEIL "$med")
      if awk -v n="$new" -v c="$cur" 'BEGIN{exit !(n<c)}'; then
        echo "TIMING MEASURE $pkg ${cur}s -> ${new}s"
        printf '%s\t%s\n' "$pkg" "$new" >> "$NEW"
        lowered=$((lowered+1))
      else
        printf '%s\t%s\n' "$pkg" "$cur" >> "$NEW"
        unchanged=$((unchanged+1))
      fi
    done < "$MEDIANS"
    # Every package already budgeted in THIS suite but NOT in this round's
    # medians (e.g. this measure ran a package subset) keeps its number.
    awk -F'\t' -v s="$SUITE_NAME" '$1=="pkg" && $2==s {print $3"\t"$4}' "$BUDGETS" | while IFS=$'\t' read -r pkg cur; do
      grep -q "^$pkg"$'\t' "$NEW" || printf '%s\t%s\n' "$pkg" "$cur" >> "$NEW"
    done

    suite_med=$(awk -F'\t' '$1=="SUITE"{print $2}' "$MEDIANS")
    new_suite="$cur_suite"
    if [ "$suite_complete" -eq 1 ] && [ -n "$suite_med" ] && awk -v n="$(CEIL "$suite_med")" -v c="$cur_suite" 'BEGIN{exit !(n<c)}'; then
      new_suite="$(CEIL "$suite_med")"
      echo "TIMING MEASURE SUITE(${SUITE_NAME}) ${cur_suite}s -> ${new_suite}s"
    fi

    # Every OTHER suite this round never remeasured (e.g. --suite e2e leaves
    # unit's whole block alone) is copied through byte-for-byte via its own
    # yml_fields rows — never zeroed, never dropped.
    OTHERSUITES="$T/other-suites"
    awk -F'\t' -v skip="$SUITE_NAME" '$1=="suite" && $2!=skip {print $2}' "$BUDGETS" | sort -u > "$OTHERSUITES"

    tol=$(awk -F'\t' '$1=="tolerance"{print $2}' "$BUDGETS")
    {
      grep '^#' "$YML"
      echo "tolerance: $tol"
      echo "suites:"
      printf '  %s:\n' "$SUITE_NAME"
      printf '    wall_s: %s\n' "$new_suite"
      echo "    packages:"
      sort "$NEW" | while IFS=$'\t' read -r pkg val; do
        printf '      %s: %s\n' "$pkg" "$val"
      done
      while IFS= read -r other; do
        [ -n "$other" ] || continue
        oval=$(awk -F'\t' -v n="$other" '$1=="suite" && $2==n {print $3}' "$BUDGETS")
        printf '  %s:\n' "$other"
        printf '    wall_s: %s\n' "$oval"
        echo "    packages:"
        awk -F'\t' -v s="$other" '$1=="pkg" && $2==s {print "      "$3": "$4}' "$BUDGETS"
      done < "$OTHERSUITES"
    } > "$T/new.yml"
    mv "$T/new.yml" "$YML"
    echo "TIMING MEASURE: $lowered lowered, $unchanged unchanged -> $YML"
    exit 0
    ;;
esac
