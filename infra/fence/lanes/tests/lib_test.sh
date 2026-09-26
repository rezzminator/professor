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
SHTEST_TAG=lane-lib-test
# shellcheck source=/dev/null
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"

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
    why: "a fixture gap"
    owner: FIXTURE
    date: 2026-09-17
    expires: 2099-01-01
  - beat: TL.06-noexpiry
    why: "no expiry on purpose"
    owner: FIXTURE
    date: 2026-09-17
  - beat: TL.07-expired
    why: "expired on purpose"
    owner: FIXTURE
    date: 2026-09-17
    expires: 2000-01-01
  - beat: TL.08-arch
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
  OUT="$(LANE_OUT_DIR="$LANE_DIR" LANE_GAPS="${LANE_GAPS_FIXTURE:-$T/gaps.yml}" LANE_PFM_LOG="${LANE_PFM_LOG_FIXTURE:-$T/absent.jsonl}" \
    LANE_TODAY="${LANE_TODAY_FIXTURE:-2026-09-17}" bash "$T/$name.sh" 2>&1)"
  RC=$?
}

gaps_file "$T/gaps.yml"

# A log that EXISTS (empty is fine) — the default `run_lane` fixture is
# $T/absent.jsonl, which is never created (LANE_LOG_STATE=ABSENT), and that
# is now its own red (F13, below). Any test whose own subject is unrelated to
# the log's presence uses this fixture instead, so the two concerns stay apart.
PRESENT_LOG="$T/present.jsonl"
: >"$PRESENT_LOG"

# gaps_validate's stale-beat-id check reads `beat <id>` lines from real lane
# scripts under $LANE_SCRIPTS_DIR — a fixture directory here, never this
# tree's own lanes/*.sh, so a fixture ledger is judged only against fixture
# beats.
GAPS_SCRIPTS_DIR="$T/gap-fixture-scripts"
mkdir -p "$GAPS_SCRIPTS_DIR"
cat >"$GAPS_SCRIPTS_DIR/TL.sh" <<'SH'
beat TL.05-ledgered
beat TL.06-noexpiry
beat TL.07-expired
beat TL.08-arch
beat X.01
SH

# ---- 1: pass, fail and the blocked chain ----------------------------------

LANE_PFM_LOG_FIXTURE="$PRESENT_LOG" run_lane basic \
  'lane_begin TL' \
  'beat TL.01-ok; spends cc:1; pass "held"' \
  'beat TL.02-bad; target TL_CHAT; fail "did not hold"' \
  'beat TL.03-dep; requires TL.02-bad && pass "must not happen"' \
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
if [ "$header" = "$(printf 'lane\tbeat\tt_plus_s\tverdict\tdur_s\tseat\tdetail')" ] &&
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

LANE_PFM_LOG_FIXTURE="$PRESENT_LOG" run_lane known \
  'lane_begin TL' \
  'beat TL.05-ledgered; known TL.05-ledgered' \
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
  'beat TL.05-ledgered; pass "it works now"' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'known-gap now passes — remove the entry'; then
  ok "known-gap strictness: a listed beat that passes is ✗ 'known-gap now passes'"
else
  bad "unexpected pass" "rc=$RC" "$OUT"
fi

# ---- 6: an entry with no expires, and an expired one, are both red -------

run_lane noexpiry \
  'lane_begin TL' \
  'beat TL.06-noexpiry; known TL.06-noexpiry' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'has no expires:'; then
  ok "ledger law: known on an entry with no expires: is ✗"
else
  bad "no-expires" "rc=$RC" "$OUT"
fi

run_lane expired \
  'lane_begin TL' \
  'beat TL.07-expired; known TL.07-expired' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'expired 2000-01-01'; then
  ok "ledger law: known on an expired entry is ✗, naming the date"
else
  bad "expired" "rc=$RC" "$OUT"
fi

# ---- 7: an arch-scoped entry does not excuse another architecture ---------

run_lane arch \
  'lane_begin TL' \
  'beat TL.08-arch; known TL.08-arch' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'scoped to plan9-vax'; then
  ok "arch scope: known on a foreign-arch entry is ✗ ('assert it for real here')"
