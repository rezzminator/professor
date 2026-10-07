#!/usr/bin/env bash
# Fixture-driven tests for infra/fence/housekeeping.sh — its nine steps
# (containers, images, lane-images, legacy-volumes, checkouts, build-cache,
# cache-budget, scratch), the sim volume name, the cache
# volume ensure, the caller-survival law under `set -euo pipefail`, the stamp's
# home, and the fence's label + volume contract in docker-compose.yml and
# pfm-dev.Dockerfile. docker is a stub, so no container, image or volume is
# ever touched.
#
#   bash infra/fence/lanes/tests/housekeeping_test.sh
#   HOUSEKEEPING_SUT=/tmp/old/housekeeping.sh bash …/housekeeping_test.sh   # red-first
#
# BROKEN STATE: a missing SUT exits 2 before any assertion; python3 without
# PyYAML fails the compose contract case by name, never skips it.
set -uo pipefail

FENCE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SUT="${HOUSEKEEPING_SUT:-$FENCE/housekeeping.sh}"
SHTEST_TAG=fence-housekeeping-test
# shellcheck source=/dev/null
source "$FENCE/../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "housekeeping_test: no housekeeping.sh at $SUT" >&2; exit 2; }

# ── fixture repo ────────────────────────────────────────────────────────────
# A real git repo $FX (the SUT and a lanes/root.sh stub committed) with a linked
# worktree $SECOND: housekeeping keeps the current lane-root hash of every
# checkout `git worktree list` names. Each stub prints its own checkout's
# untracked lanes/hash file and fails without one (`hashes MAIN SECOND` sets
# them). The stamp dir comes from the main checkout's name (.hkfx-N →
# /tmp/hkfx-N/fence).
FXNAME="hkfx-$$"
FX="$T/.$FXNAME"
SECOND="$T/hkwt-$$"
mkdir -p "$FX/infra/fence/lanes"
cp "$SUT" "$FX/infra/fence/housekeeping.sh"
cat >"$FX/infra/fence/lanes/root.sh" <<'EOF'
h="${0%/*}/hash"
[ -s "$h" ] || { echo "root: HASH-UNDERIVABLE: fixture" >&2; exit 2; }
printf '%s\n' "$(<"$h")"
EOF
(cd "$FX" && git init -q && git add -A && git -c user.email=t@t -c user.name=t commit -qm fixture && git worktree add -q "$SECOND" 2>/dev/null) ||
  { echo "housekeeping_test: the fixture repo could not be built" >&2; exit 2; }
HK="$FX/infra/fence/housekeeping.sh"
hashes() {
  printf '%s\n' "$1" >"$FX/infra/fence/lanes/hash"
  if [ -n "$2" ]; then printf '%s\n' "$2" >"$SECOND/infra/fence/lanes/hash"; else rm -f "$SECOND/infra/fence/lanes/hash"; fi
}
hashes aaa eee

