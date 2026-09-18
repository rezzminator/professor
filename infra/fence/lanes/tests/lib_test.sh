#!/usr/bin/env bash
# Fixture-driven tests for lanes/lib.sh — the beat library's verdicts, the
# known-gap ledger's three red rows, the `need` prelude, the activity-log slice
# and its ABSENT line. No docker, no model, no real fleet: `pfm` and `tmux` are
# stubs on PATH, so every assertion here is about the library's own behaviour.
#
# Harness borrowed from pfm/scripts/test-timing_test.sh (mktemp -d jail, plain
# [[ ]] assertions, ok/bad counters, a trap-cleaned scratch dir).
#
#   bash infra/fence/lanes/tests/lib_test.sh
#   LANE_SUT_DIR=/tmp/mutated-lanes bash …/lib_test.sh   # red-first: drive a
#                                                        # deliberately broken copy
set -uo pipefail

SUT_DIR="${LANE_SUT_DIR:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)}"
LIB="$SUT_DIR/lib.sh"
T="$(mktemp -d "${TMPDIR:-/tmp}/lane-lib-test.XXXXXX")"
trap 'rm -rf -- "$T"' EXIT

PASS=0 FAIL=0
ok() { printf 'PASS  %s\n' "$1"; PASS=$((PASS + 1)); }
bad() { printf 'FAIL  %s\n' "$1" >&2; shift; [ $# -gt 0 ] && printf '      %s\n' "$@" >&2; FAIL=$((FAIL + 1)); }

[ -f "$LIB" ] || { echo "lib_test: no lib.sh at $LIB" >&2; exit 2; }

# ── stubs: a fleet that lists nothing, a tmux that captures nothing ──────────
BIN="$T/bin"
mkdir -p "$BIN"
# The fleet the stub reports is a FIXTURE FILE ($T/rows.tsv, header-less rows in
# `pfm ls --tsv` shape). Absent, the fleet lists nothing — which is what most of
# these cases want; the liveness cases rewrite it between beats to make a chat
# appear and vanish under the lane.
ROWS="$T/rows.tsv"
cat >"$BIN/pfm" <<STUB
#!/usr/bin/env bash
ROWS="$ROWS"
LAST="$T/last.txt"
STUB
cat >>"$BIN/pfm" <<'STUB'
case "$1 ${2:-}" in
  "ls --plain")
    [ -f "$ROWS" ] && awk -F'\t' '$1 ~ /^live-/ { print "● " $5 " " }' "$ROWS"
    exit 0 ;;
  "ls --tsv")
    # the real default view: killed rows (column 10) drop out of sight.
    printf 'kind\tid\tproject\tcwd\tname\tprompts\tsize\tactivity_ns\taccount\tkilled\tsocket\n'
    [ -f "$ROWS" ] && awk -F'\t' '$10 != "true"' "$ROWS"
    exit 0 ;;
  "ls -a")
    # -a is the only view that still carries killed rows; every caller of it
    # must pass --tsv too, or the stub itself is being called wrong.
    if [ "${3:-}" != --tsv ]; then
      echo "pfm stub: ls -a called without --tsv" >&2
      exit 2
    fi
    printf 'kind\tid\tproject\tcwd\tname\tprompts\tsize\tactivity_ns\taccount\tkilled\tsocket\n'
    [ -f "$ROWS" ] && cat "$ROWS"
    exit 0 ;;
  "chat last") [ -f "$LAST" ] && cat "$LAST"; exit 0 ;;
  *) exit 0 ;;
esac
STUB
cat >"$BIN/tmux" <<'STUB'
#!/usr/bin/env bash
echo "tmux stub: no server on $*" >&2
exit 1
STUB
chmod +x "$BIN/pfm" "$BIN/tmux"
export PATH="$BIN:$PATH"

live_rows() { # live_rows — TL_CHAT live on its socket, plus its own resume row
  printf 'live-claude\tsid-new\tp\t/work\tTL_CHAT\t1\t10\t1\t1\tfalse\tcc-1-2-3\n' >"$ROWS"
  printf 'resume-claude\tsid-old\tp\t/work\tTL_CHAT\t1\t10\t1\t1\tfalse\t\n' >>"$ROWS"
}
dead_rows() { rm -f "$ROWS"; }