else
  bad "arch-scoped known" "rc=$RC" "$OUT"
fi

LANE_PFM_LOG_FIXTURE="$PRESENT_LOG" run_lane archpass \
  'lane_begin TL' \
  'beat TL.08-arch; pass "works on this arch"' \
  'lane_end'
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q 'TL ✓ TL.08-arch'; then
  ok "arch scope: passing a foreign-arch entry is CLEAN, not an unexpected pass"
else
  bad "arch-scoped pass" "rc=$RC" "$OUT"
fi

# ---- 8: gaps_validate names every offender before a run ------------------

OUT="$(LANE_GAPS="$T/gaps.yml" LANE_TODAY=2026-09-17 LANE_SCRIPTS_DIR="$GAPS_SCRIPTS_DIR" bash -c ". '$LIB'; gaps_validate" 2>&1)"
RC=$?
if [ "$RC" -ne 0 ] &&
  printf '%s' "$OUT" | grep -q 'TL.06-noexpiry — no expires:' &&
  printf '%s' "$OUT" | grep -q 'TL.07-expired — expired 2000-01-01'; then
  ok "gaps_validate: exit 1 naming the entry with no expiry AND the expired one"
else
  bad "gaps_validate" "rc=$RC" "$OUT"
fi

# ---- 8b: an unreadable ledger is named, not silently "not listed" ---------
# gap_listed's stderr must carry gap_record's own KNOWN-GAPS-UNREADABLE line —
# an unreadable ledger and "no entry for this beat" must never collapse into
# the same silent false.

LANE_GAPS_FIXTURE="$T/no-such-gaps.yml" run_lane unreadable_gaps \
  'lane_begin TL' \
  'beat TL.09-nogaps; pass "held"' \
  'lane_end'
if printf '%s' "$OUT" | grep -q 'KNOWN-GAPS-UNREADABLE'; then
  ok "gap_listed: an unreadable ledger is NAMED (KNOWN-GAPS-UNREADABLE), never silently 'not listed'"
else
  bad "unreadable ledger" "$OUT"
fi

cat >"$T/clean-gaps.yml" <<'YML'
gaps:
  - beat: X.01
    why: "fine"
    owner: FIXTURE
    date: 2026-09-17
    expires: 2099-01-01
YML
if LANE_GAPS="$T/clean-gaps.yml" LANE_TODAY=2026-09-17 LANE_SCRIPTS_DIR="$GAPS_SCRIPTS_DIR" bash -c ". '$LIB'; gaps_validate" >/dev/null 2>&1; then
  ok "gaps_validate: a ledger where every entry is dated and current exits 0"
else
  bad "gaps_validate clean" "a valid ledger was rejected"
fi

# ---- 8c: a ledger beat: id with no matching 'beat <id>' line anywhere is a
# named stale entry (a renamed/deleted beat left behind) ---------------------

cat >"$T/stale-gaps.yml" <<'YML'
gaps:
  - beat: TL.99-ghost-beat
    why: "the beat this entry named was renamed or deleted"
    owner: FIXTURE
    date: 2026-09-17
    expires: 2099-01-01
YML
OUT="$(LANE_GAPS="$T/stale-gaps.yml" LANE_TODAY=2026-09-17 LANE_SCRIPTS_DIR="$GAPS_SCRIPTS_DIR" bash -c ". '$LIB'; gaps_validate" 2>&1)"
RC=$?
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q "TL.99-ghost-beat — no lane script under .* has a 'beat TL.99-ghost-beat' line"; then
  ok "gaps_validate: a ledger beat id with no matching 'beat <id>' line anywhere is named stale"
else
  bad "gaps_validate stale beat" "rc=$RC" "$OUT"
fi

# ---- 8d: non-comment content that parses to ZERO entries is UNPARSEABLE, --
# never the same clean answer as a genuinely empty ledger --------------------

cat >"$T/garbage-gaps.yml" <<'YML'
gaps:
    beat: TL.05-ledgered
    why: "differently indented — no leading '- ' before beat:, so the parser matches nothing"
