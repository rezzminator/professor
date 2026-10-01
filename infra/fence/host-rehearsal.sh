#!/usr/bin/env bash
set -uo pipefail

# host-rehearsal.sh — rehearse the host install on a COPY of a real host
# backup (infra/fence/host-backup.sh layout), inside the pfm-dev image.
#
# usage: infra/fence/host-rehearsal.sh BACKUP SCRATCH [--stress]
#        infra/fence/host-rehearsal.sh compare BACKUP HOME JOURNAL
#
# BACKUP SCRATCH: copies BACKUP/home and BACKUP/etc into SCRATCH (hard links,
# modes and link targets kept; credentials never), places this repository's
# tracked files at the clone path the copied source-repo marker names, beside a
# .git file naming the repository's git dir (so the build is VCS-stamped) and
# the clone's pfm.config.json / harvester.config.json from the backup, and
# runs one container with SCRATCH/home mounted at the home= path of
# BACKUP/meta and SCRATCH/etc/claude-code read-only at /etc/claude-code (the
# host's root-owned /etc) and read-write at /rehearsal-etc (sudo's view).
# Steps, in order:
#   copy build hash-before preview apply doctor apply-again manifest rollback hash-after pair
# build: when the copied home holds a legacy config (.config/pfm/pfm.config.json
# or config.json), host-install must name the pending migration; the new pfm
# must carry vcs.revision = this repository's HEAD. rollback: over a legacy
# config it must print its `next` block and restart none of the units it left
# stopped. pair: runs the rollback's numbered `next` commands in order (no
# block: make -C {clone}/pfm rollback), then {pfm} ls on the restored binary.
# --stress: the managed drop-in is removed from the copied /etc, and a fake
# live pid 424242 holds the legacy state database (proc/424242/fd/3); apply
# must stop pfm-mcp.service (which releases the holder) and write the drop-in
# through sudo; rollback must remove it again.
# BACKUP is only read. Outputs in SCRATCH/rehearsal/:
#   verdict.txt   first line REHEARSAL PASS or REHEARSAL FAIL {step}: {reason},
#                 then one line per step run: step {name} ok | step {name} FAILED {reason}
#   plan.txt      the preview's stdout, verbatim (the install window diffs its layout plan)
#   {step}.log    each container step's output; doctor.log ends with doctor's exit code
#   hash-before.txt / hash-preview.txt / hash-after.txt
#                 the tree of SCRATCH/home (path, type, mode, link target or
#                 sha256), .local/state/pfm/migrations/ and .config/go/ excluded
#                 (.config/go: the Go toolchain's telemetry, written by pfm's
#                 `go version` dependency probe; outside the backup, not pfm's)
#                 and every *.db-wal / *.db-shm (SQLite creates them on any open
#                 of a WAL database; the live host always has them, a .backup
#                 copy never does; the databases themselves are compared)
#   stubs/        systemctl: stateful, one state word per unit in units/{unit}
#                 (the three fleet units start active, any other unit inactive);
#                 stop/start/restart set it, is-active and show answer it; argv
#                 appended to systemctl.log. sudo: drops -n, maps /etc/claude-code
#                 onto /rehearsal-etc, argv appended to sudo.log, then runs it
#   pair.log      the pair step's commands and their output
#   proc/         PFM_PROC_ROOT, read-write: empty (no chat is live), or with
#                 --stress the holder 424242, removed by the stub's stop of
#                 pfm-mcp.service
# stdout ends with the verdict path.
# Disk: on REHEARSAL PASS, SCRATCH/home and SCRATCH/etc (the size of the
# backed-up home) are deleted and SCRATCH/rehearsal/ is kept; on FAIL SCRATCH is
# kept whole and stderr says so. PFM_REHEARSAL_KEEP_SCRATCH=1 keeps it on PASS
# too. Fence housekeeping (infra/fence/housekeeping.sh) runs before the image
# build, and the container carries --label pfm.fence=1.
# PFM_REHEARSAL_SOURCE_TREE, when set, supplies the tracked candidate files
# from a fixture git tree; the normal rehearsal copies this repository.
#
# compare BACKUP HOME JOURNAL: the manifest check alone — every sessions.sha256
# hash present in HOME/.claude/{projects,file-history,tasks,session-env} or in
# JOURNAL/backup/conflicts; every db.txt table count equal in HOME's database
# (.cc/fleet.db → .local/state/pfm/pfm.db, .local/state/pfm/fleet.db →
# .local/state/pfm/pfm-cache.db, callmeter.db → itself) except swap_event and
# hidden, each counted from a temp copy with its -wal/-shm.
#
# BROKEN STATE: wrong arguments (a third one other than --stress) print usage, exit 2. A backup lacking home= in
# meta, manifest/sessions.sha256 or manifest/db.txt, a non-empty SCRATCH, or a
# missing host tool / unreachable docker daemon (TOOLCHAIN-MISSING) refuse
# before anything is copied or started, exit 1, naming what. After the copy,
# the first failed step writes REHEARSAL FAIL {step}: {reason}, stops, exit 1;
# REHEARSAL PASS is written only after every step's own check passed; a PASS
# whose copies cannot be deleted stays PASS and prints `WARN could not remove`
# naming them. Housekeeping never fails the rehearsal: each failed step is one
# `WARN fence housekeeping: …` line on stderr. compare
# prints manifest: ok (0), manifest: FAILED — {reason} (1), or
# manifest: UNREADABLE — {cause} (2) when it could not look.

