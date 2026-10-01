#!/usr/bin/env bash
# Fixture-driven tests for `.claude/scripts/dev.sh iso`'s fence housekeeping
# call site: the image-building gate actions (install build typecheck verify
# test e2e cover all gate) run infra/fence/housekeeping.sh first; status, run, shell
# and sim never do (zero housekeeping cost); every compose action finds the
# cache volumes housekeeping.sh lists ensured first (compose declares them external). docker is
# a stub and dev.sh runs from a throwaway git repo, so nothing is built or run.
#
#   bash infra/fence/lanes/tests/iso-housekeeping_test.sh
#   DEV_SH_SUT=/tmp/new/dev.sh bash …/iso-housekeeping_test.sh   # a candidate dev.sh
#
# BROKEN STATE: a missing SUT exits 2 before any assertion.
set -uo pipefail

REPO="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../../.." && pwd -P)"
SUT="${DEV_SH_SUT:-$REPO/.claude/scripts/dev.sh}"
SHTEST_TAG=iso-housekeeping-test
# shellcheck source=/dev/null
source "$REPO/scripts/shtest.sh"
[ -f "$SUT" ] || { echo "iso-housekeeping_test: no dev.sh at $SUT" >&2; exit 2; }

FXNAME="isofx-$$"
R="$T/.$FXNAME"
mkdir -p "$R/.claude/scripts" "$R/pfm/scripts" "$R/infra/fence/lanes"
cp "$SUT" "$R/.claude/scripts/dev.sh"
cp "$REPO/pfm/scripts/repo-git.sh" "$R/pfm/scripts/"
cp "$REPO/infra/fence/housekeeping.sh" "$REPO/infra/fence/fence-env.sh" "$REPO/infra/fence/docker-compose.yml" "$REPO/infra/fence/steps.sh" "$REPO/infra/fence/checks.sh" "$R/infra/fence/"
# A fixture volume makes a hard-coded copy of today's list fail the order check.
sed -i 's/^\(FENCE_CACHE_VOLUMES="[^"]*\)"/\1 fixture-cache"/' "$R/infra/fence/housekeeping.sh"
printf 'printf "%%s\\n" aaa\n' >"$R/infra/fence/lanes/root.sh"
(cd "$R" && git init -q && git add -A && git -c user.email=t@t -c user.name=t commit -qm fixture)

BIN="$T/bin"
mkdir -p "$BIN"
cat >"$BIN/docker" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_DOCKER_LOG"
case "$1 ${2:-}" in
  "volume inspect") [ "${STUB_VOLUME:-1}" = 1 ] || { echo "Error response from daemon: no such volume" >&2; exit 1; } ;;
esac
exit 0
STUB
chmod +x "$BIN/docker"
export PATH="$BIN:$PATH" STUB_DOCKER_LOG="$T/docker.log" PFM_FENCE_STAMP_DIR="$T/stamp"

iso() { : >"$STUB_DOCKER_LOG"; OUT="$(bash "$R/.claude/scripts/dev.sh" iso "$@" 2>&1)"; RC=$?; }
line_of() { grep -n -- "$1" "$STUB_DOCKER_LOG" | head -1 | cut -d: -f1; }
ensured_before_compose() {
  local e c volumes
  volumes="$(sed -n 's/^FENCE_CACHE_VOLUMES="\([^"]*\)"/\1/p' "$R/infra/fence/housekeeping.sh")"
  e="$(line_of "^volume inspect $volumes\$")"; c="$(line_of '^compose ')"
  [ -n "$e" ] && [ -n "$c" ] && [ "$e" -lt "$c" ]
}
housekept() { grep -q '^image prune' "$STUB_DOCKER_LOG"; }

for a in status "run true" shell "sim true"; do
  # shellcheck disable=SC2086
  iso $a
  if [ "$RC" -eq 0 ] && ! housekept && ensured_before_compose; then ok "iso $a: no housekeeping, caches ensured before compose"
  else bad "iso $a" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi
done

for a in test build; do
  iso "$a" templates
  hk="$(line_of '^image prune')"; c="$(line_of '^compose ')"
  if [ "$RC" -eq 0 ] && [ -n "$hk" ] && [ -n "$c" ] && [ "$hk" -lt "$c" ] && ensured_before_compose; then ok "iso $a: housekeeping and the cache ensure before compose"
  else bad "iso $a" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi
done

iso gate
if [ "$RC" -eq 0 ] && housekept && ensured_before_compose && [ "$(grep -c '^compose ' "$STUB_DOCKER_LOG")" -eq 1 ] \
  && grep -q '^compose .*pfm-dev bash -c .*bash infra/fence/egress\.sh run \./\.claude/scripts/dev\.sh gate all$' "$STUB_DOCKER_LOG"; then
  ok "iso gate: housekeeping, then one compose run of the egress recorder"
else bad "iso gate" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi

STUB_VOLUME=0 iso status
if [ "$RC" -eq 0 ] && grep -qx 'volume create pfm-dev-gocache' "$STUB_DOCKER_LOG" && grep -qx 'volume create pfm-dev-lintcache' "$STUB_DOCKER_LOG" && grep -qx 'volume create fixture-cache' "$STUB_DOCKER_LOG"; then
  ok "iso status: absent caches are created before compose runs"
else bad "iso status creates caches" "rc=$RC" "$(cat "$STUB_DOCKER_LOG")"; fi

rm -rf "/tmp/$FXNAME"
shtest_end
