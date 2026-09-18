#!/usr/bin/env bash
# lib.sh — the Tier B beat library: sourced by every lane (`lanes/<id>.sh`) and,
# later, by infra/demo/verify.sh, so the demo deck is a subset VIEW of the lanes
# and never a second implementation.
#
#   . "$(dirname "$0")/lib.sh"
#   lane_begin E1
#   need "a live chat" 'live_chat E1_MAIN' 'spawn_main' || lane_abort "no chat"
#   lane_reopen 'spawn_main'
#   beat E1.12-status C22 C23 ; spends cc:1 ; target_live E1_MAIN
#   requires E1.01-open-seat1 && { ... ; pass "status reported idle" ; }
#   lane_end
#
# Liveness — a beat that cannot assert anything without a live chat declares it
# with `target_live <chat>` (or `anchor_socket <socket>`, for the window where a
# `/reload --new` has taken the NAME off the live session). `requires` then
# costs one `pfm ls --tsv`: no live row and the beat is `blocked-by <the beat
# that last saw it alive>` in seconds instead of ✗ after a full wait, the lane
# spends its ONE `lane_reopen` command, and every later beat says the same. Both
# waits (`wait_last`, `wait_for`) abandon a dead anchor the same way, returning
# 2 with LANE_WAIT_WHY — a bound no deadline can outrun.
#
# Verdicts — exactly one per beat, and a beat left without one is named by the
# NEXT beat (never silently dropped):
#   pass [detail]        ✓ the assertion held
#   fail <why>           ✗ it did not; the raw pane dump lands in the lane log
#   known [gap-id]       the beat is a ledger entry (known-gaps.yml): counted
#                        apart, does not fail the lane
#   blocked <by> [why]   a declared precondition failed — a beat, the run's own
#                        shape (`seats cc:1`), or the target chat's liveness:
#                        never ✗ for somebody else's failure, never silent
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
# Every lane runs pfm at debug (Wave 4 ruling, 2026-09-18): the activity log
# (Wave 6) is judged per beat, and a thinner level would hide the records a
# slice is judged on. Forced over any caller's own level, never defaulted.
export PFM_LOG_LEVEL=debug

LANE_ID="" LANE_LOG="" LANE_TIMELINE="" LANE_T0=0
LANE_BEATS=0 LANE_FAILED=0 LANE_KNOWN=0 LANE_BLOCKED=0
LANE_CUR="" LANE_CUR_T0=0 LANE_CUR_IDS="" LANE_CUR_SEAT="none" LANE_CUR_OFFSET=""
# The offset the NEXT beat() must start its slice from instead of re-stamping
# to the log's current end — set by requires() right before _lane_reopen runs,
# so the re-open's own log writes land inside the following beat's slice
# rather than a gap no beat's sweep ever reads.
LANE_NEXT_OFFSET=""
LANE_CUR_EXPECT="" LANE_BAD_BEATS="" LANE_TARGET="" LANE_LOG_STATE="UNKNOWN"
# The liveness anchor (see `target_live` / `anchor_socket`), the beat that last
# saw it alive, the lane's one re-open command and whether it has been spent.
LANE_ANCHOR="" LANE_ALIVE_SEEN="" LANE_REOPEN="" LANE_REOPEN_DONE=0 LANE_WAIT_WHY=""

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

# gap_listed <beat> — 0 when the beat has a ledger entry. `gap_record` already
# names an unreadable ledger on stderr (KNOWN-GAPS-UNREADABLE); that message is
# let through here on purpose so every caller (gap_applies → pass()/known())
# still SEES the ledger-unreadable case instead of it collapsing into the same
# silent false as "not listed".
gap_listed() { gap_record "$1" >/dev/null; }

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
  LANE_ANCHOR=""
  LANE_WAIT_WHY=""
  if [ -n "$LANE_NEXT_OFFSET" ]; then
    # A re-open ran just before this beat opened — keep its slice starting
    # where the BLOCKED beat's did, so the re-open's own log writes are swept.
    LANE_CUR_OFFSET="$LANE_NEXT_OFFSET"
    LANE_NEXT_OFFSET=""
  elif [ -f "$LANE_PFM_LOG" ]; then
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