YML
OUT="$(LANE_GAPS="$T/garbage-gaps.yml" LANE_TODAY=2026-09-17 LANE_SCRIPTS_DIR="$GAPS_SCRIPTS_DIR" bash -c ". '$LIB'; gaps_validate" 2>&1)"
RC=$?
if [ "$RC" -eq 2 ] && printf '%s' "$OUT" | grep -q 'UNPARSEABLE'; then
  ok "gaps_validate: non-comment content that parses to zero '- beat:' entries is UNPARSEABLE (exit 2), never a silent clean"
else
  bad "gaps_validate unparseable" "rc=$RC" "$OUT"
fi

cat >"$T/truly-empty-gaps.yml" <<'YML'
# nothing ledgered yet — comments and the bare key only
gaps:
YML
if LANE_GAPS="$T/truly-empty-gaps.yml" LANE_TODAY=2026-09-17 LANE_SCRIPTS_DIR="$GAPS_SCRIPTS_DIR" bash -c ". '$LIB'; gaps_validate" >/dev/null 2>&1; then
  ok "gaps_validate: a genuinely empty ledger (comments + bare 'gaps:' only) is NOT unparseable — it exits 0"
else
  bad "gaps_validate genuinely empty" "a genuinely empty ledger was rejected"
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
  'beat TL.10-open' \
  'beat TL.11-next; pass "fine"' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'TL ✗ TL.10-open — beat left open (no pass/fail/known/blocked before TL.11-next)'; then
  ok "a beat with no verdict is named by the next beat, never dropped"
else
  bad "orphan beat" "rc=$RC" "$OUT"
fi

run_lane orphan_end \
  'lane_begin TL' \
  'beat TL.12-open' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'beat left open — the lane reached its end with no verdict'; then
  ok "a beat still open at lane_end is named there"
else
  bad "orphan at end" "rc=$RC" "$OUT"
fi

# ---- 12: the activity log — ABSENT at lane start is a RED (F13) -----------
# Wave 6 has landed: every root image already ran pfm before it was
# committed, so ABSENT here is a broken state, not an excused gap — named at
# lane_begin (PRELUDE-LOG) and failing the lane, not just a quiet advisory.

run_lane logabsent \
  'lane_begin TL' \
  'beat TL.13-ok; pass "held"' \
  'lane_end'
if [ "$RC" -ne 0 ] &&
  printf '%s' "$OUT" | grep -q 'TL ✗ PRELUDE-LOG — activity log ABSENT at lane start' &&
  printf '%s' "$OUT" | grep -q 'TL · activity log: ABSENT at lane start' &&
  ! printf '%s' "$OUT" | grep -q 'Wave 6 not landed' &&
  [ "$(cat "$LANE_DIR/TL.logstate" 2>/dev/null)" = ABSENT ]; then
  ok "activity log: ABSENT at lane start is a named red (PRELUDE-LOG), the stale 'Wave 6 not landed' wording is gone, and it is still recorded in <lane>.logstate"
else
  bad "absent log line" "rc=$RC" "$OUT" "$(cat "$LANE_DIR/TL.logstate" 2>&1)"
fi

# ---- 13: an unexpected error record in the slice fails the beat ----------

LOG="$T/pfm.jsonl"
printf '{"level":"info","msg":"before the lane"}\n' >"$LOG"
LANE_PFM_LOG_FIXTURE="$LOG" run_lane logslice \
  'lane_begin TL' \
  "beat TL.20-dirty; printf '{\"level\":\"error\",\"msg\":\"reload lock stuck\"}\n' >> '$LOG'; pass 'the assertion held'" \
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
  "beat TL.21-clean; expect-log 'reload lock stuck'; printf '{\"level\":\"error\",\"msg\":\"reload lock stuck\"}\n' >> '$LOG'; pass 'provoked on purpose'" \
  'lane_end'
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q 'TL ✓ TL.21-clean'; then
  ok "expect-log: a declared error record does not fail its beat"
else
  bad "expect-log" "rc=$RC" "$OUT"
fi