# ── docker stub ─────────────────────────────────────────────────────────────
# Listings come from files in $STUB_DIR (exited, running, builds, created,
# roots "ID REF HASH", pins, volumes); a name/ref listed in $STUB_DIR/inuse is
# refused by rm/rmi/volume rm the way the daemon refuses an object in use.
# STUB_DOWN=1: every call fails · STUB_PRUNE=ok|busy|fail · STUB_DF=ok|fail ·
# STUB_MB: the gocache size df reports ("" = none), STUB_GOMOD_MB / STUB_UV_MB the
# gomod / uv sizes (default 10) · STUB_VOLUME=0: no such volume · STUB_BUILDER=ok|busy|fail ·
# $STUB_DIR/fenceimg: the local fence image `docker run` removes root-owned scratch with.
BIN="$T/bin"
mkdir -p "$BIN"
cat >"$BIN/docker-stub.bash" <<'STUB'
_stub_f() { [ -f "$S/$1" ] && printf '%s' "$(<"$S/$1")"; return 0; }
_stub_inuse() { [ -f "$S/inuse" ] && [[ $'\n'"$(<"$S/inuse")"$'\n' == *$'\n'"$1"$'\n'* ]]; }
docker() {
local -
set +eu +o pipefail
local IFS=$' \t\n'
local S="$STUB_DIR" all want a id ref hash
printf '%s\n' "$*" >>"$STUB_DOCKER_LOG"
[ "${STUB_DOWN:-0}" = 1 ] && { echo "Cannot connect to the Docker daemon at unix:///var/run/docker.sock" >&2; return 1; }
case "$1" in
  ps) case "$*" in
    *status=exited*) _stub_f exited ;;
    *name=pfm-lane-build-*)
      if [ "${STUB_BUILDS_FAIL:-0}" = 1 ]; then echo 'fixture build listing daemon error' >&2; return 12; fi
      _stub_f builds ;;
    *) _stub_f running ;;
  esac ;;
  inspect) _stub_f created ;;
  rm) for a in "${@:2}"; do [ "$a" = -f ] || ! _stub_inuse "$a" || { echo "Error response from daemon: conflict" >&2; return 1; }; done ;;
  rmi)
    case "${STUB_RMI:-ok}" in
      missing) echo "Error response from daemon: No such image: $2" >&2; return 1 ;;
      fail) echo "Error response from daemon: rmi boom" >&2; return 1 ;;
    esac
    if _stub_inuse "$2"; then echo "Error response from daemon: conflict: unable to remove repository reference \"$2\" (must force) - container 0bad is using its referenced image 1f2e" >&2; return 1; fi ;;
  image)
    case "$2" in
      prune)
        case "${STUB_PRUNE:-ok}" in
          ok) echo "Total reclaimed space: 0B" ;;
          busy) echo "Error response from daemon: a prune operation is already running" >&2; return 1 ;;
          *) echo "Error response from daemon: prune boom" >&2; return 1 ;;
        esac ;;
      ls)
        if [ "${STUB_IMAGE_LS_FAIL:-0}" = 1 ]; then echo 'fixture image listing daemon error' >&2; return 12; fi
        case "$*" in
          *label=professor.lane-root=*)
            all="$*"; want="${all##*label=professor.lane-root=}"; want="${want%% *}"
            [ -f "$S/roots" ] || return 0
            while read -r id ref hash; do
              [ "$hash" = "$want" ] && printf '%s\n' "$id"
            done <"$S/roots"
            ;;
          *label=professor.lane-root*)
            [ -f "$S/roots" ] || return 0
            while read -r id ref hash; do
              printf '%s %s\n' "$id" "$ref"
            done <"$S/roots"
            ;;
          *pfm-lane-base*) _stub_f pins ;;
          *label=pfm.fence=1*) _stub_f fenceimg ;;
        esac ;;
    esac ;;
  volume)
    case "$2" in
      ls) _stub_f volumes ;;
      inspect) [ "${STUB_VOLUME:-1}" = 1 ] || { echo "Error response from daemon: get $3: no such volume" >&2; return 1; } ;;
      rm) if _stub_inuse "$3"; then echo "Error response from daemon: remove $3: volume is in use - [0bad]" >&2; return 1; fi ;;
      create) echo "$3" ;;
    esac ;;
  system)
    [ "${STUB_DF:-ok}" = ok ] || { echo "Error response from daemon: df boom" >&2; return 1; }
    [ -z "${STUB_MB:-}" ] || printf 'acme_pgdata\t9.5GB\npfm-dev-gocache\t%sMB\n' "$STUB_MB"
    printf 'pfm-dev-gomod\t%sMB\npfm-dev-lintcache\t10MB\npfm-lane-uv-cache\t%sMB\npfm-lane-harvest-cache\t0B\n' "${STUB_GOMOD_MB:-10}" "${STUB_UV_MB:-10}" ;;
  builder)
    case "${STUB_BUILDER:-ok}" in
      ok) echo "Total reclaimed space: 1.5GB" ;;
      busy) echo "Error response from daemon: a prune operation is already running" >&2; return 1 ;;
      *) echo "Error response from daemon: builder boom" >&2; return 1 ;;
    esac ;;
  run) # the pre-fix sizer (`docker run … alpine du -sm /c`) fails with the df probe
    case "$*" in *"--entrypoint rm"*)
      all="$*"; a="${all#*-v }"; a="${a%%:/reap*}"; want="${all##*/reap/}"
      chmod -R u+w "$a/$want" 2>/dev/null; /bin/rm -rf -- "${a:?}/$want"; return 0 ;;
    esac
    [ "${STUB_DF:-ok}" = ok ] || return 125
    case "$*" in *"du -sm"*) [ -z "${STUB_MB:-}" ] || printf '%s\t/c\n' "$STUB_MB" ;; esac ;;
esac
return 0
}
STUB
cat >"$BIN/docker" <<'STUB_WRAPPER'
#!/usr/bin/env bash
. "${0%/*}/docker-stub.bash"
docker "$@"
STUB_WRAPPER
chmod +x "$BIN/docker"
# git passes through, except `worktree list` fails under STUB_GIT_FAIL=1.
cat >"$BIN/git" <<EOF
#!/usr/bin/env bash
case "\$*" in *"worktree list"*) [ "\${STUB_GIT_FAIL:-0}" = 1 ] && { echo "fatal: worktree list refused (fixture)" >&2; exit 128; } ;; esac
exec $(command -v git) "\$@"
EOF
chmod +x "$BIN/git"
export PATH="$BIN:$PATH"
export STUB_DOCKER_LOG="$T/docker.log" STUB_DIR="$T/stub" PFM_FENCE_STAMP_DIR="$T/stamp" PFM_FENCE_PROJECT_TMP="$T/ptmp" PFM_FENCE_GOCACHE_MB=100
. "$BIN/docker-stub.bash"
export -f docker _stub_f _stub_inuse
STAMP="$PFM_FENCE_STAMP_DIR/cache-budget.stamp"

# shellcheck source=/dev/null
. "$HK"

fresh() {
  rm -rf "$PFM_FENCE_STAMP_DIR" "$STUB_DIR" "$STUB_DOCKER_LOG"
  mkdir -p "$PFM_FENCE_STAMP_DIR" "$STUB_DIR"; : >"$STUB_DOCKER_LOG"
}
logged() { grep -qxF -- "$1" "$STUB_DOCKER_LOG"; }
warned() { grep -q "^WARN fence housekeeping: $1 failed: " "$T/err"; }
nowarn() { ! grep -q '^WARN' "$T/err"; }
hk() { fence_housekeeping "$@" >"$T/out" 2>"$T/err"; }
iso_ago() { date -u -d "@$(( $(date +%s) - $1 ))" +%Y-%m-%dT%H:%M:%S.123456789Z; }

# 1 — containers: only labelled listings feed a removal; exited go, running past
#     the age limit are forced out, younger ones and unparsable dates stay
fresh; touch "$STAMP"
printf 'e1\ne2\n' >"$STUB_DIR/exited"; printf 'c1\nc2\nc3\n' >"$STUB_DIR/running"
printf '/old-shell %s\n/young-lane %s\n/odd garbage\n' "$(iso_ago $((7 * 3600)))" "$(iso_ago 600)" >"$STUB_DIR/created"
hk
if logged 'ps -a -q --filter label=pfm.fence=1 --filter status=exited' && logged 'ps -q --filter label=pfm.fence=1' &&
  logged 'rm e1 e2' && logged 'rm -f old-shell' && ! grep -q 'young-lane\|odd' <<<"$(grep '^rm' "$STUB_DOCKER_LOG")" &&
  grep -q 'removed old-shell' "$T/err" && warned containers && grep -q "odd" "$T/err"; then
  ok "containers: labelled exited removed, labelled running > 6h forced out, younger and unparsable kept"