IMAGE=professor-pfm-dev
NAME="${PFM_REHEARSAL_NAME:-pfm-host-rehearsal}"
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd -P)"
SOURCE_TREE="${PFM_REHEARSAL_SOURCE_TREE:-}"
# The tracked tree. Inside the fence a linked worktree's .git names a host
# path; the fence hands the active gitdir in as PFM_DEV_REPO_GIT_DIR.
repo_ls_files() {
  if [ -n "$SOURCE_TREE" ]; then
    git -C "$SOURCE_TREE" ls-files -z
  elif [ -n "${PFM_DEV_REPO_GIT_DIR:-}" ]; then
    GIT_DIR="$PFM_DEV_REPO_GIT_DIR" GIT_WORK_TREE="$REPO_ROOT" git -c safe.directory='*' ls-files -z
  else
    git -C "$REPO_ROOT" ls-files -z
  fi
}
repo_head() {
  if [ -n "${PFM_DEV_REPO_GIT_DIR:-}" ]; then
    GIT_DIR="$PFM_DEV_REPO_GIT_DIR" git -c safe.directory='*' rev-parse HEAD
  else
    git -C "$REPO_ROOT" rev-parse HEAD
  fi
}
# Container-side paths: fixed, never under the rehearsed home.
C_REHEARSAL=/rehearsal
C_GIT=/pfm-git-common
C_ETC=/rehearsal-etc
FLEET_UNITS="pfm-mcp.service pfm-name-sync.path pfm-name-sync.timer"
HOLDER_PID=424242
C_GOMOD=/pfm-gomod
DB_MAP=(".cc/fleet.db .local/state/pfm/pfm.db" ".local/state/pfm/fleet.db .local/state/pfm/pfm-cache.db" ".local/state/pfm/callmeter.db .local/state/pfm/callmeter.db")

die() { echo "host-rehearsal: $*" >&2; exit 1; }
usage() {
  cat >&2 <<'EOF'
usage: infra/fence/host-rehearsal.sh BACKUP SCRATCH [--stress]
       infra/fence/host-rehearsal.sh compare BACKUP HOME JOURNAL
EOF
  exit 2
}

# ---- compare ------------------------------------------------------------------

unreadable() { echo "manifest: UNREADABLE — $*"; exit 2; }

# count_tables DB OUT — "{table} {count}" per table of DB, read from a temp copy.
count_tables() {
  local db=$1 out=$2 c t n
  c="$TMP/count-${db##*/}"
  rm -f "$c" "$c-wal" "$c-shm"
  cp "$db" "$c" || return 1
  for s in -wal -shm; do [ -f "$db$s" ] && { cp "$db$s" "$c$s" || return 1; }; done
  t=$(sqlite3 "$c" "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name;" 2>&1) || { echo "$t" >"$out"; return 1; }
  : >"$out"
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    n=$(sqlite3 "$c" "SELECT count(*) FROM \"$name\";" 2>&1) || { echo "$n" >"$out"; return 1; }
    printf '%s %s\n' "$name" "$n" >>"$out"
  done <<<"$t"
}

