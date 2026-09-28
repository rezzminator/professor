#!/usr/bin/env bash
# Fixture-driven tests for lanes/container.sh: lane_base_image tags
# pfm-lane-base:<hash>, lane_base_release untags it without -f and never fails
# its caller, and lane_run labels every lane container pfm.fence=1 (fence
# housekeeping reaps only labelled containers). docker is a stub, so no image is
# ever built or removed and no container started.
#
#   bash infra/fence/lanes/tests/container_test.sh
#
# BROKEN STATE: a missing SUT exits 2 before any assertion.
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

# 3 — every lane container carries the fence label housekeeping reaps by
: >"$STUB_DOCKER_LOG"
PFM_DEV_WORKTREE="$T" PFM_DEV_GIT_COMMON="$T" PFM_DEV_GIT_DIR_REL=. lane_run lane-x pfm-lane-root:cafe01
if grep -q -- '^run -d .*--label pfm.fence=1 .*pfm-lane-root:cafe01 sleep infinity$' "$STUB_DOCKER_LOG"; then ok "lane_run labels the container pfm.fence=1"
else bad "lane_run labels the container pfm.fence=1" "$(cat "$STUB_DOCKER_LOG")"; fi

shtest_end