gaps_file() { # gaps_file <path> — the fixture ledger
  cat >"$1" <<'YML'
gaps:
  - beat: TL.05-ledgered
    landscape_id: Z1
    why: "a fixture gap"
    owner: FIXTURE
    date: 2026-09-17
    expires: 2099-01-01
  - beat: TL.06-noexpiry
    landscape_id: Z2
    why: "no expiry on purpose"
    owner: FIXTURE
    date: 2026-09-17
  - beat: TL.07-expired
    landscape_id: Z3
    why: "expired on purpose"
    owner: FIXTURE
    date: 2026-09-17
    expires: 2000-01-01
  - beat: TL.08-arch
    landscape_id: Z4
    why: "only broken on a machine nobody here has"
    owner: FIXTURE
    date: 2026-09-17
    expires: 2099-01-01
    arch: plan9-vax
YML
}

# run_lane <name> <body…> — writes a lane that sources lib.sh, runs it, prints
# nothing; $OUT holds stdout+stderr, $RC the exit code, $LANE_DIR the artifacts.
run_lane() {
  local name="$1"
  shift
  LANE_DIR="$T/out-$name"
  rm -rf "$LANE_DIR"
  {
    printf '. "%s"\n' "$LIB"
    printf '%s\n' "$@"
  } >"$T/$name.sh"
  OUT="$(LANE_OUT_DIR="$LANE_DIR" LANE_GAPS="$T/gaps.yml" LANE_PFM_LOG="${LANE_PFM_LOG_FIXTURE:-$T/absent.jsonl}" \
    LANE_TODAY="${LANE_TODAY_FIXTURE:-2026-09-17}" bash "$T/$name.sh" 2>&1)"
  RC=$?
}

gaps_file "$T/gaps.yml"

# ---- 1: pass, fail and the blocked chain ----------------------------------

run_lane basic \
  'lane_begin TL' \
  'beat TL.01-ok Z1; spends cc:1; pass "held"' \
  'beat TL.02-bad Z2; target TL_CHAT; fail "did not hold"' \
  'beat TL.03-dep Z3; requires TL.02-bad && pass "must not happen"' \
  'lane_end'
row="$(cat "$LANE_DIR/TL.row.tsv" 2>/dev/null)"
if [ "$RC" -ne 0 ] &&
  printf '%s' "$OUT" | grep -q 'TL ✓ TL.01-ok — held' &&
  printf '%s' "$OUT" | grep -q 'TL ✗ TL.02-bad — did not hold' &&
  printf '%s' "$OUT" | grep -q 'TL blocked TL.03-dep — blocked-by TL.02-bad' &&
  [ "$(printf '%s' "$row" | cut -f3-6)" = "$(printf '3\t1\t0\t1')" ]; then
  ok "verdicts: ✓/✗/blocked stream, lane exits 1, row.tsv = 3 beats 1 failed 0 known 1 blocked"
else
  bad "verdicts" "rc=$RC" "row=[$row]" "$OUT"
fi

# ---- 2: the timeline carries lane · beat · t+s per beat -------------------

header="$(head -1 "$LANE_DIR/TL.timeline.tsv" 2>/dev/null)"
rows="$(tail -n +2 "$LANE_DIR/TL.timeline.tsv" 2>/dev/null | wc -l | tr -d ' ')"
stamp="$(awk -F'\t' '$2 == "TL.01-ok" { print $3 }' "$LANE_DIR/TL.timeline.tsv" 2>/dev/null)"
if [ "$header" = "$(printf 'lane\tbeat\tt_plus_s\tverdict\tdur_s\tseat\tlandscape_ids\tdetail')" ] &&
  [ "$rows" -eq 3 ] && printf '%s' "$stamp" | grep -qE '^t\+[0-9]+$'; then
  ok "timeline: header + one row per beat + a t+<s> stamp ($stamp)"
else
  bad "timeline" "header=[$header]" "rows=$rows" "stamp=[$stamp]"
fi

# ---- 3: a raw pane dump is attached to a ✗, or its absence is NAMED -------

if grep -q 'raw-dump: NO SOCKET for TL_CHAT' "$LANE_DIR/TL.log" 2>/dev/null; then
  ok "fail: the raw pane dump names why it could not capture (no socket), never silence"
else
  bad "raw dump" "$(cat "$LANE_DIR/TL.log" 2>&1)"
fi

# ---- 4: a ledgered beat reports known and does not fail the lane ----------

run_lane known \
  'lane_begin TL' \
  'beat TL.05-ledgered Z1; known TL.05-ledgered' \
  'lane_end'
