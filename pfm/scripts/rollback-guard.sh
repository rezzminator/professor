#!/usr/bin/env bash
# make rollback's first line: refuse to restore a pfm.prev that was installed
# before a host-layout migration that has not been rolled back. That previous
# pfm reads the pre-migration layout; run on the migrated host it falls back to
# defaults and forks the databases. The safe order is to replay the layout
# journals first (`pfm install --rollback {id}`, newest first), then restore.
#
# usage: rollback-guard.sh <installed-binary-path> <repo-dir>
#
# Proof: host-install's `cp -f` stamps {BIN}.prev's mtime at the swap that made
# it previous. A journal under $HOME/.local/state/pfm/migrations/{id}/ counts
# when its id is a UTC stamp at or after that mtime, it has journal.json, no
# rolled-back marker, and journal.json names a row other than `install` (the
# non-layout row). A pending journal counts.
#
# Outcomes: silent exit 0 when nothing counts (or no pfm.prev, or no
# migrations dir); the refusal on stderr, exit 1; `rollback-guard: ERROR — …`
# on stderr, exit 2, when a journal or the timestamp cannot be read. FORCE=1
# overrides both the refusal and the error, printing a FORCED line, exit 0.
set -uo pipefail

usage() {
  echo "usage: rollback-guard.sh <installed-binary-path> <repo-dir>" >&2
  exit 2
}

BIN_PATH="${1:-}"
REPO_DIR="${2:-}"
if [ -z "$BIN_PATH" ] || [ -z "$REPO_DIR" ]; then usage; fi

error() {
  echo "rollback-guard: ERROR — $1" >&2
  if [ "${FORCE:-}" = "1" ]; then
    echo "rollback-guard: FORCED past error — $1"
    exit 0
  fi
  exit 2
}

PREV="$BIN_PATH.prev"
MIGRATIONS="${HOME:?HOME unset}/.local/state/pfm/migrations"
[ -e "$PREV" ] || exit 0
[ -d "$MIGRATIONS" ] || exit 0
if [ ! -r "$MIGRATIONS" ] || [ ! -x "$MIGRATIONS" ]; then error "cannot read $MIGRATIONS"; fi

stamp="$(date -u -r "$PREV" +%Y%m%dT%H%M%SZ 2>&1)" || error "cannot read the mtime of $PREV: $stamp"

ids=()
for dir in "$MIGRATIONS"/*/; do
  [ -d "$dir" ] || continue
  dir="${dir%/}"
  id="${dir##*/}"
  [[ "$id" =~ ^[0-9]{8}T[0-9]{6}Z$ ]] || continue
  [ -e "$dir/journal.json" ] || continue
  [ -e "$dir/rolled-back" ] && continue
  [[ "$id" < "$stamp" ]] && continue
  content="$(cat -- "$dir/journal.json" 2>&1)" || error "cannot read $dir/journal.json: $content"
  layout="$(printf '%s\n' "$content" \
    | grep -oE '"row"[[:space:]]*:[[:space:]]*"[^"]*"' \
    | grep -vE '"install"$' || true)"
  [ -n "$layout" ] && ids+=("$id")
done

[ "${#ids[@]}" -eq 0 ] && exit 0

# A read loop, not mapfile: macOS runs this under bash 3.2, which lacks it.
sorted=()
while IFS= read -r id; do sorted+=("$id"); done < <(printf '%s\n' "${ids[@]}" | sort -r)
ids=("${sorted[@]}")
joined="$(printf '%s, ' "${ids[@]}")"
joined="${joined%, }"

if [ "${FORCE:-}" = "1" ]; then
  echo "rollback-guard: FORCED past refusal — $joined"
  exit 0
fi

{
  echo "rollback: REFUSED — $PREV was installed before layout migration $joined, not rolled back; restoring it would run the previous pfm on defaults and fork the databases. In order:"
  n=0
  for id in "${ids[@]}"; do
    n=$((n + 1))
    echo "  $n. pfm install --rollback $id"
  done
  echo "  $((n + 1)). make -C $REPO_DIR/pfm rollback"
  echo "Or run FORCE=1 make rollback to override."
} >&2
exit 1
