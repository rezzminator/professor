#!/usr/bin/env bash
# pfm concurrency sweet-spot RND runner — `make sweep`, the report at
# docs/dev/testing/concurrency-sweep.md, and the TESTFLAGS pin in
# pfm/Makefile. Full design: docs/dev/testing/concurrency-sweep.md.
#
# Two one-dimensional sweeps plus one shuffle validation leg, never the full
# matrix (a 5x4x2 matrix x 3 reps is hours of fence CPU for no extra knowledge):
#   1. -p in {1,2,4,8,NPROC} at -parallel=NPROC        (does -p even matter?)
#   2. -parallel in {1,4,8,16} at the p knee from (1)  (does -parallel?)
# Each -count=1. One test-cache warm-up run (`go clean -testcache` first) is
# recorded separately, outside the sweep table. It is not a cold build: the Go
# build cache remains warm. 3 repetitions per point, median.
#
# CPU-seconds vs wall: measured with bash's own `time` builtin (real/user/sys),
# not `/usr/bin/time -v` — the fence image (infra/pfm-dev.Dockerfile) does not
# install the `time` package, and the builtin needs nothing extra on the host
# either. CPU-seconds = user + sys.
#
# --plan   prints the matrix (both sweeps, the shuffle leg, the cache warm-up,
#          and the rep count) and exits — no docker, no go, safe with neither
#          on PATH.
# --preflight checks Docker's concurrency guard and the host load (< 4), then
#          exits without invoking Go. This is the host-side gate before the
#          fenced run.
# --run --label {fence|host} [--out FILE] [--reps N] [--json DIR] [--wait-quiet]
#          performs the actual measurement for ONE environment (this
#          invocation's own `go`/`$GOCMD`) and writes the raw per-rep TSV:
#          phase  p  parallel  rep  wall_s  cpu_s  status
#          With --json, each timed test stdout stream is retained under DIR;
#          three repetitions at a chosen point can feed test-timing.sh
#          --measure without another suite run.
#          --wait-quiet polls `uptime` immediately before each timed capture
#          for up to five minutes, sleeping 60 seconds between valid high-load
#          probes outside the timed command. Sustained load warns and proceeds;
#          it never holds the train indefinitely. Dated before/after snapshots,
#          including failed captures and high-load starts, go to OUT.load.log.
#          Refuses to start while another fence test run is live: checks
#          `docker ps` for a professor-pfm-dev / *pfm-dev* container. When
#          `docker` itself is not on PATH (true INSIDE the fence container,
#          which has no docker socket) the guard is skipped, not failed —
#          absence of the tool is not the same as an unchecked risk, since a
#          run reaching this point already passed the guard once, on the host
#          side, before `dev.sh iso run` launched it.
# --report FENCE_TSV [HOST_TSV] [--doc FILE] [--makefile FILE]
#          combines the raw TSVs into docs/dev/testing/concurrency-sweep.md
#          (the host TSV is optional) and pins pfm/Makefile's TESTFLAGS to the
#          independently measured fence knees.
set -uo pipefail
export LC_ALL=C

PFM="${PFM:-$(cd "$(dirname "$0")/.." && pwd)}"
REPO="${REPO:-$(cd "$PFM/.." && pwd)}"
PROJECT="$(basename "$REPO")"; PROJECT="${PROJECT#.}"
TMP_BASE="/tmp/$PROJECT"
GOCMD="${GOCMD:-go}"
REPS=3
REQUIRE_DOCKER=0
JSON_DIR=""
WAIT_QUIET=0
QUIET_MAX_WAITS="${SWEEP_QUIET_MAX_WAITS:-5}"
LOAD_LOG=""
SNAPSHOT_RAW=""
SNAPSHOT_LOAD=""

nproc_online() {
  if command -v nproc >/dev/null 2>&1; then nproc
  elif command -v getconf >/dev/null 2>&1; then getconf _NPROCESSORS_ONLN
  else return 1
  fi
}

usage() {
  cat >&2 <<'EOF'
usage: test-sweep.sh --plan
       test-sweep.sh --preflight
       test-sweep.sh --run --label {fence|host} [--out FILE] [--reps N] [--json DIR] [--wait-quiet]
       test-sweep.sh --report FENCE_TSV [HOST_TSV] [--doc FILE] [--makefile FILE]
EOF
  exit 2
}

MODE=""
LABEL=""
OUT=""
DOC="$REPO/docs/dev/testing/concurrency-sweep.md"
MAKEFILE="$PFM/Makefile"
POSITIONAL=()
while [ $# -gt 0 ]; do
  case "$1" in
    --plan) MODE="plan"; shift ;;
    --preflight) MODE="preflight"; shift ;;
    --run) MODE="run"; shift ;;
    --report) MODE="report"; shift ;;
    --label) LABEL="${2:?--label needs fence|host}"; shift 2 ;;
    --out) OUT="${2:?--out needs FILE}"; shift 2 ;;
    --reps) REPS="${2:?--reps needs N}"; shift 2 ;;
    --json) JSON_DIR="${2:?--json needs DIR}"; shift 2 ;;
    --wait-quiet) WAIT_QUIET=1; shift ;;
    --doc) DOC="${2:?--doc needs FILE}"; shift 2 ;;
    --makefile) MAKEFILE="${2:?--makefile needs FILE}"; shift 2 ;;
    -h|--help) usage ;;
    -*) echo "test-sweep: unknown flag $1" >&2; usage ;;
    *) POSITIONAL+=("$1"); shift ;;
  esac
