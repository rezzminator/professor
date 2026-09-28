#!/usr/bin/env bash
# Fixture-driven tests for lanes/container.sh's base-image pin: lane_base_image
# tags pfm-lane-base:<hash>, lane_base_release untags it without -f and never
# fails its caller. docker is a stub, so no image is ever built or removed.
#
#   bash infra/fence/lanes/tests/container_test.sh
set -uo pipefail

SUT="${CONTAINER_SUT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)/container.sh}"
SHTEST_TAG=lane-container-test
# shellcheck source=/dev/null
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "container_test: no container.sh at $SUT" >&2; exit 2; }

# STUB_RMI_RC: the exit status `docker rmi` answers with (1 = no such image).
BIN="$T/bin"
mkdir -p "$BIN"
cat >"$BIN/docker" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_DOCKER_LOG"
case "$1" in rmi) exit "${STUB_RMI_RC:-0}" ;; *) exit 0 ;; esac
STUB
chmod +x "$BIN/docker"
export PATH="$BIN:$PATH"
export STUB_DOCKER_LOG="$T/docker.log"

# shellcheck source=/dev/null
. "$SUT"

# 1 — the pin and its release name the same tag; the release never forces
: >"$STUB_DOCKER_LOG"
base="$(lane_base_image "$T" cafe01 2>/dev/null)"
lane_base_release cafe01
if [ "$base" = "pfm-lane-base:cafe01" ] && grep -qx 'tag professor-pfm-dev pfm-lane-base:cafe01' "$STUB_DOCKER_LOG" &&
  grep -qx 'rmi pfm-lane-base:cafe01' "$STUB_DOCKER_LOG"; then
  ok "release untags exactly the pin lane_base_image made, without -f"
else
  bad "pin and release pair" "base=[$base]" "$(cat "$STUB_DOCKER_LOG")"
fi

# 2 — an already-gone pin (a second cleanup, a refused rmi) never fails the caller
if STUB_RMI_RC=1 lane_base_release cafe01; then ok "a failed rmi returns 0 to the caller"
else bad "a failed rmi returns 0 to the caller"; fi

shtest_end
