#!/usr/bin/env bash
# gate-history.sh: the permanent, append-only ledger of every real gate run, and
# its report of medians, regressions and verdict flips, so a trend shows without
# re-measuring. Runs on the host (after a gate) and in the fence (its suite).
#
#   gate-history.sh ingest <timing-base>... [--host-load-now] [--project P]
#   gate-history.sh report [--last N] [--project P]        (N defaults to 20)
#
# A timing base is the directory holding run.* gate runs (/tmp/<worktree>/timing).
# A run is a real gate when its gate.tsv has a templates.leak row and its
# gate.meta (when there is one) does not say `fixtures<TAB>1`; every other run is
# skipped silently, by design. One ledger row per profile.tsv row (the GATE row
# included); a run without profile.tsv yields one row per gate.tsv step plus a
# GATE row, its unmeasured columns NA. A run is keyed worktree + run, so a second
# ingest of the same base appends nothing. Each run's rows go in as one append.
#
# Ledger: ${PFM_GATE_HISTORY_DIR:-$HOME/.local/state/pfm/gate-history}/<project>/ledger.tsv.
# <project> is the parent directory of `git rev-parse --git-common-dir`, its
# basename without the leading dot; --project overrides. This script writes
# nowhere else.
#
# Report: the gates (a worktree + run) ordered by stamp, the last N of them; per
# step of the newest gate: runs, median and p90 (nearest rank, NOT-RUN rows left
# out) over the gates before the newest, the newest wall and verdict, and a flag:
#   history <3        fewer than 3 earlier measurements: no judgement
#   REGRESSION        newest wall > 1.5 x the median, attribution CODE
#   slow (<WORD>)     the same slowdown with attribution CONTENTION or not measured
#   not run / no wall the newest row has no measured wall
#   ok                otherwise
# then one `FLIP <step>: <before> → <after> (<run>)` line per step whose verdict
# differs between the last two gates.
#
# Exit: 0 done; 1 a failure that is named on stderr (LEDGER-NOT-WRITTEN, NO LEDGER,
# an unreadable base, a malformed run, a bad ledger header); 2 bad usage or an
# unknown project.
#
# Portable to the macOS host's bash 3.2: no associative arrays, no mapfile, no GNU
# flags; mtimes through perl (python3 as the fallback, else NA).
# Test seams: GATE_HISTORY_OS (default `uname -s`), GATE_HISTORY_LOADAVG_FILE (a
# loadavg-format file read instead of sysctl or /proc/loadavg).
set -uo pipefail

TAB=$'\t'
NL=$'\n'
HEADER="stamp${TAB}run${TAB}worktree${TAB}commit${TAB}target${TAB}step${TAB}verdict${TAB}wall_s${TAB}cpu_s${TAB}io_mb${TAB}psi_cpu_s${TAB}psi_io_s${TAB}psi_mem_s${TAB}attribution${TAB}host_load"
PROFILE_HEADER_HEAD="step${TAB}verdict${TAB}wall_s"

usage() {
  cat >&2 <<'EOF'
usage: gate-history.sh ingest <timing-base>... [--host-load-now] [--project P]
       gate-history.sh report [--last N] [--project P]
EOF
}

usage_error() { # usage_error <reason>: exit 2
  printf 'gate-history: %s\n' "$1" >&2
  usage
  exit 2
}

# --- arguments ---------------------------------------------------------------
cmd=${1:-}
[ $# -gt 0 ] && shift
case $cmd in
  ingest | report) ;;
  -h | --help | help)
    usage
    exit 0
    ;;
  '') usage_error "no command given" ;;
  *) usage_error "unknown command: $cmd" ;;
esac

project="" last=20 host_load=0 bases=""
while [ $# -gt 0 ]; do
  case $1 in
    --project)
      [ $# -ge 2 ] && [ -n "$2" ] || usage_error "--project needs a value"
      project=$2
      shift 2
      ;;
    --project=*)
      project=${1#--project=}
      [ -n "$project" ] || usage_error "--project needs a value"
      shift
      ;;
    --last | --last=*)
      [ "$cmd" = report ] || usage_error "--last belongs to report"
      if [ "$1" = --last ]; then
        [ $# -ge 2 ] || usage_error "--last needs a value"
        last=$2
        shift 2
      else
        last=${1#--last=}
        shift
      fi
      case $last in
        '' | *[!0-9]*) usage_error "--last needs a positive integer, got: $last" ;;
        *[!0]*) ;;
        *) usage_error "--last needs a positive integer, got: $last" ;;
      esac
      ;;
    --host-load-now)
      [ "$cmd" = ingest ] || usage_error "--host-load-now belongs to ingest"
      host_load=1
      shift
      ;;
    -*) usage_error "unknown option: $1" ;;
    *)
      [ "$cmd" = ingest ] || usage_error "report takes no timing base: $1"
      bases="$bases$1$NL"
      shift
      ;;
  esac
