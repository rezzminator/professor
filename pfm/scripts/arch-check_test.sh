#!/usr/bin/env bash
# Self-test for scripts/arch-check.sh's C1, C9, C12 and C23 ratchets. It runs
# arch-check.sh against a throwaway git fixture (PFM=<fixture>), never against
# this repo, and asserts on the relevant CHECK line rather than the exit
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

check_line() {
  local repo=$1 id=$2
  env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE PFM="$repo" bash "$SUT" </dev/null 2>&1 | grep "CHECK $id "
}

# ---- C9: every package needs a package doc ---------------------------------

REPO_C9="$T/package-doc"
if fixture "$REPO_C9"; then
  : > "$REPO_C9/.arch/no-package-doc.txt"
  for f in "$REPO_C9"/internal/{loud,obs}/*.go "$REPO_C9"/cmd/pfm/*.go; do
    sed -i '1i// Package fixture documents this package.' "$f"
  done
  sed -i '1d' "$REPO_C9/internal/loud/loud.go"
  line=$(check_line "$REPO_C9" C9-package-doc)
  if [[ "$line" == *FAIL* && "$line" == *"new: internal/loud"* ]]; then
    ok "C9: a package without a doc FAILs naming its directory"
  else
    bad "C9: expected FAIL for an undocumented package" "$line"
  fi

  sed -i '1i// Package loud documents this package.' "$REPO_C9/internal/loud/loud.go"
  line=$(check_line "$REPO_C9" C9-package-doc)
  if [[ "$line" == *PASS* && "$line" == *"0 baselined, 0 new"* ]]; then
    ok "C9: documented packages pass"
  else
    bad "C9: expected PASS for documented packages" "$line"
  fi
else
  bad "C9: could not build the git fixture"
fi

REPO_C9_EMPTY="$T/empty-sources"
if fixture "$REPO_C9_EMPTY"; then
  find "$REPO_C9_EMPTY" -name '*.go' -delete
  if output=$(env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE PFM="$REPO_C9_EMPTY" bash "$SUT" </dev/null 2>&1); then check_rc=0; else check_rc=$?; fi
  if [ "$check_rc" -eq 2 ] && [[ "$output" == *"CHECK setup"* && "$output" == *"no Go sources listed"* ]]; then
    ok "C9: an empty source list reports setup ERROR with rc 2"
  else
    bad "C9: expected rc 2 setup ERROR for an empty source list" "rc=$check_rc" "$output"
  fi
else
  bad "C9: could not build the empty-source fixture"
fi

# ---- C12: a named knob needs a production read -----------------------------

REPO_C12="$T/claude-pointers"
if fixture "$REPO_C12"; then
  : > "$REPO_C12/.arch/claude-dangling.txt"
  printf 'PFM_NOT_READ\n' > "$REPO_C12/CLAUDE.md"
  line=$(check_line "$REPO_C12" C12-claude-pointers)
  if [[ "$line" == *FAIL* && "$line" == *PFM_NOT_READ* ]]; then
    ok "C12: a named knob absent from production sources FAILs"
  else
    bad "C12: expected FAIL for an absent knob" "$line"
  fi

  printf 'package loud\nconst X = "PFM_X"\n' > "$REPO_C12/internal/loud/loud.go"
  printf 'PFM_X\n' > "$REPO_C12/CLAUDE.md"
  line=$(check_line "$REPO_C12" C12-claude-pointers)
  if [[ "$line" == *FAIL* && "$line" == *PFM_X* ]]; then
    ok "C12: a declaration without a use FAILs"
  else
    bad "C12: expected FAIL for a declaration without a use" "$line"
  fi

  printf 'var _ = X\n' >> "$REPO_C12/internal/loud/loud.go"
  line=$(check_line "$REPO_C12" C12-claude-pointers)
  if [[ "$line" == *PASS* && "$line" == *"0 baselined, 0 new"* ]]; then
    ok "C12: a declared constant used on another line passes"
  else
    bad "C12: expected PASS for a read constant" "$line"
  fi
else
  bad "C12: could not build the git fixture"
fi

# ---- C1: an unbaselined file over the source ceiling FAILs ------------------

REPO_C1="$T/ceiling-src"
if fixture "$REPO_C1"; then
  : > "$REPO_C1/.arch/ceiling-src.txt"
  printf 'package loud\n' > "$REPO_C1/internal/loud/loud.go"
  for _ in {1..10}; do printf '\n' >> "$REPO_C1/internal/loud/loud.go"; done
  line=$(CEIL_SRC=10 check_line "$REPO_C1" C1-ceiling-src)
  if [[ "$line" == *FAIL* && "$line" == *"internal/loud/loud.go (new 11)"* ]]; then
    ok "C1: a new source file over the ceiling FAILs naming the file"
  else
    bad "C1: expected FAIL for an unbaselined over-ceiling source" "$line"
  fi
else
  bad "C1: could not build the git fixture"
fi

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
