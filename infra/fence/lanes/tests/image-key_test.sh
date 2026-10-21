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

compat_bash="${PFM_TEST_BASH32:-bash}"

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
  unset STUB_TARGET STUB_ARG STUB_REGISTRY STUB_UNKNOWN STUB_NO_IMAGE STUB_NO_CONTEXT STUB_CONFIG_FAIL STUB_IMAGE_FAIL DOCKER_DEFAULT_PLATFORM
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
      [ -z "${STUB_REGISTRY:-}" ] || printf '      args:\n        PFM_NPM_REGISTRY: %s\n' "$STUB_REGISTRY"
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
# Bash 3.2's command-substitution parser fails at runtime, even when `bash -n` passes.
export -f docker
compat_key="$(CTX="$CTX" "$compat_bash" -c 'source "$1"; fence_image_key "$2" pfm-dev' _ "$SUT" "$COMPOSE" 2>"$T/parser.err")"; parse_rc=$?
if [ "$parse_rc" = 0 ] && [ -n "$base" ] && [ "$compat_key" = "$base" ]; then
  ok "runtime parser with $compat_bash preserves the inputs key"
else bad "image-key runtime parser" "rc=$parse_rc key=$compat_key expected=$base" "$(cat "$T/parser.err")"; fi
export -n -f docker
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
    registry) STUB_REGISTRY=https://npm.example.invalid ;;
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

for scenario in file added executable symlink dockerfile target arg registry platform; do check_change "$scenario"; done

# A .dockerignore allow-list (`*`, then `!path` lines) is the build context docker sends: the key
# covers .dockerignore and the allowed paths alone, so an edit to any other file under the context
# (a check script beside the Dockerfile) neither rebuilds the image nor forks the shared container.
reset_context
printf '*\n!pfm-dev.Dockerfile\n!data\n' >"$CTX/.dockerignore"
allow_base="$(fence_image_key "$COMPOSE" pfm-dev)"
printf 'changed\n' >"$CTX/other"
allow_other="$(fence_image_key "$COMPOSE" pfm-dev)"
printf 'changed\n' >"$CTX/data"
allow_data="$(fence_image_key "$COMPOSE" pfm-dev)"
printf '*\n!pfm-dev.Dockerfile\n!data\n!other\n' >"$CTX/.dockerignore"
allow_list="$(fence_image_key "$COMPOSE" pfm-dev)"
if [ -n "$allow_base" ] && [ "$allow_other" = "$allow_base" ] && [ "$allow_data" != "$allow_base" ] && [ "$allow_list" != "$allow_data" ]; then
  ok "a .dockerignore allow-list keys only the paths it lets through"
else bad "dockerignore allow-list" "base=$allow_base other=$allow_other data=$allow_data list=$allow_list"; fi
printf '*.md\n' >"$CTX/.dockerignore"
fence_image_key "$COMPOSE" pfm-dev >/dev/null 2>"$T/ignore.err"; rc=$?
if [ "$rc" -eq 2 ] && grep -q 'INPUTS-UNDERIVABLE pfm-dev: .dockerignore' "$T/ignore.err"; then
  ok "a .dockerignore that is no allow-list is underivable, never guessed"
else bad "dockerignore form" "rc=$rc" "$(cat "$T/ignore.err")"; fi

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

# Exercise the tools-install RUN itself without installing tools or using the network.
install_run="$(awk '/^RUN / { run=$0; while (run ~ /\\$/ && getline) run=run "\n" $0; if (run ~ /TOOLS_BIN=/) { sub(/^RUN /, "", run); print run; exit } }' "$FENCE/pfm-dev.Dockerfile")"
printf '#!/usr/bin/env bash\nprintf "registry=%%s\\n" "${NPM_CONFIG_REGISTRY-}"\n' >"$T/tools.sh"
install_run="${install_run//\/opt\/pfm-tools\/tools.sh/$T/tools.sh}"
for registry in '' https://npm.example.invalid; do
  install_out="$(unset NPM_CONFIG_REGISTRY; export PFM_NPM_REGISTRY="$registry"; bash -ec "$install_run" 2>&1)"; rc=$?
  if [ "$rc" = 0 ] && [ "$install_out" = "registry=$registry" ]; then ok "tools install uses optional registry '$registry'"
  else bad "tools install registry '$registry'" "rc=$rc" "$install_out"; fi
done

# Supplemental real Compose resolution is offline; callers supply a standalone binary.
if [ -n "${PFM_TEST_COMPOSE_BIN:-}" ]; then
  unset -f docker
  docker() { shift; "$PFM_TEST_COMPOSE_BIN" "$@"; }
  export PFM_DEV_WORKTREE="$FENCE/../.." PFM_DEV_GIT_COMMON="$T" PFM_DEV_ENGINE_SYNC_SRC="$T"
  export PFM_DEV_GIT_DIR_REL=. PFM_FENCE_COMPOSE_MEM=1g PFM_FENCE_SIM_SHM=1g
  for service in pfm-dev pfm-sim; do
    unset PFM_NPM_REGISTRY
    config="$(docker compose -f "$FENCE/docker-compose.yml" config "$service" 2>&1)"; rc=$?
    plain="$(fence_image_key "$FENCE/docker-compose.yml" "$service")"
    export PFM_NPM_REGISTRY=https://npm.example.invalid
    custom="$(docker compose -f "$FENCE/docker-compose.yml" config "$service" 2>&1)"; custom_rc=$?
    mirror="$(fence_image_key "$FENCE/docker-compose.yml" "$service")"
    if [ "$rc" = 0 ] && [ "$custom_rc" = 0 ] && grep -q 'PFM_NPM_REGISTRY: ""' <<<"$config" &&
      grep -q 'PFM_NPM_REGISTRY: https://npm.example.invalid' <<<"$custom" && [ -n "$plain" ] && [ -n "$mirror" ] && [ "$plain" != "$mirror" ]; then
      ok "real Compose $service inherits empty/custom registry and keys differ"
    else bad "real Compose $service registry" "$config" "$custom" "keys=$plain/$mirror"; fi
  done
fi

shtest_end
