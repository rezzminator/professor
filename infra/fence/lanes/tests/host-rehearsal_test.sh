#!/usr/bin/env bash
# Fixture-driven tests for infra/fence/host-rehearsal.sh — a fixture-sized
# backup (the backup.sh layout) under the scratch jail, and stubs for docker,
# make and pfm. The docker stub plays the pfm-dev image: `run` records its argv
# and links each mount target it can create onto its source; `exec … bash -lc`
# runs the command locally under the recorded -e environment, so the script's
# step logic runs for real. No real docker, no real home, no real /etc.
#
#   bash infra/fence/lanes/tests/host-rehearsal_test.sh
#   HOST_REHEARSAL_SUT=/tmp/mutated.sh bash …/host-rehearsal_test.sh   # red-first
set -uo pipefail

SUT="${HOST_REHEARSAL_SUT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)/host-rehearsal.sh}"
export SHTEST_TAG=host-rehearsal-test
# shellcheck source=/dev/null
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "host-rehearsal_test: no host-rehearsal.sh at $SUT" >&2; exit 2; }
# host-rehearsal.sh is a host-only script (docker, sqlite3, rsync on the host);
# the pfm-dev image carries neither sqlite3 nor rsync, so there it cannot run.
missing=""
for tool in sqlite3 rsync sha256sum; do command -v "$tool" >/dev/null 2>&1 || missing+=" $tool"; done
if [ -n "$missing" ]; then
  echo "host-rehearsal_test: SKIPPED — not on PATH:$missing (host-only script; run this test on the host)" >&2
  exit 0
fi

BIN="$T/bin"
mkdir -p "$BIN"

# ---- fixture: a home, then a backup of it in the backup.sh layout ----------
FH="$T/fakehome" # the home= path; freed after the backup so docker's stub can mount onto it
mk_home() {
  rm -rf "$FH"
  mkdir -p "$FH/.claude/projects/p0" "$FH/.cc/2/projects/p1" "$FH/.local/share/pfm/install" \
    "$FH/.local/state/pfm" "$FH/.local/bin"
  echo a >"$FH/.claude/projects/p0/a.jsonl"
  echo s1 >"$FH/.cc/2/projects/p1/s1.jsonl"
  echo s2 >"$FH/.cc/2/projects/p1/s2.jsonl"
  chmod 600 "$FH/.cc/2/projects/p1/s2.jsonl"
  ln -s ../.claude/projects/p0 "$FH/.cc/p0-link"
  printf '#!/bin/sh\necho old\n' >"$FH/.local/bin/pfm"
  chmod 755 "$FH/.local/bin/pfm"
  printf '%s\n' "$FH/.professor" >"$FH/.local/share/pfm/install/source-repo"
  sqlite3 "$FH/.cc/fleet.db" "CREATE TABLE chat(id INTEGER); INSERT INTO chat VALUES(1),(2),(3);
    CREATE TABLE swap_event(id INTEGER); INSERT INTO swap_event VALUES(1),(2);
    CREATE TABLE hidden(id INTEGER); INSERT INTO hidden VALUES(1);"
}
mk_backup() { # mk_backup DEST — backup.sh's layout, manifest built its way
  local d=$1
  mkdir -p "$d/home" "$d/etc/claude-code/managed-settings.d" "$d/manifest"
  rsync -aH "$FH/" "$d/home/"
  echo '{}' >"$d/etc/claude-code/managed-settings.d/pfm.json"
  (cd "$d/home" && find .claude/projects .cc/2/projects -type f -print0 | sort -z | xargs -0 -r sha256sum) >"$d/manifest/sessions.sha256"
  (cd "$d/home" && find . -type f -printf '%s %P\n' | sort -k2) >"$d/manifest/files.txt"
  (cd "$d/home" && find . -type l -printf '%P -> %l\n' | sort) >"$d/manifest/links.txt"
  {
    echo "== .cc/fleet.db"
    sqlite3 "$d/home/.cc/fleet.db" "PRAGMA integrity_check;"
    for t in chat hidden swap_event; do echo "$t $(sqlite3 "$d/home/.cc/fleet.db" "SELECT count(*) FROM $t;")"; done
    echo "== .local/state/pfm/fleet.db ABSENT"
    echo "== .local/state/pfm/callmeter.db ABSENT"
  } >"$d/manifest/db.txt"
  printf 'home=%s\nmode=live\ntaken=2026-01-01T00:00:00Z\nprevious=none\nexcluded=--exclude=.credentials.json\n' "$FH" >"$d/meta"
}
mk_home
BK="$T/backup"
mk_backup "$BK"
rm -rf "$FH"

