#!/usr/bin/env bash
# infra/fence/mkdir-lock.sh: one case per behaviour of mkdir_lock_try — a free lock taken with its
# owner's pid, a live holder kept and named, even when mkdir reports a lost creation race as success,
# a dead holder's lock broken, a pid-file-less directory taken, a fresh empty pid file kept, an old one
# broken, a stale breaker cleared, and two waiters that saw one dead holder never both holding the
# lock. Every lock lives under $T.
#   bash infra/fence/tests/mkdir-lock_test.sh
set -uo pipefail

FENCE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${MKDIR_LOCK_SUT:-$FENCE/mkdir-lock.sh}"
SHTEST_TAG=fence-mkdir-lock-test
# shellcheck source=/dev/null
. "$FENCE/../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "mkdir-lock_test: no mkdir-lock.sh at $SUT" >&2; exit 2; }
# shellcheck source=/dev/null
. "$SUT"

old() { TZ=UTC touch -t 202001010000.00 "$1"; } # old <path>: over a minute old
dead_pid() { # a pid that names no live process
  local p
  bash -c 'exit 0' & p=$!
  wait "$p"
  printf '%s\n' "$p"
}

L="$T/free.lock"
if mkdir_lock_try "$L" 4242 && [ "$(cat "$L/pid")" = 4242 ]; then ok 'a free lock is taken, its pid file naming the owner'
else bad 'free lock' "$(ls -la "$L" 2>&1)"; fi

sleep 30 &
live=$!
L="$T/live.lock"
mkdir "$L"; printf '%s\n' "$live" >"$L/pid"
if ! mkdir_lock_try "$L" 4343 && [ "$MKDIR_LOCK_HOLDER" = "$live" ] && [ "$(cat "$L/pid")" = "$live" ]; then
  ok 'a live holder keeps its lock, MKDIR_LOCK_HOLDER names it'
else bad 'live holder' "holder=$MKDIR_LOCK_HOLDER pid=$(cat "$L/pid" 2>&1)"; fi

# uutils mkdir checks for the path, then creates it, and exits 0 when another creator wins in between:
# this mkdir, first on PATH, answers every existing directory that way.
mkdir "$T/racing-mkdir"
printf '#!/bin/sh\n[ -d "$1" ] && exit 0\nexec %s "$@"\n' "$(command -v mkdir)" >"$T/racing-mkdir/mkdir"
chmod +x "$T/racing-mkdir/mkdir"
if ! (PATH="$T/racing-mkdir:$PATH" mkdir_lock_try "$L" 4343) && [ "$(cat "$L/pid")" = "$live" ]; then
  ok "a mkdir that reports a lost creation race as success never takes a live holder's lock"
else bad 'lost mkdir race' "pid=$(cat "$L/pid" 2>&1)"; fi

L="$T/dead.lock"
mkdir "$L"; dead_pid >"$L/pid"
if mkdir_lock_try "$L" 4444 && [ "$(cat "$L/pid")" = 4444 ] && [ ! -e "$L.break" ]; then
  ok "a dead holder's lock is broken and taken, no breaker left behind"
else bad 'dead holder' "pid=$(cat "$L/pid" 2>&1) break=$(ls -d "$L.break" 2>&1)"; fi

L="$T/bare.lock"
mkdir "$L"
if mkdir_lock_try "$L" 4545 && [ "$(cat "$L/pid")" = 4545 ]; then
  ok 'a lock directory with no pid file is taken: the pid file, not the directory, names the owner'
else bad 'bare directory' "$(ls -la "$L" 2>&1)"; fi

L="$T/fresh.lock"
mkdir "$L"; : >"$L/pid"
if ! mkdir_lock_try "$L" 4545 && [ -z "$MKDIR_LOCK_HOLDER" ] && [ ! -s "$L/pid" ]; then
  ok 'a fresh empty pid file (its owner between the create and the pid write) is kept'