else bad "containers" "$(cat "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi

# 2 — an invalid age limit warns and falls back to 6 hours; a running container
#     labelled pfm.fence.long-lived=1 (a shell, the demo, a release rehearsal)
#     is never age-reaped, however old
fresh; touch "$STAMP"; printf 'c1\nc2\n' >"$STUB_DIR/running"
printf '/mid %s\n/demo %s 1\n' "$(iso_ago $((5 * 3600)))" "$(iso_ago $((30 * 3600)))" >"$STUB_DIR/created"
PFM_FENCE_CONTAINER_MAX_HOURS=6h hk
if warned containers && grep -q "PFM_FENCE_CONTAINER_MAX_HOURS" "$T/err" && ! grep -q '^rm -f' "$STUB_DOCKER_LOG" &&
  grep -q 'pfm.fence.long-lived' <(grep '^inspect ' "$STUB_DOCKER_LOG"); then
  ok "containers: PFM_FENCE_CONTAINER_MAX_HOURS=6h warns and the default 6 applies; a long-lived container past it is kept"
else bad "containers max hours" "$(cat "$T/err")" "$(cat "$STUB_DOCKER_LOG")"; fi

# 3 — images: dangling images labelled pfm.fence=1 only, never -a; a concurrent
#     prune is not a WARN, any other failure is
fresh; touch "$STAMP"; hk
r1=0; logged 'image prune -f --filter label=pfm.fence=1' && ! grep -q 'image prune.* -a' "$STUB_DOCKER_LOG" && nowarn && r1=1
fresh; touch "$STAMP"; STUB_PRUNE=busy hk; r2=0; nowarn && r2=1
fresh; touch "$STAMP"; STUB_PRUNE=fail hk; r3=0; warned images && grep -q 'prune boom' "$T/err" && r3=1
if [ "$r1$r2$r3" = 111 ]; then ok "images: prune filters label=pfm.fence=1; a concurrent prune is silent, a failure warns"
else bad "images" "scoped=$r1 busy-silent=$r2 fail-warns=$r3" "$(cat "$T/err")"; fi
fresh; touch "$STAMP"; printf 'pfm-lane-build-aaa-123\npfm-lane-build-bbb\n' >"$STUB_DIR/builds"; hk aaa
if ! logged 'image prune -f --filter label=pfm.fence=1' &&
  [ "$(grep -c 'fence housekeeping: skipped the dangling fence image prune — lane root build(s) in flight: pfm-lane-build-aaa-123 pfm-lane-build-bbb' "$T/err")" -eq 1 ] &&
  nowarn && logged 'volume ls -q'; then
  ok "images: running suffixed and legacy lane builds skip only image prune; later steps run"
else bad "running-build prune guard" "$(cat "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi
fresh; touch "$STAMP"; : >"$STUB_DIR/builds"; hk aaa
if logged 'image prune -f --filter label=pfm.fence=1' && nowarn; then
  ok "images: an empty build listing still prunes"
else bad "empty-build prune" "$(cat "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi
fresh; touch "$STAMP"; STUB_BUILDS_FAIL=1 hk aaa
if ! logged 'image prune -f --filter label=pfm.fence=1' &&
  [ "$(grep -c '^WARN fence housekeeping: images failed: listing running lane builds, prune skipped: fixture build listing daemon error$' "$T/err")" -eq 1 ] &&
  logged 'volume ls -q' &&
  (set -euo pipefail; STUB_BUILDS_FAIL=1 fence_housekeeping aaa >/dev/null 2>/dev/null); then
  ok "images: failed build listing warns, skips prune and keeps the caller alive"
else bad "failed-build-list prune guard" "$(cat "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi

# 4 — lane images: every root but the current hash of each checkout git lists
#     (the caller's aaa, the linked worktree's eee), and pins no running build
#     holds; a failing `git worktree list` or an underivable checkout removes
#     nothing and warns, naming the cause
lane_fixture() {
  printf 'r1 pfm-lane-root:aaa aaa\nr2 pfm-lane-root:bbb bbb\nr3 <none>:<none> ccc\nr4 mine:v1 bbb\nr5 mine:cur aaa\nr6 pfm-lane-root:eee eee\n' >"$STUB_DIR/roots"
  printf 'aaa\nbbb\nddd\neee\n' >"$STUB_DIR/pins"; printf 'pfm-lane-build-ddd\n' >"$STUB_DIR/builds"; printf 'mine:v1\n' >"$STUB_DIR/inuse"
}
lane_ok() {
  [ "$(grep '^rmi ' "$STUB_DOCKER_LOG" | sort | tr '\n' ' ')" = "rmi mine:v1 rmi pfm-lane-base:bbb rmi pfm-lane-root:bbb rmi r3 " ] &&
    grep -q 'mine:v1' "$T/err" && nowarn
}
fresh; touch "$STAMP"; lane_fixture; hk aaa; r1=0; lane_ok && r1=1
fresh; touch "$STAMP"; lane_fixture; hk; r2=0; lane_ok && r2=1
fresh; touch "$STAMP"; lane_fixture; hashes aaa ''; hk; hashes aaa eee
r3=0; ! grep -q '^rmi' "$STUB_DOCKER_LOG" && warned lane-images && grep -q "$SECOND" "$T/err" && r3=1
fresh; touch "$STAMP"; lane_fixture; STUB_GIT_FAIL=1 hk
r4=0; ! grep -q '^rmi' "$STUB_DOCKER_LOG" && warned lane-images && grep -q 'worktree list' "$T/err" && r4=1
if [ "$r1$r2$r3$r4" = 1111 ]; then ok "lane images: every listed checkout's current roots and running builds' pins survive; a failing worktree list or an underivable checkout removes nothing and warns"
else bad "lane images" "given=$r1 derived=$r2 underivable-checkout=$r3 worktree-list-fails=$r4" "$(cat "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi
fresh; touch "$STAMP"; printf 'aaa-unique\n' >"$STUB_DIR/pins"; : >"$STUB_DIR/builds"; hk aaa
if ! logged 'rmi pfm-lane-base:aaa-unique'; then
  ok "a current-hash pin survives before its build container starts"
