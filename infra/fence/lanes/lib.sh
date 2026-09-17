#!/usr/bin/env bash
# lib.sh — the Tier B beat library: sourced by every lane (`lanes/<id>.sh`) and,
# later, by infra/demo/verify.sh, so the demo deck is a subset VIEW of the lanes
# and never a second implementation.
#
#   . "$(dirname "$0")/lib.sh"
#   lane_begin E1
#   need "a live chat" 'live_chat E1_MAIN' 'spawn_main' || lane_abort "no chat"
#   beat E1.12-status C22 C23 ; spends cc:1 ; target E1_MAIN
#   requires E1.01-open-seat1 && { ... ; pass "status reported idle" ; }
#   lane_end
#
# Verdicts — exactly one per beat, and a beat left without one is named by the
# NEXT beat (never silently dropped):
#   pass [detail]        ✓ the assertion held
#   fail <why>           ✗ it did not; the raw pane dump lands in the lane log
#   known [gap-id]       the beat is a ledger entry (known-gaps.yml): counted
#                        apart, does not fail the lane
#   blocked <beat>       a declared precondition beat failed: never ✗, never silent
# `requires <beat>` is the guard that turns a failed precondition into `blocked`.
#
# The known-gap ledger is enforced here, not trusted: an entry without
# `expires:`, an entry past its `expires:`, and a listed beat that PASSES are
# each a red row — the third is the xfail(strict) rule the spec adopted, so the
# ledger can never rot into a coincidence detector. An `arch:`-scoped entry is a
# gap only on that architecture; anywhere else the beat must assert for real.
#
# Activity log (Wave 6, docs/dev/trains/testing-foundation/waves/6-activity-log):
# each beat snapshots the byte offset of <pfm home>/log/pfm.jsonl and, at its
# verdict, fails on any `"level":"error"` record in its own slice that no
# `expect-log <pattern>` declared — the slice is attached to the lane log. Wave 6
# has not landed: when the file is absent the lane footer and the run summary
# each say so BY NAME (`activity log: ABSENT (Wave 6 not landed) — log
# assertions not enforced`); absence is never rendered as a clean log.
#
# Artifacts, all under $LANE_OUT_DIR (run.sh copies them out of the container):
#   <lane>.log  <lane>.timeline.tsv  <lane>.row.tsv  <lane>.logstate  <lane>.seats
#
# BROKEN STATE: `lane_begin` without a writable $LANE_OUT_DIR exits 2 by name; a
# verdict called with no open beat prints `lane: <verb> with no open beat` and
# counts as a failure; a lane that dies mid-beat leaves no `.row.tsv`, which
# run.sh reports as `produced no result row`, never as a pass.
#
# bash 3.2 (macOS) compatible: no associative arrays, no mapfile, no ${x^^}.

LANE_LIB_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
: "${LANE_GAPS:=$LANE_LIB_DIR/known-gaps.yml}"
: "${LANE_STAMP:=$(date +%Y%m%d-%H%M%S)}"
: "${LANE_OUT_DIR:=${TMPDIR:-/tmp}/lanes/$LANE_STAMP}"
: "${LANE_MODE:=solo}"
: "${LANE_PRIOR:=}"
: "${LANE_SEATS:=cc:1}"
: "${LANE_PFM_LOG:=${PFM_HOME:-$HOME/.local/state/pfm}/log/pfm.jsonl}"
: "${LANE_TODAY:=$(date +%Y-%m-%d)}"

LANE_ID="" LANE_LOG="" LANE_TIMELINE="" LANE_T0=0
LANE_BEATS=0 LANE_FAILED=0 LANE_KNOWN=0 LANE_BLOCKED=0
LANE_CUR="" LANE_CUR_T0=0 LANE_CUR_IDS="" LANE_CUR_SEAT="none" LANE_CUR_OFFSET=""
LANE_CUR_EXPECT="" LANE_BAD_BEATS="" LANE_TARGET="" LANE_LOG_STATE="UNKNOWN"

# ─── plumbing ───────────────────────────────────────────────────────────────

one_line() { printf '%s' "$1" | tr '\n' ' ' | tr -s ' ' | cut -c1-400; }

_lane_say() { # one line to the operator AND to the lane log
  printf '%s\n' "$1"
  [ -n "$LANE_LOG" ] && printf '%s\n' "$1" >>"$LANE_LOG"
  return 0
}