else bad 'fresh empty pid file' "holder=$MKDIR_LOCK_HOLDER $(ls -la "$L" 2>&1)"; fi

L="$T/stale.lock"
mkdir "$L"; : >"$L/pid"; old "$L"
if mkdir_lock_try "$L" 4646 && [ "$(cat "$L/pid")" = 4646 ]; then ok 'an empty pid file over a minute old is broken and taken'
else bad 'old empty pid file' "$(ls -la "$L" 2>&1)"; fi

L="$T/breaker.lock"
mkdir "$L"; : >"$L.break"; dead_pid >"$L/pid"
if ! mkdir_lock_try "$L" 4747 && [ -f "$L.break" ]; then ok 'a fresh breaker (another waiter mid-break) is left alone'
else bad 'fresh breaker' "$(ls -la "$T" 2>&1)"; fi
old "$L.break"
mkdir_lock_try "$L" 4747 >/dev/null 2>&1
if [ ! -e "$L.break" ] && mkdir_lock_try "$L" 4747 && [ "$(cat "$L/pid")" = 4747 ]; then
  ok "a breaker over a minute old (a breaker that died) is removed, then the dead holder's lock is broken"
else bad 'stale breaker' "$(ls -la "$T" 2>&1)"; fi

# Two waiters both saw the dead holder: the first breaks and takes the lock; the second, which read the
# same dead pid before the break, re-reads it under the breaker, finds the first's live lock and keeps off.
L="$T/race.lock"
mkdir "$L"; gone="$(dead_pid)"; printf '%s\n' "$gone" >"$L/pid"
sleep 30 &
first=$!
mkdir_lock_try "$L" "$first"; rc1=$?
_mkdir_lock_create "$L.break" second
now="$(cat "$L/pid" 2>/dev/null)"
if [ "$now" = "$gone" ] && _mkdir_lock_stale "$L" "$now"; then rm -rf "$L"; fi # the second's re-check, its stale read
rm -f "$L.break"
if [ "$rc1" -eq 0 ] && [ "$(cat "$L/pid")" = "$first" ]; then ok "a second breaker's re-check under the breaker leaves the first's fresh lock"
else bad 'break re-check' "rc1=$rc1 pid=$(cat "$L/pid" 2>&1)"; fi
# Six waiters race one dead holder; a winner holds until released, so a loser can only take the lock
# through a wrong break. Counted once the losers have spent their tries.
bad_rounds=""
for round in 1 2 3 4 5 6 7 8; do
  L="$T/race$round.lock" W="$T/race$round.won" REL="$T/race$round.release"
  mkdir "$L"; dead_pid >"$L/pid"
  : >"$W"
  pids=()
  for i in 1 2 3 4 5 6; do
    bash -c '. "$1"; for ((n = 0; n < 50; n++)); do if mkdir_lock_try "$2" "$BASHPID"; then echo "$BASHPID" >>"$3"; for ((m = 0; m < 100; m++)); do [ -e "$4" ] && break; sleep 0.05; done; exit 0; fi; sleep 0.01; done; exit 0' _ "$SUT" "$L" "$W" "$REL" &
    pids+=($!)
  done
  for ((n = 0; n < 100; n++)); do [ -s "$W" ] && break; sleep 0.05; done
  sleep 1
  holders="$(wc -l <"$W" | tr -d ' ')"
  : >"$REL"
  for pid in "${pids[@]}"; do wait "$pid"; done
  [ "$holders" = 1 ] || bad_rounds="$bad_rounds round $round: $holders holder(s);"
done
if [ -z "$bad_rounds" ]; then ok 'six waiters racing one dead holder: exactly one holds the lock'
else bad 'racing waiters' "$bad_rounds"; fi

for p in "$live" "$first"; do
  ps -o args= -p "$p" 2>/dev/null | grep -qF 'sleep 30' && kill "$p" 2>/dev/null
done
shtest_end
