#!/usr/bin/env bash
# Fixture-driven tests for infra/fence/housekeeping.sh — the dangling-image prune
# and the Go cache budget: over, under, mounted, once-a-day stamp, unsizable
# volume, absent volume. docker is a stub, so no container is ever started.
#
#   bash infra/fence/lanes/tests/housekeeping_test.sh
set -uo pipefail

SUT="${HOUSEKEEPING_SUT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)/housekeeping.sh}"
SHTEST_TAG=fence-housekeeping-test
# shellcheck source=/dev/null
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "housekeeping_test: no housekeeping.sh at $SUT" >&2; exit 2; }

# ── stub ────────────────────────────────────────────────────────────────────
# STUB_DU_MB: what `du -sm` reports ("" = unsizable) · STUB_MOUNTED=1: a running
# container mounts the cache · STUB_VOLUME=0: the volume does not exist.
BIN="$T/bin"
mkdir -p "$BIN"
cat >"$BIN/docker" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_DOCKER_LOG"
case "$*" in
  "volume inspect"*) [ "${STUB_VOLUME:-1}" = 1 ] ;;
  "ps -q"*) [ "${STUB_MOUNTED:-0}" = 1 ] && echo abc123; exit 0 ;;
  *"du -sm /c"*) [ -n "${STUB_DU_MB:-}" ] && printf '%s\t/c\n' "$STUB_DU_MB"; exit 0 ;;
  *) exit 0 ;;
esac
STUB
chmod +x "$BIN/docker"
export PATH="$BIN:$PATH"
export STUB_DOCKER_LOG="$T/docker.log"
export PFM_FENCE_STAMP_DIR="$T/stamp"
export PFM_FENCE_GOCACHE_MB=100

# shellcheck source=/dev/null
. "$SUT"

fresh() { rm -rf "$PFM_FENCE_STAMP_DIR" "$STUB_DOCKER_LOG"; mkdir -p "$PFM_FENCE_STAMP_DIR"; : >"$STUB_DOCKER_LOG"; }
cleared() { grep -q 'find /c -type f -delete' "$STUB_DOCKER_LOG"; }

# 1 — over budget, nothing mounted: files cleared, directories kept
fresh; STUB_DU_MB=250 fence_gocache_budget 2>"$T/err"
if cleared && grep -q '250 MB > 100 MB' "$T/err"; then ok "over budget clears the cache files"
else bad "over budget clears the cache files" "$(cat "$STUB_DOCKER_LOG")"; fi

# 2 — under budget: sized, not cleared, stamp written
fresh; STUB_DU_MB=40 fence_gocache_budget 2>/dev/null
if ! cleared && [ -f "$PFM_FENCE_STAMP_DIR/pfm-fence-gocache-budget.stamp" ]; then ok "under budget keeps the cache and stamps the day"
else bad "under budget keeps the cache and stamps the day" "$(cat "$STUB_DOCKER_LOG")"; fi

# 3 — a container mounts the cache: never sized, never cleared
fresh; STUB_DU_MB=250 STUB_MOUNTED=1 fence_gocache_budget 2>/dev/null
if ! cleared && ! grep -q 'du -sm' "$STUB_DOCKER_LOG"; then ok "a mounted cache is left alone"
else bad "a mounted cache is left alone" "$(cat "$STUB_DOCKER_LOG")"; fi

# 4 — checked today already: no docker call at all
fresh; touch "$PFM_FENCE_STAMP_DIR/pfm-fence-gocache-budget.stamp"
STUB_DU_MB=250 fence_gocache_budget 2>/dev/null
if [ ! -s "$STUB_DOCKER_LOG" ]; then ok "a fresh stamp skips the check"
else bad "a fresh stamp skips the check" "$(cat "$STUB_DOCKER_LOG")"; fi

# 5 — unsizable volume: warns, clears nothing, leaves no stamp (retried next run)
fresh; STUB_DU_MB="" fence_gocache_budget 2>"$T/err"
if ! cleared && grep -q 'budget check skipped' "$T/err" && [ ! -f "$PFM_FENCE_STAMP_DIR/pfm-fence-gocache-budget.stamp" ]; then
  ok "an unsizable volume warns and is retried"
else bad "an unsizable volume warns and is retried" "$(cat "$T/err")"; fi

# 6 — no volume yet: nothing sized, nothing cleared
fresh; STUB_VOLUME=0 STUB_DU_MB=250 fence_gocache_budget 2>/dev/null
if ! cleared && ! grep -q 'du -sm' "$STUB_DOCKER_LOG"; then ok "an absent volume is a no-op"
else bad "an absent volume is a no-op" "$(cat "$STUB_DOCKER_LOG")"; fi

# 7 — the prune takes only the fence project's dangling images, never -a
fresh; fence_prune_dangling
if grep -qx 'image prune -f --filter label=com.docker.compose.project=fence' "$STUB_DOCKER_LOG"; then ok "prune is scoped to fence dangling images"
else bad "prune is scoped to fence dangling images" "$(cat "$STUB_DOCKER_LOG")"; fi

# 8 — housekeeping never fails its caller, even when docker does
fresh; printf '#!/usr/bin/env bash\nexit 1\n' >"$BIN/docker"
if fence_housekeeping 2>/dev/null; then ok "housekeeping returns 0 when docker fails"
else bad "housekeeping returns 0 when docker fails"; fi

shtest_end
