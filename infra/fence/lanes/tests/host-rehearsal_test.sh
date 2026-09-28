#!/usr/bin/env bash
# Fixture-driven tests for infra/fence/host-rehearsal.sh — a fixture-sized
# backup (the infra/fence/host-backup.sh layout) under the scratch jail, and stubs for docker,
# make, go and pfm. The docker stub plays the pfm-dev image: `run` records its argv
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
# host-rehearsal.sh needs sqlite3, rsync and sha256sum (the pfm-dev image
# carries all three). A test that cannot run is never a pass: exit 2, named.
missing=""
for tool in sqlite3 rsync sha256sum find; do command -v "$tool" >/dev/null 2>&1 || missing+=" $tool"; done
if command -v find >/dev/null 2>&1 && ! find . -maxdepth 0 -printf "" >/dev/null 2>&1; then
  echo "host-rehearsal_test: CANNOT RUN — GNU find required" >&2
  exit 2
fi
if [ -n "$missing" ]; then
  echo "host-rehearsal_test: CANNOT RUN — not on PATH:$missing" >&2
  exit 2
fi

BIN="$T/bin"
mkdir -p "$BIN"

# ---- fixture: a home, then a backup of it in the infra/fence/host-backup.sh layout ----------
FH="$T/fakehome" # the home= path; freed after the backup so docker's stub can mount onto it
mk_home() {
  rm -rf "$FH"
  mkdir -p "$FH/.claude/projects/p0" "$FH/.cc/2/projects/p1" "$FH/.local/share/pfm/install" \
    "$FH/.local/state/pfm" "$FH/.local/bin" "$FH/.config/pfm"
  printf '{"codex":{"homes":[{"home":"%s/.codex","id":1}]}}\n' "$FH" >"$FH/.config/pfm/pfm.config.json"
  echo a >"$FH/.claude/projects/p0/a.jsonl"
  echo s1 >"$FH/.cc/2/projects/p1/s1.jsonl"
  echo s2 >"$FH/.cc/2/projects/p1/s2.jsonl"
  chmod 600 "$FH/.cc/2/projects/p1/s2.jsonl"
  ln -s ../.claude/projects/p0 "$FH/.cc/p0-link"
  # the pre-migration binary: like 938b4a5e, a legacy config naming a Codex home
  # whose auth.json is missing or invalid refuses every command
  cat >"$FH/.local/bin/pfm" <<'OLD'
#!/usr/bin/env bash
cfg="$HOME/.config/pfm/pfm.config.json"
if [ -e "$cfg" ]; then
  for h in $(jq -r '.codex.homes[]?.home // empty' "$cfg"); do
    jq -e '.tokens.access_token and .tokens.account_id' "$h/auth.json" >/dev/null 2>&1 ||
      { echo "pfm: config: config $cfg: codex.homes[0] must contain a valid auth.json" >&2; exit 1; }
  done
fi
echo old
OLD
  chmod 755 "$FH/.local/bin/pfm"
  printf '%s\n' "$FH/.professor" >"$FH/.local/share/pfm/install/source-repo"
  # The clone's migrated configs: install-written, beside the tracked tree.
  mkdir -p "$FH/.professor"
  echo '{}' >"$FH/.professor/pfm.config.json"
  echo '{}' >"$FH/.professor/harvester.config.json"
  sqlite3 "$FH/.cc/fleet.db" "CREATE TABLE chat(id INTEGER); INSERT INTO chat VALUES(1),(2),(3);
    CREATE TABLE swap_event(id INTEGER); INSERT INTO swap_event VALUES(1),(2);
    CREATE TABLE hidden(id INTEGER); INSERT INTO hidden VALUES(1);"
}
mk_backup() { # mk_backup DEST — run the host backup against the fixture home
  local d=$1 managed="$T/managed-settings.d"
  mkdir -p "$managed"
  echo '{}' >"$managed/pfm.json"
  HOME="$FH" PFM_MANAGED_SETTINGS_DIR="$managed" bash "$(dirname "$SUT")/host-backup.sh" "$d" live >"$T/backup.log" || { cat "$T/backup.log" >&2; return 1; }
}