cmd_compare() {
  local backup=$1 home=$2 journal=$3 tool
  for tool in sqlite3 sha256sum; do command -v "$tool" >/dev/null 2>&1 || unreadable "$tool not on PATH"; done
  local sums="$backup/manifest/sessions.sha256" dbtxt="$backup/manifest/db.txt"
  [ -r "$sums" ] || unreadable "$sums unreadable"
  [ -r "$dbtxt" ] || unreadable "$dbtxt unreadable"
  TMP=$(mktemp -d "${TMPDIR:-/tmp}/host-rehearsal-compare.XXXXXX") || unreadable "mktemp failed"
  trap 'rm -rf -- "$TMP"' EXIT

  local reasons=() dirs=() d
  for d in "$home"/.claude/{projects,file-history,tasks,session-env} "$journal/backup/conflicts"; do
    [ -d "$d" ] && dirs+=("$d")
  done
  : >"$TMP/have"
  if [ ${#dirs[@]} -gt 0 ]; then
    find -H "${dirs[@]}" -type f -print0 | xargs -0 -r sha256sum |
      awk '{ print substr($0, 1 + (substr($0, 1, 1) == "\\"), 64) }' >"$TMP/have" ||
      unreadable "hashing the store under $home failed"
  fi
  awk 'NR == FNR { have[$1] = 1; next }
    { h = $1; sub(/^\\/, "", h); if (!(h in have)) { p = substr($0, index($0, "  ") + 2); print p } }' \
    "$TMP/have" "$sums" >"$TMP/missing"
  local nmiss
  nmiss=$(wc -l <"$TMP/missing")
  if [ "$nmiss" -gt 0 ]; then
    reasons+=("session $(head -1 "$TMP/missing") (backup path; $nmiss of $(wc -l <"$sums") session files) found neither in the store nor parked")
  fi

  local line src="" skip=1 target table count now entry
  while IFS= read -r line; do
    case $line in
    "== "*" ABSENT") skip=1; continue ;;
    "== "*)
      src=${line#== }; skip=0; target=""
      for entry in "${DB_MAP[@]}"; do [ "${entry%% *}" = "$src" ] && target=${entry#* }; done
      [ -n "$target" ] || unreadable "db.txt names $src, which has no mapped database"
      [ -r "$home/$target" ] || unreadable "$home/$target unreadable (backup lists $src)"
      count_tables "$home/$target" "$TMP/now" || unreadable "$home/$target: $(head -1 "$TMP/now")"
      continue ;;
    ok | "") continue ;;
    esac
    [ "$skip" = 0 ] || continue
    table=${line% *}; count=${line##* }
    case $table in swap_event | hidden) continue ;; esac
    now=""
    while read -r name value; do
      [ "$name" = "$table" ] && now=$value
    done <"$TMP/now"
    if [ -z "$now" ]; then reasons+=("$target table $table missing (backup $src had $count)")
    elif [ "$now" != "$count" ]; then reasons+=("$target table $table: backup $count, now $now"); fi
  done <"$dbtxt"

  if [ ${#reasons[@]} -eq 0 ]; then echo "manifest: ok"; exit 0; fi
  local joined
  joined=$(printf '%s; ' "${reasons[@]}")
  echo "manifest: FAILED — ${joined%; }"
  exit 1
}

# ---- rehearsal ---------------------------------------------------------------

# tree_hash DIR OUT — one sorted line per path: path, type, mode, target or sha256.
tree_hash() {
  local dir=$1 out=$2
  (cd "$dir" && : >"$out.meta" &&
    find . \( -path ./.local/state/pfm/migrations -o -path ./.config/go -o -name '*.db-wal' -o -name '*.db-shm' \) -prune -o \
      ! -path . -fprintf "$out.meta" '%P\t%y\t%m\t%l\n' -type f -print0 |
      xargs -0 -r sha256sum) >"$out.sha" || return 1
  awk -F'\t' 'NR == FNR { o = (substr($0, 1, 1) == "\\") ? 1 : 0; sha[substr($0, 69 + o)] = substr($0, 1 + o, 64); next }
    { v = ($2 == "f") ? sha[$1] : ($2 == "l" ? $4 : "-"); print $1 "\t" $2 "\t" $3 "\t" v }' \
    "$out.sha" "$out.meta" | LC_ALL=C sort >"$out"
  local rc=$?
  rm -f "$out.sha" "$out.meta"
  return $rc
}

# tree_diff A B — the first differing paths, comma-separated.
tree_diff() {
  diff "$1" "$2" | sed -n 's/^[<>] \([^\t]*\)\t.*/\1/p' | awk '!seen[$0]++' | head -5 | paste -sd, - | sed 's/,/, /g'
}

STEP_LINES=()
fail() { # fail STEP REASON — the verdict, then stop.
  STEP_LINES+=("step $1 FAILED $2")
  { echo "REHEARSAL FAIL $1: $2"; printf '%s\n' "${STEP_LINES[@]}"; } >"$R/verdict.txt"
  cat "$R/verdict.txt"
  echo "host-rehearsal: kept ${R%/rehearsal} whole (home, etc, rehearsal) for inspection — remove it when done" >&2
  echo "$R/verdict.txt"
  exit 1
}
pass() { STEP_LINES+=("step $1 ok"); echo "step $1 ok"; }

# inside CMD — one container step; the stub dir is forced first on PATH.
inside() { docker exec "$NAME" bash -lc "export PATH=$C_REHEARSAL/stubs:\$PATH; cd /; $1"; }

cmd_rehearse() {
  local backup=$1 scratch=$2 stress=$3 home f tool
  [ -r "$backup/meta" ] || die "meta missing in $backup"
  home=""
  while IFS= read -r line || [ -n "$line" ]; do
    case $line in home=*) home=${line#home=}; break ;; esac
  done <"$backup/meta"
  [ -n "$home" ] || die "home= line in meta missing in $backup"
  for f in manifest/sessions.sha256 manifest/db.txt; do [ -f "$backup/$f" ] || die "$f missing in $backup"; done
  [ -d "$backup/home" ] || die "home/ missing in $backup"
  if [ -e "$scratch" ] && [ -n "$(ls -A "$scratch" 2>/dev/null)" ]; then
    die "SCRATCH $scratch exists and is not empty — refusing to touch it"
  fi
  for tool in sqlite3 sha256sum rsync git jq; do
    command -v "$tool" >/dev/null 2>&1 || die "TOOLCHAIN-MISSING — $tool not on PATH"
  done
  command -v docker >/dev/null 2>&1 || die "TOOLCHAIN-MISSING — docker not on PATH"
  docker info >/dev/null 2>&1 || die "TOOLCHAIN-MISSING — the docker daemon is not reachable ('docker info' failed)"
  # shellcheck source=housekeeping.sh
  . "$REPO_ROOT/infra/fence/housekeeping.sh"
  fence_housekeeping
  docker container inspect "$NAME" >/dev/null 2>&1 && die "container $NAME already exists — remove it or set PFM_REHEARSAL_NAME"

  mkdir -p "$scratch/home" "$scratch/etc" "$scratch/rehearsal/stubs" "$scratch/rehearsal/proc" || die "create $scratch failed"
  scratch="$(cd "$scratch" && pwd -P)"
  R="$scratch/rehearsal"
  local H="$scratch/home" rc
  local dropin="$scratch/etc/claude-code/managed-settings.d/pfm.json"

  # copy
  rsync -aH --exclude=.credentials.json --exclude=.codex/auth.json "$backup/home/" "$H/" >"$R/copy.log" 2>&1 ||
    fail copy "rsync of $backup/home exit $?"
  if [ -d "$backup/etc" ]; then
    rsync -aH "$backup/etc/" "$scratch/etc/" >>"$R/copy.log" 2>&1 || fail copy "rsync of $backup/etc exit $?"
  fi
  mkdir -p "$scratch/etc/claude-code" || fail copy "create $scratch/etc/claude-code failed"
  local marker=.local/share/pfm/install/source-repo clone
  [ -f "$H/$marker" ] || fail copy "clone marker $marker missing in the copied home"
  IFS= read -r clone <"$H/$marker"
  case $clone in
  "$home"/?*) ;;
  *) fail copy "clone marker names '$clone', outside home $home" ;;
  esac
  case "/$clone/" in */../*) fail copy "clone marker names '$clone', which climbs out of the home" ;; esac
  local gitvals git_common git_rel
  gitvals=$(ROOT="$REPO_ROOT" FENCE_CALLER=host-rehearsal bash -c \
    'source "$1" && printf "%s\n%s\n" "$PFM_DEV_GIT_COMMON" "$PFM_DEV_GIT_DIR_REL"' _ "$REPO_ROOT/infra/fence/fence-env.sh" 2>&1) ||
    fail copy "resolve the git dir of $REPO_ROOT: $gitvals"
  local git_lines=()
  mapfile -t git_lines <<<"$gitvals"
  git_common=${git_lines[0]:-}; git_rel=${git_lines[1]:-}
  local crel=${clone#"$home"/}
  local cand="$H/$crel" cf
  { rm -rf -- "$cand" && mkdir -p "$cand"; } || fail copy "clear $cand failed"
  repo_ls_files | rsync -a --from0 --files-from=- --ignore-missing-args "${SOURCE_TREE:-$REPO_ROOT}/" "$cand/" >>"$R/copy.log" 2>&1 ||
    fail copy "placing the candidate tree from ${SOURCE_TREE:-$REPO_ROOT} failed (see copy.log)"
  # The container runs as the host uid, so git's ownership check passes and
  # the build stamps vcs.revision from the read-only common dir.
  printf 'gitdir: %s\n' "$C_GIT/$git_rel" >"$cand/.git" || fail copy "write $cand/.git failed"
  for cf in pfm.config.json harvester.config.json; do
    [ -e "$backup/home/$crel/$cf" ] || continue
    cp -p "$backup/home/$crel/$cf" "$cand/$cf" >>"$R/copy.log" 2>&1 || fail copy "copying the clone's $cf from $backup failed"
  done
  local legacy=""
  for f in .config/pfm/pfm.config.json .config/pfm/config.json; do
    [ -z "$legacy" ] && [ -e "$H/$f" ] && legacy=$f
  done
  if [ "$stress" = 1 ]; then
    local held=""
    for f in .cc/fleet.db .local/state/pfm/fleet.db; do
      [ -z "$held" ] && [ -f "$H/$f" ] && held=$f
    done
    [ -n "$held" ] || fail copy "no legacy state database to hold"
    rm -f "$dropin" || fail copy "removing the managed drop-in $dropin failed"
    { mkdir -p "$R/proc/$HOLDER_PID/fd" && ln -s "$home/$held" "$R/proc/$HOLDER_PID/fd/3"; } ||
      fail copy "creating the holder pid $HOLDER_PID failed"
  fi
  pass copy

  # build: stubs, container, make host-install
  {
    echo '#!/usr/bin/env bash'
    printf 'stress=%q fleet=%q holder=%q\n' "$stress" "$FLEET_UNITS" "$HOLDER_PID"
    cat <<'EOF'
# The rehearsal dir ($C_REHEARSAL in the container): the parent of stubs/.
D=${0%/*}; D=${D%/*}
printf '%s\n' "$*" >>"$D/systemctl.log"
[ -d "$D/units" ] || mkdir -p "$D/units"
state() {
  if [ -f "$D/units/$1" ]; then cat "$D/units/$1"; return; fi
  case " $fleet " in *" $1 "*) echo active ;; *) echo inactive ;; esac
}
verb="" prop="" quiet=0 units=()
while [ $# -gt 0 ]; do
  case $1 in
  -p | --property) prop=${2:-}; shift ;;
  --property=*) prop=${1#--property=} ;;
  -q | --quiet) quiet=1 ;;
  -*) ;;
  *) if [ -z "$verb" ]; then verb=$1; else units+=("$1"); fi ;;
  esac
  shift
done
case $verb in
stop)
  for u in "${units[@]}"; do
    echo inactive >"$D/units/$u"
    if [ "$stress" = 1 ] && [ "$u" = pfm-mcp.service ]; then rm -rf "$D/proc/$holder"; fi
  done ;;
start | restart) for u in "${units[@]}"; do echo active >"$D/units/$u"; done ;;
is-active)
  s=$(state "${units[0]}")
  [ "$quiet" = 1 ] || echo "$s"
  [ "$s" = active ] || exit 3 ;;
show)
  s=$(state "${units[0]}")
  case $prop in
  ActiveState) echo "$s" ;;
  MainPID) if [ "$s" = active ]; then echo 4242; else echo 0; fi ;;
  esac ;;
cat) case " $fleet " in *" ${units[0]} "*) ;; *) exit 1 ;; esac ;;
esac
exit 0
EOF
  } >"$R/stubs/systemctl"
  {
    echo '#!/usr/bin/env bash'
    cat <<'EOF'
# /etc/claude-code is mounted read-only, as the host's root-owned /etc is to its
# user; this sudo writes the read-write mount of the same directory.
D=${0%/*}; D=${D%/*}
printf '%s\n' "$*" >>"$D/sudo.log"
etc=${PFM_REHEARSAL_ETC:?sudo stub: PFM_REHEARSAL_ETC unset}
[ "${1:-}" = -n ] && shift
args=()
for a in "$@"; do
  case $a in /etc/claude-code | /etc/claude-code/*) args+=("$etc${a#/etc/claude-code}") ;; *) args+=("$a") ;; esac
done
exec "${args[@]}"
EOF
  } >"$R/stubs/sudo"
  chmod 755 "$R/stubs/systemctl" "$R/stubs/sudo"
  docker build -q -t "$IMAGE" -f "$REPO_ROOT/infra/fence/pfm-dev.Dockerfile" "$REPO_ROOT/infra/fence" >"$R/build.log" 2>&1 ||
    fail build "docker build of $IMAGE failed (see build.log)"
  trap 'docker rm -f "$NAME" >/dev/null 2>&1' EXIT
  # The Go module cache is the fence's volume, read-only; build cache and
  # telemetry config live in the container's /tmp, never in the home.
  docker run -d --name "$NAME" --init --label pfm.fence=1 --user "$(id -u):$(id -g)" \
    -v "$H:$home" \
    -v "$scratch/etc/claude-code:/etc/claude-code:ro" \
    -v "$scratch/etc/claude-code:$C_ETC" \
    -v "$R:$C_REHEARSAL" \
    -v "$R/proc:$C_REHEARSAL/proc" \
    -v "$R/stubs:$C_REHEARSAL/stubs:ro" \
    -v "$git_common:$C_GIT:ro" \
    -v "pfm-dev-gomod:$C_GOMOD:ro" \
    -e "HOME=$home" \
    -e "PATH=$C_REHEARSAL/stubs:$home/.local/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
    -e PFM_LOG_LEVEL=off \
    -e "PFM_PROC_ROOT=$C_REHEARSAL/proc" \
    -e "PFM_REHEARSAL_ETC=$C_ETC" \
    -e "GOMODCACHE=$C_GOMOD" \
    -e GOCACHE=/tmp/go-build \
    -e GOPATH=/tmp/go \
    -e "PFM_DEV_REPO_GIT_DIR=$C_GIT/$git_rel" \
    -e "PFM_DEV_REPO_WORK_TREE=$clone" \
    "$IMAGE" sleep infinity >>"$R/build.log" 2>&1 || fail build "docker run of $IMAGE failed (see build.log)"
  local qclone qpfm qjid
  printf -v qclone '%q' "$clone"
  printf -v qpfm '%q' "$home/.local/bin/pfm"
  # The host's own environment: an XDG_CONFIG_HOME override hides the legacy
  # config that host-install's smoke test meets on a real crossing.
  inside "make -C $qclone/pfm host-install" >>"$R/build.log" 2>&1
  rc=$?
  [ $rc -eq 0 ] || fail build "make host-install exit $rc: $(tail -1 "$R/build.log")"
  [ -x "$H/.local/bin/pfm" ] || fail build "make host-install left no .local/bin/pfm"
  if [ -n "$legacy" ] && ! grep -qF 'host-install: binary swapped; pfm refuses until' "$R/build.log"; then
    fail build "host-install did not name the pending migration"
  fi
  local head_sha vcs stamp
  head_sha=$(repo_head 2>&1) || fail build "reading HEAD of $REPO_ROOT failed: $head_sha"
  vcs=$(inside "go version -m $qpfm" 2>&1)
  printf '%s\n' "$vcs" >>"$R/build.log"
  stamp=""
  while IFS= read -r line; do
    if [[ $line =~ ^[[:space:]]*build[[:space:]]+vcs\.revision=(.*)$ ]]; then stamp=${BASH_REMATCH[1]}; break; fi
  done <<<"$vcs"
  { [ -n "$stamp" ] && [ "$stamp" = "$head_sha" ]; } || fail build "pfm carries no VCS stamp of HEAD $head_sha"
  pass build

  tree_hash "$H" "$R/hash-before.txt" || fail hash-before "hashing $H failed"
  pass hash-before

  inside "$qpfm install --skip-harvest" >"$R/plan.txt" 2>"$R/preview.log"
  rc=$?
  cat "$R/plan.txt" >>"$R/preview.log"
  [ $rc -eq 0 ] || fail preview "pfm install --skip-harvest exit $rc: $(tail -1 "$R/preview.log")"
  tree_hash "$H" "$R/hash-preview.txt" || fail preview "hashing $H failed"
  cmp -s "$R/hash-before.txt" "$R/hash-preview.txt" ||
    fail preview "tree changed at $(tree_diff "$R/hash-before.txt" "$R/hash-preview.txt")"
  pass preview

  inside "$qpfm install --yes --skip-harvest" >"$R/apply.log" 2>&1
  rc=$?
  local jdir jid
  jdir=""
  while IFS= read -r line; do
    case $line in 'install journal: '*) jdir=${line#install journal: } ;; esac
  done <"$R/apply.log"
  [ $rc -eq 0 ] || fail apply "pfm install --yes exit $rc: $(tail -1 "$R/apply.log")"
  [ -n "$jdir" ] || fail apply "pfm install --yes printed no 'install journal:' line"
  case $jdir in "$home"/?*) ;; *) fail apply "journal $jdir is outside home $home" ;; esac
  jid=${jdir##*/}
  if [ "$stress" = 1 ]; then
    [ -f "$dropin" ] || fail apply "--stress: the managed drop-in $C_ETC/managed-settings.d/pfm.json was not written"
    grep -qF /etc/claude-code/managed-settings.d/pfm.json "$R/sudo.log" 2>/dev/null ||
      fail apply "--stress: sudo.log names no write of /etc/claude-code/managed-settings.d/pfm.json"
  fi
  pass apply

  inside "$qpfm doctor" >"$R/doctor.log" 2>&1
  rc=$?
  echo "(doctor exit $rc; only layout findings are judged)" >>"$R/doctor.log"
  local finding
  finding=""
  while IFS= read -r line; do
    case $line in
    'session-store: '* | 'managed-cleanup: '* | 'legacy: '* | 'state: '* | 'layout: '*)
      [ "$line" = 'managed-cleanup: check off by config' ] || { finding=$line; break; } ;;
    esac
  done <"$R/doctor.log"
  [ -z "$finding" ] || fail doctor "$finding"
  pass doctor

  inside "$qpfm install --yes --skip-harvest" >"$R/apply-again.log" 2>&1
  rc=$?
  [ $rc -eq 0 ] || fail apply-again "pfm install --yes exit $rc: $(tail -1 "$R/apply-again.log")"
  local again
  again=""
  while IFS= read -r line; do
    case $line in 'install journal: '*) again=$line ;; esac
  done <"$R/apply-again.log"
  [ -z "$again" ] || fail apply-again "the second apply recorded changes: $again"
  pass apply-again

  local verdict
  bash "$0" compare "$backup" "$H" "$H/${jdir#"$home"/}" >"$R/manifest.log" 2>&1
  verdict=""
  while IFS= read -r line || [ -n "$line" ]; do verdict=$line; done <"$R/manifest.log"
  [ "$verdict" = "manifest: ok" ] || fail manifest "${verdict#manifest: }"
  pass manifest

  # Units the rollback leaves stopped are judged from the log lines it adds.
  local logged=0
  if [ -f "$R/systemctl.log" ]; then
    while IFS= read -r line; do ((logged += 1)); done <"$R/systemctl.log"
  fi
  printf -v qjid '%q' "$jid"
  inside "$qpfm install --rollback $qjid" >"$R/rollback.log" 2>&1
  rc=$?
  [ $rc -eq 0 ] || fail rollback "pfm install --rollback $jid exit $rc: $(tail -1 "$R/rollback.log")"
  if [ -n "$legacy" ]; then
    local refuse stopped="" line w x u verb
    refuse=""
    while IFS= read -r line; do
      case $line in '  next    this pfm refuses the restored legacy config'*) refuse=$line; break ;; esac
    done <"$R/rollback.log"
    [ -n "$refuse" ] || fail rollback "no next step printed for the restored legacy config"
    case $refuse in *"fleet units left stopped: "*) stopped=${refuse##*fleet units left stopped: } ;; esac
    [ "$stopped" = none ] && stopped=""
    if [ -n "$stopped" ] && [ -f "$R/systemctl.log" ]; then
      local idx=0
      while IFS= read -r line; do
        ((idx += 1))
        [ "$idx" -le "$logged" ] && continue
        read -ra w <<<"$line"
        verb=""
        for x in "${w[@]}"; do case $x in -*) ;; *) verb=$x; break ;; esac; done
        case $verb in start | restart) ;; *) continue ;; esac
        for u in $stopped; do
          for x in "${w[@]}"; do
            [ "$x" = "$u" ] && fail rollback "fleet unit $u restarted on a binary that refuses the legacy config"
          done
        done
      done <"$R/systemctl.log"
    fi
  fi
  if [ "$stress" = 1 ] && [ -e "$dropin" ]; then
    fail rollback "--stress: the managed drop-in $C_ETC/managed-settings.d/pfm.json is still present"
  fi
  pass rollback

  # Before the pairing: make rollback changes .local/bin/pfm.
  tree_hash "$H" "$R/hash-after.txt" || fail hash-after "hashing $H failed"
  cmp -s "$R/hash-before.txt" "$R/hash-after.txt" || fail hash-after "$(tree_diff "$R/hash-before.txt" "$R/hash-after.txt")"
  pass hash-after

  # pair: the rollback's own next steps, then the restored binary answers.
  local cmds=() c rest number
  while IFS= read -r c; do
    case $c in
    '  next    '[0-9]*'. '*)
      rest=${c#'  next    '}; number=${rest%%.*}
      [[ $number =~ ^[0-9]+$ ]] && cmds+=("${rest#"$number. "}") ;;
    esac
  done <"$R/rollback.log"
  [ ${#cmds[@]} -gt 0 ] || cmds=("make -C $qclone/pfm rollback")
  cmds+=("$qpfm chat ls") # non-interactive: the ls picker needs a terminal
  : >"$R/pair.log"
  # A backup never carries credentials, and the pre-migration binary refuses a
  # config naming a Codex home without a valid auth.json: each such home in the
  # scratch copy (never the backup) gets a placeholder, after hash-after.
  local cfg="$H/.config/pfm/pfm.config.json" ch
  if [ -f "$cfg" ]; then
    while IFS= read -r ch; do
      case $ch in "$home"/*) ch=$H/${ch#"$home"/} ;; *) continue ;; esac
      [ -f "$ch/auth.json" ] && continue
      mkdir -p "$ch" && printf '{"tokens":{"access_token":"rehearsal","account_id":"rehearsal"}}\n' >"$ch/auth.json" ||
        fail pair "placeholder $ch/auth.json could not be written"
      printf '# placeholder credential: %s/auth.json\n' "$ch" >>"$R/pair.log"
    done < <(jq -r '.codex.homes[]? | if type == "object" then .home else . end // empty' "$cfg" 2>/dev/null)
  fi
  for c in "${cmds[@]}"; do
    printf '$ %s\n' "$c" >>"$R/pair.log"
    inside "$c" >"$R/pair.last.log" 2>&1
    rc=$?
    cat "$R/pair.last.log" >>"$R/pair.log"
    [ $rc -eq 0 ] || fail pair "$c exit $rc: $(tail -1 "$R/pair.last.log")"
  done
  rm -f "$R/pair.last.log"
  pass pair

  { echo "REHEARSAL PASS"; printf '%s\n' "${STEP_LINES[@]}"; } >"$R/verdict.txt"
  # The copies go (their read-only directories made writable first); the
  # verdict, logs, plan.txt and hashes in SCRATCH/rehearsal/ stay.
  if [ "${PFM_REHEARSAL_KEEP_SCRATCH:-0}" = 1 ]; then
    echo "host-rehearsal: PFM_REHEARSAL_KEEP_SCRATCH=1 — kept $scratch whole" >&2
  else
    docker rm -f "$NAME" >/dev/null 2>&1
    chmod -R u+w "$H" "$scratch/etc" 2>/dev/null
    if rm -rf -- "${H:?}" "${scratch:?}/etc"; then echo "host-rehearsal: removed $H and $scratch/etc; kept $R" >&2
    else echo "host-rehearsal: WARN could not remove all of $H and $scratch/etc — remove them by hand" >&2; fi
  fi
  echo "REHEARSAL PASS"
  echo "$R/verdict.txt"
}

case ${1:-} in
compare) [ $# -eq 4 ] || usage; cmd_compare "$2" "$3" "$4" ;;
*)
  if [ $# -eq 2 ]; then cmd_rehearse "$1" "$2" 0
  elif [ $# -eq 3 ] && [ "$3" = --stress ]; then cmd_rehearse "$1" "$2" 1
  else usage; fi ;;
esac
