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
printf '%s\n' "$H/clone" >"$H/.local/share/pfm/install/source-repo"
# Every other path pfm install writes: the renudge link, the OpenCode config,
# the VS Code machine settings and the clone's migrated configs.
mkdir -p "$H/.local/bin" "$H/.config/opencode" "$H/.vscode-server/data/Machine" "$H/clone"
ln -s "$H/.local/share/pfm/install/bin/tmux-title-renudge" "$H/.local/bin/tmux-title-renudge"
echo '{}' >"$H/.config/opencode/opencode.jsonc"
echo '{}' >"$H/.vscode-server/data/Machine/settings.json"
echo '{}' >"$H/clone/pfm.config.json"
echo '{}' >"$H/clone/harvester.config.json"
echo tracked >"$H/clone/README.md"
echo secret >"$H/.claude/.credentials.json"
echo secret >"$H/.codex/auth.json"
echo '{}' >"$M/pfm.json"
sqlite3 "$H/.cc/fleet.db" 'CREATE TABLE chat(id INTEGER); INSERT INTO chat VALUES(1);'
run() { HOME="$H" PFM_MANAGED_SETTINGS_DIR="$M" bash "$SUT" "$@" 2>&1; }
OUT=$(run "$D" live); RC=$?
if [ "$RC" -eq 0 ] && [ "$(tail -1 <<<"$OUT")" = "BACKUP OK $D" ] && [ -d "$D/home" ] && [ -f "$D/etc/claude-code/managed-settings.d/pfm.json" ] && [ -f "$D/manifest/sessions.sha256" ] && [ -f "$D/manifest/files.txt" ] && [ -f "$D/manifest/links.txt" ] && [ -f "$D/manifest/db.txt" ] && grep -qxF "home=$H" "$D/meta"; then ok "live backup layout and metadata"; else bad "live backup" "rc=$RC" "$OUT"; fi
covered=1
for p in .local/bin/tmux-title-renudge .config/opencode/opencode.jsonc .vscode-server/data/Machine/settings.json clone/pfm.config.json clone/harvester.config.json; do
  [ -e "$D/home/$p" ] || [ -L "$D/home/$p" ] || { covered=0; bad "install-written path copied" "$p missing"; }
