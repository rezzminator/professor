#!/usr/bin/env bash
# Fixture-driven tests for infra/fence/release-rehearsal.sh `up`: fence
# housekeeping runs before the image build, and the rehearsal container carries
# the fence label and the long-lived label, so housekeeping reaps it once exited
# but never by age mid-release. docker and
# housekeeping are stubs, so no image is built and no container is started.
#
#   bash infra/fence/lanes/tests/release-rehearsal_test.sh
#   RELEASE_REHEARSAL_SUT=/tmp/old/release-rehearsal.sh bash …/release-rehearsal_test.sh   # red-first
#
# BROKEN STATE: a missing SUT exits 2 before any assertion.
set -uo pipefail

FENCE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SUT="${RELEASE_REHEARSAL_SUT:-$FENCE/release-rehearsal.sh}"
SHTEST_TAG=release-rehearsal-test
# shellcheck source=/dev/null
source "$FENCE/../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "release-rehearsal_test: no release-rehearsal.sh at $SUT" >&2; exit 2; }

R="$T/repo"
mkdir -p "$R/infra/fence"
cp "$SUT" "$R/infra/fence/release-rehearsal.sh"
cat >"$R/infra/fence/housekeeping.sh" <<'EOF'
fence_housekeeping() { echo "fence_housekeeping $*" >>"$STUB_DOCKER_LOG"; }
fence_volumes_ensure() { :; }
EOF
(cd "$R" && git init -q && git -c user.email=t@t -c user.name=t commit -q --allow-empty -m fixture)

BIN="$T/bin"
mkdir -p "$BIN"
cat >"$BIN/docker" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_DOCKER_LOG"
case "$1" in
  container)
    [ -f "$STUB_RUNNING" ] || exit 1
    case "$*" in *State.Running*) echo true ;; esac ;;
  run) : >"$STUB_RUNNING" ;;
esac
exit 0
STUB
chmod +x "$BIN/docker"
export PATH="$BIN:$PATH" STUB_DOCKER_LOG="$T/docker.log" STUB_RUNNING="$T/running"

: >"$STUB_DOCKER_LOG"
OUT="$(bash "$R/infra/fence/release-rehearsal.sh" up 2>&1)"; RC=$?
hk_line="$(grep -n '^fence_housekeeping' "$STUB_DOCKER_LOG" | head -1 | cut -d: -f1)"
build_line="$(grep -n '^build ' "$STUB_DOCKER_LOG" | head -1 | cut -d: -f1)"
run_argv="$(grep '^run ' "$STUB_DOCKER_LOG")"

if [ "$RC" -eq 0 ] && [ -n "$hk_line" ] && [ -n "$build_line" ] && [ "$hk_line" -lt "$build_line" ]; then
  ok "up: fence housekeeping runs before the image build"
else bad "up: housekeeping before build" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi

if grep -q -- '--label pfm.fence=1 ' <<<"$run_argv" && grep -q -- '--label pfm.fence.long-lived=1 ' <<<"$run_argv"; then
  ok "up: the rehearsal container carries --label pfm.fence=1 and --label pfm.fence.long-lived=1"
else bad "up: fence labels" "$run_argv"; fi

shtest_end