printf '{"level":"info","msg":"before the lane"}\n' >"$LOG"
LANE_PFM_LOG_FIXTURE="$LOG" run_lane expectlog_other \
  'lane_begin TL' \
  "beat TL.22-other; expect-log 'a different error'; printf '{\"level\":\"error\",\"msg\":\"reload lock stuck\"}\n' >> '$LOG'; pass 'x'" \
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
  'beat TL.30-live; target_live TL_CHAT; requires && pass "the chat is alive"' \
  "rm -f '$ROWS'" \
  'beat TL.31-gone; target_live TL_CHAT; requires && pass "MUST-NOT-HAPPEN"' \
  'beat TL.32-back; target_live TL_CHAT; requires && pass "the re-open brought it back"' \
  "rm -f '$ROWS'" \
  'beat TL.33-gone-again; target_live TL_CHAT; requires && pass "MUST-NOT-HAPPEN"' \
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
  'beat TL.34-gone; target_live TL_CHAT; requires && pass "MUST-NOT-HAPPEN"' \
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
  'beat TL.40-wait; target_live TL_CHAT; t0=$SECONDS; wait_last TL_CHAT NEVER 300; rc=$?;
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
  'beat TL.41-timeout; target_live TL_CHAT; wait_last TL_CHAT NEVER 1; rc=$?;
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
  'beat TL.42-seats; blocked "seats cc:1" "no second seat in this run"' \
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
  'beat TL.50-dead; target TL_CHAT; fail "nothing answered"' \
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

# ---- 22: a lane that ran ZERO beats is a named red (F6) -------------------

run_lane zerobeats \
  'lane_begin TL' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q 'TL ✗ ZERO-BEATS — the lane ran no beats at all'; then
  ok "F6: a lane with zero beats is a named red (ZERO-BEATS), not a silent pass"
else
  bad "zero beats" "rc=$RC" "$OUT"
fi

# ---- 23: a lane whose EVERY beat is blocked is a named red (F6) -----------

run_lane allblocked \
  'lane_begin TL' \
  'beat TL.60-b1; blocked "seats cc:1" "no second seat in this run"' \
  'beat TL.61-b2; blocked "seats cc:1" "no second seat in this run"' \
  'lane_end'
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -qF "TL ✗ ALL-BLOCKED — every one of this lane's 2 beat(s) was blocked"; then
  ok "F6: an all-blocked lane is a named red (ALL-BLOCKED), naming its beat count"
else
  bad "all blocked" "rc=$RC" "$OUT"
fi

# A lane that is a MIX of pass/blocked (not every beat blocked) must stay
# clean of the ALL-BLOCKED line — the guard is "every beat", never "any beat".
run_lane mixedblocked \
  'lane_begin TL' \
  'beat TL.62-ok; pass "held"' \
  'beat TL.63-blocked; blocked "seats cc:1" "no second seat in this run"' \
  'lane_end'
if ! printf '%s' "$OUT" | grep -q 'ALL-BLOCKED'; then
  ok "F6: a lane with at least one non-blocked beat is never ALL-BLOCKED"
else
  bad "mixed blocked wrongly flagged" "$OUT"
fi

# ---- 24: a read failure of a PRESENT log is a FAIL distinct from an empty
# slice (F13) — never the same "clean" answer -------------------------------

# The read is failed by a `tail` stub, not by chmod 000: permission bits deny
# nothing to root, which is what the fence runs as.
FAILBIN="$T/failbin"
mkdir -p "$FAILBIN"
printf '#!/bin/sh\nexit 1\n' >"$FAILBIN/tail"
chmod +x "$FAILBIN/tail"
READLOG="$T/pfm-readfail.jsonl"
printf '{"level":"info","msg":"before the lane"}\n' >"$READLOG"
LANE_PFM_LOG_FIXTURE="$READLOG" run_lane logreadfail \
  'lane_begin TL' \
  "beat TL.25-unreadable; PATH='$FAILBIN':\$PATH; pass 'the assertion held'" \
  'lane_end'