mk_home
BK="$T/backup"
mk_backup "$BK"
# A home with no legacy config: host-install names no migration, rollback prints no next block.
mk_home
rm "$FH/.config/pfm/pfm.config.json"
BKN="$T/backup-nolegacy"
mk_backup "$BKN"
rm -rf "$FH"
# The HEAD the rehearsal's build must find stamped into pfm (read as the SUT reads it).
if [ -n "${PFM_DEV_REPO_GIT_DIR:-}" ]; then
  HEAD_SHA=$(GIT_DIR="$PFM_DEV_REPO_GIT_DIR" git -c safe.directory='*' rev-parse HEAD)
else
  HEAD_SHA=$(git -C "$(dirname "$SUT")/../.." rev-parse HEAD)
fi
[ -n "$HEAD_SHA" ] || { echo "host-rehearsal_test: CANNOT RUN — no HEAD of the SUT's repository" >&2; exit 2; }

# ---- stubs ------------------------------------------------------------------
# pfm, per 0-install-journal B and 0-update-window; $T/mode picks the defect a case plants.
# apply stops the fleet (releasing a live holder), writes an absent managed
# drop-in through sudo, starts the fleet; rollback stops the fleet and, over a
# legacy config, prints the next block and starts nothing.
{
  printf '#!/usr/bin/env bash\nT=%q\n' "$T"
  cat <<'STUB'
mode=$(cat "$T/mode")
J="$HOME/.local/state/pfm/migrations/20260101T000000Z"
UNITS="pfm-mcp.service pfm-name-sync.path pfm-name-sync.timer"
DROPIN=/etc/claude-code/managed-settings.d/pfm.json
case "$*" in
"install --skip-harvest")
  echo "pfm install: plan"
  echo "  change  layout .cc/2/projects -> .claude/projects"
  [ "$mode" = preview-writes ] && echo x >"$HOME/.local/state/pfm/preview-wrote"
  # pfm's dependency probe runs `go version`; the Go toolchain writes its telemetry under the home.
  [ "$mode" = go-telemetry ] && mkdir -p "$HOME/.config/go/telemetry/local" && echo 1 >"$HOME/.config/go/telemetry/local/go.count"
  exit 0 ;;
"install --yes --skip-harvest")
  if [ -d "$J" ] && [ "$mode" != apply-again-records ]; then echo "layout: nothing to do"; exit 0; fi
  # SQLite opens a WAL database with -wal/-shm beside it; the live host always has them.
  [ "$mode" = db-sidecars ] && mkdir -p "$HOME/.cc" && : >"$HOME/.cc/fleet.db-wal" && : >"$HOME/.cc/fleet.db-shm"
  h="$PFM_PROC_ROOT/424242/fd/3"
  [ -L "$h" ] && readlink "$h" >"$T/holder-seen"
  systemctl --user stop $UNITS
  mkdir -p "$J"
  if [ ! -d "$HOME/.claude/projects/p1" ]; then
    mv "$HOME/.cc/2/projects/p1" "$HOME/.claude/projects/p1"
    cp "$HOME/.cc/fleet.db" "$HOME/.local/state/pfm/pfm.db"
    [ "$mode" = drop-session ] && rm "$HOME/.claude/projects/p1/s1.jsonl"
  fi
  if [ ! -e "$PFM_REHEARSAL_ETC/managed-settings.d/pfm.json" ]; then
    echo '{}' >"$J/managed.tmp"
    sudo -n install -D -m 0644 "$J/managed.tmp" "$DROPIN" || exit 1
    : >"$J/managed-written"
  fi
  systemctl --user start $UNITS
  echo "install journal: $J"
  exit 0 ;;
doctor)
  echo "accounts: 1 without credentials"
  [ "$mode" = doctor-finding ] && echo "legacy: ~/.cc/fleet.db still present"
  exit 1 ;;
