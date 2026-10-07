#!/usr/bin/env bash
# Self-test for scripts/arch-check.sh's C1, C9, C12, C23, C25 and C26 ratchets and for
# how it folds its concurrent checks: print order, exit status, and a check job
# that dies. It runs arch-check.sh against a throwaway git fixture (PFM=<fixture>),
# never against this repo, and asserts on the relevant CHECK line rather than the
# exit status — the fixture carries none of the other baselines, so every other
# check legitimately reports ERROR there. The last section builds a green fixture
# (every baseline written by --measure) where the exit status can be asserted.
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

  # A directory pointer resolves under pfm/ or at the repo root (pfm/..).
  printf 'PFM_X\n`croot/`\n' > "$REPO_C12/CLAUDE.md"
  line=$(check_line "$REPO_C12" C12-claude-pointers)
  if [[ "$line" == *FAIL* && "$line" == *croot/* ]]; then
    ok "C12: a directory pointer that resolves nowhere FAILs"
  else
    bad "C12: expected FAIL for a dangling directory pointer" "$line"
  fi
  mkdir -p "$T/croot"
  line=$(check_line "$REPO_C12" C12-claude-pointers)
  if [[ "$line" == *PASS* ]]; then
    ok "C12: a directory pointer resolving at the repo root passes"
  else
    bad "C12: expected PASS for a repo-root directory pointer" "$line"
  fi

  # A generated directory the repo root's .gitignore names resolves before any build made it.
  printf 'PFM_X\n`gen/`\n' > "$REPO_C12/CLAUDE.md"
  printf '/gen/\n' > "$T/.gitignore"
  line=$(check_line "$REPO_C12" C12-claude-pointers)
  if [[ "$line" == *PASS* ]]; then
    ok "C12: a gitignored generated directory pointer passes before it exists"
  else
    bad "C12: expected PASS for a gitignored generated directory pointer" "$line"
  fi
  rm -f "$T/.gitignore"
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

# ---- C25: every test package's TestMain reaches testjail.Run ---------------

JAILED_MAIN='package loud\n\nimport (\n\t"os"\n\t"testing"\n\n\t"pfm/internal/testjail"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }\n'
c25_fixture() { # <dir> <loud_test.go body printf-format>; empty baseline
  fixture "$1" || return 1
  : > "$1/.arch/testmain-jail.txt"
  printf "$2" > "$1/internal/loud/loud_test.go"
  git -C "$1" -c user.email=t@example.invalid -c user.name=t add -A 2>/dev/null
}

REPO_C25="$T/c25-missing"
if c25_fixture "$REPO_C25" 'package loud\n\nimport "testing"\n\nfunc TestShout(t *testing.T) {}\n'; then
  line=$(check_line "$REPO_C25" C25-testmain-jail)
  if [[ "$line" == *FAIL* && "$line" == *"new: internal/loud"* ]]; then
    ok "C25: a test package without TestMain FAILs naming its directory"
  else
    bad "C25: expected FAIL for a package without TestMain" "$line"
  fi
else
  bad "C25: could not build the missing-TestMain fixture"
fi

REPO_C25_BARE="$T/c25-bare"
if c25_fixture "$REPO_C25_BARE" 'package loud\n\nimport (\n\t"os"\n\t"testing"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n'; then
  line=$(check_line "$REPO_C25_BARE" C25-testmain-jail)
  if [[ "$line" == *FAIL* && "$line" == *"new: internal/loud"* ]]; then
    ok "C25: a TestMain calling m.Run() only FAILs"
  else
    bad "C25: expected FAIL for an unjailed TestMain" "$line"
  fi
else
  bad "C25: could not build the unjailed-TestMain fixture"
fi

REPO_C25_OK="$T/c25-jailed"
if c25_fixture "$REPO_C25_OK" "$JAILED_MAIN"; then
  line=$(check_line "$REPO_C25_OK" C25-testmain-jail)
  if [[ "$line" == *PASS* && "$line" == *"0 baselined, 0 new"* ]]; then
    ok "C25: a TestMain through testjail.Run passes"
  else
    bad "C25: expected PASS for a jailed TestMain" "$line"
  fi
else
  bad "C25: could not build the jailed-TestMain fixture"
fi

REPO_C25_DATA="$T/c25-testdata"
if c25_fixture "$REPO_C25_DATA" "$JAILED_MAIN"; then
  mkdir -p "$REPO_C25_DATA/internal/loud/testdata/fix"
  printf 'package fix\n\nimport "testing"\n\nfunc TestFix(t *testing.T) {}\n' > "$REPO_C25_DATA/internal/loud/testdata/fix/fix_test.go"
  git -C "$REPO_C25_DATA" -c user.email=t@example.invalid -c user.name=t add -A 2>/dev/null
  line=$(check_line "$REPO_C25_DATA" C25-testmain-jail)
  if [[ "$line" == *PASS* && "$line" == *"0 baselined, 0 new"* ]]; then
    ok "C25: a _test.go under testdata is not judged"
  else
    bad "C25: expected PASS with a testdata test file" "$line"
  fi
else
  bad "C25: could not build the testdata fixture"
fi

REPO_C25_NONE="$T/c25-none"
if fixture "$REPO_C25_NONE"; then
  line=$(check_line "$REPO_C25_NONE" C25-testmain-jail)
  if [[ "$line" == *PASS* && "$line" == *"0 test packages"* ]]; then
    ok "C25: a tree with no tests passes with 0 test packages"
  else
    bad "C25: expected PASS 0 test packages" "$line"
  fi
else
  bad "C25: could not build the no-tests fixture"
fi

REPO_C25_SELF="$T/c25-testjail"
if c25_fixture "$REPO_C25_SELF" "$JAILED_MAIN"; then
  mkdir -p "$REPO_C25_SELF/internal/testjail"
  printf 'package testjail\n\nimport (\n\t"os"\n\t"testing"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(Run(m)) }\n' > "$REPO_C25_SELF/internal/testjail/testjail_test.go"
  git -C "$REPO_C25_SELF" -c user.email=t@example.invalid -c user.name=t add -A 2>/dev/null
  line=$(check_line "$REPO_C25_SELF" C25-testmain-jail)
  if [[ "$line" == *PASS* && "$line" == *"0 baselined, 0 new"* ]]; then
    ok "C25: internal/testjail whose TestMain calls Run(m) passes"
  else
    bad "C25: expected PASS for internal/testjail calling Run(m)" "$line"
  fi
else
  bad "C25: could not build the testjail fixture"
fi

# ---- C26: an executable test write goes through testjail.WriteExecutable ----

c26_fixture() { # <dir> <file under the fixture> <Go source on stdin>; empty baseline
  fixture "$1" || return 1
  : > "$1/.arch/exec-writes.txt"
  printf "package loud\n" > "$1/internal/loud/clean_test.go" || return 1
  mkdir -p "$(dirname "$1/$2")" && cat > "$1/$2" || return 1
  git -C "$1" -c user.email=t@example.invalid -c user.name=t add -A 2>/dev/null
}

c26_case() { # <name> <file> <want: PASS|FAIL> <FAIL detail> <Go source on stdin>
  local repo="$T/c26-$1" line
  if ! c26_fixture "$repo" "$2"; then bad "C26 $1: could not build the fixture"; return; fi
  line=$(check_line "$repo" C26-exec-write)
  if [ "$3" = PASS ] && [[ "$line" == *PASS* && "$line" == *"0 baselined, 0 new"* ]]; then
    ok "C26 $1: passes"
  elif [ "$3" = FAIL ] && [[ "$line" == *FAIL* && "$line" == *"new: $4"* ]]; then
    ok "C26 $1: FAILs naming $4"
  else
    bad "C26 $1: expected $3 $4" "$line"
  fi
}

c26_case exec-literal internal/loud/loud_test.go FAIL internal/loud/loud_test.go:6 <<'GO'
package loud

import "os"

func writeStub(path string) error {
	return os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700)
}
GO

c26_case multi-line internal/loud/loud_test.go FAIL internal/loud/loud_test.go:6 <<'GO'
package loud

import "os"

func writeStub(path string) error {
	return os.WriteFile(
		path,
		[]byte("#!/bin/sh\nprintf '%s,(%s)\n' \"$1\", x"),
		0755,
	)
}
GO

c26_case variable-mode internal/loud/loud_test.go FAIL internal/loud/loud_test.go:6 <<'GO'
package loud

import "os"

func writeFixture(path string, mode os.FileMode) error {
	return os.WriteFile(path, []byte(`raw ( "body", 0o600`), mode)
}
GO

c26_case testjail-source internal/testjail/stub.go FAIL internal/testjail/stub.go:11 <<'GO'
package testjail

import (
	"os"
	"path/filepath"
)

// Stubs writes one stub per name into dir.
func Stubs(dir string, names ...string) error {
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stub"), 0o755); err != nil {
			return err
		}
	}
	return nil
}
GO

c26_case plain-modes internal/loud/loud_test.go PASS "" <<'GO'
package loud

import "os"

// os.WriteFile(path, body, 0o700) in a comment is not a call.
func writeData(path string) error {
	if err := os.WriteFile(path, []byte("0o700)"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("data"), 0644)
}
GO

c26_case under-forklock internal/loud/loud_test.go PASS "" <<'GO'
package loud

import (
	"os"
	"syscall"
)

func writeExecutableUnderForkLock(path string, body []byte, mode os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, body, mode)
}
GO

c26_case lock-ends-with-its-func internal/loud/loud_test.go FAIL internal/loud/loud_test.go:13 <<'GO'
package loud

import (
	"os"
	"syscall"
)

func lock() {
	syscall.ForkLock.RLock()
}

func writeStub(path string) error {
	return os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755)
}
GO

c26_case production-source internal/loud/stub.go PASS "" <<'GO'
package loud

import "os"

func writeLauncher(path string) error {
	return os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700)
}
GO

# ---- the concurrent checks: order, exit status, a job that dies ------------

# green_fixture <dir>: a tree on which all 26 checks PASS — the fixture plus what
# C12, C14 and C15 parse, an internal/mcpserv package for C10, a jailed test file
# for C21 and C25, and the C24 script arch-check.sh runs from $PFM/scripts, with
# every baseline the --measure of that tree writes.
green_fixture() {
  local dir=$1
  fixture "$dir" || return 1
  mkdir -p "$dir/scripts" && cp "$ROOT/scripts/arch-c24.sh" "$ROOT/scripts/repo-git.sh" "$dir/scripts/" || return 1
  : > "$dir/CLAUDE.md"
  cat > "$dir/cmd/pfm/main.go" <<'GO'
package main

import (
    "fmt"
    "os"
)

func run(args []string) int {
    switch args[0] {
    case "list":
        return 0
    }
    return 1
}

func printUsage() {
    fmt.Fprintln(os.Stderr, "  list the chats")
}

// usage: pfm internal a|b
func runInternal(args []string) {
    if args[0] == "a" || args[0] != "b" {
        return
    }
}

func main() { printUsage() }
GO
  mkdir -p "$dir/internal/mcpserv" && printf 'package mcpserv\n' > "$dir/internal/mcpserv/mcpserv.go" || return 1
  printf "$JAILED_MAIN" > "$dir/internal/loud/loud_test.go"
  env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE PFM="$dir" bash "$SUT" --measure </dev/null >/dev/null 2>&1 || return 1
}

# run_all <dir> [env assignments…]: the whole run's stdout in $out and its exit status in $rc.
run_all() {
  local dir=$1; shift
  out=$(env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE "$@" PFM="$dir" bash "$SUT" </dev/null 2>/dev/null); rc=$?
}

# Today's order: C1 … C23, then C25, C26, then C24 (its own script, last).
EXPECTED_ORDER="C1-ceiling-src C2-ceiling-test C3-cmd-budget C4-cmd-primitives C5-tmux-runner C6-atomic-write C7-sql-open C8-negation-dirs C9-package-doc C10-mcp-argv C11-db-names C12-claude-pointers C13-test-mirror C14-usage-parity C15-internal-usage C16-env-outside-paths C17-dup-functions C18-engine-spellings C19-env-namespace C20-codex-home C21-test-jail C22-host-doors C23-bare-log C25-testmain-jail C26-exec-write C24-unwrapped-door"

REPO_GREEN="$T/green"
if green_fixture "$REPO_GREEN"; then
  run_all "$REPO_GREEN"
  order=$(printf '%s\n' "$out" | awk '$1 == "CHECK" {printf "%s ", $2}' | sed 's/ $//')
  passes=$(printf '%s\n' "$out" | grep -c '^CHECK [^ ]* *PASS ')
  if [ "$rc" -eq 0 ] && [ "$passes" -eq 26 ] && [ "$order" = "$EXPECTED_ORDER" ]; then
    ok "exit: a tree where all 26 checks PASS exits 0, one line per check in today's order"
  else
    bad "exit: expected rc 0, 26 PASS lines in order" "rc=$rc passes=$passes" "$order" "$out"
  fi
else
  bad "exit: could not build the green fixture"
fi

REPO_FAIL="$T/green-fail"
if green_fixture "$REPO_FAIL"; then
  printf 'func Again() { log.Fatalf("bye") }\n' >> "$REPO_FAIL/internal/loud/loud.go"
  run_all "$REPO_FAIL"
  others=$(printf '%s\n' "$out" | grep '^CHECK ' | grep -vc 'C23-bare-log .*FAIL ')
  if [ "$rc" -eq 1 ] && [ "$others" -eq 25 ] && [[ "$out" == *"C23-bare-log"*"FAIL"*"internal/loud/loud.go (1->2)"* ]]; then
    ok "exit: one FAIL and no ERROR exits 1"
  else
    bad "exit: expected rc 1 with only C23 failing" "rc=$rc others=$others" "$out"
  fi

  rm -f "$REPO_FAIL/.arch/ceiling-src.txt"
  run_all "$REPO_FAIL"
  if [ "$rc" -eq 2 ] && [[ "$out" == *"C1-ceiling-src"*"ERROR"*"baseline .arch/ceiling-src.txt missing"* ]] && [[ "$out" == *"C23-bare-log"*"FAIL"* ]]; then
    ok "exit: an ERROR beside a FAIL exits 2, the maximum, and both lines print"
  else
    bad "exit: expected rc 2 with C1 ERROR and C23 FAIL" "rc=$rc" "$out"
  fi
else
  bad "exit: could not build the FAIL fixture"
fi

# ---- a check job that dies leaves no result: its own ERROR line, exit 2 -----

REPO_KILL="$T/green-killed"
if green_fixture "$REPO_KILL"; then
  # C1 is the only job that hands wc source files as operands (C2's are _test.go,
  # the rest pipe), so this wc kills C1's job — the subshell that is its parent —
  # and nothing else.
  mkdir -p "$REPO_KILL/bin"
  printf '%s\n' '#!/usr/bin/env bash' \
    'for a in "$@"; do case $a in -*|*_test.go) ;; *) kill -KILL "$PPID"; exit 9 ;; esac; done' \
    'exec /usr/bin/wc "$@"' > "$REPO_KILL/bin/wc"
  chmod +x "$REPO_KILL/bin/wc"
  run_all "$REPO_KILL" PATH="$REPO_KILL/bin:$PATH"
  passes=$(printf '%s\n' "$out" | grep -c '^CHECK [^ ]* *PASS ')
  lines=$(printf '%s\n' "$out" | grep -c '^CHECK ')
  line=$(printf '%s\n' "$out" | grep '^CHECK C1-ceiling-src ' || true)
  if [ "$rc" -eq 2 ] && [ "$lines" -eq 26 ] && [ "$passes" -eq 25 ] && [[ "$line" == *ERROR* && "$line" == *"without leaving its CHECK line"* ]]; then
    ok "a check job killed before its result is an ERROR line naming it, exit 2, the other 24 checks PASS"
  else
    bad "a killed check job must print ERROR for that check and exit 2" "rc=$rc lines=$lines passes=$passes" "$line"
  fi
else
  bad "a killed check job: could not build the green fixture"
fi

shtest_end
