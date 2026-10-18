# shellcheck shell=bash
# infra/fence/mkdir-lock.sh — one try at a mkdir lock directory holding its owner's pid (mkdir is
# atomic; flock is not on the macOS host). Sourced by infra/fence/shared.sh (the shared container's
# start lock), infra/fence/lanes/root.sh (the root build lock) and infra/fence/gate-history.sh (the
# ledger lock); each loops over it with its own wait and its own named timeout. Portable to bash 3.2.
#
#   mkdir_lock_try <lock dir> <owner pid>   # 0 taken; 1 held, MKDIR_LOCK_HOLDER its pid (or empty)
#
# A stale lock — its pid names a process that is gone, or it has no pid and is over a minute old — is
# broken under the breaker directory <lock dir>.break, the staleness read again there: of two waiters
# that saw one dead holder, the second finds the first's fresh lock and leaves it, so a break never
# makes two holders. A breaker that died mid-break leaves a .break over a minute old, which the next
# try removes. The owner releases with `rm -rf <lock dir>`.
# BROKEN STATE: a lock that cannot be taken or broken stays held — the caller's wait ends in its own
# named timeout line, never a second holder.

_mkdir_lock_stale() { # _mkdir_lock_stale <lock dir> <holder pid read>: 0 when the lock is stale
  if [ -n "$2" ]; then
    ! kill -0 "$2" 2> /dev/null
    return
  fi
  [ -n "$(find "$1" -maxdepth 0 -mmin +1 2> /dev/null)" ]
}

mkdir_lock_try() {
  local lk="$1" me="$2" holder now
  MKDIR_LOCK_HOLDER=
  if mkdir "$lk" 2> /dev/null; then
    printf '%s\n' "$me" > "$lk/pid"
    return 0
  fi
  holder="$(cat "$lk/pid" 2> /dev/null || true)"
  MKDIR_LOCK_HOLDER="$holder"
  _mkdir_lock_stale "$lk" "$holder" || return 1
  if ! mkdir "$lk.break" 2> /dev/null; then
    [ -z "$(find "$lk.break" -maxdepth 0 -mmin +1 2> /dev/null)" ] || rmdir "$lk.break" 2> /dev/null
    return 1
  fi
  now="$(cat "$lk/pid" 2> /dev/null || true)"
  if [ -d "$lk" ] && [ "$now" = "$holder" ] && _mkdir_lock_stale "$lk" "$now"; then rm -rf "$lk"; fi
  rmdir "$lk.break" 2> /dev/null
  if mkdir "$lk" 2> /dev/null; then
    printf '%s\n' "$me" > "$lk/pid"
    return 0
  fi
  return 1
}