done
if [ "$covered" = 1 ] && [ ! -e "$D/home"/clone/README.md ]; then ok "every install-written path copied, the clone's tracked files not"; elif [ "$covered" = 1 ]; then bad "clone tracked files copied" "clone/README.md present"; fi
if [ ! -e "$D/home/.claude/.credentials.json" ] && [ ! -e "$D/home/.codex/auth.json" ]; then ok "credentials excluded"; else bad "credentials excluded"; fi
OUT=$(run "$Q" quiet "$D"); RC=$?
if [ "$RC" -eq 0 ] && [ "$(tail -1 <<<"$OUT")" = "BACKUP OK $Q" ] && [ "$D/home/.claude/projects/p0/s.jsonl" -ef "$Q/home/.claude/projects/p0/s.jsonl" ]; then ok "quiet backup hard links unchanged file"; else bad "quiet backup" "rc=$RC" "$OUT"; fi
P=$(cd "$T" && pwd -P)
OUT=$(cd "$T" && run rel-live live); RC=$?
if [ "$RC" -eq 0 ] && [ "$(tail -1 <<<"$OUT")" = "BACKUP OK $P/rel-live" ] && grep -qx ok "$T/rel-live/manifest/db.txt"; then ok "relative DEST resolved before the manifest step"; else bad "relative DEST" "rc=$RC" "$OUT"; fi
OUT=$(cd "$T" && run rel-quiet quiet rel-live); RC=$?
if [ "$RC" -eq 0 ] && [ "$T/rel-live/home/.claude/projects/p0/s.jsonl" -ef "$T/rel-quiet/home/.claude/projects/p0/s.jsonl" ]; then ok "relative PREVIOUS hard links unchanged file"; else bad "relative PREVIOUS" "rc=$RC" "$OUT"; fi
# No source-repo marker in this home: the backup runs as it did before the marker read.
N="$T/nodb-home"; mkdir -p "$N/.claude/projects/p0"; echo one >"$N/.claude/projects/p0/s.jsonl"
OUT=$(HOME="$N" PFM_MANAGED_SETTINGS_DIR="$M" bash "$SUT" "$T/nodb" live 2>&1); RC=$?
if [ "$RC" -eq 0 ] && grep -qx 'db_integrity_ok=0' <<<"$OUT" && [ "$(tail -1 <<<"$OUT")" = "BACKUP OK $T/nodb" ]; then ok "no database still reaches BACKUP OK"; else bad "no database" "rc=$RC" "$OUT"; fi
O="$T/out-home"; mkdir -p "$O/.claude/projects/p0" "$O/.local/share/pfm/install"; echo one >"$O/.claude/projects/p0/s.jsonl"
printf '%s\n' /opt/elsewhere >"$O/.local/share/pfm/install/source-repo"
OUT=$(HOME="$O" PFM_MANAGED_SETTINGS_DIR="$M" bash "$SUT" "$T/out" live 2>&1); RC=$?
if [ "$RC" -eq 0 ] && grep -qxF 'clone outside home, its configs not copied: /opt/elsewhere' <<<"$OUT" && [ ! -e "$T/out/home"/opt ] && [ "$(tail -1 <<<"$OUT")" = "BACKUP OK $T/out" ]; then ok "clone outside home named, its configs not copied"; else bad "clone outside home" "rc=$RC" "$OUT"; fi
# pfm no longer owns callmeter.db and an install never touches it, but an old
# chat may still write it: the backup names it and never copies it, or its -wal/-shm.
R="$T/retired-home"; mkdir -p "$R/.claude/projects/p0" "$R/.local/state/pfm"
echo one >"$R/.claude/projects/p0/s.jsonl"
for s in "" -wal -shm; do echo torn >"$R/.local/state/pfm/callmeter.db$s"; done
echo keep >"$R/.local/state/pfm/other.state"
OUT=$(HOME="$R" PFM_MANAGED_SETTINGS_DIR="$M" bash "$SUT" "$T/retired" live 2>&1); RC=$?
leaked=""
for s in "" -wal -shm; do [ -e "$T/retired/home/.local/state/pfm/callmeter.db$s" ] && leaked+=" callmeter.db$s"; done
if [ "$RC" -eq 0 ] && [ -z "$leaked" ] && [ -f "$T/retired/home/.local/state/pfm/other.state" ] && grep -qxF 'retired database, not copied: .local/state/pfm/callmeter.db' <<<"$OUT" && [ "$(tail -1 <<<"$OUT")" = "BACKUP OK $T/retired" ]; then ok "retired callmeter.db named, never copied"; else bad "retired callmeter.db" "rc=$RC leaked:$leaked" "$OUT"; fi
OUT=$(run "$T/no-retired" live); RC=$?
if [ "$RC" -eq 0 ] && ! grep -q 'retired database' <<<"$OUT"; then ok "no retired line without a callmeter.db"; else bad "no retired line" "rc=$RC" "$OUT"; fi
OUT=$(run "$T/bad-mode" other); RC=$?
if [ "$RC" -eq 2 ] && [ "$OUT" = 'mode must be live or quiet' ] && [ ! -e "$T/bad-mode" ]; then ok "bad mode refused before DEST"; else bad "bad mode" "rc=$RC" "$OUT"; fi
OUT=$(run /sys/host-backup-test live); RC=$?
if [ "$RC" -eq 1 ] && grep -q '^FAILED: create ' <<<"$OUT" && ! grep -q 'BACKUP OK' <<<"$OUT"; then ok "failed step named, no success"; else bad "failed step" "rc=$RC" "$OUT"; fi
# A non-Linux kernel is refused before DEST exists: the manifest step needs
# GNU find -printf and sha256sum, so a macOS run would otherwise fail midway.
K="$T/uname-darwin"; mkdir -p "$K"; printf '#!/bin/sh\necho Darwin\n' >"$K/uname"; chmod +x "$K/uname"
OUT=$(PATH="$K:$PATH" run "$T/darwin" live); RC=$?
if [ "$RC" -eq 1 ] && [ "$OUT" = "FAILED: platform — host-backup.sh runs on Linux only (GNU find -printf, sha256sum); this kernel is Darwin" ] && [ ! -e "$T/darwin" ]; then ok "non-Linux kernel refused before DEST"; else bad "non-Linux kernel" "rc=$RC" "$OUT"; fi
shtest_end