else bad "pin creation race" "$(cat "$STUB_DOCKER_LOG")"; fi
fresh; STUB_RMI=missing _fence_hk_rmi pfm-lane-root:gone 2>"$T/err"
if grep -q 'fence housekeeping: image pfm-lane-root:gone already removed' "$T/err" && nowarn; then
  ok "an image removed by another run is reported without WARN"
else bad "already-removed image" "$(cat "$T/err")"; fi
fresh; STUB_RMI=fail _fence_hk_rmi pfm-lane-root:broken 2>"$T/err"
if warned lane-images && grep -q 'rmi boom' "$T/err"; then ok "another rmi failure still warns without failing the caller"
else bad "rmi failure" "$(cat "$T/err")"; fi

fresh; STUB_IMAGE_LS_FAIL=1 _fence_hk_lane_images aaa 2>"$T/err"
if grep -qxF 'WARN fence housekeeping: lane-images failed: listing lane roots: fixture image listing daemon error' "$T/err" &&
  [ "$(<"$STUB_DOCKER_LOG")" = "image ls --filter label=professor.lane-root --format {{.ID}} {{.Repository}}:{{.Tag}}" ]; then
  ok "lane images: a failed root listing warns and stops the sweep"
else bad "lane-image listing failure" "$(cat "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi

# 5 — legacy volumes: ^(fence|infra)_pfm-dev- only; the host's other stacks untouched
fresh; touch "$STAMP"
printf '%s\n' fence_pfm-dev-gocache infra_pfm-dev-lintcache fence_pfm-dev-gomod pfm-dev-gocache pfm-dev-gomod \
  acme_pgdata shop_db tracing_clickhouse notes_data 3f2a9c0e1b7d4a5f8e6c2d1b0a9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c3b2a1f \
  myfence_pfm-dev-gocache fence_pfm-devx fence_other >"$STUB_DIR/volumes"
printf 'fence_pfm-dev-gomod\n' >"$STUB_DIR/inuse"
hk
if [ "$(grep '^volume rm ' "$STUB_DOCKER_LOG" | sort | tr '\n' ' ')" = "volume rm fence_pfm-dev-gocache volume rm fence_pfm-dev-gomod volume rm infra_pfm-dev-lintcache " ] &&
  grep -q 'removed legacy volume fence_pfm-dev-gocache' "$T/err" && grep -q 'fence_pfm-dev-gomod' "$T/err" && nowarn; then
  ok "legacy volumes: the three fence_/infra_pfm-dev- volumes only (an in-use one kept); every other volume untouched"
else bad "legacy volumes" "$(grep '^volume' "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi

# 6 — budget: over → the volume is removed (no file walk, no alpine), day stamped
fresh; STUB_MB=250 hk
if logged 'volume rm pfm-dev-gocache' && [ -f "$STAMP" ] && grep -q '250 MB > 100 MB' "$T/err" &&
  ! grep -q 'alpine\|find \|^run ' "$STUB_DOCKER_LOG" && nowarn; then
  ok "budget: over budget removes pfm-dev-gocache and stamps the day"
else bad "budget over" "$(cat "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi

# 7 — budget: under → kept, day stamped
fresh; STUB_MB=40 hk
if ! grep -q '^volume rm pfm-dev-gocache' "$STUB_DOCKER_LOG" && [ -f "$STAMP" ] && nowarn; then ok "budget: under budget keeps the cache and stamps the day"
else bad "budget under" "$(cat "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi

# 8 — budget: over but mounted → the daemon's refusal is a WARN naming size and reason, no stamp
fresh; printf 'pfm-dev-gocache\n' >"$STUB_DIR/inuse"; STUB_MB=250 hk
if warned cache-budget && grep -q '250 MB' "$T/err" && grep -q 'in use' "$T/err" && [ ! -f "$STAMP" ]; then
  ok "budget: a mounted over-budget cache warns with its size and reason and is retried"
else bad "budget mounted" "$(cat "$T/err")"; fi

# 9 — budget: a fresh stamp skips the sizing entirely
fresh; touch "$STAMP"; STUB_MB=250 hk
if ! grep -q '^system df\|^volume inspect pfm-dev-gocache\|^volume rm pfm-dev-gocache' "$STUB_DOCKER_LOG"; then ok "budget: a fresh stamp skips the check"
else bad "budget stamp" "$(cat "$STUB_DOCKER_LOG")"; fi

