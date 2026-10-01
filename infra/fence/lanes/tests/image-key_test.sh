#!/usr/bin/env bash
# Fixture-driven inputs-key tests. Docker is a sourced function, so these
# cases exercise the host library without starting an image or container.
set -uo pipefail

FENCE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SUT="${IMAGE_KEY_SUT:-$FENCE/image-key.sh}"
SHTEST_TAG=fence-image-key-test
# shellcheck source=/dev/null
. "$FENCE/../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "image-key_test: no image-key.sh at $SUT" >&2; exit 2; }
# shellcheck source=/dev/null
. "$SUT"

CTX="$T/context"
COMPOSE="$T/docker-compose.yml"
printf 'services: {}\n' >"$COMPOSE"
reset_context() {
  rm -rf "$CTX"
  mkdir -p "$CTX/sub"
  printf 'FROM scratch\n' >"$CTX/pfm-dev.Dockerfile"
  printf 'first\n' >"$CTX/data"
  printf 'second\n' >"$CTX/other"
  chmod -x "$CTX/data"
  unset STUB_TARGET STUB_ARG STUB_UNKNOWN STUB_NO_IMAGE STUB_NO_CONTEXT STUB_CONFIG_FAIL STUB_IMAGE_FAIL DOCKER_DEFAULT_PLATFORM
}

docker() {
  case "$1 ${2:-}" in
    'compose -f')
      [ "${STUB_CONFIG_FAIL:-0}" = 0 ] || { echo 'fixture compose error' >&2; return 7; }
      local service="${@: -1}" target="${STUB_TARGET:-${@: -1}}"
      printf 'services:\n  %s:\n    build:\n' "$service"
      [ "${STUB_NO_CONTEXT:-0}" = 1 ] || printf '      context: %s\n' "$CTX"
      printf '      dockerfile: pfm-dev.Dockerfile\n      target: %s\n' "$target"
      [ -z "${STUB_ARG:-}" ] || printf '      args:\n        REVISION: %s\n' "$STUB_ARG"
      printf '      labels:\n        pfm.fence.inputs: unkeyed\n'
      [ -z "${STUB_UNKNOWN:-}" ] || printf '      shm_size: 2gb\n'
      [ "${STUB_NO_IMAGE:-0}" = 1 ] || printf '    image: professor-%s\n' "$service" ;;
    'image inspect')
      [ "${STUB_IMAGE_FAIL:-0}" = 0 ] || { echo 'fixture image absent' >&2; return 1; }
      printf '%s\n' "${STUB_LABEL:-unkeyed}" ;;
  esac
}

reset_context
base="$(fence_image_key "$COMPOSE" pfm-dev)"
STUB_LABEL="$base"
fence_image_prepare "$COMPOSE" pfm-dev 2>"$T/current.err"; rc=$?
if [ "$rc" -eq 0 ] && [ "$PFM_DEV_INPUTS_KEY" = "$base" ] && [ "${#FENCE_IMAGE_BUILD[@]}" -eq 0 ] &&
  grep -qx "fence image: professor-pfm-dev current (inputs ${base:0:12})" "$T/current.err"; then
  ok "current image exports its key"
else bad "current image" "rc=$rc" "$(cat "$T/current.err")"; fi

check_change() { # one manifest input changes while the old label stays put
  local scenario="$1" old new service=pfm-dev
  reset_context
  old="$(fence_image_key "$COMPOSE" pfm-dev)"
  case "$scenario" in
    file) printf 'changed\n' >"$CTX/data" ;;
    added) printf 'new\n' >"$CTX/sub/new" ;;
    executable) chmod +x "$CTX/data" ;;
    symlink) ln -s data "$CTX/link"; old="$(fence_image_key "$COMPOSE" pfm-dev)"; rm "$CTX/link"; ln -s other "$CTX/link" ;;
    dockerfile) printf 'FROM scratch\nRUN true\n' >"$CTX/pfm-dev.Dockerfile" ;;
    target) STUB_TARGET=pfm-sim ;;
    arg) STUB_ARG=new ;;
    platform) DOCKER_DEFAULT_PLATFORM=linux/amd64 ;;
  esac
  new="$(fence_image_key "$COMPOSE" "$service")"
  STUB_LABEL="$old"
  fence_image_prepare "$COMPOSE" "$service" 2>"$T/$scenario.err"; rc=$?
  if [ "$rc" -eq 0 ] && [ "$new" != "$old" ] && [ "$PFM_DEV_INPUTS_KEY" = "$new" ] &&
    [ "${FENCE_IMAGE_BUILD[0]:-}" = --build ] &&
    grep -qx "fence image: professor-$service rebuilds — inputs ${old:0:12} -> ${new:0:12}" "$T/$scenario.err"; then
    ok "$scenario changes the inputs key and rebuilds"
  else bad "$scenario key" "rc=$rc old=$old new=$new" "$(cat "$T/$scenario.err")"; fi
}

for scenario in file added executable symlink dockerfile target arg platform; do check_change "$scenario"; done

reset_context
dev_key="$(fence_image_key "$COMPOSE" pfm-dev)"
sim_key="$(fence_image_key "$COMPOSE" pfm-sim)"
if [ "$dev_key" != "$sim_key" ]; then ok "dev and sim services have distinct keys"
else bad "dev and sim keys" "$dev_key"; fi

reset_context
STUB_IMAGE_FAIL=1
fence_image_prepare "$COMPOSE" pfm-dev 2>"$T/no-image.err"; rc=$?
if [ "$rc" -eq 0 ] && [ "${FENCE_IMAGE_BUILD[0]:-}" = --build ] &&
  grep -q 'rebuilds — fixture image absent' "$T/no-image.err"; then
  ok "image inspect failure rebuilds and reports docker's first line"
else bad "no image" "rc=$rc" "$(cat "$T/no-image.err")"; fi

for failure in STUB_CONFIG_FAIL STUB_UNKNOWN STUB_NO_IMAGE STUB_NO_CONTEXT; do
  reset_context
  printf -v "$failure" 1
  fence_image_prepare "$COMPOSE" pfm-dev 2>"$T/$failure.err"; rc=$?
  case "$failure" in
    STUB_CONFIG_FAIL) reason='fixture compose error' ;;
    STUB_UNKNOWN) reason='unsupported build field shm_size' ;;
    STUB_NO_IMAGE) reason='missing image' ;;
    STUB_NO_CONTEXT) reason='missing build.context' ;;
  esac
  if [ "$rc" -eq 2 ] && grep -q "INPUTS-UNDERIVABLE pfm-dev: $reason" "$T/$failure.err" &&
    [ -z "${PFM_DEV_INPUTS_KEY+x}" ] && [ -z "${FENCE_IMAGE_BUILD+x}" ]; then
    ok "$failure reports underivable inputs"
  else bad "$failure" "rc=$rc" "$(cat "$T/$failure.err")"; fi
done

shtest_end