# ---- stubs ------------------------------------------------------------------
# pfm, per 0-install-journal B; $T/mode picks the defect a case plants.
cat >"$T/pfm-stub" <<STUB
#!/usr/bin/env bash
mode=\$(cat "$T/mode")
J="\$HOME/.local/state/pfm/migrations/20260101T000000Z"
case "\$*" in
"install --skip-harvest")
  echo "pfm install: plan"
  echo "  change  layout .cc/2/projects -> .claude/projects"
  [ "\$mode" = preview-writes ] && echo x >"\$HOME/.local/state/pfm/preview-wrote"
  exit 0 ;;
"install --yes --skip-harvest")
  if [ -d "\$J" ] && [ "\$mode" != apply-again-records ]; then echo "layout: nothing to do"; exit 0; fi
  mkdir -p "\$J"
  if [ ! -d "\$HOME/.claude/projects/p1" ]; then
    mv "\$HOME/.cc/2/projects/p1" "\$HOME/.claude/projects/p1"
    cp "\$HOME/.cc/fleet.db" "\$HOME/.local/state/pfm/pfm.db"
    [ "\$mode" = drop-session ] && rm "\$HOME/.claude/projects/p1/s1.jsonl"
  fi
  echo "install journal: \$J"
  exit 0 ;;
doctor)
  echo "accounts: 1 without credentials"
  [ "\$mode" = doctor-finding ] && echo "legacy: ~/.cc/fleet.db still present"
  exit 1 ;;
"install --rollback 20260101T000000Z")
  [ "\$mode" = rollback-incomplete ] && exit 0
  mv "\$HOME/.claude/projects/p1" "\$HOME/.cc/2/projects/p1"
  rm -f "\$HOME/.local/state/pfm/pfm.db"
  date -u +%FT%TZ >"\$J/rolled-back"
  exit 0 ;;
esac
echo "pfm stub: unexpected \$*" >&2
exit 9
STUB
chmod +x "$T/pfm-stub"

cat >"$BIN/make" <<STUB
#!/usr/bin/env bash
[ "\$1 \$3" = "-C host-install" ] && [ -f "\$2/Makefile" ] || { echo "make stub: bad call \$* (no candidate Makefile)" >&2; exit 2; }
cp -f "\$HOME/.local/bin/pfm" "\$HOME/.local/bin/pfm.prev"
cp -f "$T/pfm-stub" "\$HOME/.local/bin/pfm"
echo "installed: \$HOME/.local/bin/pfm"
STUB

cat >"$BIN/docker" <<STUB
#!/usr/bin/env bash
case \$1 in
info) [ -f "$T/docker-down" ] && exit 1; exit 0 ;;
build) echo sha256:fixture; exit 0 ;;
container) exit 1 ;;
rm) echo "\$*" >>"$T/docker-rm.log"; exit 0 ;;
run)
  printf '%s\n' "\$@" >"$T/docker-run.argv"
  : >"$T/docker.env"; : >"$T/docker.rewrite"
  shift
  while [ \$# -gt 0 ]; do
    case \$1 in
    -e) printf '%s\n' "\$2" >>"$T/docker.env"; shift 2 ;;
    -v)
      src=\${2%%:*}; rest=\${2#*:}; dst=\${rest%%:*}
      if [ "\${src#/}" != "\$src" ] && [ ! -e "\$dst" ] && [ -w "\$(dirname "\$dst")" ]; then ln -s "\$src" "\$dst"
      else printf '%s\t%s\n' "\$dst" "\$src" >>"$T/docker.rewrite"; fi
      shift 2 ;;
    --name | --user | -w) shift 2 ;;
    -*) shift ;;
    *) break ;;
    esac
  done
  echo fixture-container; exit 0 ;;
