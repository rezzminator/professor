#!/usr/bin/env bash
# Regression for test-format-md.sh: a hook that never enters the formatter must
# fail with a readiness diagnostic within the suite's bound, including exit 0.
# BROKEN STATE: a hung suite or missing readiness diagnostic fails an assertion.
set -uo pipefail
SHTEST_TAG=format-md-readiness
source "$(dirname "$0")/shtest.sh"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export REAL_HOOK="$ROOT/templates/project/scripts/format-md.sh"
export SHTEST_WAIT_SH="$ROOT/scripts/shtest-wait.sh" # the stalled hook holds through the wait door
cat > "$T/hook.sh" <<'HOOK'
#!/usr/bin/env bash
input=$(cat)
if [[ $(jq -r '.session_id' <<< "$input") == overlap ]]; then
  # a stalled hook announces it will never reach the formatter, then blocks until killed
  if [[ $EARLY_HOOK_MODE == stall ]]; then : > "$STUB_FMT_GATE/stalled"; . "$SHTEST_WAIT_SH"; shtest_hold early-hook; fi
  exit "$EARLY_HOOK_MODE"
fi
printf '%s' "$input" | bash "$REAL_HOOK"
HOOK
for mode in 0 7 stall; do
  out=$(EARLY_HOOK_MODE="$mode" bash "$ROOT/scripts/test-format-md.sh" --readiness-only "$T/hook.sh" 2>&1); rc=$?
  if [[ $mode == stall ]]; then reason='hook stalled before formatter readiness';
  else reason="hook exited before formatter readiness (exit $mode)"; fi
  if [[ $rc == 1 && $out == *"formatter readiness FAILED — $reason"* ]]; then
    ok "readiness failure is bounded and named: $mode"
  else bad "readiness failure is bounded and named: $mode" "rc=$rc (want 1)" "$out"; fi
done
shtest_end