if [ "$RC" -ne 0 ] &&
  printf '%s' "$OUT" | grep -q 'activity-log sweep FAILED' &&
  ! printf '%s' "$OUT" | grep -q 'activity-log sweep SKIPPED'; then
  ok "F13: a read failure on a PRESENT log (tail cannot read it) is its own FAIL, distinct from an empty slice or the SKIPPED case"
else
  bad "log read failure" "rc=$RC" "$OUT"
fi

# ---- 25: with_restored — the crash-safe plant/restore contract (F4) -------

# 25a: SIGTERM mid-beat still restores the file via the exit trap.
printf 'original content\n' >"$T/wr-target.txt"
MARKER="$T/wr-marker"
rm -f "$MARKER"
cat >"$T/wr-interrupt.sh" <<SCRIPT
#!/usr/bin/env bash
. "$LIB"
with_restored "$T/wr-target.txt" || exit 9
printf 'mutated\n' >"$T/wr-target.txt"
: >"$MARKER"
sleep 30
SCRIPT
chmod +x "$T/wr-interrupt.sh"
"$T/wr-interrupt.sh" &
wr_pid=$!
waited=0
while [ ! -e "$MARKER" ] && [ "$waited" -lt 100 ]; do
  sleep 0.1
  waited=$((waited + 1))
done
kill -TERM "$wr_pid" 2>/dev/null
wait "$wr_pid" 2>/dev/null
wr_content="$(cat "$T/wr-target.txt" 2>/dev/null)"
if [ -e "$MARKER" ] && [ "$wr_content" = "original content" ]; then
  ok "with_restored: a SIGTERM mid-beat still restores the file via the composed exit trap"
else
  bad "with_restored interrupt" "marker=[$([ -e "$MARKER" ] && echo yes || echo no)] content=[$wr_content]"
fi

# 25b: a pre-existing "<path>.lane-backup" is a crashed prior run — restored
# onto the file at once, and NEVER overwritten with a fresh backup.
printf 'corrupted-by-crash\n' >"$T/wr-target2.txt"
printf 'crashed-original\n' >"$T/wr-target2.txt.lane-backup"
wr_out2="$(bash -c ". '$LIB'; with_restored '$T/wr-target2.txt'; echo RC=\$?" 2>&1)"
wr_content2="$(cat "$T/wr-target2.txt" 2>/dev/null)"
if printf '%s' "$wr_out2" | grep -q 'RC=1' &&
  printf '%s' "$wr_out2" | grep -q 'a prior run crashed mid-beat' &&
  [ "$wr_content2" = "crashed-original" ] && [ ! -e "$T/wr-target2.txt.lane-backup" ]; then
  ok "with_restored: a pre-existing backup is a crashed prior run — restored onto the file, never overwritten, and this call refuses"
else
  bad "with_restored pre-existing backup" "$wr_out2" "content=[$wr_content2]"
fi

# 25c: a backup-copy failure aborts BEFORE any mutation — the file is untouched.
mkdir -p "$T/wr-nowrite"
printf 'keepme\n' >"$T/wr-nowrite/target3.txt"
# A failing `cp` stub, not a read-only directory: root writes through chmod 555.
printf '#!/bin/sh\nexit 1\n' >"$FAILBIN/cp"
chmod +x "$FAILBIN/cp"
wr_out3="$(PATH="$FAILBIN:$PATH" bash -c ". '$LIB'; with_restored '$T/wr-nowrite/target3.txt'; echo RC=\$?" 2>&1)"
rm -f "$FAILBIN/cp"
wr_content3="$(cat "$T/wr-nowrite/target3.txt" 2>/dev/null)"
if printf '%s' "$wr_out3" | grep -q 'RC=1' &&
  printf '%s' "$wr_out3" | grep -q 'the backup copy failed' &&
  [ "$wr_content3" = "keepme" ] && [ ! -e "$T/wr-nowrite/target3.txt.lane-backup" ]; then
  ok "with_restored: a backup-copy failure aborts before any mutation — the caller's beat fails and the file is untouched"
else
  bad "with_restored backup failure" "$wr_out3" "content=[$wr_content3]"
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

shtest_end