done
[ -n "$MODE" ] || usage

case "$REPS" in
  ''|*[!0-9]*|0)
    echo "test-sweep: ERROR: --reps must be a positive integer, got [$REPS]" >&2
    exit 2
    ;;
esac
case "$QUIET_MAX_WAITS" in
  ''|*[!0-9]*)
    echo "test-sweep: ERROR: SWEEP_QUIET_MAX_WAITS must be a nonnegative integer, got [$QUIET_MAX_WAITS]" >&2
    exit 2
    ;;
esac

p_sweep_set() { # deduped "1 2 4 8 <nproc>" in ascending order
  local n="$1"
  printf '%s\n' 1 2 4 8 "$n" | sort -nu | tr '\n' ' ' | sed 's/ $//'
}

if [ "$MODE" = plan ]; then
  n="$(nproc_online)" || { echo "test-sweep: ERROR: could not determine online CPU count" >&2; exit 2; }
  case "$n" in ''|*[!0-9]*|0) echo "test-sweep: ERROR: invalid online CPU count [$n]" >&2; exit 2 ;; esac
  pset="$(p_sweep_set "$n")"
  echo "SWEEP PLAN"
  echo "  cache-warmup    1x      \$GOCMD clean -testcache && \$GOCMD test -count=1 ./...  (test cache only; not a cold build; recorded separately)"
  echo "  p-sweep         ${REPS}x each  p in {${pset}} parallel=${n}"
  echo "  parallel-sweep  ${REPS}x each  parallel in {1 4 8 16} at p=<p-knee-from-sweep-1>"
  echo "  shuffle         ${REPS}x each  -shuffle=on at the measured p/parallel pair"
  echo "  quiet: --wait-quiet polls load before each timed capture for at most 5 minutes (sleep outside measured time), then warns and proceeds; evidence goes to OUT.load.log"
  echo "  guard: refuses to start (--run) while a professor-pfm-dev / *pfm-dev* container is already running (docker ps); skipped when docker itself is absent (inside the fence)"
  exit 0
fi

load_average() {
  local raw
  if [ "$#" -eq 0 ]; then
    command -v uptime >/dev/null 2>&1 || return 1
    raw="$(uptime 2>&1)" || return 1
  elif [ "$#" -eq 1 ]; then
    raw="$1"
  else
    return 2
  fi
  printf '%s\n' "$raw" | parse_load_average
}

parse_load_average() {
  awk -F'load average[s]*: *' '
    BEGIN { found=0 }
    NF > 1 {
      value=$2
      gsub(/^[[:space:]]+/, "", value)
      split(value, fields, "[,[:space:]]+")
      if (fields[1] != "") { print fields[1]; found++ }
    }
    END { if (found != 1) exit 1 }
  '
}

valid_load() {
  awk -v host_load="$1" 'BEGIN {
    if (host_load !~ /^[0-9]+([.][0-9]+)?([eE][-+]?[0-9]+)?$/ || host_load + 0 != host_load + 0 || host_load + 0 < 0) exit 1
  }'
}

uptime_snapshot() {
  command -v uptime >/dev/null 2>&1 || {
    echo "SWEEP-ERROR: --wait-quiet requires uptime on PATH" >&2
    return 2
  }
  local raw="" rc
  if raw="$(uptime 2>&1)"; then
    printf '%s' "$raw"
  else
    rc=$?
    echo "SWEEP-ERROR: --wait-quiet uptime probe failed (exit $rc): $raw" >&2
    return 2
  fi
}

take_load_snapshot() {
  if ! SNAPSHOT_RAW="$(uptime_snapshot)"; then
    return 2
  fi
  if ! SNAPSHOT_LOAD="$(load_average "$SNAPSHOT_RAW")"; then
    echo "SWEEP-ERROR: --wait-quiet uptime output has no single load average" >&2
    return 2
  fi
  if ! valid_load "$SNAPSHOT_LOAD"; then
    echo "SWEEP-ERROR: --wait-quiet uptime reported an invalid load average [$SNAPSHOT_LOAD]" >&2
    return 2
  fi
}

record_load_snapshot() {
  local edge="$1" phase="$2" p="$3" parallel="$4" rep="$5" timestamp uptime_line
  if ! command -v date >/dev/null 2>&1; then
    echo "SWEEP-ERROR: --wait-quiet requires date to timestamp load evidence" >&2
    return 2
  fi
  if ! timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)" || [ -z "$timestamp" ]; then
    echo "SWEEP-ERROR: --wait-quiet could not timestamp load evidence" >&2
    return 2
  fi
  uptime_line="$(printf '%s' "$SNAPSHOT_RAW" | tr '\t\r\n' '   ')"
  if ! printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
      "$timestamp" "$edge" "$phase" "$p" "$parallel" "$rep" "$SNAPSHOT_LOAD" "$uptime_line" >> "$LOAD_LOG"; then
    echo "SWEEP-ERROR: --wait-quiet could not append load evidence to $LOAD_LOG" >&2
    return 2
  fi
}