done
if [ "$cmd" = ingest ] && [ -z "$bases" ]; then usage_error "ingest needs at least one timing base"; fi

# --- project and ledger path ---------------------------------------------------
resolve_project() {
  local common root name
  if [ -n "$project" ]; then
    case $project in
      */* | . | ..) usage_error "--project must be one path component, got: $project" ;;
    esac
    return 0
  fi
  if ! command -v git > /dev/null 2>&1; then
    printf 'gate-history: PROJECT-UNKNOWN: git is not installed and no --project was given\n' >&2
    exit 2
  fi
  if ! common=$(git rev-parse --git-common-dir 2> /dev/null) || [ -z "$common" ]; then
    printf 'gate-history: PROJECT-UNKNOWN: %s is not inside a git work tree and no --project was given\n' "$(pwd)" >&2
    exit 2
  fi
  if ! common=$(cd "$common" 2> /dev/null && pwd); then
    printf 'gate-history: PROJECT-UNKNOWN: cannot enter the git common dir %s from %s\n' "$common" "$(pwd)" >&2
    exit 2
  fi
  root=$(dirname "$common")
  name=$(basename "$root")
  while [ "${name#.}" != "$name" ]; do name=${name#.}; done
  if [ -z "$name" ] || [ "$name" = / ]; then
    printf 'gate-history: PROJECT-UNKNOWN: no project name in the git common dir %s\n' "$common" >&2
    exit 2
  fi
  project=$name
}
resolve_project

if [ -n "${PFM_GATE_HISTORY_DIR:-}" ]; then
  history_root=$PFM_GATE_HISTORY_DIR
elif [ -n "${HOME:-}" ]; then
  history_root=$HOME/.local/state/pfm/gate-history
else
  printf 'gate-history: PROJECT-UNKNOWN: neither PFM_GATE_HISTORY_DIR nor HOME is set, so no ledger path\n' >&2
  exit 2
fi
history_root=${history_root%/}
ledger=$history_root/$project/ledger.tsv

# --- portable helpers ------------------------------------------------------------
mtime_epoch() { # mtime_epoch <path>: epoch seconds, or NA
  local v=""
  if command -v perl > /dev/null 2>&1; then
    v=$(perl -e 'my @s = stat($ARGV[0]); print $s[9] if @s' "$1" 2> /dev/null)
  elif command -v python3 > /dev/null 2>&1; then
    v=$(python3 -c 'import os, sys; print(int(os.stat(sys.argv[1]).st_mtime))' "$1" 2> /dev/null)
  fi
  case $v in
    '' | *[!0-9]*) printf 'NA\n' ;;
    *) printf '%s\n' "$v" ;;
  esac
}

epoch_utc() { # epoch_utc <epoch>: %Y-%m-%dT%H:%M:%SZ, or NA
  local v=""
  case $1 in '' | *[!0-9]*)
    printf 'NA\n'
    return 0
    ;;
  esac
  if command -v perl > /dev/null 2>&1; then
    v=$(perl -MPOSIX -e 'print strftime("%Y-%m-%dT%H:%M:%SZ", gmtime($ARGV[0]))' "$1" 2> /dev/null)
  elif command -v python3 > /dev/null 2>&1; then
    v=$(python3 -c 'import sys, datetime; print(datetime.datetime.fromtimestamp(int(sys.argv[1]), datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"))' "$1" 2> /dev/null)
  fi
  printf '%s\n' "${v:-NA}"
}

meta_value() { # meta_value <gate.meta> <key>: the value, NA when the file, the key or the value is missing or `NA <reason>`
  local v=""
  [ -f "$1" ] && v=$(awk -F'\t' -v k="$2" '$1 == k { print $2; exit }' "$1" 2> /dev/null)
  case $v in
    '' | NA | 'NA '*) printf 'NA\n' ;;
    *) printf '%s\n' "$v" ;;
  esac
}

host_load_now() { # host_load_now: the 1-minute load average, or NA
  local os=${GATE_HISTORY_OS:-$(uname -s 2> /dev/null)} raw="" first
  if [ -n "${GATE_HISTORY_LOADAVG_FILE:-}" ]; then
    raw=$(cat "$GATE_HISTORY_LOADAVG_FILE" 2> /dev/null)
  elif [ "$os" = Darwin ]; then
    raw=$(sysctl -n vm.loadavg 2> /dev/null)
  else
    raw=$(cat /proc/loadavg 2> /dev/null)
  fi
  # shellcheck disable=SC2086 # word splitting into fields is the point
  set -- $raw
  [ "${1:-}" = "{" ] && shift
  first=${1:-}
  case $first in
    '' | *[!0-9.]* | .* | *. | *.*.*) printf 'NA\n' ;;
    *) printf '%s\n' "$first" ;;
  esac
}

fail() { # fail <line>: a named line on stderr, exit 1
  printf 'gate-history: %s\n' "$1" >&2
  exit 1
}

# --- ingest ------------------------------------------------------------------------
ensure_ledger() { # the ledger exists with its header; any other first line is refused
  local dir=${ledger%/*} err first
  if [ ! -s "$ledger" ]; then
    if ! err=$(mkdir -p "$dir" 2>&1); then fail "LEDGER-NOT-WRITTEN $ledger: cannot create $dir: ${err##*: }"; fi
    if ! err=$({ printf '%s\n' "$HEADER" >> "$ledger"; } 2>&1); then fail "LEDGER-NOT-WRITTEN $ledger: ${err##*: }"; fi
  fi
  first=$(head -n 1 "$ledger" 2> /dev/null)
  [ "$first" = "$HEADER" ] || fail "LEDGER-NOT-WRITTEN $ledger: the first line is not the ledger header (found: ${first:-<empty>})"
}

has_row() { # has_row <gate.tsv> <step>: a row whose first field is the step
  awk -F'\t' -v s="$2" '$1 == s { f = 1; exit } END { exit !f }' "$1"
}

eligible() { # eligible <run dir>: 0 a real gate, 1 not one; an unreadable gate.tsv is a stderr line and 1
  local gt=$1/gate.tsv
  [ -e "$gt" ] || return 1
  if [ ! -r "$gt" ]; then
    printf 'gate-history: UNREADABLE %s\n' "$gt" >&2
    errors=$((errors + 1))
    return 1
  fi
  has_row "$gt" templates.leak || return 1
  if [ -f "$1/gate.meta" ] && awk -F'\t' '$1 == "fixtures" && $2 == "1" { f = 1; exit } END { exit !f }' "$1/gate.meta" 2> /dev/null; then return 1; fi
  return 0
}

# rows_of_profile <profile.tsv>: its rows behind PREFIX, the ledger columns after the verdict kept 1:1.
# Exit 3 on a header that is not the profile header, 4 on a short row or no GATE row.
rows_of_profile() {
  awk -F'\t' -v OFS='\t' -v want="$PROFILE_HEADER_HEAD" '
    BEGIN { prefix = ENVIRON["PREFIX"]; load = ENVIRON["LOAD"] }
    NR == 1 {
      if ($1 OFS $2 OFS $3 != want || $9 != "attribution") { bad = 3; exit }
      next
    }
    NF == 0 { next }
    NF < 9 { bad = 4; exit }
    {
      for (i = 1; i <= 9; i++) if ($i == "") $i = "NA"
      if ($1 == "GATE") gate = 1
      print prefix, $1, $2, $3, $4, $5, $6, $7, $8, $9, load
    }
    END { if (bad) exit bad; if (!gate) exit 4 }
  ' "$1"
}

# rows_of_gate <gate.tsv>: a step row per gate.tsv step plus the GATE row from STEPS (WALL in an old run).
# Exit 3 on a header that is not the gate.tsv header, 4 when there is no STEPS or WALL row.
rows_of_gate() {
  awk -F'\t' -v OFS='\t' '
    BEGIN { prefix = ENVIRON["PREFIX"]; load = ENVIRON["LOAD"] }
    NR == 1 {
      if ($1 OFS $2 OFS $3 != "step" OFS "verdict" OFS "seconds") { bad = 3; exit }
      next
    }
    NF == 0 { next }
    $1 == "STEPS" { sv = $2; ss = $3; haveS = 1; next }
    $1 == "WALL" { wv = $2; ws = $3; haveW = 1; next }
    $1 == "BUDGET" { budget = $2; next }
    { print prefix, $1, $2, $3, "NA", "NA", "NA", "NA", "NA", "NA", load }
    END {
      if (bad) exit bad
      if (haveS) { v = sv; s = ss } else if (haveW) { v = wv; s = ws } else exit 4
      if (budget == "FAIL") v = "FAIL"
      print prefix, "GATE", v, s, "NA", "NA", "NA", "NA", "NA", "NA", load
    }
  ' "$1"
}

ingest_run() { # ingest_run <run dir> <worktree> <load>: append the run's rows; 0 appended, 1 a named failure
  local run=$1 wt=$2 load=$3 name stamp commit target rows rc src err
  name=${run##*/}
  stamp=$(meta_value "$run/gate.meta" started)
  case $stamp in
    [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]Z) ;;
    *) stamp=$(epoch_utc "$(mtime_epoch "$run")") ;;
  esac
  commit=$(meta_value "$run/gate.meta" commit)
  target=$(meta_value "$run/gate.meta" target)
  export PREFIX="$stamp$TAB$name$TAB$wt$TAB$commit$TAB$target" LOAD=$load
  if [ -f "$run/profile.tsv" ]; then
    rows=$(rows_of_profile "$run/profile.tsv")
    rc=$?
    src="$run/profile.tsv"
  else
    rows=$(rows_of_gate "$run/gate.tsv")
    rc=$?
    src="$run/gate.tsv"
  fi
  case $rc in
    0) ;;
    3)
      printf 'gate-history: SKIPPED %s: %s has an unexpected header\n' "$name" "$src" >&2
      return 1
      ;;
    *)
      printf 'gate-history: SKIPPED %s: %s is incomplete (a short row, or no GATE / STEPS / WALL row)\n' "$name" "$src" >&2
      return 1
      ;;
  esac
  if ! err=$({ cat >> "$ledger" <<< "$rows"; } 2>&1); then
    printf 'gate-history: LEDGER-NOT-WRITTEN %s: %s\n' "$ledger" "${err##*: }" >&2
    return 1
  fi
  return 0
}

