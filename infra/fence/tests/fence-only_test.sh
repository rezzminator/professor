#!/usr/bin/env bash
# Every script that clones or runs a suite asks
# scripts/platform.sh's platform_in_fence before it acts, never PFM_DEV_FENCE alone: a host shell
# exporting that variable and PFM_STATE_DB once ran a suite against the live pfm.db. Each script
# runs from a copy of the tree whose door answers "not the fence" while the environment says fence
# (PFM_DEV_FENCE=1, a store path exported); each must refuse by name before it writes HOME or the
# store. The door's own rule, marker and runner included: scripts/test-platform.sh.
# BROKEN STATE: a script that trusts the variable runs past its gate: its first stderr line is not
# the refusal and the row FAILs naming what it printed.
set -uo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SHTEST_TAG=fence-only-test
# shellcheck source=scripts/shtest.sh
source "$ROOT/scripts/shtest.sh"
shtest_isolate_host

F="$T/tree"
mkdir -p "$F/scripts" "$F/infra" "$F/.claude/scripts" "$F/pfm/scripts" "$T/bin"
cp -R "$ROOT/infra/fence" "$F/infra/"
cp "$ROOT/scripts/shtest.sh" "$F/scripts/"
cp "$ROOT/.claude/scripts/dev.sh" "$ROOT/.claude/scripts/test.sh" "$ROOT/.claude/scripts/unit-path.sh" "$F/.claude/scripts/"
cp "$ROOT/pfm/scripts/repo-git.sh" "$F/pfm/scripts/"
{ cat "$ROOT/scripts/platform.sh"; printf '%s\n' 'platform_in_fence() { return 1; }'; } >"$F/scripts/platform.sh"
# A refused dev.sh re-runs itself through `iso`, which asks docker first: this one never starts a fence.
printf '#!/bin/sh\nprintf "docker %%s\\n" "$*" >>"%s"\nexit 1\n' "$T/docker.calls" >"$T/bin/docker"
chmod +x "$T/bin/docker"

# refuses LABEL WANT_RC WANT_LINE CMD...: CMD, under the fence variable and an exported store, exits
# WANT_RC with WANT_LINE as its first stderr line and leaves its HOME and the store untouched.
refuses() {
  local label=$1 want_rc=$2 want_line=$3 home="$T/home-$1" rc=0 first
  shift 3
  mkdir -p "$home" "$T/state"
  env -u GITHUB_ACTIONS -u RUNNER_ENVIRONMENT HOME="$home" PATH="$T/bin:$PATH" PFM_DEV_FENCE=1 \
    PFM_STATE_DB="$T/state/pfm.db" PFM_CONFIG="$home/pfm.config.json" PFM_FENCE_PROFILE=native \
    "$@" >"$T/$label.out" 2>"$T/$label.err" </dev/null || rc=$?
  first=$(head -n 1 "$T/$label.err")
  if [ "$rc" = "$want_rc" ] && [ "$first" = "$want_line" ] && [ -z "$(ls -A "$home")" ] && [ ! -e "$T/state/pfm.db" ]; then
    ok "$label refuses a host that exports the fence variable"
  else
    bad "$label refuses a host that exports the fence variable" "want rc=$want_rc first stderr line [$want_line], HOME and store untouched" \
      "got  rc=$rc first stderr line [$first]; HOME holds [$(ls -A "$home" | tr '\n' ' ')]; store $( [ -e "$T/state/pfm.db" ] && echo written || echo absent)" \
      "(artifacts $T/$label.out $T/$label.err)"
  fi
}

refuses source-clone 1 'source-clone: requires the fence' bash "$F/infra/fence/source-clone.sh" "$ROOT" "$T/clone"
refuses test-sh-fence-half 2 'test.sh: --fence runs inside the fence only' bash "$F/.claude/scripts/test.sh" --fence
refuses dev-sh-test 1 'FENCE-ONLY: dev.sh test never runs on the host — running: dev.sh iso test pfm' \
  bash "$F/.claude/scripts/dev.sh" test pfm
refuses dev-sh-e2e 1 'FENCE-ONLY: dev.sh e2e never runs on the host — running: dev.sh iso e2e pfm' \
  bash "$F/.claude/scripts/dev.sh" e2e pfm
[ ! -e "$T/clone" ] && ok 'source-clone made no clone' || bad 'source-clone made no clone' "$T/clone exists"
shtest_end