wait_quiet_before() {
  [ "$WAIT_QUIET" -eq 1 ] || return 0
  local phase="$1" p="$2" parallel="$3" rep="$4" waits=0
  while :; do
    take_load_snapshot || return 2
    if awk -v host_load="$SNAPSHOT_LOAD" 'BEGIN { exit !(host_load + 0 < 4) }'; then
      record_load_snapshot before "$phase" "$p" "$parallel" "$rep" || return 2
      return 0
    fi
    if (( waits >= QUIET_MAX_WAITS )); then
      echo "test-sweep: WARNING: --wait-quiet load is $SNAPSHOT_LOAD after $QUIET_MAX_WAITS minute(s); proceeding with recorded high-load evidence before $phase p=$p parallel=$parallel rep=$rep" >&2
      record_load_snapshot before "$phase" "$p" "$parallel" "$rep" || return 2
      return 0
    fi
    echo "test-sweep: --wait-quiet load is $SNAPSHOT_LOAD (need < 4); sleeping 60s before $phase p=$p parallel=$parallel rep=$rep"
    if ! sleep 60; then
      echo "SWEEP-ERROR: --wait-quiet sleep 60 failed" >&2
      return 2
    fi
    waits=$((waits + 1))
  done
}

record_load_after() {
  [ "$WAIT_QUIET" -eq 1 ] || return 0
  local phase="$1" p="$2" parallel="$3" rep="$4"
  take_load_snapshot || return 2
  record_load_snapshot after "$phase" "$p" "$parallel" "$rep"
}

check_fence_free() {
  if ! command -v docker >/dev/null 2>&1; then
    if [ "$REQUIRE_DOCKER" -eq 1 ]; then
      echo "SWEEP-ERROR: docker is required for the host preflight but is not on PATH" >&2
      return 2
    fi
    echo "test-sweep: docker not on PATH — skipping the concurrency guard (expected inside the fence container itself)"
    return 0
  fi
  local running probe_rc
  if running="$(docker ps --filter 'ancestor=professor-pfm-dev' --format '{{.Names}}' 2>&1)"; then
    :
  else
    probe_rc=$?
    echo "SWEEP-ERROR: docker ps probe failed (exit $probe_rc): $running" >&2
    return 2
  fi
  if [ -z "$running" ]; then
    if running="$(docker ps --format '{{.Names}}' 2>&1)"; then
      :
    else
      probe_rc=$?
      echo "SWEEP-ERROR: docker ps fallback probe failed (exit $probe_rc): $running" >&2
      return 2
    fi
    running="$(printf '%s\n' "$running" | awk 'tolower($0) ~ /pfm-dev/')"
  fi
  if [ -n "$running" ]; then
    echo "test-sweep: REFUSING to start — another fence run is already live: $running" >&2
    return 1
  fi
  return 0
}

if [ "$WAIT_QUIET" -eq 1 ] && [ "$MODE" != run ]; then
  echo "SWEEP-ERROR: --wait-quiet is only valid with --run" >&2
  exit 2
fi

if [ "$MODE" = preflight ]; then
  [ "${#POSITIONAL[@]}" -eq 0 ] || { echo "SWEEP-ERROR: --preflight takes no positional arguments" >&2; exit 2; }
  REQUIRE_DOCKER=1
  check_fence_free || exit $?
  if ! load="$(load_average)"; then
    echo "SWEEP-ERROR: could not read the host load average" >&2
    exit 2
  fi
  if ! valid_load "$load"; then
    echo "SWEEP-ERROR: host load average is unreadable [$load]" >&2
    exit 2
  fi
  if ! awk -v host_load="$load" 'BEGIN { exit !(host_load + 0 < 4) }'; then
    echo "SWEEP-ERROR: host load average is $load (must be below 4 before a fence run)" >&2
    exit 1
  fi
  echo "test-sweep: preflight passed — Docker is reachable and host load average is $load (< 4)"
  exit 0
fi

# time_cmd <label-fields...> -- <cmd...>  → appends one TSV row to $ROWFILE
# using bash's own `time` builtin (real/user/sys — CPU-seconds = user+sys).
time_cmd() {
  local phase="$1" p="$2" parallel="$3" rep="$4"; shift 4
  local tfile status=PASS rc real="" user="" sys="" cpu
  if [ "${1:-}" = "--" ]; then shift; fi
  if [ "$#" -eq 0 ]; then
    echo "SWEEP-ERROR: $phase p=$p parallel=$parallel rep=$rep has no command" >&2
    return 1
  fi
  if ! wait_quiet_before "$phase" "$p" "$parallel" "$rep"; then
    return 2
  fi
  if ! tfile="$(mktemp "${TMPDIR:-/tmp}/pfm-sweep-time.XXXXXX")"; then
    echo "SWEEP-ERROR: could not create a timing capture for $phase p=$p parallel=$parallel rep=$rep" >&2
    return 1
  fi
  (
    TIMEFORMAT='%R %U %S'
    time "$@" 2>&3
  ) 3>&2 2>"$tfile"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    status=FAIL
  fi
  if ! record_load_after "$phase" "$p" "$parallel" "$rep"; then
    status=ERROR
  fi
  if ! read -r real user sys < "$tfile"; then
    echo "SWEEP-ERROR: $phase p=$p parallel=$parallel rep=$rep produced no timing record" >&2
    status=ERROR
  elif ! awk -v r="$real" -v u="$user" -v s="$sys" 'BEGIN {
      for (i = 1; i <= 3; i++) {
        value = (i == 1 ? r : (i == 2 ? u : s))
        if (value !~ /^[0-9]+([.][0-9]+)?([eE][-+]?[0-9]+)?$/ || value + 0 < 0) exit 1
      }
    }' </dev/null; then
    echo "SWEEP-ERROR: $phase p=$p parallel=$parallel rep=$rep produced invalid timing data" >&2
    status=ERROR
  fi
  rm -f "$tfile"
  if [ -z "${real:-}" ] || [ -z "${user:-}" ] || [ -z "${sys:-}" ]; then
    real=0; user=0; sys=0
  fi
  cpu="$(awk -v u="$user" -v s="$sys" 'BEGIN{printf "%.3f", u+s}')"
  if ! printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$phase" "$p" "$parallel" "$rep" "$real" "$cpu" "$status" >> "$ROWFILE"; then
    echo "SWEEP-ERROR: could not append $phase p=$p parallel=$parallel rep=$rep to $ROWFILE" >&2
    return 1
  fi
  echo "test-sweep: $phase p=$p parallel=$parallel rep=$rep wall=${real}s cpu=${cpu}s $status"
  if [ "$status" = PASS ]; then
    return 0
  fi
  return 1
}

