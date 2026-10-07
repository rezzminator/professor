#!/usr/bin/env bash
# gate-history.sh: one case per behaviour of the gate ledger (ingest) and its
# report. Fixture timing bases live in $T; every ledger root is a directory under
# $T (PFM_GATE_HISTORY_DIR), never the real HOME. The git cases build a throwaway
# repo and worktree under $T.
set -uo pipefail
SHTEST_TAG=gate-history-test
# shellcheck source=../../../../scripts/shtest.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
SUT="${GATE_HISTORY_SUT:-$(dirname -- "${BASH_SOURCE[0]}")/../../gate-history.sh}"
[[ $SUT == /* ]] || SUT=$PWD/$SUT # the git cases change directory

TAB=$'\t'
HEADER="stamp${TAB}run${TAB}worktree${TAB}commit${TAB}target${TAB}step${TAB}verdict${TAB}wall_s${TAB}cpu_s${TAB}io_mb${TAB}psi_cpu_s${TAB}psi_io_s${TAB}psi_mem_s${TAB}attribution${TAB}host_load"

# Nothing a developer's or CI's environment sets may steer these cases.
export HOME="$T/home"
mkdir -p "$HOME"
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GIT_CEILING_DIRECTORIES="$T"
unset GIT_DIR GIT_WORK_TREE GIT_COMMON_DIR GATE_HISTORY_OS GATE_HISTORY_LOADAVG_FILE PFM_GATE_HISTORY_DIR

eq() { # eq <name> <expected> <actual>
  if [[ $3 == "$2" ]]; then ok "$1"; else bad "$1" "expected: $2" "actual:   $3"; fi
}
sut() { bash "$SUT" "$@"; } # a case sets LD (the ledger root) before calling
gh() { PFM_GATE_HISTORY_DIR=$LD sut "$@"; }
TS() { TZ=UTC touch -t "$@"; } # TS <YYYYMMDDhhmm.ss> <path>...
rows_of() { awk -F'\t' -v r="$2" '$2 == r' "$1"; } # a run's ledger rows
rowcount() { awk 'END { print NR }' "$1"; }

# old_run <run dir> <gate.tsv mtime>: an old-style gate, no gate.meta, no profile.tsv
old_run() {
  mkdir -p "$1/steps"
  printf 'step\tverdict\tseconds\npfm.unit\tPASS\t39.1\ntemplates.leak\tPASS\t2.0\nWALL\tPASS\t63.2\n' > "$1/gate.tsv"
  TS "$2" "$1/gate.tsv"
}

# --- ingest: a profiled run -------------------------------------------------------
LD=$T/ld-prof
run=$T/wt1/timing/run.PROF
mkdir -p "$run/steps"
printf 'step\tverdict\tseconds\npfm.unit\tPASS\t39.1\ntemplates.leak\tPASS\t2.0\nSTEPS\tPASS\t41.1\nBUDGET\tPASS\t42\n' > "$run/gate.tsv"
printf 'target\tall\ncommit\tabc1234\ndirty_files\t0\nstarted\t2026-10-01T10:00:00Z\nfinished\t2026-10-01T10:01:00Z\nfixtures\t0\nsteps_bound_s\t900\n' > "$run/gate.meta"
{
  printf 'step\tverdict\twall_s\tcpu_s\tio_mb\tpsi_cpu_s\tpsi_io_s\tpsi_mem_s\tattribution\tevidence\n'
  printf 'pfm.unit\tPASS\t39.1\t120.5\t3.2\t0.4\t0.1\t0.0\tCODE\ttick-deficit 1%% (> -5%%)\n'
  printf 'templates.leak\tPASS\t2.0\tNA\tNA\tNA\tNA\tNA\tnot measured\tresources.tsv missing\n'
  printf 'GATE\tPASS\t41.1\t125.0\t3.5\t0.5\t0.1\t0.0\tCODE\ttick-deficit 1%% (> -5%%)\n'
} > "$run/profile.tsv"
out=$(gh ingest "$T/wt1/timing" --project p 2>&1); rc=$?
L=$LD/p/ledger.tsv
eq 'ingest profiled run: exit 0' 0 "$rc"
eq 'ingest profiled run: stdout names the count and the ledger' "gate-history: ingested 1 run(s) → $L" "$out"
want=$(printf '%s\n' "$HEADER" \
  "2026-10-01T10:00:00Z${TAB}run.PROF${TAB}wt1${TAB}abc1234${TAB}all${TAB}pfm.unit${TAB}PASS${TAB}39.1${TAB}120.5${TAB}3.2${TAB}0.4${TAB}0.1${TAB}0.0${TAB}CODE${TAB}NA" \
  "2026-10-01T10:00:00Z${TAB}run.PROF${TAB}wt1${TAB}abc1234${TAB}all${TAB}templates.leak${TAB}PASS${TAB}2.0${TAB}NA${TAB}NA${TAB}NA${TAB}NA${TAB}NA${TAB}not measured${TAB}NA" \
  "2026-10-01T10:00:00Z${TAB}run.PROF${TAB}wt1${TAB}abc1234${TAB}all${TAB}GATE${TAB}PASS${TAB}41.1${TAB}125.0${TAB}3.5${TAB}0.5${TAB}0.1${TAB}0.0${TAB}CODE${TAB}NA")
eq 'ingest profiled run: header, then one row per profile.tsv row (GATE included), columns per the contract' "$want" "$(cat "$L")"
cp "$L" "$T/prof.ledger"

# --- ingest: old runs ---------------------------------------------------------------
LD=$T/ld-old
b="$T/wt 2/timing"
old_run "$b/run.OLDW" 202609250000.00
TS 202609300800.00 "$b/run.OLDW" # the run directory's mtime, later than gate.tsv's
old_run "$b/run.OLDB" 202609260000.00
printf 'BUDGET\tFAIL\t99\n' >> "$b/run.OLDB/gate.tsv"
sed 's/^WALL/STEPS/' "$b/run.OLDB/gate.tsv" > "$b/run.OLDB/g.tmp" && mv "$b/run.OLDB/g.tmp" "$b/run.OLDB/gate.tsv"
TS 202609260000.00 "$b/run.OLDB/gate.tsv"
TS 202609261200.00 "$b/run.OLDB"
old_run "$b/run.OLDF" 202609270000.00
sed 's/^WALL\tPASS/STEPS\tFAIL/' "$b/run.OLDF/gate.tsv" > "$b/run.OLDF/g.tmp" && mv "$b/run.OLDF/g.tmp" "$b/run.OLDF/gate.tsv"
TS 202609270000.00 "$b/run.OLDF/gate.tsv"
TS 202609271200.00 "$b/run.OLDF"
out=$(gh ingest "$b" --project p 2>&1); rc=$?
L=$LD/p/ledger.tsv
eq 'ingest old run: three runs in a base whose path holds a space' "gate-history: ingested 3 run(s) → $L" "$out"
NAs="NA${TAB}NA${TAB}NA${TAB}NA${TAB}NA${TAB}NA"
eq 'ingest old run (WALL): a row per step plus a GATE row from WALL, unmeasured columns NA, stamp from the run directory mtime' \
  "$(printf '%s\n' \
    "2026-09-30T08:00:00Z${TAB}run.OLDW${TAB}wt 2${TAB}NA${TAB}NA${TAB}pfm.unit${TAB}PASS${TAB}39.1${TAB}${NAs}${TAB}NA" \
    "2026-09-30T08:00:00Z${TAB}run.OLDW${TAB}wt 2${TAB}NA${TAB}NA${TAB}templates.leak${TAB}PASS${TAB}2.0${TAB}${NAs}${TAB}NA" \
    "2026-09-30T08:00:00Z${TAB}run.OLDW${TAB}wt 2${TAB}NA${TAB}NA${TAB}GATE${TAB}PASS${TAB}63.2${TAB}${NAs}${TAB}NA")" \
  "$(rows_of "$L" run.OLDW)"
eq 'ingest old run (STEPS PASS + BUDGET FAIL): the GATE verdict is FAIL' "GATE${TAB}FAIL${TAB}63.2" \
  "$(rows_of "$L" run.OLDB | awk -F'\t' '$6 == "GATE" { print $6 FS $7 FS $8 }')"
eq 'ingest old run (STEPS FAIL, no BUDGET): the GATE verdict is FAIL' "GATE${TAB}FAIL${TAB}63.2" \
  "$(rows_of "$L" run.OLDF | awk -F'\t' '$6 == "GATE" { print $6 FS $7 FS $8 }')"
eq 'ingest old runs: appended oldest gate.tsv first' 'run.OLDW run.OLDB run.OLDF' \
  "$(awk -F'\t' 'NR > 1 && !s[$2]++ { printf "%s%s", (n++ ? " " : ""), $2 } END { print "" }' "$L")"

# a profiled run whose gate.meta could not be read: stamp falls back to the run directory mtime
LD=$T/ld-nameta
run=$T/wt3/timing/run.NAMETA
mkdir -p "$run"
printf 'step\tverdict\tseconds\ntemplates.leak\tPASS\t2.0\nSTEPS\tPASS\t2.0\n' > "$run/gate.tsv"
printf 'started\tNA date failed\ncommit\tNA\ntarget\ttemplates\nfixtures\t0\n' > "$run/gate.meta"
printf 'step\tverdict\twall_s\tcpu_s\tio_mb\tpsi_cpu_s\tpsi_io_s\tpsi_mem_s\tattribution\tevidence\ntemplates.leak\tPASS\t2.0\tNA\tNA\tNA\tNA\tNA\tnot measured\tx\nGATE\tPASS\t2.0\tNA\tNA\tNA\tNA\tNA\tnot measured\tx\n' > "$run/profile.tsv"
TS 202608151030.15 "$run"
out=$(gh ingest "$T/wt3/timing" --project p 2>&1)
eq 'ingest: an unreadable gate.meta value is NA, the stamp falls back to the run directory mtime' \
  "2026-08-15T10:30:15Z${TAB}run.NAMETA${TAB}wt3${TAB}NA${TAB}templates" \
  "$(rows_of "$LD/p/ledger.tsv" run.NAMETA | head -n 1 | cut -f1-5)"

# --- ingest: runs that are not a real gate ---------------------------------------------
LD=$T/ld-skip
b=$T/wt4/timing
mkdir -p "$b/run.STUB" "$b/run.FIX" "$b/run.REAL" "$b/run.RUNNING"
printf 'step\tverdict\tseconds\ncheck.a\tPASS\t0.1\nSTEPS\tPASS\t0.1\n' > "$b/run.STUB/gate.tsv"
printf 'step\tverdict\tseconds\ntemplates.leak\tPASS\t1.0\nSTEPS\tPASS\t1.0\n' > "$b/run.FIX/gate.tsv"
printf 'fixtures\t1\ntarget\tall\n' > "$b/run.FIX/gate.meta"
printf 'step\tverdict\tseconds\ntemplates.leak\tPASS\t1.0\nSTEPS\tPASS\t1.0\n' > "$b/run.REAL/gate.tsv"
printf 'fixtures\t0\ntarget\tall\nstarted\t2026-10-01T11:00:00Z\n' > "$b/run.REAL/gate.meta"
mkdir -p "$b/run.RUNNING/steps"
out=$(gh ingest "$b" --project p 2>&1)
L=$LD/p/ledger.tsv
eq 'not a real gate / fixture gate: only the real gate is ingested (stub without templates.leak, fixtures 1, a run still going: skipped)' \
  "gate-history: ingested 1 run(s) → $L" "$out"
eq 'not a real gate / fixture gate: the ledger holds the real run alone' 'run.REAL' "$(awk -F'\t' 'NR > 1 && !s[$2]++ { print $2 }' "$L")"

# --- re-ingest ----------------------------------------------------------------------------
LD=$T/ld-prof
cp "$T/prof.ledger" "$T/prof.before"
out=$(gh ingest "$T/wt1/timing" --project p 2>&1)
eq 're-ingest: the second ingest reports 0 runs' "gate-history: ingested 0 run(s) → $LD/p/ledger.tsv" "$out"
if cmp -s "$T/prof.before" "$LD/p/ledger.tsv"; then ok 're-ingest: the ledger is byte-identical'; else bad 're-ingest: the ledger changed'; fi
out=$(gh ingest "$T/wt1/timing" "$T/wt1/timing/" --project p 2>&1)
eq 're-ingest: the same base twice in one call appends nothing' "gate-history: ingested 0 run(s) → $LD/p/ledger.tsv" "$out"
LD=$T/ld-twice
out=$(gh ingest "$T/wt1/timing" "$T/wt1/timing/" --project p 2>&1)
eq 're-ingest: a fresh ledger given the same base twice in one call ingests the run once' "gate-history: ingested 1 run(s) → $LD/p/ledger.tsv" "$out"

# --- new ledger -----------------------------------------------------------------------------
LD=$T/deep/er/dirs
out=$(gh ingest "$T/wt4/timing" --project p 2>&1); rc=$?
eq 'new ledger: created with the header, parent directories made' "$HEADER" "$(head -n 1 "$LD/p/ledger.tsv" 2> /dev/null)"
printf 'not a directory\n' > "$T/afile"
LD=$T/afile
out=$(gh ingest "$T/wt1/timing" --project p 2>&1); rc=$?
eq 'new ledger: a directory that cannot be created is LEDGER-NOT-WRITTEN, exit 1' '1 yes' \
  "$rc $(grep -q '^gate-history: LEDGER-NOT-WRITTEN ' <<< "$out" && echo yes || echo "no: $out")"
eq 'new ledger: the file in the way is untouched' 'not a directory' "$(cat "$T/afile")"
LD=$T/ld-foreign
mkdir -p "$LD/p"
printf 'something\telse\n' > "$LD/p/ledger.tsv"
out=$(gh ingest "$T/wt1/timing" --project p 2>&1); rc=$?
eq 'ledger with a foreign first line: LEDGER-NOT-WRITTEN, exit 1, nothing appended' '1 yes 1' \
  "$rc $(grep -q 'LEDGER-NOT-WRITTEN .*not the ledger header' <<< "$out" && echo yes || echo "no: $out") $(rowcount "$LD/p/ledger.tsv")"

# --- a run that cannot be read is named, the others still land ---------------------------------
LD=$T/ld-bad
b=$T/wt5/timing
mkdir -p "$b/run.BADP" "$b/run.GOODP"
for r in BADP GOODP; do
  printf 'step\tverdict\tseconds\ntemplates.leak\tPASS\t1.0\nSTEPS\tPASS\t1.0\n' > "$b/run.$r/gate.tsv"
done
printf 'step\tverdict\tsecs\tcpu\ntemplates.leak\tPASS\t1.0\t1\n' > "$b/run.BADP/profile.tsv"
out=$(gh ingest "$b" "$T/no-such-base" --project p 2>&1); rc=$?
L=$LD/p/ledger.tsv
eq 'malformed profile.tsv and a missing base: both named on stderr, exit 1, the good run ingested' '1 yes yes yes 1' \
  "$rc $(grep -q 'SKIPPED run.BADP: .*unexpected header' <<< "$out" && echo yes || echo "no: $out") $(grep -q 'BASE-UNREADABLE .*no-such-base' <<< "$out" && echo yes || echo no) $(grep -q 'ingested 1 run(s)' <<< "$out" && echo yes || echo no) $(rows_of "$L" run.GOODP | awk -F'\t' '$6 == "GATE"' | awk 'END { print NR }')"

# a run whose tables are incomplete is named and skipped, never half-ingested
LD=$T/ld-incomplete
b=$T/wt6/timing
mkdir -p "$b/run.NOGATE" "$b/run.NOSTEPS"
printf 'step\tverdict\tseconds\ntemplates.leak\tPASS\t1.0\nSTEPS\tPASS\t1.0\n' > "$b/run.NOGATE/gate.tsv"
printf 'step\tverdict\twall_s\tcpu_s\tio_mb\tpsi_cpu_s\tpsi_io_s\tpsi_mem_s\tattribution\tevidence\ntemplates.leak\tPASS\t1.0\tNA\tNA\tNA\tNA\tNA\tnot measured\tx\n' > "$b/run.NOGATE/profile.tsv"
printf 'step\tverdict\tseconds\ntemplates.leak\tPASS\t1.0\n' > "$b/run.NOSTEPS/gate.tsv"
out=$(gh ingest "$b" --project p 2>&1); rc=$?
eq 'incomplete runs: no GATE row in profile.tsv, no STEPS or WALL row in gate.tsv: each named, nothing appended, exit 1' '1 yes yes 1' \
  "$rc $(grep -q 'SKIPPED run.NOGATE: .*incomplete' <<< "$out" && echo yes || echo "no: $out") $(grep -q 'SKIPPED run.NOSTEPS: .*incomplete' <<< "$out" && echo yes || echo no) $(rowcount "$LD/p/ledger.tsv")"

# --- host load ----------------------------------------------------------------------------------
b=$T/wt7/timing
old_run "$b/run.A" 202610010300.00
old_run "$b/run.B" 202610010100.00
old_run "$b/run.C" 202610010200.00
printf '0.42 0.30 0.20 1/100 123\n' > "$T/loadavg"
loads() { awk -F'\t' 'NR > 1 { v[$2] = (v[$2] ? v[$2] "," : "") $15 } END { print v["run.A"] " | " v["run.B"] " | " v["run.C"] }' "$1"; }
LD=$T/ld-load1
GATE_HISTORY_LOADAVG_FILE=$T/loadavg gh ingest "$b" --host-load-now --project p > /dev/null 2>&1
eq 'host load: the newest run (latest gate.tsv mtime) carries the load, older runs NA' '0.42,0.42,0.42 | NA,NA,NA | NA,NA,NA' "$(loads "$LD/p/ledger.tsv")"
LD=$T/ld-load2
GATE_HISTORY_LOADAVG_FILE=$T/loadavg gh ingest "$b" --project p > /dev/null 2>&1
eq 'host load: without --host-load-now every run is NA' 'NA,NA,NA | NA,NA,NA | NA,NA,NA' "$(loads "$LD/p/ledger.tsv")"
LD=$T/ld-load3
GATE_HISTORY_LOADAVG_FILE=$T/no-such-loadavg gh ingest "$b" --host-load-now --project p > /dev/null 2>&1
eq 'host load: an unreadable load is NA' 'NA,NA,NA | NA,NA,NA | NA,NA,NA' "$(loads "$LD/p/ledger.tsv")"
LD=$T/ld-load4
mkdir -p "$T/stubbin"
# shellcheck disable=SC2016 # the stub's $1 $2 are for the stub's own shell
printf '#!/bin/sh\n[ "$1 $2" = "-n vm.loadavg" ] || exit 64\necho "{ 1.50 1.00 0.50 }"\n' > "$T/stubbin/sysctl"
chmod +x "$T/stubbin/sysctl"
PATH=$T/stubbin:$PATH GATE_HISTORY_OS=Darwin gh ingest "$b" --host-load-now --project p > /dev/null 2>&1
eq 'host load: on darwin the field comes from sysctl vm.loadavg, braces stripped' '1.50,1.50,1.50 | NA,NA,NA | NA,NA,NA' "$(loads "$LD/p/ledger.tsv")"
if [[ -r /proc/loadavg ]]; then
  LD=$T/ld-load5
  gh ingest "$b" --host-load-now --project p > /dev/null 2>&1
  v=$(awk -F'\t' '$2 == "run.A" { print $15; exit }' "$LD/p/ledger.tsv")
  if [[ $v =~ ^[0-9]+\.[0-9]+$ ]]; then ok 'host load: /proc/loadavg field 1 on linux'; else bad 'host load: /proc/loadavg field 1 on linux' "got: $v"; fi
fi

# --- project --------------------------------------------------------------------------------------
mkdir -p "$T/x" "$T/nogit"
{
  git init -q "$T/x/.professor" &&
    git -C "$T/x/.professor" -c user.name=t -c user.email=t@example.invalid commit -q --allow-empty -m init &&
    git -C "$T/x/.professor" worktree add -q --detach "$T/x/wt-a"
} > "$T/git.out" 2>&1 || bad 'project: the throwaway repo and worktree' "$(cat "$T/git.out")"
mkdir -p "$T/x/wt-a/sub"
export PFM_GATE_HISTORY_DIR=$T/gh
out=$(cd "$T/x/wt-a" && sut ingest "$T/wt4/timing" 2>&1); rc=$?
eq 'project: inside a git worktree of .../.professor the ledger sits under <root>/professor/' "0 yes" \
  "$rc $([[ -f $T/gh/professor/ledger.tsv ]] && echo yes || echo "no: $out; $(ls -R "$T/gh" 2> /dev/null)")"
out=$(cd "$T/x/.professor" && sut ingest "$T/wt4/timing" 2>&1)
eq 'project: the main checkout (relative .git) resolves to the same ledger, the run is already in it' \
  "gate-history: ingested 0 run(s) → $T/gh/professor/ledger.tsv" "$out"
out=$(cd "$T/x/wt-a/sub" && sut report 2>&1); rc=$?
eq 'project: a subdirectory of the worktree resolves to the same ledger' "0 yes" \
  "$rc $(grep -q "^gate-history: $T/gh/professor/ledger.tsv" <<< "$out" && echo yes || echo "no: $out")"
out=$(cd "$T/nogit" && sut ingest "$T/wt4/timing" 2>&1); rc=$?
eq 'project: not in a git tree and no --project: exit 2 naming the cause' '2 yes' \
  "$rc $(grep -q 'PROJECT-UNKNOWN: .*not inside a git work tree and no --project' <<< "$out" && echo yes || echo "no: $out")"
out=$(cd "$T/nogit" && sut report 2>&1); rc=$?
eq 'project: report outside a git tree without --project: exit 2' 2 "$rc"
out=$(cd "$T/nogit" && sut ingest "$T/wt4/timing" --project foo 2>&1)
eq 'project: --project names the ledger directory' yes "$([[ -f $T/gh/foo/ledger.tsv ]] && echo yes || echo no)"
out=$(cd "$T/nogit" && sut ingest "$T/wt4/timing" --project ../escape 2>&1); rc=$?
eq 'project: --project must be one path component' 2 "$rc"
unset PFM_GATE_HISTORY_DIR
out=$(cd "$T/nogit" && HOME=$T/home sut ingest "$T/wt4/timing" --project hm 2>&1)
eq 'project: without PFM_GATE_HISTORY_DIR the root is HOME/.local/state/pfm/gate-history' yes \
  "$([[ -f $T/home/.local/state/pfm/gate-history/hm/ledger.tsv ]] && echo yes || echo "no: $out")"

# --- usage ------------------------------------------------------------------------------------------
LD=$T/ld-usage
for args in '' 'bogus' 'ingest' 'ingest --project p' 'ingest x --last 3' 'report --host-load-now' 'report extra' 'report --nope' 'report --project'; do
  # shellcheck disable=SC2086 # the words of $args are the arguments
  out=$(gh $args 2>&1 < /dev/null)
  eq "usage: '$args' exits 2" 2 "$?"
done

# --- report ---------------------------------------------------------------------------------------------
# led <ledger> <stamp> <run> <step> <verdict> <wall> <attribution>
led() { printf '%s\t%s\twtL\tabc\tall\t%s\t%s\t%s\tNA\tNA\tNA\tNA\tNA\t%s\tNA\n' "$2" "$3" "$4" "$5" "$6" "$7" >> "$1"; }
# gate <ledger> <n> : gate g<n> at hour <n>, steps by their wall in that gate
gate() {
  local f=$1 n=$2 stamp run unit
  stamp="2026-10-01T0$n:00:00Z" run=run.g$n
  case $n in 1) unit=10.0 ;; 2) unit=11.0 ;; 3) unit=12.0 ;; 4) unit=13.0 ;; 5) unit=14.0 ;; *) unit=15.0 ;; esac
  led "$f" "$stamp" "$run" pfm.unit PASS "$unit" CODE
  if ((n == 6)); then
    led "$f" "$stamp" "$run" pfm.slow PASS 20.0 CODE
    led "$f" "$stamp" "$run" pfm.cont PASS 20.0 CONTENTION
    led "$f" "$stamp" "$run" pfm.edge PASS 15.0 CODE
    led "$f" "$stamp" "$run" pfm.unmeas PASS 20.0 "not measured"
  else
    led "$f" "$stamp" "$run" pfm.slow PASS 10.0 CODE
    led "$f" "$stamp" "$run" pfm.cont PASS 10.0 CODE
    led "$f" "$stamp" "$run" pfm.edge PASS 10.0 CODE
    led "$f" "$stamp" "$run" pfm.unmeas PASS 10.0 CODE
  fi
  ((n >= 5)) && led "$f" "$stamp" "$run" pfm.new PASS "$((n == 5 ? 5 : 50)).0" CODE
  case $n in
    1 | 2 | 6) led "$f" "$stamp" "$run" pfm.gap PASS 5.0 CODE ;;
    *) led "$f" "$stamp" "$run" pfm.gap NOT-RUN 0.0 NA ;;
  esac
  if ((n == 6)); then
    led "$f" "$stamp" "$run" pfm.flip FAIL 1.0 CODE
    led "$f" "$stamp" "$run" pfm.skipped NOT-RUN 0.0 NA
  else
    led "$f" "$stamp" "$run" pfm.flip PASS 1.0 CODE
    led "$f" "$stamp" "$run" pfm.skipped PASS 5.0 CODE
  fi
  led "$f" "$stamp" "$run" GATE "$([[ $n == 6 ]] && echo FAIL || echo PASS)" 100.0 CODE
}
LD=$T/ld-rep
mkdir -p "$LD/p"
R=$LD/p/ledger.tsv
printf '%s\n' "$HEADER" > "$R"
for n in 1 2 3 4 6 5; do gate "$R" "$n"; done # file order is not stamp order: g5 is appended after g6
cp "$R" "$T/rep.before"
out=$(gh report --project p 2>&1); rc=$?
row() { grep -E "^$1 " <<< "$out" | tr -s ' '; }
eq 'report: exit 0' 0 "$rc"
eq 'report: the first line names the ledger and the gate count' "gate-history: $R — 6 gate(s) recorded, statistics over the last 6" "$(head -n 1 <<< "$out")"
eq 'report: the newest gate is the one with the latest stamp, not the last appended' 'last gate: run.g6 2026-10-01T06:00:00Z (wtL)' "$(sed -n 2p <<< "$out")"
eq 'report: runs, median and p90 over the gates before the last (nearest rank), last wall, verdict, flag' 'pfm.unit 6 12.0 14.0 15.0 PASS ok' "$(row pfm.unit)"
eq 'report: regression, last wall > 1.5 x median with attribution CODE' 'pfm.slow 6 10.0 10.0 20.0 PASS REGRESSION' "$(row pfm.slow)"
eq 'report: slow with attribution CONTENTION is not a regression' 'pfm.cont 6 10.0 10.0 20.0 PASS slow (CONTENTION)' "$(row pfm.cont)"
eq 'report: slow with attribution not measured says so' 'pfm.unmeas 6 10.0 10.0 20.0 PASS slow (not measured)' "$(row pfm.unmeas)"
eq 'report: exactly 1.5 x the median is not slow' 'pfm.edge 6 10.0 10.0 15.0 PASS ok' "$(row pfm.edge)"
eq 'report: fewer than 3 earlier gates is history <3' 'pfm.new 2 5.0 5.0 50.0 PASS history <3' "$(row pfm.new)"
eq 'report: a step that did not run in the newest gate is flagged, NOT-RUN rows stay out of the statistics' 'pfm.skipped 6 5.0 5.0 0.0 NOT-RUN not run' "$(row pfm.skipped)"
eq 'report: NOT-RUN rows of earlier gates stay out of the statistics (2 measured earlier gates: history <3)' 'pfm.gap 6 5.0 5.0 5.0 PASS history <3' "$(row pfm.gap)"
eq 'report: the GATE row is a step like any other' 'GATE 6 100.0 100.0 100.0 FAIL ok' "$(row GATE)"
eq 'report: one FLIP line per step whose verdict differs between the last two gates' \
  "$(printf 'FLIP pfm.gap: NOT-RUN → PASS (run.g6)\nFLIP pfm.flip: PASS → FAIL (run.g6)\nFLIP pfm.skipped: PASS → NOT-RUN (run.g6)\nFLIP GATE: PASS → FAIL (run.g6)')" "$(grep '^FLIP ' <<< "$out")"
if cmp -s "$T/rep.before" "$R"; then ok 'report: reads the ledger without changing it'; else bad 'report: the ledger changed'; fi

out=$(gh report --last 3 --project p 2>&1); rc=$?
eq '--last 3: statistics over the last 3 gates only (2 earlier, so history <3)' '0 pfm.unit 3 13.0 14.0 15.0 PASS history <3' "$rc $(row pfm.unit)"
eq '--last 3: the header counts the window' "gate-history: $R — 6 gate(s) recorded, statistics over the last 3" "$(head -n 1 <<< "$out")"
out=$(gh report --last 4 --project p 2>&1)
eq '--last 4: three earlier gates give a judgement' 'pfm.unit 4 13.0 14.0 15.0 PASS ok' "$(row pfm.unit)"
eq '--last 4: a regression is flagged inside the window' 'pfm.slow 4 10.0 10.0 20.0 PASS REGRESSION' "$(row pfm.slow)"
out=$(gh report --last 1 --project p 2>&1)
eq '--last 1: no earlier gate, no medians, no FLIP' 'pfm.unit 1 NA NA 15.0 PASS history <3 0' "$(row pfm.unit) $(grep -c '^FLIP ' <<< "$out")"
out=$(gh report --last=2 --project p 2>&1)
eq '--last=2 is accepted' 'pfm.unit 2 14.0 14.0 15.0 PASS history <3' "$(row pfm.unit)"
for v in 0 x -2 1.5 ''; do
  out=$(gh report --last "$v" --project p 2>&1)
  eq "--last '$v': not a positive integer, exit 2" 2 "$?"
done
out=$(gh report --last --project p 2>&1)
eq '--last without a value: exit 2' 2 "$?"

# nearest-rank p90 and median over ten earlier gates: the 9th and the 5th of the sorted walls
LD=$T/ld-long
mkdir -p "$LD/p"
R2=$LD/p/ledger.tsv
printf '%s\n' "$HEADER" > "$R2"
for n in 3 1 2 4 5 6 7 8 9 10 11; do
  led "$R2" "2026-10-01T$(printf '%02d' "$n"):00:00Z" "run.h$n" pfm.long PASS "$((n == 11 ? 7 : n)).0" CODE
done
out=$(gh report --project p 2>&1)
eq 'report: nearest-rank p90 of 10 earlier walls is the 9th, the median the 5th' 'pfm.long 11 5.0 9.0 7.0 PASS ok' "$(row pfm.long)"

# gates with the same stamp keep their ledger order; a gate whose stamp is NA is the oldest
LD=$T/ld-tie
mkdir -p "$LD/p"
R3=$LD/p/ledger.tsv
printf '%s\n' "$HEADER" > "$R3"
led "$R3" 2026-10-01T01:00:00Z run.tA pfm.x PASS 1.0 CODE
led "$R3" 2026-10-01T01:00:00Z run.tB pfm.x PASS 1.0 CODE
led "$R3" NA run.tN pfm.x PASS 1.0 CODE
out=$(gh report --project p 2>&1)
eq 'report: equal stamps keep ledger order, an NA stamp is the oldest' 'last gate: run.tB 2026-10-01T01:00:00Z (wtL)' "$(sed -n 2p <<< "$out")"

# a ledger carrying a damaged row names it
LD=$T/ld-dmg
mkdir -p "$LD/p"
{ printf '%s\n' "$HEADER"; printf 'only\tthree\tfields\n'; } > "$LD/p/ledger.tsv"
gate "$LD/p/ledger.tsv" 1
out=$(gh report --project p 2>&1)
eq 'report: a malformed row is counted, not hidden' yes "$(grep -q '^gate-history: 1 malformed ledger row(s) ignored' <<< "$out" && echo yes || echo "no: $out")"

# --- missing and empty ledger ----------------------------------------------------------------------------
LD=$T/ld-none
out=$(gh report --project p 2>&1); rc=$?
eq 'missing ledger: NO LEDGER at <path>, exit 1' "1 gate-history: NO LEDGER at $LD/p/ledger.tsv" "$rc $out"
LD=$T/ld-empty
mkdir -p "$LD/p"
printf '%s\n' "$HEADER" > "$LD/p/ledger.tsv"
out=$(gh report --project p 2>&1); rc=$?
eq 'empty ledger: header only is 0 gates recorded, exit 0' "0 gate-history: 0 gates recorded in $LD/p/ledger.tsv" "$rc $out"
LD=$T/ld-foreign
out=$(gh report --project p 2>&1); rc=$?
eq 'report on a ledger with a foreign first line: BAD LEDGER, exit 1' '1 yes' "$rc $(grep -q '^gate-history: BAD LEDGER ' <<< "$out" && echo yes || echo "no: $out")"

# --- ingest then report ---------------------------------------------------------------------------------------
LD=$T/ld-both
gh ingest "$T/wt1/timing" "$T/wt 2/timing" --project p > /dev/null 2>&1
out=$(gh report --project p 2>&1); rc=$?
eq 'ingest then report: the newest gate by stamp is the profiled run, and its GATE row reports' '0 yes GATE 4 63.2 63.2 41.1 PASS ok' \
  "$rc $(grep -q '^last gate: run.PROF 2026-10-01T10:00:00Z (wt1)' <<< "$out" && echo yes || echo "no: $out") $(grep -E '^GATE ' <<< "$out" | tr -s ' ')"

shtest_end