row="$(cat "$LANE_DIR/TL.row.tsv" 2>/dev/null)"
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q 'TL known TL.05-ledgered — a fixture gap' &&
  [ "$(printf '%s' "$row" | cut -f4-5)" = "$(printf '0\t1')" ]; then
  ok "known: counted apart (failed=0 known=1), lane exits 0, the why/owner/expiry are quoted"
else
  bad "known" "rc=$RC" "row=[$row]" "$OUT"
fi

# ---- 5: a ledgered beat that PASSES is a red row --------------------------

run_lane unexpected \
  'lane_begin TL' \
  'beat TL.05-ledgered Z1; pass "it works now"' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'known-gap now passes — remove the entry'; then
  ok "known-gap strictness: a listed beat that passes is ✗ 'known-gap now passes'"
else
  bad "unexpected pass" "rc=$RC" "$OUT"
fi

# ---- 6: an entry with no expires, and an expired one, are both red -------

run_lane noexpiry \
  'lane_begin TL' \
  'beat TL.06-noexpiry Z2; known TL.06-noexpiry' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'has no expires:'; then
  ok "ledger law: known on an entry with no expires: is ✗"
else
  bad "no-expires" "rc=$RC" "$OUT"
fi

run_lane expired \
  'lane_begin TL' \
  'beat TL.07-expired Z3; known TL.07-expired' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'expired 2000-01-01'; then
  ok "ledger law: known on an expired entry is ✗, naming the date"
else
  bad "expired" "rc=$RC" "$OUT"
fi

# ---- 7: an arch-scoped entry does not excuse another architecture ---------

run_lane arch \
  'lane_begin TL' \
  'beat TL.08-arch Z4; known TL.08-arch' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'scoped to plan9-vax'; then
  ok "arch scope: known on a foreign-arch entry is ✗ ('assert it for real here')"
else
  bad "arch-scoped known" "rc=$RC" "$OUT"
fi

run_lane archpass \
  'lane_begin TL' \
  'beat TL.08-arch Z4; pass "works on this arch"' \
  'lane_end'
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q 'TL ✓ TL.08-arch'; then
  ok "arch scope: passing a foreign-arch entry is CLEAN, not an unexpected pass"
else
  bad "arch-scoped pass" "rc=$RC" "$OUT"
fi

# ---- 8: gaps_validate names every offender before a run ------------------

OUT="$(LANE_GAPS="$T/gaps.yml" LANE_TODAY=2026-09-17 bash -c ". '$LIB'; gaps_validate" 2>&1)"
RC=$?
if [ "$RC" -ne 0 ] &&
  printf '%s' "$OUT" | grep -q 'TL.06-noexpiry — no expires:' &&
  printf '%s' "$OUT" | grep -q 'TL.07-expired — expired 2000-01-01'; then
  ok "gaps_validate: exit 1 naming the entry with no expiry AND the expired one"
else
  bad "gaps_validate" "rc=$RC" "$OUT"
fi

cat >"$T/clean-gaps.yml" <<'YML'
gaps:
  - beat: X.01
    landscape_id: Z9
    why: "fine"
    owner: FIXTURE
    date: 2026-09-17
    expires: 2099-01-01
YML
if LANE_GAPS="$T/clean-gaps.yml" LANE_TODAY=2026-09-17 bash -c ". '$LIB'; gaps_validate" >/dev/null 2>&1; then
  ok "gaps_validate: a ledger where every entry is dated and current exits 0"
else
  bad "gaps_validate clean" "a valid ledger was rejected"
fi

# ---- 9: need — satisfied, made, UNMET ------------------------------------

touch "$T/exists"
rm -f "$T/made" "$T/never"
run_lane need \
  'lane_begin TL' \
  "need 'a file that exists' \"[ -f '$T/exists' ]\" 'true' && echo NEED1-OK" \
  "need 'a file to make' \"[ -f '$T/made' ]\" \"touch '$T/made'\" && echo NEED2-OK" \
  "need 'an impossible file' \"[ -f '$T/never' ]\" 'exit 3' || echo NEED3-UNMET" \
  "need 'a lying maker' \"[ -f '$T/never' ]\" 'true' || echo NEED4-UNMET" \
  'lane_end'
if printf '%s' "$OUT" | grep -q 'satisfied (no-op)' &&
  printf '%s' "$OUT" | grep -q 'NEED1-OK' &&
  printf '%s' "$OUT" | grep -q '· made' && printf '%s' "$OUT" | grep -q 'NEED2-OK' &&
  printf '%s' "$OUT" | grep -q 'UNMET — the make command exited 3' && printf '%s' "$OUT" | grep -q 'NEED3-UNMET' &&
  printf '%s' "$OUT" | grep -q 'exited 0 but the check still fails' && printf '%s' "$OUT" | grep -q 'NEED4-UNMET'; then
  ok "need: satisfied / made / UNMET on a failing maker / UNMET on a maker that lied"