# median_of <values...> — the middle of N sorted values (N odd: the exact
# middle; N even: the lower of the two middle values — never interpolated,
# so the result is always one of the actual measurements). Works for any N,
# not just the spec's default 3 — --reps can be pinned lower for a
# time-boxed RND pass, and a hardcoded "exactly 3" would silently drop every
# point instead of reporting one.
median_of() {
  local n=$# mid
  mid=$(( (n + 1) / 2 ))
  printf '%s\n' "$@" | sort -n | sed -n "${mid}p"
}

# median_column <tsv> — reads one value per line from stdin/file and prints
# the median of however many rows are actually there (see median_of).
median_column() {
  local vals; vals=($(cat "$1"))
  [ "${#vals[@]}" -gt 0 ] || return 1
  median_of "${vals[@]}"
}

if [ "$MODE" = run ]; then
  case "$LABEL" in fence|host) ;; *) echo "test-sweep: --label must be fence or host" >&2; usage ;; esac
  check_fence_free || exit $?
  command -v "$GOCMD" >/dev/null 2>&1 || { echo "test-sweep: TOOLCHAIN-MISSING — \$GOCMD ($GOCMD) not found" >&2; exit 2; }
  [ -n "$OUT" ] || OUT="$TMP_BASE/timing/sweep-${LABEL}-$(date -u +%Y%m%dT%H%M%SZ).tsv"
  mkdir -p "$(dirname "$OUT")" || { echo "SWEEP-ERROR: could not create output directory for $OUT" >&2; exit 2; }
  ROWFILE="$OUT"
  printf 'phase\tp\tparallel\trep\twall_s\tcpu_s\tstatus\n' > "$ROWFILE" || { echo "SWEEP-ERROR: could not write TSV header to $ROWFILE" >&2; exit 2; }
  if [ "$WAIT_QUIET" -eq 1 ]; then
    LOAD_LOG="$OUT.load.log"
    printf 'timestamp\tedge\tphase\tp\tparallel\trep\tload_average\tuptime\n' > "$LOAD_LOG" || { echo "SWEEP-ERROR: could not write load evidence header to $LOAD_LOG" >&2; exit 2; }
  fi

  go_command() { ( cd "$PFM" && "$GOCMD" "$@" ); }
  go_test_command() {
    local json="$1"; shift
    if [ -n "$JSON_DIR" ]; then
      ( cd "$PFM" && "$GOCMD" test -json "$@" ) | tee "$json"
    else
      go_command test "$@"
    fi
  }
  if [ -n "$JSON_DIR" ]; then
    command -v tee >/dev/null 2>&1 || { echo "SWEEP-ERROR: TOOLCHAIN-MISSING — tee is required for --json" >&2; exit 2; }
    mkdir -p "$JSON_DIR" || { echo "SWEEP-ERROR: could not create JSON capture directory $JSON_DIR" >&2; exit 2; }
  fi
  if ! go_command clean -testcache; then
    echo "SWEEP-ERROR: go clean -testcache failed; no sweep was recorded" >&2
    exit 1
  fi
  n="$(nproc_online)" || { echo "SWEEP-ERROR: could not determine online CPU count" >&2; exit 2; }
  case "$n" in ''|*[!0-9]*|0) echo "SWEEP-ERROR: invalid online CPU count [$n]" >&2; exit 2 ;; esac
  time_cmd cache-warmup - - 1 -- go_test_command "${JSON_DIR:+$JSON_DIR/cache-warmup-p--parallel--rep1.json}" -count=1 ./... || { rc=$?; echo "SWEEP-ERROR: cache warm-up failed; no sweep verdict" >&2; exit "$rc"; }

  for p in $(p_sweep_set "$n"); do
    for rep in $(seq 1 "$REPS"); do
      time_cmd p-sweep "$p" "$n" "$rep" -- go_test_command "${JSON_DIR:+$JSON_DIR/p-sweep-p${p}-parallel${n}-rep${rep}.json}" -count=1 -p "$p" -parallel "$n" ./... || { rc=$?; echo "SWEEP-ERROR: p-sweep failed; no sweep verdict" >&2; exit "$rc"; }
    done
  done

  p_knee="" prev_wall="" prev_p=""
  for p in $(p_sweep_set "$n"); do
    vals=($(awk -F'\t' -v p="$p" '$1=="p-sweep" && $2==p {print $5}' "$ROWFILE"))
    [ "${#vals[@]}" -eq "$REPS" ] || { echo "SWEEP-ERROR: p-sweep is incomplete for p=$p" >&2; exit 2; }
    med="$(median_of "${vals[@]}")"
    if [ -n "$prev_wall" ] && [ -z "$p_knee" ] && awk -v prev="$prev_wall" -v m="$med" 'BEGIN{if (prev<=0) exit 0; exit !(((prev-m)/prev)<0.10)}'; then
      p_knee="$prev_p"
    fi
    prev_wall="$med"; prev_p="$p"
  done
  [ -n "$p_knee" ] || p_knee="$prev_p"
  [ -n "$p_knee" ] || { echo "SWEEP-ERROR: p-sweep produced no knee" >&2; exit 2; }
  echo "test-sweep: p knee from sweep 1 = $p_knee (second sweep will measure this p)"

  for par in 1 4 8 16; do
    for rep in $(seq 1 "$REPS"); do
      time_cmd parallel-sweep "$p_knee" "$par" "$rep" -- go_test_command "${JSON_DIR:+$JSON_DIR/parallel-sweep-p${p_knee}-parallel${par}-rep${rep}.json}" -count=1 -p "$p_knee" -parallel "$par" ./... || { rc=$?; echo "SWEEP-ERROR: parallel-sweep failed; no sweep verdict" >&2; exit "$rc"; }
    done
  done

  parallel_knee=""; prev_wall=""; prev_parallel=""
  for par in 1 4 8 16; do
    vals=($(awk -F'\t' -v par="$par" '$1=="parallel-sweep" && $3==par {print $5}' "$ROWFILE"))
    [ "${#vals[@]}" -eq "$REPS" ] || { echo "SWEEP-ERROR: parallel-sweep is incomplete for parallel=$par" >&2; exit 2; }
    med="$(median_of "${vals[@]}")"
    if [ -n "$prev_wall" ] && [ -z "$parallel_knee" ] && awk -v prev="$prev_wall" -v m="$med" 'BEGIN{if (prev<=0) exit 0; exit !(((prev-m)/prev)<0.10)}'; then
      parallel_knee="$prev_parallel"
    fi
    prev_wall="$med"; prev_parallel="$par"
  done
  [ -n "$parallel_knee" ] || parallel_knee="$prev_parallel"
  [ -n "$parallel_knee" ] || { echo "SWEEP-ERROR: parallel-sweep produced no knee" >&2; exit 2; }
  echo "test-sweep: parallel knee from sweep 2 = $parallel_knee (at p=$p_knee)"

  for rep in $(seq 1 "$REPS"); do
    time_cmd shuffle "$p_knee" "$parallel_knee" "$rep" -- go_test_command "${JSON_DIR:+$JSON_DIR/shuffle-p${p_knee}-parallel${parallel_knee}-rep${rep}.json}" -count=1 -p "$p_knee" -parallel "$parallel_knee" -shuffle=on ./... || { rc=$?; echo "SWEEP-ERROR: shuffle validation failed; no sweep verdict" >&2; exit "$rc"; }
  done
  echo "test-sweep: wrote $ROWFILE"
  exit 0