# 10 — budget: an unmeasurable size (df fails, or reports nothing) warns, never passes, no stamp
fresh; STUB_DF=fail STUB_MB=250 hk; r1=0; warned cache-budget && [ ! -f "$STAMP" ] && r1=1
fresh; STUB_MB='' hk; r2=0; warned cache-budget && [ ! -f "$STAMP" ] && r2=1
if [ "$r1$r2" = 11 ]; then ok "budget: an unmeasurable size warns and leaves no stamp"
else bad "budget unmeasurable" "df-fails=$r1 no-row=$r2" "$(cat "$T/err")"; fi

# 11 — budget: no volume yet → nothing sized, nothing removed, no WARN
fresh; STUB_VOLUME=0 STUB_MB=250 hk
if ! grep -q '^system df\|^volume rm pfm-dev-gocache' "$STUB_DOCKER_LOG" && nowarn; then ok "budget: an absent volume is a no-op"
else bad "budget absent" "$(cat "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi

# 12 — budget: an invalid PFM_FENCE_GOCACHE_MB warns and the 15000 default applies
fresh; PFM_FENCE_GOCACHE_MB=15G STUB_MB=250 hk
if warned cache-budget && grep -q 'PFM_FENCE_GOCACHE_MB' "$T/err" && ! logged 'volume rm pfm-dev-gocache' && [ -f "$STAMP" ]; then
  ok "budget: PFM_FENCE_GOCACHE_MB=15G warns and the default applies"
else bad "budget invalid" "$(cat "$T/err")" "$(cat "$STUB_DOCKER_LOG")"; fi

# 13 — F1: under the caller's set -euo pipefail, a failing size probe, a dead
#      daemon and an unwritable stamp dir each WARN and the caller survives
survives() { # label env… — runs housekeeping in a set -euo pipefail caller
  local label="$1" rc; shift
  fresh
  env "$@" bash -c 'set -euo pipefail; . "$1"; fence_housekeeping; echo SURVIVED' _ "$HK" >"$T/out" 2>"$T/err"
  rc=$?
  if [ "$rc" -eq 0 ] && [ "$(cat "$T/out")" = SURVIVED ] && grep -q '^WARN fence housekeeping: [a-z-]* failed: ' "$T/err"; then
    ok "set -euo pipefail caller survives: $label"
  else bad "set -euo pipefail caller survives: $label" "rc=$rc stdout=[$(cat "$T/out")]" "$(cat "$T/err")"; fi
}
survives "a failing size probe" STUB_DF=fail STUB_MB=250
survives "a dead docker daemon" STUB_DOWN=1
: >"$T/afile"
survives "an unwritable stamp dir" STUB_MB=40 PFM_FENCE_STAMP_DIR="$T/afile/sub"

# 14 — the stamp lives in /tmp/{project}/fence/, {project} = the main checkout's
#      name minus a leading dot, from the checkout and from a linked worktree
fresh; rm -rf "/tmp/$FXNAME"
env -u PFM_FENCE_STAMP_DIR -u PFM_FENCE_PROJECT_TMP STUB_MB=40 bash -c '. "$1"; fence_housekeeping' _ "$HK" 2>"$T/err"
r1=0; [ -f "/tmp/$FXNAME/fence/cache-budget.stamp" ] && r1=1
rm -rf "/tmp/$FXNAME"
rm -f "$FX/infra/fence/lanes/hash"
rm -rf "/tmp/$FXNAME" "/tmp/hkwt-$$"
env -u PFM_FENCE_STAMP_DIR -u PFM_FENCE_PROJECT_TMP STUB_MB=40 bash -c '. "$1"; fence_housekeeping' _ "$SECOND/infra/fence/housekeeping.sh" 2>>"$T/err"
r2=0; [ -f "/tmp/$FXNAME/fence/cache-budget.stamp" ] && [ ! -e "/tmp/hkwt-$$/fence" ] && r2=1
rm -rf "/tmp/$FXNAME" "/tmp/hkwt-$$"
hashes aaa eee
if [ "$r1$r2" = 11 ]; then ok "stamp: /tmp/{project}/fence/ from the checkout and from a linked worktree"
else bad "stamp home" "checkout=$r1 worktree=$r2" "$(cat "$T/err")"; fi

# 15 — fence_volumes_ensure: one inspect when all five exist, a create each when
#      not, and a dead daemon never fails a set -euo pipefail caller
fresh; fence_volumes_ensure 2>"$T/err"
r1=0; logged 'volume inspect pfm-dev-gocache pfm-dev-gomod pfm-dev-lintcache pfm-lane-harvest-cache pfm-lane-uv-cache' && ! grep -q '^volume create' "$STUB_DOCKER_LOG" && r1=1
fresh; STUB_VOLUME=0 fence_volumes_ensure 2>"$T/err"
r2=0; logged 'volume create pfm-dev-gocache' && logged 'volume create pfm-dev-gomod' && logged 'volume create pfm-dev-lintcache' && logged 'volume create pfm-lane-harvest-cache' && logged 'volume create pfm-lane-uv-cache' && r2=1
fresh; STUB_DOWN=1 bash -c 'set -euo pipefail; . "$1"; fence_volumes_ensure; echo SURVIVED' _ "$HK" >"$T/out" 2>"$T/err"
r3=0; [ "$(cat "$T/out")" = SURVIVED ] && warned volumes && r3=1
if [ "$r1$r2$r3" = 111 ]; then ok "volumes: ensured with one inspect, created when absent, a dead daemon warns"
else bad "volumes ensure" "present=$r1 absent=$r2 down=$r3" "$(cat "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi

# 16 — contract: compose's caches are external and bare-named, its service is
#      labelled, and the Dockerfile's base stage labels every fence image
if python3 - "$FENCE/docker-compose.yml" <<'PY' 2>"$T/err"; then
import sys, yaml
c = yaml.safe_load(open(sys.argv[1]))
vols = c["volumes"]
assert sorted(vols) == ["pfm-dev-gocache", "pfm-dev-gomod", "pfm-dev-lintcache"], vols
for k, v in vols.items():
    assert v.get("external") is True and v.get("name") == k, (k, v)
