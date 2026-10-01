#!/usr/bin/env bash
# host-backup.sh — a copy of everything `pfm install` can touch, taken
# before an install. infra/fence/host-rehearsal.sh BACKUP SCRATCH consumes it.
#
# Usage: host-backup.sh DEST live|quiet [PREVIOUS]
#   live   the fleet is running: each pfm database is copied with sqlite3's
#          .backup (one consistent file); everything else by rsync.
#   quiet  the fleet is down (the install window): databases are copied
#          byte-exact with their -wal/-shm; with PREVIOUS (an earlier backup),
#          unchanged files are hard-linked from it, so only changes are copied.
#
# Layout: DEST/home/<path relative to $HOME>, DEST/etc/<path under /etc>,
#         DEST/manifest/{sessions.sha256,files.txt,links.txt,db.txt}, DEST/meta.
# Excluded on purpose: account credentials (.credentials.json, .codex/auth.json)
# — install writes neither, and restoring an old one forks the login — and
# Codex transcripts/history/packages, the pfm Python venv and release trees,
# which install never touches.
#
# Broken state: each failing step prints "FAILED: {step}" and exits non-zero.
# "BACKUP OK" is printed only after the manifest ran and counted files.
set -euo pipefail

dest=${1:?usage: host-backup.sh DEST live|quiet [PREVIOUS]}
mode=${2:?usage: host-backup.sh DEST live|quiet [PREVIOUS]}
previous=${3:-}
case $mode in live | quiet) ;; *) echo "mode must be live or quiet" >&2; exit 2 ;; esac
H=$HOME
umask 077
fail() { echo "FAILED: $1" >&2; exit 1; }
# The manifest step needs GNU find -printf and sha256sum: refuse any other
# kernel before DEST exists, never midway through a copy.
kernel=$(uname -s) || fail "platform — uname -s did not answer"
[ "$kernel" = Linux ] || fail "platform — host-backup.sh runs on Linux only (GNU find -printf, sha256sum); this kernel is $kernel"

mkdir -p "$dest/home" "$dest/etc" "$dest/manifest" || fail "create $dest"
# Absolute from here: the manifest step runs from inside the copy, and rsync
# reads a relative --link-dest against the destination.
dest=$(cd "$dest" && pwd -P) || fail "resolve $dest"
if [ -n "$previous" ]; then previous=$(cd "$previous" && pwd -P) || fail "resolve $previous"; fi
chmod 700 "$dest"

dbs=(.cc/fleet.db .local/state/pfm/fleet.db)
excl=(
	--exclude=.credentials.json --exclude=.codex/auth.json
	--exclude=.local/state/pfm/harvest-python --exclude=.local/state/pfm/releases
	--exclude=.codex/sessions --exclude=.codex/archived_sessions --exclude=.codex/packages
	--exclude='.codex/*.sqlite*' --exclude=.codex/log
)
# pfm no longer owns callmeter.db and an install never touches it; an old chat
# may still write it, so a raw copy could be torn: named, never copied.
retired=.local/state/pfm/callmeter.db
excl+=(--exclude="$retired" --exclude="$retired-wal" --exclude="$retired-shm")
for db in "${dbs[@]}"; do excl+=(--exclude="$db" --exclude="$db-wal" --exclude="$db-shm"); done

rel=(.claude .claude.json .cc .config/pfm .local/share/pfm .local/state/pfm .codex .zshrc
	.local/bin/tmux-title-renudge .config/opencode/opencode.jsonc .vscode-server/data/Machine/settings.json)
# The clone's migrated configs; the clone's tracked files are the checkout's, not install's.
marker="$H/.local/share/pfm/install/source-repo"
if [ -f "$marker" ]; then
	clone=$(head -n 1 "$marker") || fail "read $marker"
	case $clone in
	"$H"/?*) rel+=("${clone#"$H"/}/pfm.config.json" "${clone#"$H"/}/harvester.config.json") ;;
	*) echo "clone outside home, its configs not copied: $clone" ;;
	esac