fi

if [ "$MODE" = report ]; then
  [ "${#POSITIONAL[@]}" -ge 1 ] && [ "${#POSITIONAL[@]}" -le 2 ] || { echo "SWEEP-ERROR: --report needs FENCE_TSV and optional HOST_TSV" >&2; usage; }
  FENCE_TSV="${POSITIONAL[0]}"
  HOST_TSV="${POSITIONAL[1]:-}"

  validate_tsv() {
    local tsv="$1" env="$2"
    [ -s "$tsv" ] || { echo "SWEEP-ERROR: $env report $tsv is missing or empty" >&2; return 2; }
    awk -F'\t' -v env="$env" '
      BEGIN { header="phase\tp\tparallel\trep\twall_s\tcpu_s\tstatus"; bad=0 }
      FNR==1 { if ($0 != header) { printf "SWEEP-ERROR: %s report has the wrong header\n", env > "/dev/stderr"; bad=1 } ; next }
      NF==0 { next }
      {
        if (NF != 7) { printf "SWEEP-ERROR: %s report line %d has %d fields\n", env, FNR, NF > "/dev/stderr"; bad=1; next }
        phase=$1
        if (phase != "cache-warmup" && phase != "p-sweep" && phase != "parallel-sweep" && phase != "shuffle") {
          printf "SWEEP-ERROR: %s report line %d has unknown phase %s\n", env, FNR, phase > "/dev/stderr"; bad=1; next
        }
        if (phase == "cache-warmup") {
          if ($2 != "-" || $3 != "-") { printf "SWEEP-ERROR: %s cache-warmup row has invalid p/parallel\n", env > "/dev/stderr"; bad=1 }
          if ($4 != 1) { printf "SWEEP-ERROR: %s cache-warmup row must have repetition 1\n", env > "/dev/stderr"; bad=1 }
          warmup++
        } else {
          if ($2 !~ /^[1-9][0-9]*$/ || $3 !~ /^[1-9][0-9]*$/) { printf "SWEEP-ERROR: %s report line %d has invalid p/parallel\n", env, FNR > "/dev/stderr"; bad=1 }
          if (phase == "p-sweep") {
            pcount[$2]++
            if (!($2 in ppoints)) p_point_count++
            ppoints[$2]=1
            if (p_parallel != "" && p_parallel != $3) { printf "SWEEP-ERROR: %s first sweep uses more than one parallel value\n", env > "/dev/stderr"; bad=1 }
            p_parallel=$3
          }
          if (phase == "parallel-sweep") {
            parcount[$3]++; parpoints[$3]=1
            if (!($3 in parpoints_seen)) par_point_count++
            parpoints_seen[$3]=1
            if (parallel_p != "" && parallel_p != $2) { printf "SWEEP-ERROR: %s second sweep uses more than one p\n", env > "/dev/stderr"; bad=1 }
            parallel_p=$2
          }
          if (phase == "shuffle") shuffle_count++
        }
        if ($4 !~ /^[1-9][0-9]*$/) { printf "SWEEP-ERROR: %s report line %d has invalid repetition\n", env, FNR > "/dev/stderr"; bad=1 }
        if ($5 !~ /^[0-9]+([.][0-9]+)?([eE][-+]?[0-9]+)?$/ || $6 !~ /^[0-9]+([.][0-9]+)?([eE][-+]?[0-9]+)?$/ || $5+0 != $5+0 || $6+0 != $6+0 || $5+0 < 0 || $6+0 < 0) {
          printf "SWEEP-ERROR: %s report line %d has invalid wall/cpu values\n", env, FNR > "/dev/stderr"; bad=1
        }
        if ($7 != "PASS") { printf "SWEEP-ERROR: %s report line %d has status %s (only PASS is reportable)\n", env, FNR, $7 > "/dev/stderr"; bad=1 }
        key=phase SUBSEP $2 SUBSEP $3 SUBSEP $4
        if (seen[key]++) { printf "SWEEP-ERROR: %s report line %d repeats a phase/point/repetition\n", env, FNR > "/dev/stderr"; bad=1 }
        if (phase != "cache-warmup") {
          point=phase SUBSEP $2 SUBSEP $3
          points[point]++
          if (!(point in minrep) || $4 < minrep[point]) minrep[point]=$4
          if (!(point in maxrep) || $4 > maxrep[point]) maxrep[point]=$4
        }
      }
      END {
        if (warmup != 1) { printf "SWEEP-ERROR: %s report needs exactly one cache-warmup row\n", env > "/dev/stderr"; bad=1 }
        if (p_point_count < 1) { printf "SWEEP-ERROR: %s report has no p-sweep rows\n", env > "/dev/stderr"; bad=1 }
        if (ppoints[1] != 1 || ppoints[2] != 1 || ppoints[4] != 1 || ppoints[8] != 1) { printf "SWEEP-ERROR: %s report is missing one of p points {1,2,4,8}\n", env > "/dev/stderr"; bad=1 }
        if (p_parallel == "" || ppoints[p_parallel] != 1) { printf "SWEEP-ERROR: %s report is missing the p point matching first-sweep parallel=%s\n", env, p_parallel > "/dev/stderr"; bad=1 }
        if (parpoints[1] != 1 || parpoints[4] != 1 || parpoints[8] != 1 || parpoints[16] != 1) { printf "SWEEP-ERROR: %s report is missing one of parallel points {1,4,8,16}\n", env > "/dev/stderr"; bad=1 }
        if (shuffle_count < 1) { printf "SWEEP-ERROR: %s report has no shuffle validation rows\n", env > "/dev/stderr"; bad=1 }
        expected=0
        for (point in points) {
          if (minrep[point] != 1 || maxrep[point] != points[point]) { printf "SWEEP-ERROR: %s report has a repetition gap at %s\n", env, point > "/dev/stderr"; bad=1 }
          if (expected == 0) expected=points[point]
          else if (points[point] != expected) { printf "SWEEP-ERROR: %s report has inconsistent repetition counts\n", env > "/dev/stderr"; bad=1 }
        }
        exit (bad ? 2 : 0)
      }
    ' "$tsv"
  }

  [ -f "$FENCE_TSV" ] || { echo "SWEEP-ERROR: $FENCE_TSV not found" >&2; exit 2; }
  validate_tsv "$FENCE_TSV" fence || exit 2
  if [ -n "$HOST_TSV" ]; then
    [ -f "$HOST_TSV" ] || { echo "SWEEP-ERROR: $HOST_TSV not found" >&2; exit 2; }
    validate_tsv "$HOST_TSV" host || exit 2
  fi
  [ -f "$MAKEFILE" ] || { echo "SWEEP-ERROR: Makefile $MAKEFILE not found; cannot pin measured knees" >&2; exit 2; }
  grep -q '^TESTFLAGS' "$MAKEFILE" || { echo "SWEEP-ERROR: $MAKEFILE has no TESTFLAGS line to pin measured knees" >&2; exit 2; }

  # table_and_knee <tsv> <env-label> writes its independent p and parallel
  # knees into REPORT_KNEE_FILE, and fails if any validated point is absent.
  table_and_knee() {
    local tsv="$1" env="$2" T; T="$(mktemp -d "${TMPDIR:-/tmp}/pfm-sweep-report.XXXXXX")"
    echo "#### $env"
    echo
    echo "cache warm-up run (test cache only; not a cold build):"
    awk -F'\t' '$1=="cache-warmup"{printf "- wall %ss, cpu %ss, %s\n", $5, $6, $7}' "$tsv"
    echo
    echo "**Sweep 1 — \`-p\` (parallel packages), \`-parallel\`=GOMAXPROCS**"
    echo
    echo "| p | median wall_s | median cpu_s | gain vs prev |"
    echo "| --- | --- | --- | --- |"
    local prev="" prevp="" knee=""
    for p in $(awk -F'\t' '$1=="p-sweep"{print $2}' "$tsv" | sort -nu); do
      awk -F'\t' -v p="$p" '$1=="p-sweep" && $2==p {print $5}' "$tsv" > "$T/wraw"
      awk -F'\t' -v p="$p" '$1=="p-sweep" && $2==p {print $6}' "$tsv" > "$T/craw"
      if ! w="$(median_column "$T/wraw")" || ! c="$(median_column "$T/craw")"; then
        echo "SWEEP-ERROR: $env has no complete p-sweep measurements for p=$p" >&2
        rm -rf "$T"
        return 2
      fi
      gain="-"
      if [ -n "$prev" ]; then
        gain="$(awk -v prev="$prev" -v w="$w" 'BEGIN{if (prev>0) printf "%.1f%%", (prev-w)/prev*100; else printf "n/a"}')"
        if [ -z "$knee" ] && awk -v prev="$prev" -v w="$w" 'BEGIN{if (prev<=0) exit 0; exit !(((prev-w)/prev)<0.10)}'; then knee="$prevp"; fi
      fi
      printf '| %s | %s | %s | %s |\n' "$p" "$w" "$c" "$gain"
      prev="$w"; prevp="$p"
    done
    [ -n "$prevp" ] || { echo "SWEEP-ERROR: $env has no p-sweep knee" >&2; rm -rf "$T"; return 2; }
    [ -n "$knee" ] || knee="$prevp"
    echo
    echo "Knee (first \`-p\` where the next step gains < 10%): **p=$knee**"
    echo
    echo "**Sweep 2 — \`-parallel\` (tests in flight per package) at measured \`-p=$knee\`**"
    echo
    echo "| parallel | median wall_s | median cpu_s | gain vs prev |"
    echo "| --- | --- | --- | --- |"
    local parprev="" parprevp="" parknee=""
    for par in 1 4 8 16; do
      awk -F'\t' -v par="$par" '$1=="parallel-sweep" && $3==par {print $5}' "$tsv" > "$T/wraw"
      awk -F'\t' -v par="$par" '$1=="parallel-sweep" && $3==par {print $6}' "$tsv" > "$T/craw"
      if ! w="$(median_column "$T/wraw")" || ! c="$(median_column "$T/craw")"; then
        echo "SWEEP-ERROR: $env has no complete parallel-sweep measurements for parallel=$par" >&2
        rm -rf "$T"
        return 2
      fi
      gain="-"
      if [ -n "$parprev" ]; then
        gain="$(awk -v prev="$parprev" -v w="$w" 'BEGIN{if (prev>0) printf "%.1f%%", (prev-w)/prev*100; else printf "n/a"}')"
        if [ -z "$parknee" ] && awk -v prev="$parprev" -v w="$w" 'BEGIN{if (prev<=0) exit 0; exit !(((prev-w)/prev)<0.10)}'; then parknee="$parprevp"; fi
      fi
      printf '| %s | %s | %s | %s |\n' "$par" "$w" "$c" "$gain"
      parprev="$w"; parprevp="$par"
    done
    second_p="$(awk -F'\t' '$1=="parallel-sweep"{print $2}' "$tsv" | sort -nu | tr '\n' ' ' | sed 's/ $//')"
    if [ "$second_p" != "$knee" ]; then
      echo "SWEEP-ERROR: $env second sweep was measured at p=$second_p, expected the p knee $knee" >&2
      rm -rf "$T"
      return 2
    fi
    [ -n "$parknee" ] || parknee="$parprevp"
    [ -n "$parknee" ] || { echo "SWEEP-ERROR: $env has no parallel-sweep knee" >&2; rm -rf "$T"; return 2; }
    echo
    echo "Parallel knee (first \`-parallel\` where the next step gains < 10%): **parallel=$parknee**"
    echo
    shuffle_p="$(awk -F'\t' '$1=="shuffle"{print $2}' "$tsv" | sort -nu | tr '\n' ' ' | sed 's/ $//')"
    shuffle_parallel="$(awk -F'\t' '$1=="shuffle"{print $3}' "$tsv" | sort -nu | tr '\n' ' ' | sed 's/ $//')"
    if [ "$shuffle_p" != "$knee" ] || [ "$shuffle_parallel" != "$parknee" ]; then
      echo "SWEEP-ERROR: $env shuffle validation was recorded at p=$shuffle_p parallel=$shuffle_parallel, expected p=$knee parallel=$parknee" >&2
      rm -rf "$T"
      return 2
    fi
    awk -F'\t' '$1=="shuffle"{print $5}' "$tsv" > "$T/sraw"
    awk -F'\t' '$1=="shuffle"{print $6}' "$tsv" > "$T/scraw"
    if ! shuffle_w="$(median_column "$T/sraw")"; then
      echo "SWEEP-ERROR: $env shuffle wall measurements could not be read" >&2
      rm -rf "$T"
      return 2
    fi
    if ! shuffle_c="$(median_column "$T/scraw")"; then
      echo "SWEEP-ERROR: $env shuffle CPU measurements could not be read" >&2
      rm -rf "$T"
      return 2
    fi
    [ -n "$shuffle_w" ] && [ -n "$shuffle_c" ] || { echo "SWEEP-ERROR: $env has no shuffle measurements" >&2; rm -rf "$T"; return 2; }
    echo "Shuffle validation at measured \`-p=$knee -parallel=$parknee\`: wall ${shuffle_w}s, cpu ${shuffle_c}s, PASS"
    echo "$knee" > "${REPORT_KNEE_FILE}.${env}.p"
    echo "$parknee" > "${REPORT_KNEE_FILE}.${env}.parallel"
    rm -rf "$T"
  }

  RT="$(mktemp -d "${TMPDIR:-/tmp}/pfm-sweep-knee.XXXXXX")"
  REPORT_KNEE_FILE="$RT/knee"
  BODY="$(mktemp "${TMPDIR:-/tmp}/pfm-sweep-report-body.XXXXXX")"
  if ! table_and_knee "$FENCE_TSV" fence > "$BODY"; then
    rm -rf "$RT"; rm -f "$BODY"
    exit 2
  fi
  if [ -n "$HOST_TSV" ]; then
    if ! table_and_knee "$HOST_TSV" host >> "$BODY"; then
      rm -rf "$RT"; rm -f "$BODY"
      exit 2
    fi
  else
    {
      echo
      echo "#### host"
      echo
      echo "Host measurement: not run (fence-only report)."
    } >> "$BODY"
  fi
  if ! fence_p="$(cat "$RT/knee.fence.p")"; then
    echo "SWEEP-ERROR: fence report did not write its p knee" >&2
    rm -rf "$RT"; rm -f "$BODY"
    exit 2
  fi
  if ! fence_parallel="$(cat "$RT/knee.fence.parallel")"; then
    echo "SWEEP-ERROR: fence report did not write its parallel knee" >&2
    rm -rf "$RT"; rm -f "$BODY"
    exit 2
  fi
  if [ -z "$fence_p" ] || [ -z "$fence_parallel" ]; then
    echo "SWEEP-ERROR: fence report did not produce both measured knees" >&2
    rm -rf "$RT"; rm -f "$BODY"
    exit 2
  fi
  DOC_TMP="$(mktemp "${DOC}.XXXXXX")" || { echo "SWEEP-ERROR: could not create temporary report beside $DOC" >&2; rm -rf "$RT"; rm -f "$BODY"; exit 2; }
  {
    echo "# Concurrency sweep — the Tier U sweet spot"
    echo
    echo "Generated by \`pfm/scripts/test-sweep.sh --report\`. Design and the law"
    echo "behind the two 1-D sweeps plus shuffle validation: this script's own header."
    echo
    cat "$BODY"
  } > "$DOC_TMP"
  MAKE_TMP="$(mktemp "${MAKEFILE}.XXXXXX")" || { echo "SWEEP-ERROR: could not create temporary Makefile beside $MAKEFILE" >&2; rm -rf "$RT"; rm -f "$BODY" "$DOC_TMP"; exit 2; }
  if ! cp -p "$MAKEFILE" "$MAKE_TMP"; then
    echo "SWEEP-ERROR: could not prepare a temporary Makefile beside $MAKEFILE" >&2
    rm -rf "$RT"; rm -f "$BODY" "$DOC_TMP" "$MAKE_TMP"
    exit 2
  fi
  if ! sed -i.bak "s|^TESTFLAGS.*|TESTFLAGS ?= -p $fence_p -parallel $fence_parallel|" "$MAKE_TMP"; then
    echo "SWEEP-ERROR: could not pin measured knees in temporary Makefile" >&2
    rm -rf "$RT"; rm -f "$BODY" "$DOC_TMP" "$MAKE_TMP" "$MAKE_TMP.bak"
    exit 2
  fi
  rm -f "$MAKE_TMP.bak" || { echo "SWEEP-ERROR: could not remove temporary Makefile backup" >&2; rm -rf "$RT"; rm -f "$BODY" "$DOC_TMP" "$MAKE_TMP"; exit 2; }
  if ! mv "$MAKE_TMP" "$MAKEFILE"; then
    echo "SWEEP-ERROR: could not install pinned Makefile $MAKEFILE" >&2
    rm -rf "$RT"; rm -f "$BODY" "$DOC_TMP" "$MAKE_TMP"
    exit 2
  fi
  if ! mv "$DOC_TMP" "$DOC"; then
    echo "SWEEP-ERROR: could not install report $DOC" >&2
    rm -rf "$RT"; rm -f "$BODY" "$DOC_TMP"
    exit 2
  fi
  rm -rf "$RT"; rm -f "$BODY"
  echo "test-sweep: wrote $DOC, pinned TESTFLAGS ?= -p $fence_p -parallel $fence_parallel in $MAKEFILE"
  exit 0
fi