_lane_log_only() { [ -n "$LANE_LOG" ] && printf '%s\n' "$1" >>"$LANE_LOG"; return 0; }

_lane_now() { date +%s; }

# ─── the known-gap ledger ───────────────────────────────────────────────────

# gap_record <beat> — prints "key<TAB>value" for that ledger entry; exit 1 when
# the beat is not listed at all (absence and error are different exits: a
# missing ledger FILE is exit 2 with its own line).
gap_record() {
  if [ ! -f "$LANE_GAPS" ]; then
    echo "lane: KNOWN-GAPS-UNREADABLE — $LANE_GAPS does not exist" >&2
    return 2
  fi
  awk -v want="$1" '
    function trim(s) { gsub(/^[ \t]+|[ \t]+$/, "", s); gsub(/^"|"$/, "", s); return s }
    /^[ \t]*-[ \t]*beat:/ {
      inrec = (trim(substr($0, index($0, ":") + 1)) == want)
      if (inrec) { found = 1; print "beat\t" want }
      next
    }
    inrec && /^[ \t]*[a-z_]+:/ {
      key = trim(substr($0, 1, index($0, ":") - 1))
      print key "\t" trim(substr($0, index($0, ":") + 1))
      next
    }
    END { exit(found ? 0 : 1) }
  ' "$LANE_GAPS"
}

gap_field() { # gap_field <beat> <key> — the value, or empty
  gap_record "$1" 2>/dev/null | awk -F'\t' -v k="$2" '$1 == k { print $2; exit }'
}

gap_listed() { gap_record "$1" >/dev/null 2>&1; }

# gap_applies <beat> — 0 when the ledger entry governs THIS host (an entry with
# `arch:` only governs that architecture; elsewhere the beat must assert for real).
gap_applies() {
  local arch
  gap_listed "$1" || return 1
  arch="$(gap_field "$1" arch)"
  [ -z "$arch" ] && return 0
  [ "$arch" = "$(uname -s | tr 'A-Z' 'a-z')-$(uname -m)" ] && return 0
  return 1
}

# gaps_validate — every entry needs `expires:` and must not be past it (Wave 8's
# law). Prints one named red line per offender; exit 1 when any fired.
gaps_validate() {
  local beat expires bad=0
  if [ ! -f "$LANE_GAPS" ]; then
    echo "known-gaps: UNREADABLE — $LANE_GAPS does not exist (the ledger cannot be judged)" >&2
    return 2
  fi
  for beat in $(awk -F'beat:' '/^[ \t]*-[ \t]*beat:/ { gsub(/[ \t"]/, "", $2); print $2 }' "$LANE_GAPS"); do
    expires="$(gap_field "$beat" expires)"
    if [ -z "$expires" ]; then
      echo "known-gaps: ✗ $beat — no expires: (an entry without an expiry is forbidden)" >&2
      bad=1
    elif ! expr "$expires" : '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]$' >/dev/null; then
      echo "known-gaps: ✗ $beat — expires: '$expires' is not YYYY-MM-DD" >&2
      bad=1
    elif [ "$expires" \< "$LANE_TODAY" ]; then
      echo "known-gaps: ✗ $beat — expired $expires (today $LANE_TODAY): fix the gap or re-date the entry" >&2
      bad=1
    fi
  done
  [ "$bad" -eq 0 ]
}

# ─── the lane ───────────────────────────────────────────────────────────────

lane_begin() { # lane_begin <lane-id>
  LANE_ID="$1"
  mkdir -p "$LANE_OUT_DIR" 2>/dev/null || {
    echo "lane: OUT-DIR-UNWRITABLE — $LANE_OUT_DIR could not be created" >&2
    exit 2
  }
  LANE_LOG="$LANE_OUT_DIR/$LANE_ID.log"
  LANE_TIMELINE="$LANE_OUT_DIR/$LANE_ID.timeline.tsv"
  : >"$LANE_LOG" || { echo "lane: OUT-DIR-UNWRITABLE — cannot write $LANE_LOG" >&2; exit 2; }
  printf 'lane\tbeat\tt_plus_s\tverdict\tdur_s\tseat\tlandscape_ids\tdetail\n' >"$LANE_TIMELINE"
  : >"$LANE_OUT_DIR/$LANE_ID.seats"
  LANE_T0="$(_lane_now)"
  LANE_BEATS=0 LANE_FAILED=0 LANE_KNOWN=0 LANE_BLOCKED=0 LANE_BAD_BEATS=""
  if [ -f "$LANE_PFM_LOG" ]; then LANE_LOG_STATE=PRESENT; else LANE_LOG_STATE=ABSENT; fi
  printf '%s\n' "$LANE_LOG_STATE" >"$LANE_OUT_DIR/$LANE_ID.logstate"
  _lane_say "$LANE_ID · start · mode $LANE_MODE${LANE_PRIOR:+ · after $LANE_PRIOR} · seats $LANE_SEATS · $(date -u +%H:%M:%SZ)"
  _lane_log_only "$LANE_ID · activity log $LANE_PFM_LOG: $LANE_LOG_STATE"
}

