#!/usr/bin/env bash
# Self-test for scripts/rollback-guard.sh: make rollback must refuse a pfm.prev
# installed before a layout migration that was not rolled back, naming every
# such journal newest first with the replay steps; allow a pfm.prev installed
# after every journal; ignore rolled-back and install-only journals; count a
# pending journal and one from the same second; let FORCE=1 override; and
# report an unreadable journal as an ERROR, never as allowed.
#
# Each case builds a private HOME with real journal directories in the shape
# pfm/internal/installer/layout_journal.go writes.
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="$ROOT/scripts/rollback-guard.sh"
# shellcheck disable=SC2034 # read by shtest.sh
SHTEST_TAG=pfm-rollback-guard-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"

REPO=/src/clone
N=0

# newhome: a fresh HOME with an installed pfm and a pfm.prev stamped
# 2026-01-01T00:00:00Z; sets H and BINP.
newhome() {
  N=$((N + 1))
  H="$T/home$N"
  BINP="$H/.local/bin/pfm"
  mkdir -p "$H/.local/bin"
  printf 'current\n' > "$BINP"
  printf 'previous\n' > "$BINP.prev"
  touch -d '2026-01-01T00:00:00Z' "$BINP.prev"
}

# journal <id> <result> <row>...: writes a journal with one record per row.
journal() {
  local id=$1 result=$2 dir sep=""
  shift 2
  dir="$H/.local/state/pfm/migrations/$id"
  mkdir -p "$dir"
  {
    echo "["
    for row in "$@"; do
      printf '%s  {\n    "row": "%s",\n    "verdict": "move",\n    "source": "/src",\n    "destination": "/dst",\n    "backup": "",\n    "result": "%s"\n  }' "$sep" "$row" "$result"
      sep=$',\n'
    done
    echo
    echo "]"
  } > "$dir/journal.json"
}

run() { HOME="$H" bash "$SUT" "$BINP" "$REPO" 2>&1; }

# ---- refuses: two sealed layout journals after pfm.prev --------------------
newhome
journal 20260102T000000Z "done" config state
journal 20260103T000000Z "done" install database
out="$(run)"; rc=$?
want="rollback: REFUSED — $BINP.prev was installed before layout migration 20260103T000000Z, 20260102T000000Z, not rolled back; restoring it would run the previous pfm on defaults and fork the databases. In order:
  1. pfm install --rollback 20260103T000000Z
  2. pfm install --rollback 20260102T000000Z
  3. make -C $REPO/pfm rollback
Or run FORCE=1 make rollback to override."
if [ $rc -eq 1 ] && [ "$out" = "$want" ]; then
  ok "rollback-guard: refuses a pfm.prev older than two layout journals, newest first"
else
  bad "rollback-guard: expected the pinned refusal, exit 1" "$out (rc $rc)"
fi

# ---- bash 3.2 (macOS /bin/bash) has no mapfile: the order stays newest first
# The builtin is disabled, then the guard is sourced with its arguments.
out="$(HOME="$H" bash -c 'enable -n mapfile; . "$0" "$@"' "$SUT" "$BINP" "$REPO" 2>&1)"; rc=$?
if [ $rc -eq 1 ] && [ "$out" = "$want" ]; then
  ok "rollback-guard: without mapfile (bash 3.2) the refusal is still newest first"
else
  bad "rollback-guard: without mapfile expected the pinned refusal, exit 1" "$out (rc $rc)"
fi

# ---- force past the refusal ------------------------------------------------
out="$(FORCE=1 run)"; rc=$?
if [ $rc -eq 0 ] && [ "$out" = "rollback-guard: FORCED past refusal — 20260103T000000Z, 20260102T000000Z" ]; then
  ok "rollback-guard: FORCE=1 passes a refusal with the FORCED line"
else
  bad "rollback-guard: expected the FORCED line, exit 0" "$out (rc $rc)"
fi

# ---- a pfm.prev later than every journal ----------------------------------
newhome
journal 20260102T000000Z "done" config
touch -d '2026-01-05T00:00:00Z' "$BINP.prev"
out="$(run)"; rc=$?
if [ $rc -eq 0 ] && [ -z "$out" ]; then
  ok "rollback-guard: a pfm.prev installed after every journal passes silently"
else
  bad "rollback-guard: expected silent exit 0" "$out (rc $rc)"
fi

# ---- rolled-back journals do not count -------------------------------------
newhome
journal 20260102T000000Z "done" config
: > "$H/.local/state/pfm/migrations/20260102T000000Z/rolled-back"
out="$(run)"; rc=$?
if [ $rc -eq 0 ] && [ -z "$out" ]; then
  ok "rollback-guard: a rolled-back journal does not count"
else
  bad "rollback-guard: expected exit 0 past a rolled-back journal" "$out (rc $rc)"
fi

# ---- install-only journals do not count ------------------------------------
newhome
journal 20260102T000000Z "done" install install
out="$(run)"; rc=$?
if [ $rc -eq 0 ] && [ -z "$out" ]; then
  ok "rollback-guard: an install-only journal does not count"
else
  bad "rollback-guard: expected exit 0 past an install-only journal" "$out (rc $rc)"
fi

# ---- pending journals count ------------------------------------------------
newhome
journal 20260102T000000Z pending install config
out="$(run)"; rc=$?
if [ $rc -eq 1 ] && [[ "$out" == *"layout migration 20260102T000000Z, not rolled back"* ]]; then
  ok "rollback-guard: a pending layout journal counts"
else
  bad "rollback-guard: expected a refusal naming the pending journal" "$out (rc $rc)"
fi

# ---- the same second counts as before --------------------------------------
newhome
journal 20260101T000000Z "done" config
out="$(run)"; rc=$?
if [ $rc -eq 1 ] && [[ "$out" == *"layout migration 20260101T000000Z,"* ]]; then
  ok "rollback-guard: a journal from pfm.prev's own second counts"
else
  bad "rollback-guard: expected a refusal for the same-second journal" "$out (rc $rc)"
fi

# ---- an unreadable journal is an ERROR -------------------------------------
newhome
bad_dir="$H/.local/state/pfm/migrations/20260102T000000Z"
mkdir -p "$bad_dir/journal.json" # a directory: unreadable as a file, even for root
out="$(run)"; rc=$?
if [ $rc -eq 2 ] && [[ "$out" == "rollback-guard: ERROR — "*"$bad_dir/journal.json"* ]]; then
  ok "rollback-guard: an unreadable journal is an ERROR naming it, exit 2"
else
  bad "rollback-guard: expected an ERROR naming the journal, exit 2" "$out (rc $rc)"
fi
out="$(FORCE=1 run)"; rc=$?
if [ $rc -eq 0 ]; then
  ok "rollback-guard: FORCE=1 passes an unreadable journal"
else
  bad "rollback-guard: expected FORCE=1 to pass the ERROR, exit 0" "$out (rc $rc)"
fi

# ---- no journals, no pfm.prev ----------------------------------------------
newhome
out="$(run)"; rc=$?
if [ $rc -eq 0 ] && [ -z "$out" ]; then
  ok "rollback-guard: no migrations directory passes silently"
else
  bad "rollback-guard: expected silent exit 0 without migrations" "$out (rc $rc)"
fi
newhome
journal 20260102T000000Z "done" config
rm -f "$BINP.prev"
out="$(run)"; rc=$?
if [ $rc -eq 0 ] && [ -z "$out" ]; then
  ok "rollback-guard: no pfm.prev passes silently"
else
  bad "rollback-guard: expected silent exit 0 without pfm.prev" "$out (rc $rc)"
fi

shtest_end