do_ingest() {
  local base absbase wt run epoch key known plan="" sorted line newest="" newest_load=NA appended=0 pdir load
  errors=0
  ensure_ledger
  known=$(awk -F'\t' 'NR > 1 && !seen[$3 FS $2]++ { print $3 FS $2 }' "$ledger")
  # Pass 1: which runs are new real gates, and when each finished.
  while IFS= read -r base; do
    [ -n "$base" ] || continue
    base=${base%/}
    if [ ! -d "$base" ] || ! absbase=$(cd "$base" 2> /dev/null && pwd); then
      printf 'gate-history: BASE-UNREADABLE %s: not a readable directory\n' "$base" >&2
      errors=$((errors + 1))
      continue
    fi
    pdir=$(dirname "$absbase")
    wt=$(basename "$pdir")
    [ "$pdir" = / ] && wt=NA
    for run in "$absbase"/run.*; do
      [ -d "$run" ] || continue
      eligible "$run" || continue
      key=$wt$TAB${run##*/}
      case "$NL$known$NL" in *"$NL$key$NL"*) continue ;; esac
      known="$known$NL$key"
      epoch=$(mtime_epoch "$run/gate.tsv")
      [ "$epoch" = NA ] && epoch=0
      plan="$plan$epoch$TAB$run$TAB$wt$NL"
    done
  done <<< "$bases"
  sorted=$(printf '%s' "$plan" | sort -t "$TAB" -k1,1n -k2,2)
  if [ -n "$sorted" ]; then
    newest=$(printf '%s\n' "$sorted" | tail -n 1 | cut -f2)
    [ "$host_load" = 1 ] && newest_load=$(host_load_now)
  fi
  # Pass 2: one append per run, oldest first.
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    run=$(printf '%s' "$line" | cut -f2)
    wt=$(printf '%s' "$line" | cut -f3)
    load=NA
    [ "$run" = "$newest" ] && load=$newest_load
    if ingest_run "$run" "$wt" "$load"; then appended=$((appended + 1)); else errors=$((errors + 1)); fi
  done <<< "$sorted"
  printf 'gate-history: ingested %d run(s) → %s\n' "$appended" "$ledger"
  [ "$errors" -eq 0 ]
}