fi
for f in "$H"/.local/bin/pfm* "$H"/.local/bin/claude \
	"$H"/.config/systemd/user/pfm* "$H"/.config/systemd/user/*/pfm*; do
	[ -e "$f" ] || [ -L "$f" ] || continue
	rel+=("${f#"$H"/}")
done
if [ -e "$H/$retired" ]; then echo "retired database, not copied: $retired"; fi
sources=()
for r in "${rel[@]}"; do
	if [ -e "$H/$r" ] || [ -L "$H/$r" ]; then sources+=("$H/./$r"); else echo "absent, skipped: ~/$r"; fi
done

link=()
[ -n "$previous" ] && link=(--link-dest="$previous/home")
echo "== rsync ($mode) $(date -u +%T)"
set +e
rsync -aH --relative --delete "${link[@]}" "${excl[@]}" "${sources[@]}" "$dest/home/"
rc=$?
set -e
# 24 = a source file vanished mid-copy: expected while chats run, never when quiet.
if [ $rc -ne 0 ] && ! { [ $rc -eq 24 ] && [ "$mode" = live ]; }; then fail "rsync exit $rc"; fi

echo "== databases $(date -u +%T)"
for db in "${dbs[@]}"; do
	src="$H/$db" out="$dest/home/$db"
	rm -f "$out" "$out-wal" "$out-shm"
	[ -f "$src" ] || { echo "absent, skipped: ~/$db"; continue; }
	mkdir -p "$(dirname "$out")"
	if [ "$mode" = live ]; then
		# A live writer holds the lock between its commits; wait for it instead of failing at once.
		sqlite3 "file:$src?mode=ro" ".timeout 30000" ".backup '$out'" || fail "sqlite3 .backup ~/$db"
	else
		cp -p "$src" "$out" || fail "copy ~/$db"
		for s in -wal -shm; do [ -f "$src$s" ] && { cp -p "$src$s" "$out$s" || fail "copy ~/$db$s"; }; done
	fi
done

mkdir -p "$dest/etc/claude-code/managed-settings.d"
managed=${PFM_MANAGED_SETTINGS_DIR:-/etc/claude-code/managed-settings.d}/pfm.json
if [ -f "$managed" ]; then cp -p "$managed" "$dest/etc/claude-code/managed-settings.d/" || fail "copy $managed"; fi

echo "== manifest $(date -u +%T)"
m="$dest/manifest"
cd "$dest/home"
sess=()
for base in .claude .cc/*; do
	[ -L "$base" ] && continue
	for e in projects file-history tasks session-env; do
		[ -d "$base/$e" ] && [ ! -L "$base/$e" ] && sess+=("$base/$e")
	done
done
[ ${#sess[@]} -gt 0 ] || fail "no session dirs found in the copy"
find "${sess[@]}" -type f -print0 | sort -z | xargs -0 -r sha256sum >"$m/sessions.sha256" || fail "hash sessions"
find . -type f -printf '%s %P\n' | sort -k2 >"$m/files.txt" || fail "list files"
find . -type l -printf '%P -> %l\n' | sort >"$m/links.txt" || fail "list links"

# Counts come from a scratch copy so the backup's own bytes stay untouched.
tmp=$(mktemp -d "$dest/manifest/db-scratch.XXXXXX") || fail "create db scratch"
trap 'rm -rf "$tmp"' EXIT
: >"$m/db.txt"
for db in "${dbs[@]}"; do
	f="$dest/home/$db"
	[ -f "$f" ] || { echo "== $db ABSENT" >>"$m/db.txt"; continue; }
	c="$tmp/$(basename "$db")"
	rm -f "$c" "$c-wal" "$c-shm"
	cp "$f" "$c"
	for s in -wal -shm; do [ -f "$f$s" ] && cp "$f$s" "$c$s"; done
	{
		echo "== $db"
		sqlite3 "$c" "PRAGMA integrity_check;"
		sqlite3 "$c" "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name;" |
			while read -r t; do printf '%s %s\n' "$t" "$(sqlite3 "$c" "SELECT count(*) FROM \"$t\";")"; done
	} >>"$m/db.txt" || fail "count $db"
done

{
	echo "home=$H"
	echo "mode=$mode"
	echo "taken=$(date -u +%FT%TZ)"
	echo "previous=${previous:-none}"
	echo "excluded=${excl[*]}"
} >"$dest/meta"

files=$(wc -l <"$m/files.txt")
[ "$files" -gt 0 ] || fail "manifest counted no files"
echo "files=$files session_files=$(wc -l <"$m/sessions.sha256") links=$(wc -l <"$m/links.txt") size=$(du -sh "$dest" | cut -f1)"
echo "db_integrity_ok=$(grep -c '^ok$' "$m/db.txt" || true)"
echo "BACKUP OK $dest"
