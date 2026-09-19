#!/usr/bin/env bash
# Self-test for scripts/arch-check.sh's C23-bare-log ratchet: the check must
# COUNT a bare log.Printf outside internal/obs and cmd/pfm, must NOT count one
# inside them, and must refuse a count above the committed baseline. It runs
# arch-check.sh against a throwaway git fixture (PFM=<fixture>), never against
# this repo, and asserts on the CHECK C23-bare-log line rather than the exit
# status — the fixture carries none of the other baselines, so every other
# check legitimately reports ERROR there.
#
# Harness style follows scripts/test-sweep_test.sh, the sibling shell test.
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${SUT:-$ROOT/scripts/arch-check.sh}"
SHTEST_TAG=pfm-arch-check-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"

# fixture <dir>: a minimal git tree arch-check can enumerate.
fixture() {
  local dir=$1
  mkdir -p "$dir/internal/loud" "$dir/internal/obs" "$dir/cmd/pfm" "$dir/.arch"
  printf 'package loud\n\nimport "log"\n\nfunc Shout() { log.Printf("hello") }\n' > "$dir/internal/loud/loud.go"
  printf 'package obs\n\nimport "log"\n\nfunc Shout() { log.Printf("hello") }\n' > "$dir/internal/obs/obs.go"
  printf 'package main\n\nimport (\n\t"fmt"\n\t"os"\n)\n\nfunc main() { fmt.Fprintln(os.Stderr, "usage") }\n' > "$dir/cmd/pfm/main.go"
  git -C "$dir" init -q 2>/dev/null || return 1
  git -C "$dir" -c user.email=t@example.invalid -c user.name=t add -A 2>/dev/null || return 1
}

# The fixture is its own git repo, so the fence's mounted-repo overrides
# (PFM_DEV_REPO_GIT_DIR/WORK_TREE, honoured by arch-check's repo_git) are
# dropped here — with them set, arch-check enumerates the MOUNTED repo against
# the fixture directory and lists no files at all.
c23_line() {
  env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE PFM="$1" bash "$SUT" </dev/null 2>&1 | grep 'C23-bare-log'
}

c23_measure() {
  env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE PFM="$1" bash "$SUT" --measure
}

# ---- 1: a bare log.Printf outside obs/cmd is counted, and a missing baseline
# is an ERROR, never a PASS ---------------------------------------------------

REPO="$T/no-baseline"
if fixture "$REPO"; then
  line=$(c23_line "$REPO")
  if [[ "$line" == *ERROR* && "$line" == *bare-log.txt* ]]; then
    ok "C23: a missing baseline reports ERROR, not PASS"
  else
    bad "C23: expected ERROR for a missing baseline" "$line"
  fi
else
  bad "C23: could not build the git fixture (is git available?)"
fi

# ---- 2: with a baseline that lists it, the fixture's one call site passes,
# and neither internal/obs nor cmd/pfm is counted -----------------------------

echo 'internal/loud/loud.go 1' > "$REPO/.arch/bare-log.txt"
line=$(c23_line "$REPO")
if [[ "$line" == *PASS* && "$line" == *"1 in 1 keys"* ]]; then
  ok "C23: counts the one call site outside internal/obs and cmd/pfm, and only that one"
else
  bad "C23: expected PASS with exactly one counted call site" "$line"
fi

# ---- 3: the baseline refuses to grow ----------------------------------------

printf 'package loud\n\nimport "log"\n\nfunc Shout() { log.Printf("hello") }\nfunc Again() { log.Fatalf("bye") }\n' \
  > "$REPO/internal/loud/loud.go"
line=$(c23_line "$REPO")
if [[ "$line" == *FAIL* && "$line" == *"internal/loud/loud.go (1->2)"* ]]; then
  ok "C23: a second bare call in a baselined file FAILs the ratchet"
else
  bad "C23: expected FAIL when a baselined count grows" "$line"
fi

# ---- 4: a file the baseline never listed FAILs ------------------------------

REPO2="$T/new-file"
if fixture "$REPO2"; then
  : > "$REPO2/.arch/bare-log.txt"
  line=$(c23_line "$REPO2")
  if [[ "$line" == *FAIL* && "$line" == *"internal/loud/loud.go (new 1)"* ]]; then
    ok "C23: a call site in a file the baseline does not list FAILs"
  else
    bad "C23: expected FAIL for an unlisted file" "$line"
  fi
else
  bad "C23: could not build the second git fixture"
fi

# ---- 5: a baseline write failure is an ERROR and a nonzero exit ------------

REPO3="$T/measure-write-failure"
if fixture "$REPO3"; then
  # Override cp for only the bare-log destination. The pre-fix measure path
  # ignored this failure and incorrectly reported MEASURE with exit 0.
  mkdir -p "$REPO3/bin"
  printf '%s\n' '#!/usr/bin/env bash' 'case "${!#}" in' '  */bare-log.txt) exit 7 ;;' 'esac' 'exec /usr/bin/cp "$@"' > "$REPO3/bin/cp"
  chmod +x "$REPO3/bin/cp"
  log="$REPO3/measure.log"
  if PATH="$REPO3/bin:$PATH" c23_measure "$REPO3" >"$log" 2>&1; then measure_rc=0; else measure_rc=$?; fi
  line=$(grep 'C23-bare-log' "$log" || true)
  if [ "$measure_rc" -ne 0 ] && [[ "$line" == *ERROR* ]]; then
    ok "C23: a baseline write failure reports ERROR and exits nonzero"
  else
    bad "C23: expected a nonzero ERROR for a baseline write failure" "rc=$measure_rc" "$line"
  fi
else
  bad "C23: could not build the measure-write-failure fixture"
fi

# ---- 6: a baseline replacement failure is also an ERROR --------------------

REPO4="$T/measure-mv-failure"
if fixture "$REPO4"; then
  printf 'internal/loud/loud.go 1\n' > "$REPO4/.arch/bare-log.txt"
  mkdir -p "$REPO4/bin"
  printf '%s\n' '#!/usr/bin/env bash' 'case "${!#}" in' '  */bare-log.txt) exit 7 ;;' 'esac' 'exec /usr/bin/mv "$@"' > "$REPO4/bin/mv"
  chmod +x "$REPO4/bin/mv"
  log="$REPO4/measure.log"
  if PATH="$REPO4/bin:$PATH" env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE PFM="$REPO4" bash "$SUT" --measure >"$log" 2>&1; then measure_rc=0; else measure_rc=$?; fi
  line=$(grep 'C23-bare-log' "$log" || true)
  if [ "$measure_rc" -ne 0 ] && [[ "$line" == *ERROR* ]]; then
    ok "C23: a baseline replacement failure reports ERROR and exits nonzero"
  else
    bad "C23: expected a nonzero ERROR for a baseline replacement failure" "rc=$measure_rc" "$line"
  fi
else
  bad "C23: could not build the measure-mv-failure fixture"
fi

shtest_end