"install --rollback 20260101T000000Z")
  systemctl --user stop $UNITS
  if [ "$mode" != rollback-incomplete ]; then
    mv "$HOME/.claude/projects/p1" "$HOME/.cc/2/projects/p1"
    rm -f "$HOME/.local/state/pfm/pfm.db"
    date -u +%FT%TZ >"$J/rolled-back"
  fi
  if [ -f "$J/managed-written" ]; then sudo rm -f "$DROPIN" || exit 1; fi
  legacy="$HOME/.config/pfm/pfm.config.json"
  if [ -e "$legacy" ]; then
    [ "$mode" = rollback-restarts ] && systemctl --user start pfm-mcp.service
    if [ "$mode" != no-next ]; then
      clone=$(head -1 "$HOME/.local/share/pfm/install/source-repo")
      echo "  next    this pfm refuses the restored legacy config $legacy; fleet units left stopped: $UNITS"
      echo "  next    1. make -C $clone/pfm rollback"
      echo "  next    2. systemctl --user start $UNITS"
    fi
  else
    systemctl --user start $UNITS
  fi
  exit 0 ;;
ls) echo "pfm stub: ls"; exit 0 ;;
esac
echo "pfm stub: unexpected $*" >&2
exit 9
STUB
} >"$T/pfm-stub"
chmod +x "$T/pfm-stub"

{
  printf '#!/usr/bin/env bash\nT=%q\n' "$T"
  cat <<'STUB'
printf '%s\n' "$*" >>"$T/make.log"
mode=$(cat "$T/mode")
[ "$1" = -C ] && [ -f "$2/Makefile" ] || { echo "make stub: bad call $* (no candidate Makefile)" >&2; exit 2; }
case $3 in
host-install)
  cp -f "$HOME/.local/bin/pfm" "$HOME/.local/bin/pfm.prev"
  cp -f "$T/pfm-stub" "$HOME/.local/bin/pfm"
  echo "installed: $HOME/.local/bin/pfm"
  if [ -e "$HOME/.config/pfm/pfm.config.json" ] && [ "$mode" != no-probe-line ]; then
    echo 'host-install: binary swapped; pfm refuses until `pfm install --yes` migrates this host — run it now' >&2
  fi ;;
rollback)
  [ "$mode" = pair-refused ] && { echo "rollback: REFUSED — fixture" >&2; exit 1; }
  cp -f "$HOME/.local/bin/pfm.prev" "$HOME/.local/bin/pfm"
  echo "rollback: restored $HOME/.local/bin/pfm from $HOME/.local/bin/pfm.prev" ;;
*) echo "make stub: bad target $3" >&2; exit 2 ;;
esac
STUB
} >"$BIN/make"

{
  printf '#!/usr/bin/env bash\nT=%q\nHEAD_SHA=%q\n' "$T" "$HEAD_SHA"
  cat <<'STUB'
[ "$1 $2" = "version -m" ] || { echo "go stub: unexpected $*" >&2; exit 9; }
echo "$3: go1.99"
[ "$(cat "$T/mode")" = no-vcs ] || printf '\tbuild\tvcs.revision=%s\n' "$HEAD_SHA"
STUB
} >"$BIN/go"

{
  printf '#!/usr/bin/env bash\nT=%q\nBIN=%q\n' "$T" "$BIN"
  cat <<'STUB'
case $1 in
info) [ -f "$T/docker-down" ] && exit 1; exit 0 ;;
build) echo sha256:fixture; exit 0 ;;
container) exit 1 ;;
rm) echo "$*" >>"$T/docker-rm.log"; exit 0 ;;
run)
  printf '%s\n' "$@" >"$T/docker-run.argv"
  : >"$T/docker.env"; : >"$T/docker.rewrite"
  shift
  while [ $# -gt 0 ]; do
    case $1 in
    -e) printf '%s\n' "$2" >>"$T/docker.env"; shift 2 ;;
    -v)
      src=${2%%:*}; rest=${2#*:}; dst=${rest%%:*}
      # A target inside the jail becomes a link onto its source; every other
      # target (/etc/claude-code, /rehearsal, …) is rewritten in each exec.
      if [ "${dst#"$T"/}" != "$dst" ] && [ "${src#/}" != "$src" ] && [ ! -e "$dst" ]; then ln -s "$src" "$dst"
      else printf '%s\t%s\n' "$dst" "$src" >>"$T/docker.rewrite"; fi
      shift 2 ;;
    --name | --user | -w) shift 2 ;;
    -*) shift ;;
    *) break ;;
    esac
  done
  echo fixture-container; exit 0 ;;