lane_abort() { # lane_abort <why> — a prelude that could not build its preconditions
  _lane_say "$LANE_ID ✗ PRELUDE — $1"
  LANE_FAILED=$((LANE_FAILED + 1))
  LANE_BEATS=$((LANE_BEATS + 1))
  _lane_timeline_row "PRELUDE" "fail" 0 none "" "$1"
  lane_end
  exit 1
}

_lane_timeline_row() { # <beat> <verdict> <dur> <seat> <ids> <detail>
  [ -n "$LANE_TIMELINE" ] || return 0
  printf '%s\t%s\tt+%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$LANE_ID" "$1" "$(( $(_lane_now) - LANE_T0 ))" "$2" "$3" "$4" "$5" "$(one_line "$6")" \
    >>"$LANE_TIMELINE"
}

lane_end() {
  local wall
  [ -n "$LANE_CUR" ] && fail "beat left open — the lane reached its end with no verdict"
  wall=$(( $(_lane_now) - LANE_T0 ))
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$LANE_ID" "$wall" "$LANE_BEATS" "$LANE_FAILED" "$LANE_KNOWN" "$LANE_BLOCKED" \
    >"$LANE_OUT_DIR/$LANE_ID.row.tsv"
  [ "$LANE_LOG_STATE" = ABSENT ] &&
    _lane_say "$LANE_ID · activity log: ABSENT (Wave 6 not landed) — log assertions not enforced"
  _lane_say "$LANE_ID · end · ${wall}s · $LANE_BEATS beats · $LANE_FAILED failed · $LANE_KNOWN known · $LANE_BLOCKED blocked"
  [ "$LANE_FAILED" -eq 0 ]
}

# ─── beats ──────────────────────────────────────────────────────────────────

beat() { # beat <id> [landscape-ids…]
  local id="$1"
  shift
  if [ -n "$LANE_CUR" ]; then
    local orphan="$LANE_CUR"
    LANE_CUR=""
    LANE_BEATS=$((LANE_BEATS + 1))
    LANE_FAILED=$((LANE_FAILED + 1))
    LANE_BAD_BEATS="$LANE_BAD_BEATS $orphan"
    _lane_say "$LANE_ID ✗ $orphan — beat left open (no pass/fail/known/blocked before $id)"
    _lane_timeline_row "$orphan" "fail" 0 none "" "beat left open"
  fi
  LANE_CUR="$id"
  LANE_CUR_T0="$(_lane_now)"
  LANE_CUR_IDS="$*"
  LANE_CUR_SEAT=none
  LANE_CUR_EXPECT=""
  LANE_TARGET=""
  if [ -f "$LANE_PFM_LOG" ]; then
    LANE_CUR_OFFSET="$(wc -c <"$LANE_PFM_LOG" 2>/dev/null | tr -d ' ')"
    [ -n "$LANE_CUR_OFFSET" ] || LANE_CUR_OFFSET=0
  else
    LANE_CUR_OFFSET=""
  fi
  _lane_log_only "── $id · ids:${LANE_CUR_IDS:-none} · t+$(( LANE_CUR_T0 - LANE_T0 ))s"
  return 0
}

spends() { # spends <seat> — the seat this beat's quota comes out of
  LANE_CUR_SEAT="$1"
  printf '%s\t%s\n' "${LANE_CUR:-<no beat>}" "$1" >>"$LANE_OUT_DIR/$LANE_ID.seats"
}

target() { # target <chat> — the pane a failed verdict dumps raw
  LANE_TARGET="$1"
}

expect-log() { # expect-log <pattern> — an error record this beat provokes ON PURPOSE
  LANE_CUR_EXPECT="$LANE_CUR_EXPECT
$1"
}

pane() { # pane <target> — the chat's pane as text, for an assertion
  pfm chat capture "$1" 2>&1
}

