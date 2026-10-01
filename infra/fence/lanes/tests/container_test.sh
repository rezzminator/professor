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
mkdir -p "$BIN" "$T/infra/fence"
printf 'FROM scratch\n' >"$T/infra/fence/pfm-dev.Dockerfile"
printf 'fixture\n' >"$T/infra/fence/data"
printf 'services: {}\n' >"$T/infra/fence/docker-compose.yml"
cat >"$BIN/docker" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_DOCKER_LOG"
case "$1 ${2:-}" in
  'compose -f')
    if [[ "$*" == *' config '* ]]; then
      [ "${STUB_CONFIG_FAIL:-0}" = 0 ] || { echo 'fixture config error' >&2; exit 7; }
      cat <<EOF
services:
  pfm-dev:
    build:
      context: $STUB_CONTEXT
      dockerfile: pfm-dev.Dockerfile
      target: pfm-dev
      labels:
        pfm.fence.inputs: unkeyed
    image: professor-pfm-dev
EOF
    elif [[ "$*" == *' build '* ]]; then
      printf 'KEY %s\n' "${PFM_DEV_INPUTS_KEY:-unset}" >>"$STUB_DOCKER_LOG"
      [ "${STUB_BUILD_FAIL:-0}" = 0 ] || { echo 'fixture build refused' >&2; exit 8; }
    fi ;;
  'image inspect') printf '%s\n' "${STUB_LABEL:-unkeyed}" ;;
  'rmi '*) exit "${STUB_RMI_RC:-0}" ;;
esac
exit 0
STUB
chmod +x "$BIN/docker"
export PATH="$BIN:$PATH"
export STUB_DOCKER_LOG="$T/docker.log" STUB_CONTEXT="$T/infra/fence"

# shellcheck source=/dev/null
. "$SUT"

# 1 — the pin and its release name the same tag; the release never forces
: >"$STUB_DOCKER_LOG"
base="$(lane_base_image "$T" pfm-lane-base:cafe01-unique 2>/dev/null)"
lane_base_release pfm-lane-base:cafe01-unique
if [ "$base" = "pfm-lane-base:cafe01-unique" ] && grep -qx 'tag professor-pfm-dev pfm-lane-base:cafe01-unique' "$STUB_DOCKER_LOG" &&
  grep -qx 'rmi pfm-lane-base:cafe01-unique' "$STUB_DOCKER_LOG"; then
  ok "release untags exactly the pin lane_base_image made, without -f"
else
  bad "pin and release pair" "base=[$base]" "$(cat "$STUB_DOCKER_LOG")"
fi
key="$(fence_image_key "$T/infra/fence/docker-compose.yml" pfm-dev)"
: >"$STUB_DOCKER_LOG"
stale_out="$(STUB_LABEL=unkeyed lane_base_image "$T" pfm-lane-base:stale 2>&1 >/dev/null)"; rc=$?
if [ "$rc" -eq 0 ] && grep -q '^compose .* build pfm-dev$' "$STUB_DOCKER_LOG" &&
  grep -qx "KEY $key" "$STUB_DOCKER_LOG" && grep -q 'rebuilds' <<<"$stale_out"; then
  ok "stale lane base builds with the derived inputs key"
else bad "stale lane base did not build" "$(cat "$STUB_DOCKER_LOG")"; fi

: >"$STUB_DOCKER_LOG"
base="$(STUB_LABEL="$key" STUB_BUILD_FAIL=1 lane_base_image "$T" pfm-lane-base:current 2>"$T/current.err")"; rc=$?
if [ "$rc" -eq 0 ] && [ "$base" = pfm-lane-base:current ] && grep -q 'current (inputs ' "$T/current.err" &&
  grep -qx 'tag professor-pfm-dev pfm-lane-base:current' "$STUB_DOCKER_LOG"; then
  ok "current lane base pins the labeled image"
else bad "current lane base" "$(cat "$T/current.err")" "$(cat "$STUB_DOCKER_LOG")"; fi

