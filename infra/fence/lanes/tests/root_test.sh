#!/usr/bin/env bash
# Fixture-driven tests for lanes/root.sh's build window: housekeeping runs with
# the root's own hash and the cache volumes are ensured before the build; the
# EXIT trap alone releases the build container and the base pin, exactly once,
# on success, on a failed step, and on INT/TERM/HUP, whose exit codes are
# 130/143/129. docker, creds.sh and housekeeping are stubs in a throwaway git
# repo, so no image is built and no container is started.
#
#   bash infra/fence/lanes/tests/root_test.sh
#   ROOT_SUT=/tmp/old/root.sh bash …/root_test.sh   # red-first
#
# BROKEN STATE: a missing SUT exits 2 before any assertion.
set -uo pipefail

LANES="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${ROOT_SUT:-$LANES/root.sh}"
SHTEST_TAG=lane-root-test
# shellcheck source=/dev/null
source "$LANES/../../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "root_test: no root.sh at $SUT" >&2; exit 2; }

R="$T/repo"
mkdir -p "$R/infra/fence/lanes"
cp "$SUT" "$R/infra/fence/lanes/root.sh"
cp "$LANES/container.sh" "$R/infra/fence/lanes/container.sh"
cp "$LANES/../fence-env.sh" "$R/infra/fence/fence-env.sh"
cat >"$R/infra/fence/housekeeping.sh" <<'EOF'
fence_housekeeping() { echo "fence_housekeeping $*" >>"$STUB_DOCKER_LOG"; }
fence_volumes_ensure() { echo "fence_volumes_ensure" >>"$STUB_DOCKER_LOG"; }
EOF
cat >"$R/infra/fence/lanes/creds.sh" <<'EOF'
case "$1" in --print-config) echo '{"accounts":[{"id":1}]}' ;; esac
EOF
(cd "$R" && git init -q && git add -A && git -c user.email=t@t -c user.name=t commit -qm fixture)

# STUB_STEP1: what step 1's exec does — ok, fail (exit 7), or a signal name it
# sends to root.sh (its parent) before returning 0.
BIN="$T/bin"
mkdir -p "$BIN"
cat >"$BIN/docker" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_DOCKER_LOG"
case "$1" in
  image) exit 1 ;; # image inspect: no root for this hash yet
  exec)
    case "$*" in
      *"setup.sh tools"*)
        case "${STUB_STEP1:-ok}" in
          ok) ;;
          fail) exit 7 ;;
          *) kill -"$STUB_STEP1" "$PPID" ;;
        esac ;;
      *"pfm ls --plain"*) echo "no chats" ;;
      *) cat >/dev/null 2>&1 || true ;;
    esac ;;
esac
exit 0
STUB
chmod +x "$BIN/docker"
export PATH="$BIN:$PATH" STUB_DOCKER_LOG="$T/docker.log"

run_root() { # run_root STEP1 — sets RC and OUT
  : >"$STUB_DOCKER_LOG"
  OUT="$(STUB_STEP1="$1" bash "$R/infra/fence/lanes/root.sh" --no-adopt 2>&1 </dev/null)"
  RC=$?
  HASH="$(bash "$R/infra/fence/lanes/root.sh" --print-hash 2>/dev/null)"
}
released_once() { # the pin released once; the build container removed at the start and once at exit
  [ "$(grep -cx "rmi pfm-lane-base:$HASH" "$STUB_DOCKER_LOG")" -eq 1 ] &&
    [ "$(grep -cx "rm -f pfm-lane-build-$HASH" "$STUB_DOCKER_LOG")" -eq 2 ]
}

# 1 — success: the image is the last stdout line, cleanup ran once
run_root ok
if [ "$RC" -eq 0 ] && [ "$(tail -1 <<<"$OUT")" = "pfm-lane-root:$HASH" ] && released_once; then
  ok "success: the root is committed and the EXIT trap alone releases the build, once"
else bad "success" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi

# 2 — housekeeping gets the root's own hash, and the caches are ensured, before the build
first_build="$(grep -n '^compose ' "$STUB_DOCKER_LOG" | head -1 | cut -d: -f1)"
hk_line="$(grep -nx "fence_housekeeping $HASH" "$STUB_DOCKER_LOG" | head -1 | cut -d: -f1)"
ens_line="$(grep -nx 'fence_volumes_ensure' "$STUB_DOCKER_LOG" | head -1 | cut -d: -f1)"
if [ -n "$hk_line" ] && [ -n "$ens_line" ] && [ -n "$first_build" ] && [ "$hk_line" -lt "$first_build" ] && [ "$ens_line" -lt "$first_build" ]; then
  ok "housekeeping runs with the root's hash, and the caches are ensured, before the base build"
else bad "housekeeping before build" "$(cat "$STUB_DOCKER_LOG")"; fi

# 3 — a failed step: exit 1, cleanup once
run_root fail
if [ "$RC" -eq 1 ] && grep -q 'setup.sh tools failed (exit 7)' <<<"$OUT" && released_once; then
  ok "a failed step exits 1 and the EXIT trap releases the build, once"
else bad "failed step" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi

# 4 — signals: INT → 130, TERM → 143, HUP → 129, cleanup once each
for pair in INT:130 TERM:143 HUP:129; do
  run_root "${pair%%:*}"
  if [ "$RC" -eq "${pair#*:}" ] && released_once; then ok "SIG${pair%%:*} exits ${pair#*:} and releases the build, once"
  else bad "SIG${pair%%:*}" "rc=$RC want ${pair#*:}" "$(cat "$STUB_DOCKER_LOG")"; fi
done

shtest_end