_lane_raw_dump() { # the failed beat's pane, escapes included (zellij's rule)
  local sock
  if [ -z "$LANE_TARGET" ]; then
    _lane_log_only "   raw-dump: no pane target declared for this beat (target <chat> declares one)"
    return 0
  fi
  sock="$(pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$LANE_TARGET" '$5 == n { print $11; exit }')"
  if [ -z "$sock" ]; then
    _lane_log_only "   raw-dump: NO SOCKET for $LANE_TARGET — pfm ls --tsv carries no row for it"
    return 0
  fi
  _lane_log_only "   raw-dump: tmux -S $sock capture-pane -e -p -S - ($LANE_TARGET)"
  tmux -S "$sock" capture-pane -e -p -S - >>"$LANE_LOG" 2>&1 ||
    _lane_log_only "   raw-dump: capture-pane FAILED on $sock (the server may be gone)"
}

# _lane_log_slice — the activity-log records written during this beat. Prints
# the unexpected `"level":"error"` ones; returns 1 when any exist.
_lane_log_slice() {
  local slice errors pattern kept
  [ -n "$LANE_CUR_OFFSET" ] || return 0
  [ -f "$LANE_PFM_LOG" ] || return 0
  slice="$(tail -c "+$((LANE_CUR_OFFSET + 1))" "$LANE_PFM_LOG" 2>/dev/null)"
  [ -n "$slice" ] || return 0
  errors="$(printf '%s\n' "$slice" | grep '"level":"error"' || true)"
  [ -n "$errors" ] || return 0
  kept="$errors"
  # A here-doc, not a pipe: the loop must run in THIS shell or the filtered
  # result dies with the subshell (and every beat would read as clean).
  while IFS= read -r pattern; do
    [ -n "$pattern" ] || continue
    kept="$(printf '%s\n' "$kept" | grep -v -- "$pattern" || true)"
  done <<EOF
$LANE_CUR_EXPECT
EOF
  [ -n "$kept" ] || return 0
  printf '%s\n' "$kept"
  return 1
}

_lane_close() { # _lane_close <verdict> <glyph> <detail>
  local verdict="$1" glyph="$2" detail="$3" dur unexpected
  if [ -z "$LANE_CUR" ]; then
    _lane_say "$LANE_ID ✗ (no beat) — $verdict called with no open beat: $detail"
    LANE_FAILED=$((LANE_FAILED + 1))
    LANE_BEATS=$((LANE_BEATS + 1))
    return 1
  fi
  dur=$(( $(_lane_now) - LANE_CUR_T0 ))
  # An unexpected error record turns any non-✗ verdict into ✗ (the spec's law:
  # a beat passes on its asserted result AND a clean activity log).
  if [ "$verdict" != fail ]; then
    unexpected="$(_lane_log_slice)" || {
      _lane_log_only "   activity log: unexpected error record(s) in this beat's slice:"
      _lane_log_only "$unexpected"
      verdict=fail glyph="✗"
      detail="$detail; activity log carried $(printf '%s\n' "$unexpected" | grep -c . ) unexpected error record(s) — slice in $LANE_LOG"
    }
  fi
  LANE_BEATS=$((LANE_BEATS + 1))
  case "$verdict" in
    pass) ;;
    fail) LANE_FAILED=$((LANE_FAILED + 1)); LANE_BAD_BEATS="$LANE_BAD_BEATS $LANE_CUR" ;;
    known) LANE_KNOWN=$((LANE_KNOWN + 1)) ;;
    blocked) LANE_BLOCKED=$((LANE_BLOCKED + 1)); LANE_BAD_BEATS="$LANE_BAD_BEATS $LANE_CUR" ;;
  esac
  _lane_say "$LANE_ID $glyph $LANE_CUR — $detail (${dur}s)"
  _lane_timeline_row "$LANE_CUR" "$verdict" "$dur" "$LANE_CUR_SEAT" "$LANE_CUR_IDS" "$detail"
  [ "$verdict" = fail ] && _lane_raw_dump
  LANE_CUR=""
  [ "$verdict" != fail ]
}

pass() { # pass [detail]
  local beat="$LANE_CUR"
  if [ -n "$beat" ] && gap_applies "$beat"; then
    _lane_close fail "✗" "known-gap now passes — remove the entry from $(basename "$LANE_GAPS") (${1:-assertion held})"
    return 1
  fi
  _lane_close pass "✓" "${1:-asserted}"
}

