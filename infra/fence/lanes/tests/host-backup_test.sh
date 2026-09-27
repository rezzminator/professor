#!/usr/bin/env bash
set -uo pipefail
SUT="${HOST_BACKUP_SUT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)/host-backup.sh}"
export SHTEST_TAG=host-backup-test
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
missing=""
for tool in rsync sqlite3 sha256sum find; do command -v "$tool" >/dev/null 2>&1 || missing+=" $tool"; done
if command -v find >/dev/null 2>&1 && ! find . -maxdepth 0 -printf "" >/dev/null 2>&1; then
  echo "host-backup_test: CANNOT RUN — GNU find required" >&2
  exit 2
fi
if [ -n "$missing" ]; then echo "host-backup_test: CANNOT RUN — not on PATH:$missing" >&2; exit 2; fi
H="$T/home"; M="$T/managed"; D="$T/live"; Q="$T/quiet"
mkdir -p "$H/.claude/projects/p0" "$H/.cc/2/projects/p1" "$H/.local/share/pfm/install" "$H/.codex" "$M"
echo one >"$H/.claude/projects/p0/s.jsonl"
echo two >"$H/.cc/2/projects/p1/s.jsonl"
echo marker >"$H/.local/share/pfm/install/source-repo"
echo secret >"$H/.claude/.credentials.json"
echo secret >"$H/.codex/auth.json"
echo '{}' >"$M/pfm.json"
sqlite3 "$H/.cc/fleet.db" 'CREATE TABLE chat(id INTEGER); INSERT INTO chat VALUES(1);'
run() { HOME="$H" PFM_MANAGED_SETTINGS_DIR="$M" bash "$SUT" "$@" 2>&1; }
OUT=$(run "$D" live); RC=$?
if [ "$RC" -eq 0 ] && [ "$(tail -1 <<<"$OUT")" = "BACKUP OK $D" ] && [ -d "$D/home" ] && [ -f "$D/etc/claude-code/managed-settings.d/pfm.json" ] && [ -f "$D/manifest/sessions.sha256" ] && [ -f "$D/manifest/files.txt" ] && [ -f "$D/manifest/links.txt" ] && [ -f "$D/manifest/db.txt" ] && grep -qxF "home=$H" "$D/meta"; then ok "live backup layout and metadata"; else bad "live backup" "rc=$RC" "$OUT"; fi
if [ ! -e "$D/home/.claude/.credentials.json" ] && [ ! -e "$D/home/.codex/auth.json" ]; then ok "credentials excluded"; else bad "credentials excluded"; fi
OUT=$(run "$Q" quiet "$D"); RC=$?
if [ "$RC" -eq 0 ] && [ "$(tail -1 <<<"$OUT")" = "BACKUP OK $Q" ] && [ "$D/home/.claude/projects/p0/s.jsonl" -ef "$Q/home/.claude/projects/p0/s.jsonl" ]; then ok "quiet backup hard links unchanged file"; else bad "quiet backup" "rc=$RC" "$OUT"; fi
P=$(cd "$T" && pwd -P)
OUT=$(cd "$T" && run rel-live live); RC=$?
if [ "$RC" -eq 0 ] && [ "$(tail -1 <<<"$OUT")" = "BACKUP OK $P/rel-live" ] && grep -qx ok "$T/rel-live/manifest/db.txt"; then ok "relative DEST resolved before the manifest step"; else bad "relative DEST" "rc=$RC" "$OUT"; fi
OUT=$(cd "$T" && run rel-quiet quiet rel-live); RC=$?
if [ "$RC" -eq 0 ] && [ "$T/rel-live/home/.claude/projects/p0/s.jsonl" -ef "$T/rel-quiet/home/.claude/projects/p0/s.jsonl" ]; then ok "relative PREVIOUS hard links unchanged file"; else bad "relative PREVIOUS" "rc=$RC" "$OUT"; fi
N="$T/nodb-home"; mkdir -p "$N/.claude/projects/p0"; echo one >"$N/.claude/projects/p0/s.jsonl"
OUT=$(HOME="$N" PFM_MANAGED_SETTINGS_DIR="$M" bash "$SUT" "$T/nodb" live 2>&1); RC=$?
if [ "$RC" -eq 0 ] && grep -qx 'db_integrity_ok=0' <<<"$OUT" && [ "$(tail -1 <<<"$OUT")" = "BACKUP OK $T/nodb" ]; then ok "no database still reaches BACKUP OK"; else bad "no database" "rc=$RC" "$OUT"; fi
OUT=$(run "$T/bad-mode" other); RC=$?
if [ "$RC" -eq 2 ] && [ "$OUT" = 'mode must be live or quiet' ] && [ ! -e "$T/bad-mode" ]; then ok "bad mode refused before DEST"; else bad "bad mode" "rc=$RC" "$OUT"; fi
OUT=$(run /sys/host-backup-test live); RC=$?
if [ "$RC" -eq 1 ] && grep -q '^FAILED: create ' <<<"$OUT" && ! grep -q 'BACKUP OK' <<<"$OUT"; then ok "failed step named, no success"; else bad "failed step" "rc=$RC" "$OUT"; fi
shtest_end