else
  bad "need" "$OUT"
fi

# ---- 10: lane_abort ends the lane with a named PRELUDE failure ------------

run_lane abort \
  'lane_begin TL' \
  'lane_abort "the fixture precondition is unbuildable"' \
  'echo MUST-NOT-REACH'
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'TL ✗ PRELUDE — the fixture precondition is unbuildable' &&
  ! printf '%s' "$OUT" | grep -q MUST-NOT-REACH && [ -f "$LANE_DIR/TL.row.tsv" ]; then
  ok "lane_abort: named PRELUDE ✗, exit 1, a row.tsv still written, nothing after it runs"
else
  bad "lane_abort" "rc=$RC" "$OUT"
fi

# ---- 11: a beat left open is named by the next one -----------------------

run_lane orphan \
  'lane_begin TL' \
  'beat TL.10-open Z1' \
  'beat TL.11-next Z2; pass "fine"' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'TL ✗ TL.10-open — beat left open (no pass/fail/known/blocked before TL.11-next)'; then
  ok "a beat with no verdict is named by the next beat, never dropped"
else
  bad "orphan beat" "rc=$RC" "$OUT"
fi

run_lane orphan_end \
  'lane_begin TL' \
  'beat TL.12-open Z1' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'beat left open — the lane reached its end with no verdict'; then
  ok "a beat still open at lane_end is named there"
else
  bad "orphan at end" "rc=$RC" "$OUT"
fi

# ---- 12: the activity log — ABSENT is named ------------------------------

if printf '%s' "$OUT" | grep -q 'TL · activity log: ABSENT (Wave 6 not landed) — log assertions not enforced' &&
  [ "$(cat "$LANE_DIR/TL.logstate" 2>/dev/null)" = ABSENT ]; then
  ok "activity log: the ABSENT line is printed by name and recorded in <lane>.logstate"
else
  bad "absent log line" "$OUT" "$(cat "$LANE_DIR/TL.logstate" 2>&1)"
fi

# ---- 13: an unexpected error record in the slice fails the beat ----------

LOG="$T/pfm.jsonl"
printf '{"level":"info","msg":"before the lane"}\n' >"$LOG"
LANE_PFM_LOG_FIXTURE="$LOG" run_lane logslice \
  'lane_begin TL' \
  "beat TL.20-dirty Z1; printf '{\"level\":\"error\",\"msg\":\"reload lock stuck\"}\n' >> '$LOG'; pass 'the assertion held'" \
  'lane_end'
if [ "$RC" -ne 0 ] &&
  printf '%s' "$OUT" | grep -q 'unexpected error record' &&
  grep -q 'reload lock stuck' "$LANE_DIR/TL.log" 2>/dev/null; then
  ok "activity log: an unexpected error record turns a passing beat ✗ and the slice is attached"
else
  bad "log slice" "rc=$RC" "$OUT" "$(cat "$LANE_DIR/TL.log" 2>&1)"
fi

# ---- 14: expect-log declares the error the beat provokes on purpose -------

printf '{"level":"info","msg":"before the lane"}\n' >"$LOG"
LANE_PFM_LOG_FIXTURE="$LOG" run_lane expectlog \
  'lane_begin TL' \
  "beat TL.21-clean Z1; expect-log 'reload lock stuck'; printf '{\"level\":\"error\",\"msg\":\"reload lock stuck\"}\n' >> '$LOG'; pass 'provoked on purpose'" \
  'lane_end'
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q 'TL ✓ TL.21-clean'; then
  ok "expect-log: a declared error record does not fail its beat"
else
  bad "expect-log" "rc=$RC" "$OUT"
fi

printf '{"level":"info","msg":"before the lane"}\n' >"$LOG"
LANE_PFM_LOG_FIXTURE="$LOG" run_lane expectlog_other \
  'lane_begin TL' \
  "beat TL.22-other Z1; expect-log 'a different error'; printf '{\"level\":\"error\",\"msg\":\"reload lock stuck\"}\n' >> '$LOG'; pass 'x'" \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'unexpected error record'; then
  ok "expect-log: a pattern that does not match the record still fails the beat"
else
  bad "expect-log mismatch" "rc=$RC" "$OUT"
fi