exec)
  [ "\$3 \$4" = "bash -lc" ] || { echo "docker stub: exec wants bash -lc, got \$*" >&2; exit 2; }
  cmd=\$5; envs=()
  while IFS= read -r e; do envs+=("\$e"); done <"$T/docker.env"
  while IFS=\$'\t' read -r dst src; do
    cmd=\${cmd//"\$dst"/"\$src"}
    for i in "\${!envs[@]}"; do envs[i]=\${envs[i]//"\$dst"/"\$src"}; done
  done <"$T/docker.rewrite"
  p=; for e in "\${envs[@]}"; do case \$e in PATH=*) p=\${e#PATH=} ;; esac; done
  exec env -i "\${envs[@]}" PATH="$BIN:\$p" bash -c "\$cmd" ;;
esac
echo "docker stub: unexpected \$*" >&2
exit 9
STUB
chmod +x "$BIN/make" "$BIN/docker"

HOSTPATH="$BIN:$PATH"
# rehearse MODE SCRATCH — one full run; sets RC and OUT.
rehearse() {
  echo "$1" >"$T/mode"
  rm -rf "$FH"
  OUT="$(PATH="$HOSTPATH" bash "$SUT" "$BK" "$2" 2>&1)"
  RC=$?
  rm -rf "$FH"
}
verdict() { head -1 "$1/rehearsal/verdict.txt" 2>/dev/null; }

# ---- usage ------------------------------------------------------------------
OUT="$(PATH="$HOSTPATH" bash "$SUT" 2>&1 >/dev/null)"; RC=$?
OUT3="$(PATH="$HOSTPATH" bash "$SUT" a b c 2>&1 >/dev/null)"; RC3=$?
if [ "$RC" -eq 2 ] && [ "$RC3" -eq 2 ] && grep -q usage <<<"$OUT" && grep -q usage <<<"$OUT3"; then ok "usage: wrong argument count → usage on stderr, exit 2"
else bad "usage" "rc=$RC/$RC3" "$OUT" "$OUT3"; fi

# ---- bad backup -------------------------------------------------------------
bad_backup() { # NAME MUTATION EXPECTED-WHAT
  local b="$T/bad-$1"
  rm -rf "$b"; cp -a "$BK" "$b"; eval "$2"
  OUT="$(PATH="$HOSTPATH" bash "$SUT" "$b" "$T/s-bad-$1" 2>&1)"; RC=$?
  if [ "$RC" -eq 1 ] && grep -qF "host-rehearsal: $3 missing in $b" <<<"$OUT" && [ ! -e "$T/s-bad-$1" ]; then ok "bad backup: $1 → refused before copying"
  else bad "bad backup: $1" "rc=$RC" "$OUT"; fi
}
bad_backup no-home "sed -i '/^home=/d' \"\$b/meta\"" "home= line in meta"
bad_backup no-sessions "rm \"\$b/manifest/sessions.sha256\"" "manifest/sessions.sha256"
bad_backup no-db "rm \"\$b/manifest/db.txt\"" "manifest/db.txt"

# ---- scratch in use ---------------------------------------------------------
S="$T/s-busy"; mkdir -p "$S"; echo keep >"$S/mine"
OUT="$(PATH="$HOSTPATH" bash "$SUT" "$BK" "$S" 2>&1)"; RC=$?
if [ "$RC" -eq 1 ] && grep -qF "$S" <<<"$OUT" && [ "$(ls -A "$S")" = mine ] && [ "$(cat "$S/mine")" = keep ]; then ok "scratch in use: refused, named, untouched"
else bad "scratch in use" "rc=$RC" "$OUT" "$(ls -A "$S")"; fi

# ---- docker down ------------------------------------------------------------
touch "$T/docker-down"; rm -f "$T/docker-run.argv"
OUT="$(PATH="$HOSTPATH" bash "$SUT" "$BK" "$T/s-down" 2>&1)"; RC=$?
rm -f "$T/docker-down"
if [ "$RC" -eq 1 ] && grep -q '^host-rehearsal: TOOLCHAIN-MISSING — ' <<<"$OUT" && [ ! -e "$T/docker-run.argv" ] && [ ! -e "$T/s-down" ]; then ok "docker down: TOOLCHAIN-MISSING, nothing started"
else bad "docker down" "rc=$RC" "$OUT"; fi

# ---- host tool missing ------------------------------------------------------
for tool in sqlite3 sha256sum rsync; do
  LB="$T/limited-$tool"; mkdir -p "$LB"
  for c in bash sh env cat dirname basename mkdir rm mv cp ln ls find sort xargs awk sed grep head tail tr wc cut id git diff comm mktemp chmod readlink date sqlite3 sha256sum rsync; do
    [ "$c" = "$tool" ] && continue
    w=$(command -v "$c") && ln -sf "$w" "$LB/$c"
  done
  ln -sf "$BIN/docker" "$LB/docker"; ln -sf "$BIN/make" "$LB/make"
  rm -f "$T/docker-run.argv"
  OUT="$(PATH="$LB" "$LB/bash" "$SUT" "$BK" "$T/s-tool-$tool" 2>&1)"; RC=$?
  if [ "$RC" -eq 1 ] && grep -q "^host-rehearsal: TOOLCHAIN-MISSING — .*$tool" <<<"$OUT" && [ ! -e "$T/docker-run.argv" ]; then ok "host tool missing: $tool named, nothing started"
  else bad "host tool missing: $tool" "rc=$RC" "$OUT"; fi
done

# ---- no clone marker --------------------------------------------------------
marker_case() { # NAME MUTATION EXPECTED
  local b="$T/mk-$1" s="$T/s-mk-$1"
  rm -rf "$b"; cp -a "$BK" "$b"; eval "$2"
  echo happy >"$T/mode"; rm -f "$T/docker-run.argv"
  OUT="$(PATH="$HOSTPATH" bash "$SUT" "$b" "$s" 2>&1)"; RC=$?
  if [ "$RC" -eq 1 ] && [[ "$(verdict "$s")" == "REHEARSAL FAIL copy: "*"$3"* ]] && [ -d "$s/home/.cc" ] && [ ! -e "$T/docker-run.argv" ]; then ok "no clone marker: $1 → refused after the copy"
  else bad "no clone marker: $1" "rc=$RC" "verdict=$(verdict "$s")" "$OUT"; fi
}
marker_case absent "rm \"\$b/home/.local/share/pfm/install/source-repo\"" "source-repo"
marker_case outside "echo /opt/elsewhere >\"\$b/home/.local/share/pfm/install/source-repo\"" "/opt/elsewhere"

# ---- happy path + container shape --------------------------------------------
S="$T/s-happy"
rehearse happy "$S"
R="$S/rehearsal"
want_steps=$'step copy ok\nstep build ok\nstep hash-before ok\nstep preview ok\nstep apply ok\nstep doctor ok\nstep apply-again ok\nstep manifest ok\nstep rollback ok\nstep hash-after ok'
plan_want=$'pfm install: plan\n  change  layout .cc/2/projects -> .claude/projects'
if [ "$RC" -eq 0 ] && [ "$(verdict "$S")" = "REHEARSAL PASS" ] && [ "$(tail -n +2 "$R/verdict.txt")" = "$want_steps" ] &&
  [ "$(cat "$R/plan.txt")" = "$plan_want" ] && [ "$(tail -1 <<<"$OUT")" = "$R/verdict.txt" ] &&
  [ -f "$S/home/.professor/pfm/Makefile" ] && [ -s "$R/hash-before.txt" ] && cmp -s "$R/hash-before.txt" "$R/hash-after.txt"; then
  ok "happy path: REHEARSAL PASS, one ok line per step, plan.txt = preview stdout, exit 0"
else bad "happy path" "rc=$RC" "$(cat "$R/verdict.txt" 2>/dev/null)" "$OUT"; fi

ARGV="$(cat "$T/docker-run.argv" 2>/dev/null)"
has_pair() { grep -qxF -- "$2" <<<"$(grep -xF -A1 -- "$1" <<<"$ARGV")"; }
proc=$(grep -x 'PFM_PROC_ROOT=.*' <<<"$ARGV" | head -1); proc=${proc#PFM_PROC_ROOT=}
pathv=$(grep -x 'PATH=.*' <<<"$ARGV" | head -1); pathv=${pathv#PATH=}
stubmnt=$(grep -F -- "$R/stubs:" <<<"$ARGV" | head -1)
if has_pair -v "$S/home:$FH" && has_pair -v "$S/etc/claude-code:/etc/claude-code" && has_pair -e "HOME=$FH" &&
  has_pair --user "$(id -u):$(id -g)" && has_pair -e PFM_LOG_LEVEL=off && grep -qx -- --init <<<"$ARGV" &&
  [ -n "$proc" ] && [[ "$proc" != "$FH"* ]] && has_pair -v "$R/proc:$proc:ro" && [ -z "$(ls -A "$R/proc")" ] &&
  [ -n "$stubmnt" ] && stubdst=${stubmnt#*:} && [ "${pathv%%:*}" = "${stubdst%:ro}" ] && [ -x "$R/stubs/systemctl" ] && [ -x "$R/stubs/sudo" ]; then
  ok "container shape: home at home=, /etc/claude-code, HOME, uid:gid, PFM_LOG_LEVEL=off, empty PFM_PROC_ROOT outside home, stubs first on PATH"
else bad "container shape" "$ARGV"; fi

# stubs: systemctl logs and answers is-active with 3; sudo runs its arguments.
SYSLOG_OUT="$( "$R/stubs/systemctl" --user is-active pfm-mcp.service; echo "rc=$?"; "$R/stubs/systemctl" --user daemon-reload; echo "rc=$?"; "$R/stubs/sudo" echo via-sudo)"
if [ "$SYSLOG_OUT" = $'rc=3\nrc=0\nvia-sudo' ] && grep -qx -- '--user daemon-reload' "$R/systemctl.log" 2>/dev/null; then ok "stubs: systemctl logs argv, is-active exits 3; sudo runs unprivileged"
else bad "stubs" "$SYSLOG_OUT" "$(cat "$R/systemctl.log" 2>/dev/null)"; fi

# ---- step failures ----------------------------------------------------------
fail_case() { # MODE EXPECTED-VERDICT-PREFIX ABSENT-LOG LABEL
  local s="$T/s-$1"
  rehearse "$1" "$s"
  if [ "$RC" -eq 1 ] && [[ "$(verdict "$s")" == "$2"* ]] && { [ -z "$3" ] || [ ! -e "$s/rehearsal/$3.log" ]; }; then ok "$4"
  else bad "$4" "rc=$RC" "$(cat "$s/rehearsal/verdict.txt" 2>/dev/null)" "$OUT"; fi
}
fail_case preview-writes "REHEARSAL FAIL preview: tree changed at .local/state/pfm/preview-wrote" apply "preview writes: FAIL preview naming the path, later steps not run"
fail_case doctor-finding "REHEARSAL FAIL doctor: legacy: ~/.cc/fleet.db still present" apply-again "layout finding after apply: FAIL doctor naming the line"
fail_case apply-again-records "REHEARSAL FAIL apply-again: " manifest "second apply records: FAIL apply-again"
fail_case drop-session "REHEARSAL FAIL manifest: " rollback "manifest: a lost session fails the manifest step"
if grep -q '\.cc/2/projects/p1/s1\.jsonl' "$T/s-drop-session/rehearsal/verdict.txt" 2>/dev/null; then ok "manifest: the verdict names the session's backup path"; else bad "manifest names path" "$(cat "$T/s-drop-session/rehearsal/verdict.txt" 2>/dev/null)"; fi
fail_case rollback-incomplete "REHEARSAL FAIL hash-after: " "" "rollback incomplete: FAIL hash-after"
if grep -q 'REHEARSAL FAIL hash-after: \.cc/2/projects/p1, ' "$T/s-rollback-incomplete/rehearsal/verdict.txt" 2>/dev/null; then ok "rollback incomplete: names the differing paths"; else bad "hash-after names path" "$(cat "$T/s-rollback-incomplete/rehearsal/verdict.txt" 2>/dev/null)"; fi

# ---- compare ----------------------------------------------------------------
# A migrated home: sessions in the one store, the state DB at its new name.
MH="$T/migrated"
mk_migrated() {
  rm -rf "$MH" "$T/journal"
  mkdir -p "$MH/.claude" "$MH/.local/state/pfm" "$T/journal"
  cp -a "$BK/home/.claude/projects" "$MH/.claude/projects"
  cp -a "$BK/home/.cc/2/projects/p1" "$MH/.claude/projects/p1"
  cp "$BK/home/.cc/fleet.db" "$MH/.local/state/pfm/pfm.db"
}
compare() { OUT="$(PATH="${1:-$HOSTPATH}" bash "$SUT" compare "$BK" "$MH" "$T/journal" 2>&1)"; RC=$?; }
mk_migrated; compare
if [ "$RC" -eq 0 ] && [ "$OUT" = "manifest: ok" ]; then ok "compare: pass"; else bad "compare pass" "rc=$RC" "$OUT"; fi

mk_migrated; rm "$MH/.claude/projects/p1/s2.jsonl"; compare
if [ "$RC" -eq 1 ] && [[ "$OUT" == "manifest: FAILED — "*".cc/2/projects/p1/s2.jsonl"* ]]; then ok "compare: a missing hash names its backup path"; else bad "compare missing hash" "rc=$RC" "$OUT"; fi

mk_migrated; sqlite3 "$MH/.local/state/pfm/pfm.db" "INSERT INTO chat VALUES(4);"; compare
if [ "$RC" -eq 1 ] && [[ "$OUT" == "manifest: FAILED — "*chat* ]]; then ok "compare: a count mismatch names the table"; else bad "compare count" "rc=$RC" "$OUT"; fi

mk_migrated; mkdir -p "$T/journal/backup/conflicts/x"; mv "$MH/.claude/projects/p1/s2.jsonl" "$T/journal/backup/conflicts/x/"; compare
if [ "$RC" -eq 0 ] && [ "$OUT" = "manifest: ok" ]; then ok "compare: a parked conflict counts as kept"; else bad "compare parked" "rc=$RC" "$OUT"; fi

mk_migrated; sqlite3 "$MH/.local/state/pfm/pfm.db" "DELETE FROM swap_event; INSERT INTO hidden VALUES(2);"; compare
if [ "$RC" -eq 0 ] && [ "$OUT" = "manifest: ok" ]; then ok "compare: swap_event/hidden drift ignored"; else bad "compare drift" "rc=$RC" "$OUT"; fi

mk_migrated; compare "$T/limited-sqlite3:$BIN"
if [ "$RC" -eq 2 ] && [[ "$OUT" == "manifest: UNREADABLE — "*sqlite3* ]]; then ok "compare: missing sqlite3 → UNREADABLE, exit 2"; else bad "compare no sqlite3" "rc=$RC" "$OUT"; fi

mk_migrated; rm "$MH/.local/state/pfm/pfm.db"; compare
if [ "$RC" -eq 2 ] && [[ "$OUT" == "manifest: UNREADABLE — "*pfm.db* ]]; then ok "compare: absent DB → UNREADABLE, exit 2"; else bad "compare absent db" "rc=$RC" "$OUT"; fi

mk_migrated; echo garbage >"$MH/.local/state/pfm/pfm.db"; compare
if [ "$RC" -eq 2 ] && [[ "$OUT" == "manifest: UNREADABLE — "*pfm.db* ]]; then ok "compare: unreadable DB → UNREADABLE, exit 2"; else bad "compare garbage db" "rc=$RC" "$OUT"; fi

shtest_end