assert str(c["services"]["pfm-dev"]["labels"]["pfm.fence"]) == "1", c["services"]["pfm-dev"].get("labels")
assert "labels" not in c["services"]["pfm-sim"] and c["services"]["pfm-sim"]["extends"]["service"] == "pfm-dev"
assert "pfm-dev-lintcache:/root/.cache/golangci-lint" in c["services"]["pfm-dev"]["volumes"]
PY
  ok "compose: three external bare-named caches, including shared lint cache"
else bad "compose contract" "$(cat "$T/err")"; fi
if awk '/^FROM .* AS pfm-base/ { b = 1; next } /^FROM / { b = 0 } b && /^LABEL pfm\.fence=1$/ { f = 1 } END { exit !f }' "$FENCE/pfm-dev.Dockerfile"; then
  ok "Dockerfile: LABEL pfm.fence=1 in the base stage, so every builder's image carries it"
else bad "Dockerfile label" "$(grep -n 'LABEL\|^FROM' "$FENCE/pfm-dev.Dockerfile")"; fi

# 17 — checkouts: only sim volumes recorded by this clone can be removed.
#      A mounted volume retains its ledger row for the next run.
SIM_GONE="$T/hksimgone-$$"; SIM_BUSY="$T/hksimbusy-$$"
git -C "$FX" worktree add -q "$SIM_GONE" 2>/dev/null
git -C "$FX" worktree add -q "$SIM_BUSY" 2>/dev/null
printf 'fff\n' >"$SIM_GONE/infra/fence/lanes/hash"
printf '999\n' >"$SIM_BUSY/infra/fence/lanes/hash"
FXK="$(fence_sim_volume "$FX")"; SK="$(fence_sim_volume "$SECOND")"
GONEK="$(fence_sim_volume "$SIM_GONE")"; BUSYK="$(fence_sim_volume "$SIM_BUSY")"
GONEROW="$SIM_GONE"$'\t'"$(_fence_hk_scratch_root "$SIM_GONE")"$'\t'"$GONEK"
BUSYROW="$SIM_BUSY"$'\t'"$(_fence_hk_scratch_root "$SIM_BUSY")"$'\t'"$BUSYK"
fresh; touch "$STAMP"
printf '%s\n' "$FXK" "$SK" "$GONEK" "$BUSYK" >"$STUB_DIR/volumes"; hk
if grep -qxF "$GONEROW" "$PFM_FENCE_STAMP_DIR/checkouts" && grep -qxF "$BUSYROW" "$PFM_FENCE_STAMP_DIR/checkouts" &&
  ! grep -q '^volume rm' "$STUB_DOCKER_LOG" && nowarn; then
  ok "checkouts: live checkout paths, scratch roots and sim volumes are recorded together"
else bad "checkouts record ownership" "$(cat "$PFM_FENCE_STAMP_DIR/checkouts")" "$(cat "$T/err")"; fi
cp "$PFM_FENCE_STAMP_DIR/checkouts" "$T/sim-ledger"
git -C "$FX" worktree remove --force "$SIM_GONE"
git -C "$FX" worktree remove --force "$SIM_BUSY"
fresh; touch "$STAMP"; cp "$T/sim-ledger" "$PFM_FENCE_STAMP_DIR/checkouts"
printf '%s\n' "$FXK" "$SK" "$GONEK" "$BUSYK" pfm-sim-harvest-other-clone-123 acme_pgdata pfm-dev-gomod >"$STUB_DIR/volumes"
printf '%s\n' "$BUSYK" >"$STUB_DIR/inuse"; hk
if ! logged 'volume rm pfm-sim-harvest-other-clone-123' &&
  [ "$(grep '^volume rm ' "$STUB_DOCKER_LOG" | sort | tr '\n' ' ')" = "$(printf 'volume rm %s\n' "$GONEK" "$BUSYK" | sort | tr '\n' ' ')" ] &&
  grep -q "removed $GONEK — its checkout is gone" "$T/err" && grep -q "kept $BUSYK" "$T/err" && nowarn; then
  ok "checkouts: another clone's unrecorded sim volume stays; a recorded gone volume goes; a mounted one stays"
else bad "checkouts sim ownership" "$(grep '^volume' "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi
if grep -qxF "$BUSYROW" "$PFM_FENCE_STAMP_DIR/checkouts" && ! grep -qxF "$GONEROW" "$PFM_FENCE_STAMP_DIR/checkouts"; then
  ok "checkouts: a mounted gone checkout's sim volume retains its ownership record"
else bad "checkouts sim retry ledger" "$(cat "$PFM_FENCE_STAMP_DIR/checkouts")"; fi
cp "$PFM_FENCE_STAMP_DIR/checkouts" "$T/sim-ledger"
fresh; touch "$STAMP"; cp "$T/sim-ledger" "$PFM_FENCE_STAMP_DIR/checkouts"
printf '%s\n' "$BUSYK" >"$STUB_DIR/volumes"; hk
if logged "volume rm $BUSYK" && ! grep -qxF "$BUSYROW" "$PFM_FENCE_STAMP_DIR/checkouts" && nowarn; then
  ok "checkouts: an unmounted recorded sim volume is retried and its completed row dropped"
else bad "checkouts sim retry" "$(cat "$T/err")"; fi