# ---- 15: the row readers tell a LIVE row from its own resume row ----------

live_rows
OUT="$(bash -c ". '$LIB'; printf '%s|%s|%s|%s\n' \"\$(live_field TL_CHAT 11)\" \"\$(live_field TL_CHAT 2)\" \"\$(socket_field cc-1-2-3 2)\" \"\$(socket_field cc-1-2-3 5)\"" 2>&1)"
if [ "$OUT" = 'cc-1-2-3|sid-new|sid-new|TL_CHAT' ]; then
  ok "live_field/socket_field read the LIVE row, never the resume row that shares its name ($OUT)"
else
  bad "live row readers" "got=[$OUT] want=[cc-1-2-3|sid-new|sid-new|TL_CHAT]"
fi
dead_rows

# ---- 16: a vanished target is BLOCKED (fast), named, and re-opened ONCE ----

live_rows
# The re-open the lane declares: the chat comes back on its socket.
cp "$ROWS" "$T/rows-live.tsv"
printf '#!/usr/bin/env bash\ncp "%s" "%s"\n' "$T/rows-live.tsv" "$ROWS" >"$T/reopen.sh"
run_lane liveness \
  'lane_begin TL' \
  "lane_reopen \"bash '$T/reopen.sh'\"" \
  'beat TL.30-live Z1; target_live TL_CHAT; requires && pass "the chat is alive"' \
  "rm -f '$ROWS'" \
  'beat TL.31-gone Z2; target_live TL_CHAT; requires && pass "MUST-NOT-HAPPEN"' \
  'beat TL.32-back Z3; target_live TL_CHAT; requires && pass "the re-open brought it back"' \
  "rm -f '$ROWS'" \
  'beat TL.33-gone-again Z4; target_live TL_CHAT; requires && pass "MUST-NOT-HAPPEN"' \
  'lane_end'
dur="$(awk -F'\t' '$2 == "TL.31-gone" { print $5 }' "$LANE_DIR/TL.timeline.tsv" 2>/dev/null)"
if printf '%s' "$OUT" | grep -q 'TL blocked TL.31-gone — blocked-by TL.30-live' &&
  printf '%s' "$OUT" | grep -q 'TL_CHAT has no live row' &&
  ! printf '%s' "$OUT" | grep -q MUST-NOT-HAPPEN &&
  printf '%s' "$OUT" | grep -q 'TL ✓ TL.32-back' &&
  printf '%s' "$OUT" | grep -q 'TL blocked TL.33-gone-again — blocked-by TL.32-back' &&
  grep -q 'reopen: already attempted once' "$LANE_DIR/TL.log" 2>/dev/null &&
  [ -n "$dur" ] && [ "$dur" -lt 5 ]; then
  ok "liveness guard: a dead target is blocked-by the beat that last saw it alive in ${dur}s, re-opened exactly once"
else
  bad "liveness guard" "dur=[$dur]" "$OUT" "$(cat "$LANE_DIR/TL.log" 2>&1)"
fi
dead_rows

# ---- 17: a lane with no re-open declared says so instead of going quiet ----

run_lane noreopen \
  'lane_begin TL' \
  'beat TL.34-gone Z1; target_live TL_CHAT; requires && pass "MUST-NOT-HAPPEN"' \
  'lane_end'
if printf '%s' "$OUT" | grep -q 'TL blocked TL.34-gone — blocked-by' &&
  grep -q 'reopen: no re-open command declared' "$LANE_DIR/TL.log" 2>/dev/null; then
  ok "liveness guard with no lane_reopen: still blocked, and the missing re-open is NAMED"
else
  bad "no reopen declared" "$OUT" "$(cat "$LANE_DIR/TL.log" 2>&1)"
fi

# ---- 17b: row_field reads a KILLED row too — the default `ls --tsv` view --
# drops killed rows, so the readers must go through `ls -a --tsv`.

printf 'live-claude\tsid-k\tp\t/work\tTL_CHAT\t1\t10\t1\t1\ttrue\tcc-9-9-9\n' >"$ROWS"
OUT="$(bash -c ". '$LIB'; row_field TL_CHAT 10" 2>&1)"
if [ "$OUT" = true ]; then
  ok "row_field reads a killed row (column 10 = true) that the default ls --tsv view hides"
else
  bad "row_field on a killed row" "got=[$OUT] want=[true]"
fi
dead_rows

# ---- 18: every wait is bounded and fails fast on a dead target ------------