: >"$STUB_DOCKER_LOG"
base="$(STUB_CONFIG_FAIL=1 lane_base_image "$T" pfm-lane-base:underivable 2>"$T/underivable.err")"; rc=$?
if [ "$rc" -eq 1 ] && grep -q 'INPUTS-UNDERIVABLE pfm-dev: fixture config error' "$T/underivable.err"; then
  ok "underivable lane base reports the config error"
else bad "underivable lane base" "rc=$rc" "$(cat "$T/underivable.err")"; fi

# 2 — an already-gone pin (a second cleanup, a refused rmi) never fails the caller
if STUB_RMI_RC=1 lane_base_release pfm-lane-base:cafe01-unique; then ok "a failed rmi returns 0 to the caller"
else bad "a failed rmi returns 0 to the caller"; fi

# 3 — every lane container carries the fence label housekeeping reaps by
: >"$STUB_DOCKER_LOG"
PFM_DEV_WORKTREE="$T" PFM_DEV_GIT_COMMON="$T" PFM_DEV_GIT_DIR_REL=. lane_run lane-x pfm-lane-root:cafe01
if grep -q -- '^run -d .*--label pfm.fence=1 .*pfm-lane-root:cafe01 sleep infinity$' "$STUB_DOCKER_LOG"; then ok "lane_run labels the container pfm.fence=1"
else bad "lane_run labels the container pfm.fence=1" "$(cat "$STUB_DOCKER_LOG")"; fi
if ! grep -q 'pfm-lane-harvest-cache\|pfm-lane-uv-cache' "$STUB_DOCKER_LOG"; then ok "lane run has no root-build download caches"
else bad "lane run mounted a root-build cache" "$(cat "$STUB_DOCKER_LOG")"; fi

# 4 — run containers can disable networking; root builds keep docker's default.
: >"$STUB_DOCKER_LOG"
PFM_DEV_WORKTREE="$T" PFM_DEV_GIT_COMMON="$T" PFM_DEV_GIT_DIR_REL=. lane_run lane-x pfm-lane-root:cafe01 none
if grep -q -- '--network none' "$STUB_DOCKER_LOG" && grep -q -- '-e GOPROXY=off' "$STUB_DOCKER_LOG"; then ok "lane_run none disables networking and the Go proxy at container start"
else bad "run network and Go proxy" "$(cat "$STUB_DOCKER_LOG")"; fi
: >"$STUB_DOCKER_LOG"
PFM_DEV_WORKTREE="$T" PFM_DEV_GIT_COMMON="$T" PFM_DEV_GIT_DIR_REL=. lane_run lane-build pfm-lane-base:cafe01
if ! grep -qE -- '--network|-e GOPROXY=off' "$STUB_DOCKER_LOG"; then ok "root build uses docker's default network and Go proxy"
else bad "build network and Go proxy" "$(cat "$STUB_DOCKER_LOG")"; fi
if grep -q -- '-e MOCK_ENGINE_SCENARIO=/root/.local/share/pfm-lanes/default.json' "$STUB_DOCKER_LOG"; then ok "every container receives the default mock scenario"
else bad "scenario environment" "$(cat "$STUB_DOCKER_LOG")"; fi

# 5 — optional docker arguments follow the network selector before the image.
: >"$STUB_DOCKER_LOG"
PFM_DEV_WORKTREE="$T" PFM_DEV_GIT_COMMON="$T" PFM_DEV_GIT_DIR_REL=. lane_run lane-build pfm-lane-base:cafe01 '' -v pfm-lane-uv-cache:/root/.cache/uv -e UV_LINK_MODE=copy
if grep -q -- '--label pfm.fence=1 -v pfm-lane-uv-cache:/root/.cache/uv -e UV_LINK_MODE=copy -v ' "$STUB_DOCKER_LOG"; then
  ok "lane_run passes optional docker arguments before the image"
else bad "lane_run optional arguments" "$(cat "$STUB_DOCKER_LOG")"; fi

shtest_end