exec)
  [ "$3 $4" = "bash -lc" ] || { echo "docker stub: exec wants bash -lc, got $*" >&2; exit 2; }
  cmd=$5; envs=(); dsts=(); srcs=()
  while IFS= read -r e; do envs+=("$e"); done <"$T/docker.env"
  # Longest target first, each to a placeholder, then each placeholder to its
  # source: a target prefixing another (/rehearsal, /rehearsal/proc) rewrites once.
  while IFS=$'\t' read -r dst src; do dsts+=("$dst"); srcs+=("$src"); done < <(
    awk -F'\t' '{ print length($1) "\t" $0 }' "$T/docker.rewrite" | sort -t "$(printf '\t')" -k1,1nr | cut -f2-)
  for i in "${!dsts[@]}"; do
    cmd=${cmd//"${dsts[i]}"/"@@mount$i@@"}
    for j in "${!envs[@]}"; do envs[j]=${envs[j]//"${dsts[i]}"/"@@mount$i@@"}; done
  done
  for i in "${!dsts[@]}"; do
    cmd=${cmd//"@@mount$i@@"/"${srcs[i]}"}
    for j in "${!envs[@]}"; do envs[j]=${envs[j]//"@@mount$i@@"/"${srcs[i]}"}; done
  done
  p=; for e in "${envs[@]}"; do case $e in PATH=*) p=${e#PATH=} ;; esac; done
  exec env -i "${envs[@]}" PATH="$BIN:$p" bash -c "$cmd" ;;
esac
echo "docker stub: unexpected $*" >&2
exit 9
STUB
} >"$BIN/docker"
chmod +x "$BIN/make" "$BIN/docker" "$BIN/go"

HOSTPATH="$BIN:$PATH"
# rehearse MODE SCRATCH [BACKUP] [--stress] — one full run; sets RC and OUT.
rehearse() {
  echo "$1" >"$T/mode"
  rm -rf "$FH"
  rm -f "$T/holder-seen" "$T/make.log"
  OUT="$(PATH="$HOSTPATH" bash "$SUT" "${3:-$BK}" "$2" ${4:+"$4"} 2>&1)"
  RC=$?
  rm -rf "$FH"
}
verdict() { head -1 "$1/rehearsal/verdict.txt" 2>/dev/null; }

# ---- usage ------------------------------------------------------------------
OUT="$(PATH="$HOSTPATH" bash "$SUT" 2>&1 >/dev/null)"; RC=$?
OUT3="$(PATH="$HOSTPATH" bash "$SUT" a b c 2>&1 >/dev/null)"; RC3=$?
if [ "$RC" -eq 2 ] && [ "$RC3" -eq 2 ] && grep -q usage <<<"$OUT" && grep -qF 'BACKUP SCRATCH [--stress]' <<<"$OUT3"; then ok "usage: wrong argument count or an unknown third argument → usage naming [--stress] on stderr, exit 2"
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
want_steps=$'step copy ok\nstep build ok\nstep hash-before ok\nstep preview ok\nstep apply ok\nstep doctor ok\nstep apply-again ok\nstep manifest ok\nstep rollback ok\nstep hash-after ok\nstep pair ok'
plan_want=$'pfm install: plan\n  change  layout .cc/2/projects -> .claude/projects'
if [ "$RC" -eq 0 ] && [ "$(verdict "$S")" = "REHEARSAL PASS" ] && [ "$(tail -n +2 "$R/verdict.txt")" = "$want_steps" ] &&
  [ "$(cat "$R/plan.txt")" = "$plan_want" ] && [ "$(tail -1 <<<"$OUT")" = "$R/verdict.txt" ] &&
  [ -f "$S/home/.professor/pfm/Makefile" ] && [ -s "$R/hash-before.txt" ] && cmp -s "$R/hash-before.txt" "$R/hash-after.txt"; then
  ok "happy path: REHEARSAL PASS, one ok line per step, plan.txt = preview stdout, exit 0"
else bad "happy path" "rc=$RC" "$(cat "$R/verdict.txt" 2>/dev/null)" "$OUT"; fi
if [ -f "$S/home/.professor/pfm.config.json" ] && [ -f "$S/home/.professor/harvester.config.json" ] && [ -f "$S/home/.professor/pfm/Makefile" ]; then
  ok "clone configs: pfm.config.json and harvester.config.json from the backup beside the tracked tree"
else bad "clone configs" "$(ls -A "$S/home/.professor" 2>/dev/null)"; fi
pair_want="\$ make -C $FH/.professor/pfm rollback
\$ systemctl --user start pfm-mcp.service pfm-name-sync.path pfm-name-sync.timer
\$ $FH/.local/bin/pfm ls"
if [ "$(grep '^\$ ' "$R/pair.log" 2>/dev/null)" = "$pair_want" ] && [ "$(tail -1 "$R/pair.log")" = old ] && grep -qx -- "-C $FH/.professor/pfm rollback" "$T/make.log" 2>/dev/null; then
  ok "pair: the rollback's next commands in order, then ls on the restored binary"
else bad "pair" "$(cat "$R/pair.log" 2>/dev/null)"; fi

ARGV="$(cat "$T/docker-run.argv" 2>/dev/null)"
has_pair() { grep -qxF -- "$2" <<<"$(grep -xF -A1 -- "$1" <<<"$ARGV")"; }
proc=$(grep -x 'PFM_PROC_ROOT=.*' <<<"$ARGV" | head -1); proc=${proc#PFM_PROC_ROOT=}
pathv=$(grep -x 'PATH=.*' <<<"$ARGV" | head -1); pathv=${pathv#PATH=}
stubmnt=$(grep -F -- "$R/stubs:" <<<"$ARGV" | head -1)
gitdir=$(grep -x 'PFM_DEV_REPO_GIT_DIR=.*' <<<"$ARGV" | head -1); gitdir=${gitdir#PFM_DEV_REPO_GIT_DIR=}
if has_pair -v "$S/home:$FH" && has_pair -v "$S/etc/claude-code:/etc/claude-code:ro" && has_pair -v "$S/etc/claude-code:/rehearsal-etc" &&
  has_pair -e "HOME=$FH" && has_pair --user "$(id -u):$(id -g)" && has_pair -e PFM_LOG_LEVEL=off && grep -qx -- --init <<<"$ARGV" &&
  [ -n "$proc" ] && [[ "$proc" != "$FH"* ]] && has_pair -v "$R/proc:$proc" && [ -z "$(ls -A "$R/proc")" ] &&
  ! grep -qx 'GOFLAGS=-buildvcs=false' <<<"$ARGV" &&
  [[ "$gitdir" == /pfm-git-common/?* ]] && [ "$(cat "$S/home/.professor/.git" 2>/dev/null)" = "gitdir: $gitdir" ] &&
  [ -n "$stubmnt" ] && stubdst=${stubmnt#*:} && [ "${pathv%%:*}" = "${stubdst%:ro}" ] && [ -x "$R/stubs/systemctl" ] && [ -x "$R/stubs/sudo" ]; then
  ok "container shape: home at home=, /etc/claude-code :ro plus /rehearsal-etc, HOME, uid:gid, PFM_LOG_LEVEL=off, empty read-write PFM_PROC_ROOT outside home, no -buildvcs=false, clone .git names the mounted gitdir, stubs first on PATH"
else bad "container shape" "$ARGV" "$(cat "$S/home/.professor/.git" 2>/dev/null)"; fi

# stubs: a fresh copy of the rehearsal's stubs keeps its own state and logs.
FS="$T/fresh"; mkdir -p "$FS/stubs"; cp "$R/stubs/systemctl" "$R/stubs/sudo" "$FS/stubs/"
sc() { "$FS/stubs/systemctl" --user "$@"; echo "rc=$?"; }
SYS_OUT="$(sc show --property=ActiveState --value pfm-mcp.service; sc stop pfm-mcp.service
  sc show --property=ActiveState --value pfm-mcp.service; sc is-active pfm-mcp.service
  sc start pfm-mcp.service; sc is-active pfm-mcp.service
  sc show --property=ActiveState --value pfm-name-sync.service; sc show-environment; sc cat pfm-mcp.service)"
sys_want=$'active\nrc=0\nrc=0\ninactive\nrc=0\ninactive\nrc=3\nrc=0\nactive\nrc=0\ninactive\nrc=0\nrc=0\nrc=0'
if [ "$SYS_OUT" = "$sys_want" ] && [ "$(wc -l <"$FS/systemctl.log" 2>/dev/null)" -eq 9 ] &&
  grep -qx -- '--user stop pfm-mcp.service' "$FS/systemctl.log" && grep -qx -- '--user cat pfm-mcp.service' "$FS/systemctl.log"; then
  ok "stubs: systemctl is stateful (active → stop → inactive rc 3 → start → active), other units inactive, every argv logged"
else bad "stubs" "$SYS_OUT" "$(cat "$FS/systemctl.log" 2>/dev/null)"; fi
SE="$T/fresh-etc"; mkdir -p "$SE"; echo '{"x":1}' >"$T/dropin-src.json"
PFM_REHEARSAL_ETC="$SE" "$FS/stubs/sudo" -n install -D -m 0644 "$T/dropin-src.json" /etc/claude-code/managed-settings.d/pfm.json; SRC1=$?
wrote=no; [ -f "$SE/managed-settings.d/pfm.json" ] && wrote=yes
PFM_REHEARSAL_ETC="$SE" "$FS/stubs/sudo" rm -f /etc/claude-code/managed-settings.d/pfm.json; SRC2=$?
if [ "$SRC1" -eq 0 ] && [ "$SRC2" -eq 0 ] && [ "$wrote" = yes ] && [ ! -e "$SE/managed-settings.d/pfm.json" ] &&
  grep -qxF -- "-n install -D -m 0644 $T/dropin-src.json /etc/claude-code/managed-settings.d/pfm.json" "$FS/sudo.log" &&
  grep -qxF -- "rm -f /etc/claude-code/managed-settings.d/pfm.json" "$FS/sudo.log"; then
  ok "stubs: sudo writes and removes /etc/claude-code on its read-write mount, argv logged"
else bad "sudo stub" "rc=$SRC1/$SRC2 wrote=$wrote" "$(cat "$FS/sudo.log" 2>/dev/null)"; fi

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
rehearse db-sidecars "$T/s-db-sidecars"
if [ "$RC" -eq 0 ] && [ "$(verdict "$T/s-db-sidecars")" = "REHEARSAL PASS" ]; then ok "db sidecars: SQLite's -wal/-shm beside a database never fail the tree hash"
else bad "db sidecars" "rc=$RC" "$(cat "$T/s-db-sidecars/rehearsal/verdict.txt" 2>/dev/null)"; fi
rehearse go-telemetry "$T/s-go-telemetry"
if [ "$RC" -eq 0 ] && [ "$(verdict "$T/s-go-telemetry")" = "REHEARSAL PASS" ]; then ok "go telemetry: the Go toolchain's own counters under .config/go never fail the tree hash"
else bad "go telemetry" "rc=$RC" "$(cat "$T/s-go-telemetry/rehearsal/verdict.txt" 2>/dev/null)"; fi
fail_case rollback-incomplete "REHEARSAL FAIL hash-after: " "" "rollback incomplete: FAIL hash-after"
if grep -q 'REHEARSAL FAIL hash-after: \.cc/2/projects/p1, ' "$T/s-rollback-incomplete/rehearsal/verdict.txt" 2>/dev/null; then ok "rollback incomplete: names the differing paths"; else bad "hash-after names path" "$(cat "$T/s-rollback-incomplete/rehearsal/verdict.txt" 2>/dev/null)"; fi

fail_case no-probe-line "REHEARSAL FAIL build: host-install did not name the pending migration" preview "build: a legacy config and no host-install probe line fails build"
fail_case no-vcs "REHEARSAL FAIL build: pfm carries no VCS stamp of HEAD $HEAD_SHA" preview "build: a pfm without vcs.revision of HEAD fails build"
fail_case rollback-restarts "REHEARSAL FAIL rollback: fleet unit pfm-mcp.service restarted on a binary that refuses the legacy config" pair "rollback: a listed unit started after the rollback began fails rollback"
fail_case no-next "REHEARSAL FAIL rollback: no next step printed for the restored legacy config" pair "rollback: no next block over a legacy config fails rollback"
fail_case pair-refused "REHEARSAL FAIL pair: make -C $FH/.professor/pfm rollback exit 1: rollback: REFUSED — fixture" "" "pair: a failing next command fails pair, naming command, exit and last line"

S="$T/s-nolegacy"
rehearse happy "$S" "$BKN"
if [ "$RC" -eq 0 ] && [ "$(verdict "$S")" = "REHEARSAL PASS" ] && [ "$(tail -n +2 "$S/rehearsal/verdict.txt")" = "$want_steps" ] &&
  [ "$(grep '^\$ ' "$S/rehearsal/pair.log" 2>/dev/null)" = "\$ make -C $FH/.professor/pfm rollback
\$ $FH/.local/bin/pfm ls" ]; then
  ok "no legacy config: no probe line needed, no next block, pair runs make rollback then ls"
else bad "no legacy config" "rc=$RC" "$(cat "$S/rehearsal/verdict.txt" 2>/dev/null)" "$(cat "$S/rehearsal/pair.log" 2>/dev/null)" "$OUT"; fi

# ---- stress -----------------------------------------------------------------
S="$T/s-stress"
rehearse happy "$S" "$BK" --stress
if [ "$RC" -eq 0 ] && [ "$(verdict "$S")" = "REHEARSAL PASS" ] && [ "$(tail -n +2 "$S/rehearsal/verdict.txt")" = "$want_steps" ] &&
  [ "$(cat "$T/holder-seen" 2>/dev/null)" = "$FH/.cc/fleet.db" ] && [ ! -e "$S/rehearsal/proc/424242" ] &&
  grep -q -- '^-n install -D -m 0644 .* /etc/claude-code/managed-settings.d/pfm.json$' "$S/rehearsal/sudo.log" 2>/dev/null &&
  grep -qx -- '--user stop pfm-mcp.service pfm-name-sync.path pfm-name-sync.timer' "$S/rehearsal/systemctl.log" &&
  [ ! -e "$S/etc/claude-code/managed-settings.d/pfm.json" ]; then
  ok "stress: holder 424242 on the legacy database released by the stop, drop-in written through sudo, absent after rollback"
else bad "stress" "rc=$RC" "$(cat "$S/rehearsal/verdict.txt" 2>/dev/null)" "holder=$(cat "$T/holder-seen" 2>/dev/null)" "$(cat "$S/rehearsal/sudo.log" 2>/dev/null)" "$OUT"; fi
b="$T/bk-nodb"; rm -rf "$b"; cp -a "$BK" "$b"; rm -f "$b/home/.cc/fleet.db" "$b/home/.local/state/pfm/fleet.db"
S="$T/s-stress-nodb"
rehearse happy "$S" "$b" --stress
if [ "$RC" -eq 1 ] && [ "$(verdict "$S")" = "REHEARSAL FAIL copy: no legacy state database to hold" ]; then ok "stress: no legacy state database → FAIL copy"
else bad "stress no db" "rc=$RC" "$(cat "$S/rehearsal/verdict.txt" 2>/dev/null)" "$OUT"; fi

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