run_lane waits \
  'lane_begin TL' \
  'beat TL.40-wait Z1; target_live TL_CHAT; t0=$SECONDS; wait_last TL_CHAT NEVER 300; rc=$?;
   printf "WAITLAST rc=%s elapsed=%s why=%s\n" "$rc" "$((SECONDS - t0))" "$LANE_WAIT_WHY"
   t0=$SECONDS; wait_for 300 false; rc=$?
   printf "WAITFOR rc=%s elapsed=%s why=%s\n" "$rc" "$((SECONDS - t0))" "$LANE_WAIT_WHY"
   pass "the waits returned"' \
  'lane_end'
lastline="$(printf '%s\n' "$OUT" | grep '^WAITLAST ')"
forline="$(printf '%s\n' "$OUT" | grep '^WAITFOR ')"
if printf '%s' "$lastline" | grep -qE 'rc=2 elapsed=([0-9]|1[0-9]) why=.*no live row' &&
  printf '%s' "$forline" | grep -qE 'rc=2 elapsed=([0-9]|1[0-9]) why=.*no live row'; then
  ok "waits: wait_last and wait_for abandon a dead target in seconds, not minutes, naming why ($lastline)"
else
  bad "bounded waits" "last=[$lastline]" "for=[$forline]" "$OUT"
fi

# ---- 19: a timeout still reports as a TIMEOUT, not as a dead target -------

live_rows
run_lane waittimeout \
  'lane_begin TL' \
  'beat TL.41-timeout Z1; target_live TL_CHAT; wait_last TL_CHAT NEVER 1; rc=$?;
   printf "TIMEOUT rc=%s why=%s\n" "$rc" "$LANE_WAIT_WHY"; pass "x"' \
  'lane_end'
if printf '%s\n' "$OUT" | grep -qE '^TIMEOUT rc=1 why=.*(timed out|1s)'; then
  ok "waits: a real timeout on a LIVE target is rc 1 and says it timed out (never 'no live row')"
else
  bad "wait timeout" "$(printf '%s\n' "$OUT" | grep '^TIMEOUT ')" "$OUT"
fi
dead_rows

# ---- 20: blocked carries a reason beside the beat that blocked it ---------

run_lane blockedwhy \
  'lane_begin TL' \
  'beat TL.42-seats Z1; blocked "seats cc:1" "no second seat in this run"' \
  'lane_end'
if printf '%s' "$OUT" | grep -q 'TL blocked TL.42-seats — blocked-by seats cc:1 — no second seat in this run'; then
  ok "blocked: a reason travels beside the blocker ('no second seat in this run')"
else
  bad "blocked reason" "$OUT"
fi

# ---- 21: a failure with no socket still leaves BYTES ----------------------

mkdir -p "$T/tmuxdir"
: >"$T/tmuxdir/cc-ghost-1"
: >"$T/tmuxdir/cc-ghost-2"
export TMUX_TMPDIR="$T/tmuxdir"
run_lane evidence \
  'lane_begin TL' \
  'beat TL.50-dead Z1; target TL_CHAT; fail "nothing answered"' \
  'lane_end'
unset TMUX_TMPDIR
log="$(cat "$LANE_DIR/TL.log" 2>/dev/null)"
if printf '%s' "$log" | grep -q 'raw-dump: NO SOCKET for TL_CHAT' &&
  printf '%s' "$log" | grep -q 'raw-dump: pfm ls --tsv' &&
  printf '%s' "$log" | grep -qP 'kind\tid\tproject' &&
  printf '%s' "$log" | grep -q 'raw-dump: cc-ghost-1' &&
  printf '%s' "$log" | grep -q 'raw-dump: cc-ghost-2' &&
  printf '%s' "$log" | grep -q 'tmux stub: no server'; then
  ok "failure evidence: no socket → the fleet listing AND a capture attempt per socket under the tmux tmpdir"
else
  bad "failure evidence" "$log"
fi

# ── the debug ruling: every lane runs pfm at PFM_LOG_LEVEL=debug ─────────────
# Forced, not defaulted: a caller's own level would leave the activity log
# (Wave 6) too thin for a beat's slice to be judged on.
level="$(PFM_LOG_LEVEL=info bash -c ". '$LIB'; printf '%s' \"\$PFM_LOG_LEVEL\"")"
if [ "$level" = debug ]; then
  ok "sourcing lib.sh forces PFM_LOG_LEVEL=debug even over a caller's own level"
else
  bad "PFM_LOG_LEVEL ruling" "got '$level', want 'debug'"
fi

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