# target_live <chat> — the pane a ✗ dumps AND the chat this beat cannot assert
# anything without. `requires` refuses to run the body when that chat has no
# live row, and every wait abandons it the moment its row dies.
target_live() {
  LANE_TARGET="$1"
  LANE_ANCHOR="name:$1"
}

# anchor_socket <socket> — follow the chat by its tmux SOCKET instead of its
# name, for the window where the name cannot be trusted: `/reload --new` leaves
# the NAME on the old session id (now a resume row) and auto-names the fresh
# live session from its steer, while the socket is unchanged across the reboot.
anchor_socket() { LANE_ANCHOR="sock:$1"; }

# lane_reopen <cmd> — the ONE re-open this lane may spend when its anchored chat
# has died: run `need`-style after the first blocked beat, never twice.
lane_reopen() { LANE_REOPEN="$1"; }

_lane_alive_probe() {
  case "$LANE_ANCHOR" in
    name:*) [ -n "$(live_field "${LANE_ANCHOR#name:}" 11)" ] ;;
    sock:*) [ -n "$(socket_field "${LANE_ANCHOR#sock:}" 2)" ] ;;
    *) return 0 ;;
  esac
}

# lane_alive — 0 when this beat's anchor still has a live row. No anchor
# declared: nothing to judge, so 0 (a beat that never named a chat is not a beat
# this guard governs). Three probes a second apart, because a reboot in place
# empties the row for a moment and a single miss there is a false burial — still
# seconds, never the minutes a full wait costs.
lane_alive() {
  _lane_alive_probe && return 0
  sleep 1
  _lane_alive_probe && return 0
  sleep 1
  _lane_alive_probe
}

_lane_anchor_label() {
  case "$LANE_ANCHOR" in
    name:*) printf '%s' "${LANE_ANCHOR#name:}" ;;
    sock:*) printf 'the chat on socket %s' "${LANE_ANCHOR#sock:}" ;;
    *) printf '%s' "${LANE_TARGET:-<no target>}" ;;
  esac
}

_lane_mark_alive() { # remember which beat last saw this anchor alive
  [ -n "$LANE_ANCHOR" ] || return 0
  LANE_ALIVE_SEEN="$(printf '%s\n' "$LANE_ALIVE_SEEN" | grep -v "^$LANE_ANCHOR	" || true)
$LANE_ANCHOR	$LANE_CUR"
}

_lane_last_alive() { # the beat that last saw this anchor alive, or a named absence
  local seen
  seen="$(printf '%s\n' "$LANE_ALIVE_SEEN" | awk -F'\t' -v a="$LANE_ANCHOR" '$1 == a { b = $2 } END { print b }')"
  printf '%s' "${seen:-no beat in this lane ever saw it alive}"
}

# _lane_reopen — the lane's single re-open attempt, spent AFTER the blocked
# verdict is recorded so the beat itself still costs seconds.
_lane_reopen() {
  if [ -z "$LANE_REOPEN" ]; then
    _lane_log_only "   reopen: no re-open command declared (lane_reopen <cmd>) — $(_lane_anchor_label) stays gone for every later beat"
    return 0
  fi
  if [ "$LANE_REOPEN_DONE" -ne 0 ]; then
    _lane_log_only "   reopen: already attempted once this lane — not retried (a lane re-opens its chat once, never in a loop)"
    return 0
  fi
  LANE_REOPEN_DONE=1
  need "$(_lane_anchor_label) back alive" 'lane_alive' "$LANE_REOPEN" || true
}

expect-log() { # expect-log <pattern> — an error record this beat provokes ON PURPOSE
  LANE_CUR_EXPECT="$LANE_CUR_EXPECT
$1"
}

pane() { # pane <target> — the chat's pane as text, for an assertion
  pfm chat capture "$1" 2>&1
}

# _lane_tmux_dir — where this machine's tmux sockets live, first existing wins.
_lane_tmux_dir() {
  local d
  for d in "${PFM_TMUX_DIR:-}" "${TMUX_TMPDIR:-}" "/tmp/tmux-$(id -u)"; do
    [ -n "$d" ] && [ -d "$d" ] && { printf '%s' "$d"; return 0; }
  done
  return 1
}