# 18 — checkouts: a gone checkout loses only stale timing/build/lanes/guard
#      directories; young purpose directories, other content and unrecorded
#      roots stay. An empty root can be removed and its row retired.
THIRD="$T/hk3-$$"; R3="/tmp/hk3-$$"; STRAY="/tmp/hkstray-$$"
shtest_clean_also "$R3" "$STRAY"
git -C "$FX" worktree add -q "$THIRD" 2>/dev/null
printf 'fff\n' >"$THIRD/infra/fence/lanes/hash"
V3="$(fence_sim_volume "$THIRD")"; ROW3="$THIRD"$'\t'"$R3"$'\t'"$V3"
fresh; touch "$STAMP"; mkdir -p "$R3/timing/run.x" "$STRAY/timing" "$PFM_FENCE_PROJECT_TMP/review"; hk
cp "$PFM_FENCE_STAMP_DIR/checkouts" "$T/scratch-ledger"
git -C "$FX" worktree remove --force "$THIRD"
printf 'another owner\n' >"$R3/keep"
for purpose in build lanes guard; do mkdir -p "$R3/$purpose"; : >"$R3/$purpose/f"; done
find "$R3/build" "$R3/lanes" "$R3/guard" "$STRAY" -exec touch -d '2 days ago' {} +
fresh; touch "$STAMP"; cp "$T/scratch-ledger" "$PFM_FENCE_STAMP_DIR/checkouts"; hk
if [ -d "$R3/timing/run.x" ] && [ "$(cat "$R3/keep" 2>/dev/null)" = 'another owner' ] &&
  [ -d "$STRAY/timing" ] && [ -d "$PFM_FENCE_PROJECT_TMP/review" ] && nowarn; then
  ok "checkouts: gone checkout keeps young timing and non-purpose content; an unrecorded root stays"
else bad "checkouts scratch preservation" "$(cat "$T/err")"; fi
if [ ! -e "$R3/build" ] && [ ! -e "$R3/lanes" ] && [ ! -e "$R3/guard" ] &&
  grep -q "removed $R3/build, freed" "$T/err" && grep -q "removed $R3/lanes, freed" "$T/err" && grep -q "removed $R3/guard, freed" "$T/err" &&
  grep -qxF "$ROW3" "$PFM_FENCE_STAMP_DIR/checkouts"; then
  ok "checkouts: only stale build, lanes and guard are removed; a nonempty root retains its ledger row"
else bad "checkouts purpose cleanup" "$(cat "$T/err")"; fi
mkdir -p "$R3/timing/run.x"; printf 'another owner\n' >"$R3/keep"
find "$R3/timing" -exec touch -d '2 days ago' {} +
fresh; touch "$STAMP"; cp "$T/scratch-ledger" "$PFM_FENCE_STAMP_DIR/checkouts"; hk
if [ ! -e "$R3/timing" ] && [ -f "$R3/keep" ] && grep -q "removed $R3/timing, freed" "$T/err" &&
  grep -qxF "$ROW3" "$PFM_FENCE_STAMP_DIR/checkouts" && nowarn; then
  ok "checkouts: stale timing is removed while non-purpose content and its ledger row stay"
else bad "checkouts stale timing" "$(cat "$T/err")"; fi
rm -f "$R3/keep"; mkdir -p "$R3"
fresh; touch "$STAMP"; cp "$T/scratch-ledger" "$PFM_FENCE_STAMP_DIR/checkouts"; hk
if [ ! -e "$R3" ] && ! grep -qxF "$ROW3" "$PFM_FENCE_STAMP_DIR/checkouts" && grep -q "removed $R3.*freed 0 MB" "$T/err" && nowarn; then
  ok "checkouts: an empty gone scratch root is removed and its completed ledger row dropped"
else bad "checkouts empty root" "$(cat "$T/err")"; fi
fresh; touch "$STAMP"; printf '%s\n' pfm-sim-harvest-other-clone-123 >"$STUB_DIR/volumes"; STUB_GIT_FAIL=1 hk
if ! grep -q '^volume rm' "$STUB_DOCKER_LOG" && warned checkouts && warned scratch; then
  ok "checkouts: a failed worktree listing removes nothing and warns"
else bad "checkouts listing failure" "$(cat "$T/err")"; fi
VICTIM="$T/victim"; DOTROOT="/tmp/.hkdot-$$"; mkdir -p "$VICTIM/keep" "$DOTROOT" "$R3/timing"
shtest_clean_also "$DOTROOT"
fresh; touch "$STAMP"
printf '%s\n' "$VICTIM" "$DOTROOT" "$R3" "$THIRD"$'\t'"$R3"$'\t'pfm-sim-harvest-other-clone-123 "$ROW3"$'\t'extra >"$PFM_FENCE_STAMP_DIR/checkouts"
printf '%s\n' pfm-sim-harvest-other-clone-123 >"$STUB_DIR/volumes"; hk
if [ -d "$VICTIM/keep" ] && [ -d "$DOTROOT" ] && [ -d "$R3/timing" ] && warned checkouts &&
  [ "$(grep -c 'ledger line.*dropped, nothing removed' "$T/err")" = 5 ] && ! grep -q '^volume rm' "$STUB_DOCKER_LOG" &&
  ! grep -qF "$R3" "$PFM_FENCE_STAMP_DIR/checkouts"; then
  ok "checkouts: malformed, legacy and mismatched ownership rows are dropped with WARN; no state is removed"
else bad "checkouts ledger validation" "$(cat "$T/err")" "$(cat "$PFM_FENCE_STAMP_DIR/checkouts")"; fi
rm -rf "$VICTIM" "$DOTROOT" "$R3" "$STRAY"