fail() { # fail <why>
  _lane_close fail "✗" "${1:-no reason given}"
}

known() { # known [gap-id] — defaults to the current beat, the ledger's key
  local gap="${1:-$LANE_CUR}" expires arch
  if ! gap_listed "$gap"; then
    _lane_close fail "✗" "known '$gap' is not an entry in $(basename "$LANE_GAPS") — a gap is ledgered or it is a failure"
    return 1
  fi
  expires="$(gap_field "$gap" expires)"
  if [ -z "$expires" ]; then
    _lane_close fail "✗" "known-gap $gap has no expires: — every entry needs one"
    return 1
  fi
  if [ "$expires" \< "$LANE_TODAY" ]; then
    _lane_close fail "✗" "known-gap $gap expired $expires (today $LANE_TODAY)"
    return 1
  fi
  arch="$(gap_field "$gap" arch)"
  if [ -n "$arch" ] && ! gap_applies "$gap"; then
    _lane_close fail "✗" "known-gap $gap is scoped to $arch; this host is $(uname -s | tr 'A-Z' 'a-z')-$(uname -m) — assert it for real here"
    return 1
  fi
  _lane_close known "known" "$(gap_field "$gap" why) [owner $(gap_field "$gap" owner) · expires $expires]"
}

blocked() { # blocked <by-beat>
  _lane_close blocked "blocked" "blocked-by $1"
}

# requires <beat…> — 0 when every named beat passed. Otherwise the CURRENT beat
# is closed `blocked-by <first bad one>` and 1 is returned, so the caller's
# `requires X && { … }` body never runs and never reports ✗ for someone else's
# failure.
requires() {
  local b
  for b in "$@"; do
    case " $LANE_BAD_BEATS " in
      *" $b "*) blocked "$b"; return 1 ;;
    esac
  done
  return 0
}

# need <name> <check-cmd> <make-cmd> — a lane's own precondition: satisfied when
# the sequence already built it, made when the lane runs solo, UNMET by name
# when the make command cannot build it.
need() {
  local name="$1" check="$2" make="$3" out rc
  if eval "$check" >/dev/null 2>&1; then
    _lane_say "$LANE_ID need: $name · satisfied (no-op)"
    return 0
  fi
  _lane_say "$LANE_ID need: $name · absent — making it"
  out="$(eval "$make" 2>&1)"
  rc=$?
  _lane_log_only "$(one_line "$out")"
  if [ "$rc" -ne 0 ]; then
    _lane_say "$LANE_ID need: $name · UNMET — the make command exited $rc: $(one_line "$out")"
    return 1
  fi
  if eval "$check" >/dev/null 2>&1; then
    _lane_say "$LANE_ID need: $name · made"
    return 0
  fi
  _lane_say "$LANE_ID need: $name · UNMET — the make command exited 0 but the check still fails: $(one_line "$out")"
  return 1
}

# ─── shared assertions every lane uses ──────────────────────────────────────

live_chat() { pfm ls --plain 2>/dev/null | grep -q "^● $1 "; }

chat_row() { # chat_row <name> — the chat's `pfm ls --tsv` row, or nothing
  pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$1" '$5 == n { print; exit }'
}

# row_field <name> <column> — one field of that row. Columns (pfm ls --tsv
# header): 1 kind · 2 id · 3 project · 4 cwd · 5 name · 6 prompts · 7 size ·
# 8 activity_ns · 9 account · 10 killed · 11 socket.
row_field() { chat_row "$1" | awk -F'\t' -v c="$2" '{ print $c }'; }

# wait_last <chat> <needle> <secs> — polls the chat's LAST assistant message for
# a literal needle. 1 on timeout, so the caller can name what never arrived.
wait_last() {
  local deadline=$(( $(_lane_now) + $3 ))
  while [ "$(_lane_now)" -lt "$deadline" ]; do
    pfm chat last "$1" 2>/dev/null | grep -qF -- "$2" && return 0
    sleep 5
  done
  return 1
}

# wait_for <secs> <cmd> — polls until cmd succeeds; 1 on timeout (never a silent
# pass: the caller names what it was waiting for).
wait_for() {
  local deadline=$(( $(_lane_now) + $1 ))
  shift
  while [ "$(_lane_now)" -lt "$deadline" ]; do
    # shellcheck disable=SC2294 # the condition arrives as a shell string, by design
    eval "$@" >/dev/null 2>&1 && return 0
    sleep 3
  done
  return 1
}
