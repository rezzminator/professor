# shellcheck shell=bash
# infra/fence/mkdir-lock.sh — one try at a mkdir lock directory holding its owner's pid (flock is not on
# the macOS host). Sourced by infra/fence/shared.sh (the shared container's start lock) and
# infra/fence/gate-history.sh (the ledger lock); each loops over it with its own wait and its own named
# timeout. Portable to bash 3.2.
#
#   mkdir_lock_try <lock dir> <owner pid>   # 0 taken; 1 held, MKDIR_LOCK_HOLDER its pid (or empty)
#
# The owner is whoever creates <lock dir>/pid, an exclusive create (noclobber: open with O_EXCL); mkdir
# only makes sure the directory is there. A mkdir binary is no test-and-set: uutils mkdir checks for
# the path, then creates it, and exits 0 when it loses that race to another creator, so two callers both
# saw "created". A pid file that exists but is still empty is its owner between the create and the write.
# A stale lock — its pid names a process that is gone, or its pid is empty or missing and it is over a
# minute old — is broken under the breaker file <lock dir>.break, created the same exclusive way, the
# staleness read again there: of two waiters that saw one dead holder, the second finds the first's
# fresh lock and leaves it, so a break never makes two holders. A breaker that died mid-break leaves a
# .break over a minute old, which the next try removes. The owner releases with `rm -rf <lock dir>`.
# BROKEN STATE: a lock that cannot be taken or broken stays held — the caller's wait ends in its own
# named timeout line, never a second holder.

_mkdir_lock_create() { # _mkdir_lock_create <file> <content>: 0 when this call created the file
  (
    set -C
    printf '%s\n' "$2" > "$1"
  ) 2> /dev/null
}

_mkdir_lock_claim() { # _mkdir_lock_claim <lock dir> <owner pid>: 0 when this call created the pid file
  mkdir "$1" 2> /dev/null
  _mkdir_lock_create "$1/pid" "$2"
}

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
  _mkdir_lock_claim "$lk" "$me" && return 0
  holder="$(cat "$lk/pid" 2> /dev/null || true)"
  MKDIR_LOCK_HOLDER="$holder"
  _mkdir_lock_stale "$lk" "$holder" || return 1
  if ! _mkdir_lock_create "$lk.break" "$me"; then
    [ -z "$(find "$lk.break" -maxdepth 0 -mmin +1 2> /dev/null)" ] || rm -rf "$lk.break"
    return 1
  fi
  now="$(cat "$lk/pid" 2> /dev/null || true)"
  if [ -d "$lk" ] && [ "$now" = "$holder" ] && _mkdir_lock_stale "$lk" "$now"; then rm -rf "$lk"; fi
  rm -f "$lk.break"
  _mkdir_lock_claim "$lk" "$me"
}