# 19 — scratch: an entry untouched for 7 days goes, a young one stays, a young
#      dir is walked one level; fence/ and tmp/tools are kept; a tree the host
#      cannot remove goes through a local fence image
old() { mkdir -p "$1"; : >"$1/f"; touch -d '10 days ago' "$1/f" "$1"; }
fresh; touch "$STAMP"; P="$PFM_FENCE_PROJECT_TMP"; rm -rf "$P" "$FX/tmp"
old "$P/oldpurpose"; mkdir -p "$P/young"; : >"$P/young/f"
old "$P/timing/run.old"; mkdir -p "$P/timing/run.new"; : >"$P/timing/run.new/f"
old "$P/fence"; old "$FX/tmp/tools"; old "$FX/tmp/qa-old"
hk
if [ ! -e "$P/oldpurpose" ] && [ ! -e "$P/timing/run.old" ] && [ ! -e "$FX/tmp/qa-old" ] &&
  [ -d "$P/young" ] && [ -d "$P/timing/run.new" ] && [ -d "$P/fence" ] && [ -d "$FX/tmp/tools" ] &&
  grep -q "removed $P/oldpurpose, freed [0-9]* MB" "$T/err" && nowarn; then
  ok "scratch: 7-day-old entries and runs removed with their size; young entries, fence/ and tmp/tools kept"
else bad "scratch age" "$(cd "$T" && find ptmp "$FX/tmp" 2>/dev/null | head -20)" "$(cat "$T/err")"; fi
fresh; touch "$STAMP"; old "$P/rootowned"
cat >"$BIN/rm" <<'EOF'
#!/usr/bin/env bash
case "$*" in *rootowned*) echo "rm: cannot remove 'rootowned/f': Permission denied" >&2; exit 1 ;; esac
exec /bin/rm "$@"
EOF
chmod +x "$BIN/rm"; hash -r; printf 'img1\n' >"$STUB_DIR/fenceimg"; hk
r1=0; [ ! -e "$P/rootowned" ] && grep -q -- '--entrypoint rm img1 -rf -- /reap/rootowned' "$STUB_DOCKER_LOG" && nowarn && r1=1
fresh; touch "$STAMP"; old "$P/rootowned"; hk
r2=0; [ -d "$P/rootowned" ] && warned scratch && grep -q 'no local fence image' "$T/err" && r2=1
/bin/rm -f "$BIN/rm"; hash -r; /bin/rm -rf "$P/rootowned"
REALFIND="$(command -v find)"; fresh; touch "$STAMP"; old "$P/unreadable"
printf '#!/usr/bin/env bash\ncase "$1" in *unreadable*) echo "find: $1/sub: Permission denied" >&2; exit 1 ;; esac\nexec %s "$@"\n' "$REALFIND" >"$BIN/find"
chmod +x "$BIN/find"; hash -r; hk
r3=0; [ -d "$P/unreadable" ] && warned scratch && grep -q "$P/unreadable" "$T/err" && r3=1
/bin/rm -f "$BIN/find"; hash -r; /bin/rm -rf "$P/unreadable"
if [ "$r1$r2$r3" = 111 ]; then ok "scratch: a root-owned tree goes through a local fence image; with none it is kept and warns; one find cannot read whole is kept and warns"
else bad "scratch root-owned" "via-image=$r1 no-image-warns=$r2 unreadable-kept=$r3" "$(cat "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi

# 20 — build cache: at most daily, last-use age 168 h by default; a concurrent
#      prune is silent, a failure warns and leaves no stamp
BSTAMP="$PFM_FENCE_STAMP_DIR/build-cache.stamp"
fresh; touch "$STAMP"; hk
r1=0; logged 'builder prune -f --filter until=168h' && [ -f "$BSTAMP" ] && grep -q 'removed build cache unused for 168h — Total reclaimed space: 1.5GB' "$T/err" && nowarn && r1=1
hk; r2=0; [ "$(grep -c '^builder prune' "$STUB_DOCKER_LOG")" = 1 ] && r2=1
fresh; touch "$STAMP"; STUB_BUILDER=fail hk; r3=0; warned build-cache && [ ! -f "$BSTAMP" ] && r3=1
fresh; touch "$STAMP"; STUB_BUILDER=busy hk; r4=0; nowarn && r4=1
fresh; touch "$STAMP"; PFM_FENCE_BUILDCACHE_HOURS=1w hk; r5=0; warned build-cache && logged 'builder prune -f --filter until=168h' && r5=1
if [ "$r1$r2$r3$r4$r5" = 11111 ]; then ok "build cache: daily prune of cache unused for 168h; busy silent, failure warns unstamped, invalid hours default"
else bad "build cache" "prune=$r1 daily=$r2 fail=$r3 busy=$r4 invalid=$r5" "$(cat "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi

# 21 — cache budget: every shared volume has one; gomod over its 4000 MB goes,
#      uv under its 8000 MB stays, from one df call
fresh; STUB_MB=40 STUB_GOMOD_MB=4500 STUB_UV_MB=7000 hk
if logged 'volume rm pfm-dev-gomod' && ! grep -q '^volume rm pfm-lane-uv-cache\|^volume rm pfm-dev-gocache' "$STUB_DOCKER_LOG" &&
  [ "$(grep -c '^system df' "$STUB_DOCKER_LOG")" = 1 ] && grep -q 'removed pfm-dev-gomod, 4500 MB > 4000 MB budget, freed 4500 MB' "$T/err" && [ -f "$STAMP" ] && nowarn; then
  ok "cache budget: gomod over 4000 MB removed, uv under 8000 MB kept, one sizing call"
else bad "cache budget all volumes" "$(cat "$STUB_DOCKER_LOG")" "$(cat "$T/err")"; fi

shtest_end