# --- report ------------------------------------------------------------------------
do_report() {
  [ -f "$ledger" ] || fail "NO LEDGER at $ledger"
  [ -r "$ledger" ] || fail "LEDGER-UNREADABLE $ledger"
  local first
  first=$(head -n 1 "$ledger" 2> /dev/null)
  if [ -n "$first" ] && [ "$first" != "$HEADER" ]; then fail "BAD LEDGER $ledger: the first line is not the ledger header (found: $first)"; fi
  LEDGER_PATH=$ledger awk -F'\t' -v last="$last" '
    function gt(a, b,    sa, sb) {
      sa = gstamp[a] ""; sb = gstamp[b] ""
      if (sa != sb) return sa > sb
      return a > b
    }
    function isnum(v) { return v ~ /^[0-9]+(\.[0-9]+)?$/ }
    function pad(s, w) { return sprintf("%-" w "s", s) }
    BEGIN { ledger = ENVIRON["LEDGER_PATH"]; n = 0; bad = 0 }
    $1 == "stamp" { next }
    NF == 0 { next }
    NF != 15 { bad++; next }
    {
      key = $3 FS $2
      if (!(key in gi)) {
        n++; gi[key] = n; grun[n] = $2; gwt[n] = $3; gcnt[n] = 0
        gstamp[n] = ($1 == "NA" ? "" : $1)
      }
      g = gi[key]; s = $6
      if (!((g, s) in V)) { gcnt[g]++; gstep[g, gcnt[g]] = s }
      V[g, s] = $7; W[g, s] = $8; A[g, s] = $14
    }
    END {
      if (n == 0) {
        printf "gate-history: 0 gates recorded in %s\n", ledger
        if (bad) printf "gate-history: %d malformed ledger row(s) ignored\n", bad
        exit 0
      }
      for (i = 1; i <= n; i++) ord[i] = i
      for (i = 2; i <= n; i++) {
        v = ord[i]; j = i - 1
        while (j >= 1 && gt(ord[j], v)) { ord[j + 1] = ord[j]; j-- }
        ord[j + 1] = v
      }
      lo = n - last + 1; if (lo < 1) lo = 1
      w = n - lo + 1
      L = ord[n]
      printf "gate-history: %s — %d gate(s) recorded, statistics over the last %d\n", ledger, n, w
      printf "last gate: %s %s (%s)\n", grun[L], (gstamp[L] == "" ? "NA" : gstamp[L]), gwt[L]
      if (bad) printf "gate-history: %d malformed ledger row(s) ignored\n", bad
      sw = 4
      for (i = 1; i <= gcnt[L]; i++) if (length(gstep[L, i]) > sw) sw = length(gstep[L, i])
      printf "%s %5s %9s %9s %9s %-8s %s\n", pad("step", sw), "runs", "median_s", "p90_s", "last_s", "verdict", "flag"
      for (i = 1; i <= gcnt[L]; i++) {
        s = gstep[L, i]
        runs = 0; m = 0
        for (k = lo; k <= n; k++) {
          g = ord[k]
          if (!((g, s) in V)) continue
          runs++
          if (k < n && isnum(W[g, s]) && V[g, s] != "NOT-RUN") { m++; samp[m] = W[g, s] + 0 }
        }
        for (a = 2; a <= m; a++) {
          v = samp[a]; b = a - 1
          while (b >= 1 && samp[b] > v) { samp[b + 1] = samp[b]; b-- }
          samp[b + 1] = v
        }
        if (m >= 1) { med = samp[int((50 * m + 99) / 100)]; p90 = samp[int((90 * m + 99) / 100)] }
        lv = V[L, s]; lw = W[L, s]; at = A[L, s]
        if (lv == "NOT-RUN") flag = "not run"
        else if (!isnum(lw)) flag = "no wall"
        else if (m < 3) flag = "history <3"
        else if (lw + 0 > 1.5 * med) {
          if (at == "CODE") flag = "REGRESSION"
          else if (at == "CONTENTION") flag = "slow (CONTENTION)"
          else flag = "slow (" ((at == "" || at == "NA") ? "not measured" : at) ")"
        } else flag = "ok"
        printf "%s %5d %9s %9s %9s %-8s %s\n", pad(s, sw), runs, (m >= 1 ? sprintf("%.1f", med) : "NA"), (m >= 1 ? sprintf("%.1f", p90) : "NA"), (isnum(lw) ? sprintf("%.1f", lw) : "NA"), lv, flag
      }
      if (w >= 2) {
        P = ord[n - 1]
        for (i = 1; i <= gcnt[L]; i++) {
          s = gstep[L, i]
          if (((P, s) in V) && V[P, s] != V[L, s]) printf "FLIP %s: %s → %s (%s)\n", s, V[P, s], V[L, s], grun[L]
        }
      }
    }
  ' "$ledger"
}

case $cmd in
  ingest) do_ingest ;;
  report) do_report ;;
esac