# _lane_fleet_dump — the fleet's own listing plus a pane tail from EVERY live
# tmux socket. A chat that vanished must still leave bytes: "NO SOCKET" alone
# says only that the row is gone, never what the machine looked like when it went.
_lane_fleet_dump() {
  local dir sock rows
  rows="$(pfm ls --tsv 2>&1)"
  _lane_log_only "   raw-dump: pfm ls --tsv (the whole fleet at this beat's failure)"
  _lane_log_only "${rows:-   raw-dump: pfm ls --tsv printed nothing}"
  if ! dir="$(_lane_tmux_dir)"; then
    _lane_log_only "   raw-dump: no tmux socket directory found (tried \$PFM_TMUX_DIR, \$TMUX_TMPDIR, /tmp/tmux-$(id -u)) — no pane could be captured"
    return 0
  fi
  for sock in "$dir"/*; do
    [ -S "$sock" ] || [ -f "$sock" ] || continue
    _lane_log_only "   raw-dump: $(basename "$sock") capture-pane -e -p (tail 20)"
    if ! tmux -S "$sock" capture-pane -e -p 2>&1 | tail -20 >>"$LANE_LOG"; then
      _lane_log_only "   raw-dump: capture-pane FAILED on $(basename "$sock")"
    fi
  done
}

_lane_raw_dump() { # the failed beat's pane, escapes included (zellij's rule)
  local sock
  if [ -z "$LANE_TARGET" ]; then
    _lane_log_only "   raw-dump: no pane target declared for this beat (target <chat> declares one)"
    return 0
  fi
  sock="$(live_field "$LANE_TARGET" 11)"
  case "$LANE_ANCHOR" in sock:*) [ -n "$sock" ] || sock="${LANE_ANCHOR#sock:}" ;; esac
  if [ -z "$sock" ]; then
    _lane_log_only "   raw-dump: NO SOCKET for $LANE_TARGET — pfm ls --tsv carries no live row for it; the fleet listing and every tmux socket follow"
    _lane_fleet_dump
    return 0
  fi
  _lane_log_only "   raw-dump: tmux -S $sock capture-pane -e -p -S - ($LANE_TARGET)"
  if ! tmux -S "$sock" capture-pane -e -p -S - >>"$LANE_LOG" 2>&1; then
    _lane_log_only "   raw-dump: capture-pane FAILED on $sock (the server may be gone) — the fleet listing and every tmux socket follow"
    _lane_fleet_dump
  fi
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
  # A log that was PRESENT at lane start and cannot be swept now is "we failed
  # to look", never a clean log: the beat goes ✗ and says so on its own line.
  if [ "$verdict" != fail ] && [ "$LANE_LOG_STATE" = PRESENT ] &&
    { [ -z "$LANE_CUR_OFFSET" ] || [ ! -f "$LANE_PFM_LOG" ]; }; then
    verdict=fail glyph="✗"
    detail="$detail; activity-log sweep SKIPPED — $LANE_PFM_LOG gone or no offset recorded for this beat"
  fi
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

blocked() { # blocked <by-beat|condition> [why]
  _lane_close blocked "blocked" "blocked-by $1${2:+ — $2}"
}

# requires <beat…> — 0 when every named beat passed AND this beat's declared
# live chat still has a live row. Otherwise the CURRENT beat is closed
# `blocked-by <the first bad one | the beat that last saw the chat alive>` and 1
# is returned, so the caller's `requires X && { … }` body never runs and never
# reports ✗ for someone else's failure — nor spends minutes waiting on a chat
# that is already gone.
requires() {
  local b
  for b in "$@"; do
    case " $LANE_BAD_BEATS " in
      *" $b "*) blocked "$b"; return 1 ;;
    esac
  done
  [ -n "$LANE_ANCHOR" ] || return 0
  if lane_alive; then
    _lane_mark_alive
    return 0
  fi
  blocked "$(_lane_last_alive)" "$(_lane_anchor_label) has no live row in pfm ls --tsv — nothing to assert against"
  # blocked() just closed the beat and cleared LANE_CUR_OFFSET's owner; carry
  # this beat's own slice start forward so the re-open's log writes below land
  # in the NEXT beat's slice instead of a window no beat's sweep ever reads.
  LANE_NEXT_OFFSET="$LANE_CUR_OFFSET"
  _lane_reopen
  return 1
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

_rows_named() { # every row carrying this name, header dropped. Reads `pfm ls
  # -a --tsv`, never the default `--tsv` view: a killed row drops out of the
  # default view entirely (compose.go defaultEligible), so row_field/live_row
  # built on the default view could never read a kill back after it happened.
  pfm ls -a --tsv 2>/dev/null | awk -F'\t' -v n="$1" 'NR > 1 && $5 == n'
}

chat_row() { # chat_row <name> — the chat's row, the LIVE one when it has one
  local row
  row="$(live_row "$1")"
  if [ -n "$row" ]; then
    printf '%s\n' "$row"
    return 0
  fi
  _rows_named "$1" | head -1
}

# live_row <name> — the LIVE row carrying that name. A name is regularly held by
# one live row AND its own resume rows (`/reload --new` leaves one behind); a
# reader that takes the first match reads a dead conversation's fields.
live_row() { _rows_named "$1" | awk -F'\t' '$1 ~ /^live-/ { print; exit }'; }

# row_field <name> <column> — one field of that row. Columns (pfm ls --tsv
# header): 1 kind · 2 id · 3 project · 4 cwd · 5 name · 6 prompts · 7 size ·
# 8 activity_ns · 9 account · 10 killed · 11 socket.
row_field() { chat_row "$1" | awk -F'\t' -v c="$2" '{ print $c }'; }

# live_field <name> <column> — one field of the LIVE row.
live_field() { live_row "$1" | awk -F'\t' -v c="$2" '{ print $c }'; }

# socket_field <socket> <column> — one field of the live row ON that socket: the
# only handle that survives a `/reload --new`, which changes both the session id
# and the name while the tmux socket stays put.
socket_field() {
  pfm ls --tsv 2>/dev/null |
    awk -F'\t' -v s="$1" -v c="$2" 'NR > 1 && $1 ~ /^live-/ && $11 == s { print $c; exit }'
}

# _lane_wait_dead — 0 when this beat's anchored chat has died under a wait; sets
# LANE_WAIT_WHY. A wait with no anchor has nothing to abandon. TWO consecutive
# dead reads end the wait: one is a reboot in flight, two in a row is a chat
# nothing will bring back inside this wait.
LANE_WAIT_MISSES=0
_lane_wait_dead() {
  [ -n "$LANE_ANCHOR" ] || return 1
  if lane_alive; then
    LANE_WAIT_MISSES=0
    return 1
  fi
  LANE_WAIT_MISSES=$((LANE_WAIT_MISSES + 1))
  [ "$LANE_WAIT_MISSES" -ge 2 ] || return 1
  LANE_WAIT_WHY="$(_lane_anchor_label) has no live row (two consecutive reads) — the wait was abandoned rather than run to its deadline"
  return 0
}

# wait_last <chat> <needle> <secs> — polls the chat's LAST assistant message for
# a literal needle. 1 on timeout and 2 when the beat's anchored chat died first
# — each with its own LANE_WAIT_WHY, because "it never answered" and "there was
# nothing left to answer" are different findings.
wait_last() {
  local deadline=$(( $(_lane_now) + $3 ))
  LANE_WAIT_WHY="" LANE_WAIT_MISSES=0
  while [ "$(_lane_now)" -lt "$deadline" ]; do
    _lane_wait_dead && return 2
    pfm chat last "$1" 2>/dev/null | grep -qF -- "$2" && return 0
    sleep 5
  done
  LANE_WAIT_WHY="timed out after $3s waiting for '$2' from $1"
  return 1
}

# wait_for <secs> <cmd> — polls until cmd succeeds; 1 on timeout, 2 when the
# beat's anchored chat died first (never a silent pass: the caller names what it
# was waiting for, and LANE_WAIT_WHY names which of the two happened).
wait_for() {
  local secs="$1" deadline=$(( $(_lane_now) + $1 ))
  shift
  LANE_WAIT_WHY="" LANE_WAIT_MISSES=0
  while [ "$(_lane_now)" -lt "$deadline" ]; do
    _lane_wait_dead && return 2
    # shellcheck disable=SC2294 # the condition arrives as a shell string, by design
    eval "$@" >/dev/null 2>&1 && return 0
    sleep 3
  done
  # shellcheck disable=SC2034 # read by the lanes, which name the reason in their verdict
  LANE_WAIT_WHY="timed out after ${secs}s waiting for: $*"
  return 1
}
