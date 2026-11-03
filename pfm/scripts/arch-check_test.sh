#!/usr/bin/env bash
# Self-test for scripts/arch-check.sh's C1, C5, C9, C10, C12, C23, C25, C26, C27, C28, C29 and C30 ratchets and for
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

# ---- C27: identity variables are read only through internal/resolve ---------

REPO_C27="$T/identity-env"
if fixture "$REPO_C27"; then
  : > "$REPO_C27/.arch/identity-env.txt"
  mkdir -p "$REPO_C27/internal/resolve" "$REPO_C27/internal/glance" "$REPO_C27/internal/launch"
  # The home reads the names; a write (an exec env stripping TMUX, a "TMUX=" pair) is no read.
  printf 'package resolve\n\nconst ClaudeSessionEnv = "CLAUDE_CODE_SESSION_ID"\n\nfunc of(get func(string) string) string { return get("TMUX") + get(ClaudeSessionEnv) }\n' > "$REPO_C27/internal/resolve/resolve.go"
  printf 'package launch\n\nfunc env(with func(string, string) []string, pane string) []string {\n\treturn append(with("TMUX", ""), "TMUX_PANE="+pane)\n}\n' > "$REPO_C27/internal/launch/launch.go"
  git -C "$REPO_C27" -c user.email=t@example.invalid -c user.name=t add -A 2>/dev/null
  line=$(check_line "$REPO_C27" C27-identity-env)
  if [[ "$line" == *PASS* && "$line" == *"0 in 0 keys"* ]]; then
    ok "C27: identity reads inside internal/resolve and identity writes elsewhere pass"
  else
    bad "C27: expected PASS for reads inside resolve and writes outside it" "$line"
  fi

  # Three planted reads outside resolve: a literal through a getter, a map index
  # of another process's environ, and a constant that holds a name.
  printf 'package glance\n\nimport "os"\n\nconst socketEnv = "HERDR_SOCKET_PATH"\n\nfunc pane(env func(string) string) string { return env("TMUX_PANE") }\n\nfunc sender(environ map[string]string) string { return environ["CHAT_SENDER_LABEL"] }\n\nfunc socket() string { return os.Getenv(socketEnv) }\n' > "$REPO_C27/internal/glance/glance.go"
  git -C "$REPO_C27" -c user.email=t@example.invalid -c user.name=t add -A 2>/dev/null
  line=$(check_line "$REPO_C27" C27-identity-env)
  if [[ "$line" == *FAIL* && "$line" == *"internal/glance/glance.go (new 3)"* && "$line" != *internal/resolve* && "$line" != *internal/launch* ]]; then
    ok "C27: a literal, an environ index and a name constant read outside resolve FAIL, counted per file"
  else
    bad "C27: expected FAIL counting the three planted reads" "$line"
  fi

  printf 'internal/glance/glance.go 3\n' > "$REPO_C27/.arch/identity-env.txt"
  line=$(check_line "$REPO_C27" C27-identity-env)
  if [[ "$line" == *PASS* && "$line" == *"3 in 1 keys"* ]]; then
    ok "C27: baselined identity reads pass"
  else
    bad "C27: expected PASS once the reads are baselined" "$line"
  fi
else
  bad "C27: could not build the git fixture"
fi

# ---- C28: a pid is signalled only through internal/gather's signal door ---

REPO_C28="$T/signal-door"
if fixture "$REPO_C28"; then
  : > "$REPO_C28/.arch/signal-door.txt"
  mkdir -p "$REPO_C28/internal/gather" "$REPO_C28/internal/sweep"
  # The door's own files send and probe; nothing else is planted yet.
  printf 'package gather\n\nimport "golang.org/x/sys/unix"\n\nfunc send(pid int) error { return unix.Kill(pid, unix.SIGTERM) }\n' > "$REPO_C28/internal/gather/signal_darwin.go"
  printf 'package gather\n\nimport "syscall"\n\nfunc ProbeProcess(pid int) error { return syscall.Kill(pid, 0) }\n' > "$REPO_C28/internal/gather/pidalive.go"
  git -C "$REPO_C28" -c user.email=t@example.invalid -c user.name=t add -A 2>/dev/null
  line=$(check_line "$REPO_C28" C28-signal-door)
  if [[ "$line" == *PASS* && "$line" == *"0 in 0 keys"* ]]; then
    ok "C28: the door's own sends and its signal-0 probe pass"
  else
    bad "C28: expected PASS for kill calls inside the door's files" "$line"
  fi

  # Four planted signal paths outside the door: a kill primitive as a function
  # value, a process found by pid, an *os.Process signalled, and one held.
  printf 'package sweep\n\nimport (\n\t"os"\n\t"os/exec"\n\t"syscall"\n)\n\nvar send = syscall.Kill\n\nfunc stop(cmd *exec.Cmd, pid int) {\n\tfound, _ := os.FindProcess(pid)\n\t_ = found\n\t_ = cmd.Process.Signal(os.Interrupt)\n\theld := cmd.Process\n\t_ = held\n}\n' > "$REPO_C28/internal/sweep/sweep.go"
  git -C "$REPO_C28" -c user.email=t@example.invalid -c user.name=t add -A 2>/dev/null
  line=$(check_line "$REPO_C28" C28-signal-door)
  if [[ "$line" == *FAIL* && "$line" == *"internal/sweep/sweep.go (new 4)"* && "$line" != *internal/gather* ]]; then
    ok "C28: a kill primitive, a found process, a signalled and a held *os.Process outside the door FAIL, counted per file"
  else
    bad "C28: expected FAIL counting the four planted signal paths" "$line"
  fi

  printf 'internal/sweep/sweep.go 4\n' > "$REPO_C28/.arch/signal-door.txt"
  line=$(check_line "$REPO_C28" C28-signal-door)
  if [[ "$line" == *PASS* && "$line" == *"4 in 1 keys"* ]]; then
    ok "C28: baselined own-child signals pass"
  else
    bad "C28: expected PASS once the signals are baselined" "$line"
  fi
else
  bad "C28: could not build the git fixture"
fi

# ---- C29: an OS difference lives inside its door ---------------------------

# expect_line <label> <got> <id> <verdict> <detail>: the whole CHECK line, as say prints it.
expect_line() {
  local want; want=$(printf 'CHECK %-22s %-7s %s' "$3" "$4" "$5")
  if [ "$2" = "$want" ]; then ok "$1"; else bad "$1" "want: $want" "got:  $2"; fi
}

# c29_registry <dir> <rows>: a deps registry fixture holding the given rows.
c29_registry() {
  mkdir -p "$1/internal/deps"
  printf 'package deps\n\nvar fixedCommands = []Entry{\n%s}\n' "$2" > "$1/internal/deps/registry.go"
}
C29_ROWS='	{
		Name:      "launchctl",
		Purpose:   "one platform",
		Platforms: []string{platformDarwin},
	},
	{
		Name:      "python3",
		Platforms: []string{platformLinux, platformDarwin},
	},
	{Name: "sh", Purpose: "every platform"},
'

REPO_C29="$T/os-door"
if fixture "$REPO_C29"; then
  : > "$REPO_C29/.arch/os-door.txt"
  c29_registry "$REPO_C29" "$C29_ROWS"
  # Every counted class inside a door's own files.
  printf 'package deps\n\nfunc door() {\n\tif runtime.GOOS == "darwin" {\n\t}\n\t_, _ = os.ReadFile("/proc/self/stat")\n\t_ = filepath.Join(root, "cmdline")\n\t_ = exec.Command("launchctl")\n}\n' > "$REPO_C29/internal/deps/foo.go"
  mkdir -p "$REPO_C29/internal/gather" && printf 'package gather\n\nfunc unix() string { return filepath.Join(root, "net", "unix") }\n' > "$REPO_C29/internal/gather/sockets.go"
  printf '//go:build darwin\n\npackage gather\n' > "$REPO_C29/internal/gather/table_darwin.go"
  git -C "$REPO_C29" -c user.email=t@example.invalid -c user.name=t add -A 2>/dev/null
  expect_line "C29: an OS read, a proc read, a one-OS command and a tagged file inside the doors pass" \
    "$(check_line "$REPO_C29" C29-os-door)" C29-os-door PASS "0 in 0 keys, none above baseline"

  # Outside the doors: an OS compare (1), a /proc literal and a proc file join
  # (2), a one-OS command beside a two-platform one that does not count (1), a
  # file named for an OS (1 whatever it holds), a //go:build OS selection (1)
  # and a //go:build race constraint that selects no OS (0).
  mkdir -p "$REPO_C29"/internal/{foo,proc,cmdlit,sel,race}
  printf 'package foo\n\nfunc f() {\n\tif runtime.GOOS == "darwin" {\n\t}\n}\n' > "$REPO_C29/internal/foo/foo.go"
  printf 'package foo\n\nfunc g() {\n\tif runtime.GOOS == "darwin" {\n\t}\n\t_ = "/proc/self"\n}\n' > "$REPO_C29/internal/foo/foo_darwin.go"
  printf 'package proc\n\nfunc p(root string, pid int) {\n\t_, _ = os.ReadFile("/proc/self/stat")\n\t_ = filepath.Join(root, strconv.Itoa(pid), "cmdline")\n}\n' > "$REPO_C29/internal/proc/proc.go"
  printf 'package cmdlit\n\nfunc c() {\n\t_, _ = runner.Run(ctx, []string{"launchctl", "print"})\n\t_, _ = runner.Run(ctx, []string{"python3"})\n}\n' > "$REPO_C29/internal/cmdlit/cmdlit.go"
  printf '// Package sel is selected on Linux.\n//go:build linux\n\npackage sel\n' > "$REPO_C29/internal/sel/sel.go"
  printf '//go:build race\n\npackage race\n\nconst raceEnabled = true\n' > "$REPO_C29/internal/race/race.go"
  git -C "$REPO_C29" -c user.email=t@example.invalid -c user.name=t add -A 2>/dev/null
  expect_line "C29: each class outside the doors FAILs counted per file, a tagged file counting 1, a race constraint and a two-platform command not at all" \
    "$(check_line "$REPO_C29" C29-os-door)" C29-os-door FAIL \
    "internal/cmdlit/cmdlit.go (new 1) internal/foo/foo.go (new 1) internal/foo/foo_darwin.go (new 1) internal/proc/proc.go (new 2) internal/sel/sel.go (new 1) "

  printf 'internal/cmdlit/cmdlit.go 1\ninternal/foo/foo.go 1\ninternal/foo/foo_darwin.go 1\ninternal/proc/proc.go 2\ninternal/sel/sel.go 1\n' > "$REPO_C29/.arch/os-door.txt"
  expect_line "C29: baselined OS branches pass" \
    "$(check_line "$REPO_C29" C29-os-door)" C29-os-door PASS "6 in 5 keys, none above baseline"

  c29_registry "$REPO_C29" '	{
		Name:      "python3",
		Platforms: []string{platformLinux, platformDarwin},
	},
'
  expect_line "C29: a registry with no one-platform command is ERROR, never a clean pass" \
    "$(check_line "$REPO_C29" C29-os-door)" C29-os-door ERROR \
    "no one-platform command derived from internal/deps/registry.go — the enumerator did not run"

  rm -f "$REPO_C29/internal/deps/registry.go"
  expect_line "C29: an unreadable registry is ERROR" \
    "$(check_line "$REPO_C29" C29-os-door)" C29-os-door ERROR "could not read internal/deps/registry.go"
else
  bad "C29: could not build the git fixture"
fi

# ---- C30: a shell OS difference lives inside scripts/platform.sh -----------

# C30 reads the repo root, the parent of PFM, so each fixture's PFM is <root>/pfm.
ROOT_C30="$T/shell-platform"
REPO_C30="$ROOT_C30/pfm"
if fixture "$REPO_C30"; then
  : > "$REPO_C30/.arch/shell-platform.txt"
  mkdir -p "$ROOT_C30/scripts" "$ROOT_C30/.claude/scripts" "$REPO_C30/scripts"
  line='if command -v sha256sum >/dev/null; then'
  printf '%s\n' "$line" > "$ROOT_C30/scripts/platform.sh"
  printf '%s\n' "$line" 'uname -s' > "$ROOT_C30/scripts/test-x.sh"
  printf '%s\n' "$line" 'uname -s' > "$REPO_C30/scripts/x_test.sh"
  printf '%s\n' 'echo portable' > "$REPO_C30/scripts/clean.sh"
  expect_line "C30: the door and the shell tests name OS tools and pass" \
    "$(check_line "$REPO_C30" C30-shell-platform)" C30-shell-platform PASS "0 in 0 keys, none above baseline"

  printf '%s\n' '# sha256sum is the GNU tool' "$line" > "$ROOT_C30/scripts/x.sh"
  printf '%s\n' 'pgrep -f serve' > "$ROOT_C30/.claude/scripts/y.sh"
  printf 'restart:\n\tsystemctl --user restart x\n' > "$REPO_C30/Makefile"
  expect_line "C30: an OS tool in scripts/, .claude/scripts/ and pfm/Makefile FAILs counted per file, a comment not at all" \
    "$(check_line "$REPO_C30" C30-shell-platform)" C30-shell-platform FAIL \
    ".claude/scripts/y.sh (new 1) pfm/Makefile (new 1) scripts/x.sh (new 1) "

  printf '.claude/scripts/y.sh 1\npfm/Makefile 1\nscripts/x.sh 1\n' > "$REPO_C30/.arch/shell-platform.txt"
  expect_line "C30: baselined shell OS lines pass" \
    "$(check_line "$REPO_C30" C30-shell-platform)" C30-shell-platform PASS "3 in 3 keys, none above baseline"

  printf '.claude/scripts/y.sh 1\npfm/Makefile 1\nscripts/gone.sh 1\nscripts/x.sh 1\n' > "$REPO_C30/.arch/shell-platform.txt"
  expect_line "C30: a baselined shell file that is gone is a stale FAIL, never a quiet pass" \
    "$(check_line "$REPO_C30" C30-shell-platform)" C30-shell-platform FAIL \
    "stale: scripts/gone.sh — .arch/shell-platform.txt names a file that no longer exists; run --measure"
else
  bad "C30: could not build the git fixture"
fi

REPO_C30_EMPTY="$T/shell-none/pfm"
if fixture "$REPO_C30_EMPTY"; then
  : > "$REPO_C30_EMPTY/.arch/shell-platform.txt"
  line=$(check_line "$REPO_C30_EMPTY" C30-shell-platform)
  if [[ "$line" == *ERROR* && "$line" == *"no shell sources listed under $REPO_C30_EMPTY/.."* ]]; then
    ok "C30: no shell source to read is ERROR, never a clean pass"
  else
    bad "C30: expected ERROR when no shell source is listed" "$line"
  fi
else
  bad "C30: could not build the git fixture"
fi

# ---- C5: tmux and herdr run only through their façades ---------------------

REPO_C5="$T/tmux-runner"
if fixture "$REPO_C5"; then
  : > "$REPO_C5/.arch/tmux-runners.txt"
  mkdir -p "$REPO_C5/internal/tmux" "$REPO_C5/internal/herdr" "$REPO_C5/internal/attach" "$REPO_C5/internal/glance" "$REPO_C5/internal/admin"
  # The façades themselves may spell their binaries.
  printf 'package tmux\n\nfunc run() []string { return []string{"tmux", "-S", "x"} }\n' > "$REPO_C5/internal/tmux/tmux.go"
  printf 'package herdr\n\nfunc run() []string { return []string{"herdr", "agent", "attach"} }\n' > "$REPO_C5/internal/herdr/herdr.go"
  line=$(check_line "$REPO_C5" C5-tmux-runner)
  if [[ "$line" == *PASS* && "$line" == *"0 baselined, 0 new"* ]]; then
    ok "C5: tmux and herdr argv inside internal/tmux and internal/herdr pass"
  else
    bad "C5: expected PASS for argv inside the façades" "$line"
  fi

  # Three planted runners outside the façades, one per door shape the ratchet
  # missed before it was widened: a herdr argv slice, a runner handed "tmux"
  # and its -S flag, a herdr binary resolved by hand.
  printf 'package attach\n\nfunc argv(pane string) []string { return []string{"herdr", "agent", "attach", pane} }\n' > "$REPO_C5/internal/attach/attach.go"
  printf 'package glance\n\nfunc read(run func(string, ...string) error, path string) error {\n\treturn run("tmux", "-S", path, "display-message")\n}\n' > "$REPO_C5/internal/glance/glance.go"
  printf 'package admin\n\nfunc find(lookPath func(string) (string, error)) (string, error) { return lookPath("herdr") }\n' > "$REPO_C5/internal/admin/admin.go"
  line=$(check_line "$REPO_C5" C5-tmux-runner)
  if [[ "$line" == *FAIL* && "$line" == *internal/attach/attach.go* && "$line" == *internal/glance/glance.go* && "$line" == *internal/admin/admin.go* ]]; then
    ok "C5: a herdr argv, a raw tmux -S run and a hand-resolved herdr binary outside the façades FAIL, each named"
  else
    bad "C5: expected FAIL naming every planted runner" "$line"
  fi

  printf 'internal/admin/admin.go\ninternal/attach/attach.go\ninternal/glance/glance.go\n' > "$REPO_C5/.arch/tmux-runners.txt"
  line=$(check_line "$REPO_C5" C5-tmux-runner)
  if [[ "$line" == *PASS* && "$line" == *"3 baselined, 0 new"* ]]; then
    ok "C5: baselined runners pass"
  else
    bad "C5: expected PASS once the runners are baselined" "$line"
  fi
else
  bad "C5: could not build the git fixture"
fi

# ---- C10: MCP reaches chat verbs through internal/chat, never pfmcall --------

REPO_C10="$T/mcp-pfmcall"
if fixture "$REPO_C10"; then
  mkdir -p "$REPO_C10/internal/mcpserv"
  printf 'package mcpserv\n\nfunc keys() { _ = 0 }\n' > "$REPO_C10/internal/mcpserv/keys.go"
  : > "$REPO_C10/.arch/mcp-argv-calls.txt"
  line=$(check_line "$REPO_C10" C10-mcp-argv)
  if [[ "$line" == *PASS* ]]; then
    ok "C10: an mcpserv with no argv dispatch and no pfmcall use passes"
  else
    bad "C10: expected PASS for a clean mcpserv" "$line"
  fi

  cat > "$REPO_C10/internal/mcpserv/keys.go" <<'GO'
package mcpserv

func keys() error {
	transport, err := pfmcall.New(pfmcall.Dependencies{})
	if err != nil {
		return err
	}
	return transport.SendKey(nil, target, "Enter")
}
GO
  line=$(check_line "$REPO_C10" C10-mcp-argv)
  if [[ "$line" == *FAIL* && "$line" == *"pfmcall:internal/mcpserv/keys.go (new 1)"* ]]; then
    ok "C10: a direct pfmcall engine built inside mcpserv FAILs, counted under pfmcall:"
  else
    bad "C10: expected FAIL counting the planted pfmcall use" "$line"
  fi

  printf 'internal/mcpserv/keys.go 1\n' > "$REPO_C10/.arch/mcp-argv-calls.txt"
  printf 'func run() { backend.dispatch(nil) }\n' >> "$REPO_C10/internal/mcpserv/keys.go"
  line=$(check_line "$REPO_C10" C10-mcp-argv)
  if [[ "$line" == *FAIL* && "$line" == *"pfmcall:internal/mcpserv/keys.go (new 1)"* && "$line" != *" internal/mcpserv/keys.go ("* ]]; then
    ok "C10: an argv count and a pfmcall count of one file are separate keys"
  else
    bad "C10: expected the argv key to pass and the pfmcall key to FAIL" "$line"
  fi

  printf 'internal/mcpserv/keys.go 1\npfmcall:internal/mcpserv/keys.go 1\n' > "$REPO_C10/.arch/mcp-argv-calls.txt"
  line=$(check_line "$REPO_C10" C10-mcp-argv)
  if [[ "$line" == *PASS* ]]; then
    ok "C10: baselined argv and pfmcall counts pass"
  else
    bad "C10: expected PASS once both counts are baselined" "$line"
  fi
else
  bad "C10: could not build the git fixture"
fi

# ---- the concurrent checks: order, exit status, a job that dies ------------

# green_fixture <dir>: a tree on which all 30 checks PASS — the fixture plus what
# C12, C14 and C15 parse, an internal/mcpserv package for C10, a deps registry
# for C29, a jailed test file for C21 and C25, and the C24 script arch-check.sh
# runs from $PFM/scripts (which C30 reads, so <dir> is <root>/pfm), with every
# baseline the --measure of that tree writes.
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
  c29_registry "$dir" "$C29_ROWS" || return 1
  printf "$JAILED_MAIN" > "$dir/internal/loud/loud_test.go"
  env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE PFM="$dir" bash "$SUT" --measure </dev/null >/dev/null 2>&1 || return 1
}

# run_all <dir> [env assignments…]: the whole run's stdout in $out and its exit status in $rc.
run_all() {
  local dir=$1; shift
  out=$(env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE "$@" PFM="$dir" bash "$SUT" </dev/null 2>/dev/null); rc=$?
}

# Today's order: C1 … C23, then C25, C26, C27, C28, C29, C30, then C24 (its own script, last).
EXPECTED_ORDER="C1-ceiling-src C2-ceiling-test C3-cmd-budget C4-cmd-primitives C5-tmux-runner C6-atomic-write C7-sql-open C8-negation-dirs C9-package-doc C10-mcp-argv C11-db-names C12-claude-pointers C13-test-mirror C14-usage-parity C15-internal-usage C16-env-outside-paths C17-dup-functions C18-engine-spellings C19-env-namespace C20-codex-home C21-test-jail C22-host-doors C23-bare-log C25-testmain-jail C26-exec-write C27-identity-env C28-signal-door C29-os-door C30-shell-platform C24-unwrapped-door"

REPO_GREEN="$T/green/pfm"
if green_fixture "$REPO_GREEN"; then
  run_all "$REPO_GREEN"
  order=$(printf '%s\n' "$out" | awk '$1 == "CHECK" {printf "%s ", $2}' | sed 's/ $//')
  passes=$(printf '%s\n' "$out" | grep -c '^CHECK [^ ]* *PASS ')
  if [ "$rc" -eq 0 ] && [ "$passes" -eq 30 ] && [ "$order" = "$EXPECTED_ORDER" ]; then
    ok "exit: a tree where all 30 checks PASS exits 0, one line per check in today's order"
  else
    bad "exit: expected rc 0, 30 PASS lines in order" "rc=$rc passes=$passes" "$order" "$out"
  fi

  sed -i 's/^func runInternal(/func runInternalInput(/' "$REPO_GREEN/cmd/pfm/main.go"
  cat >> "$REPO_GREEN/cmd/pfm/main.go" <<'GO'

func runInternal(args []string) {
    runInternalInput(args)
}
GO
  line=$(check_line "$REPO_GREEN" C15-internal-usage)
  expected=$(printf 'CHECK %-22s %-7s %s' C15-internal-usage PASS '0 baselined, 0 new')
  if [ "$line" = "$expected" ]; then
    ok "C15: usage covers the input dispatcher behind the capture wrapper"
  else
    bad "C15: expected the wrapped dispatcher to pass" "expected: $expected" "actual: $line"
  fi
  cp "$REPO_GREEN/cmd/pfm/main.go" "$T/c15-main.go"

  sed -i 's/usage: pfm internal a|b/usage: pfm internal a|c/' "$REPO_GREEN/cmd/pfm/main.go"
  line=$(check_line "$REPO_GREEN" C15-internal-usage)
  expected=$(printf 'CHECK %-22s %-7s %s' C15-internal-usage FAIL 'new: b ')
  if [ "$line" = "$expected" ]; then
    ok "C15: an undocumented input dispatcher entry fails by name"
  else
    bad "C15: expected the missing usage entry" "expected: $expected" "actual: $line"
  fi

  sed -i 's/args\[0\]/args[1]/g' "$REPO_GREEN/cmd/pfm/main.go"
  line=$(check_line "$REPO_GREEN" C15-internal-usage)
  expected=$(printf 'CHECK %-22s %-7s %s' C15-internal-usage ERROR 'no entries parsed from cmd/pfm/main.go runInternalInput()')
  if [ "$line" = "$expected" ]; then
    ok "C15: an empty input dispatcher reports ERROR"
  else
    bad "C15: expected a loud empty dispatcher error" "expected: $expected" "actual: $line"
  fi

  cp "$T/c15-main.go" "$REPO_GREEN/cmd/pfm/main.go"
  sed -i '/usage: pfm internal/d' "$REPO_GREEN/cmd/pfm/main.go"
  line=$(check_line "$REPO_GREEN" C15-internal-usage)
  expected=$(printf 'CHECK %-22s %-7s %s' C15-internal-usage ERROR "no multi-entry 'usage: pfm internal a|b' line in cmd/pfm/main.go")
  if [ "$line" = "$expected" ]; then
    ok "C15: missing usage reports ERROR"
  else
    bad "C15: expected a loud missing usage error" "expected: $expected" "actual: $line"
  fi
else
  bad "exit: could not build the green fixture"
fi

REPO_FAIL="$T/green-fail/pfm"
if green_fixture "$REPO_FAIL"; then
  printf 'func Again() { log.Fatalf("bye") }\n' >> "$REPO_FAIL/internal/loud/loud.go"
  run_all "$REPO_FAIL"
  others=$(printf '%s\n' "$out" | grep '^CHECK ' | grep -vc 'C23-bare-log .*FAIL ')
  if [ "$rc" -eq 1 ] && [ "$others" -eq 29 ] && [[ "$out" == *"C23-bare-log"*"FAIL"*"internal/loud/loud.go (1->2)"* ]]; then
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

REPO_KILL="$T/green-killed/pfm"
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
  if [ "$rc" -eq 2 ] && [ "$lines" -eq 30 ] && [ "$passes" -eq 29 ] && [[ "$line" == *ERROR* && "$line" == *"without leaving its CHECK line"* ]]; then
    ok "a check job killed before its result is an ERROR line naming it, exit 2, the other 29 checks PASS"
  else
    bad "a killed check job must print ERROR for that check and exit 2" "rc=$rc lines=$lines passes=$passes" "$line"
  fi
else
  bad "a killed check job: could not build the green fixture"
fi

# ---- a baseline entry naming a Go file that is gone is stale, never a "fixed" pass

REPO_STALE="$T/stale-baseline"
if fixture "$REPO_STALE"; then
  printf 'cmd/pfm/main.go\ninternal/loud/loud.go\ninternal/obs/obs.go\n' > "$REPO_STALE/.arch/untested-sources.txt"
  line=$(check_line "$REPO_STALE" C13-test-mirror)
  if [[ "$line" == *PASS* ]]; then
    ok "C13: a baseline naming only existing files passes"
  else
    bad "C13: expected PASS for a correct untested-sources baseline" "$line"
  fi
  echo 'internal/gone/gone.go' >> "$REPO_STALE/.arch/untested-sources.txt"
  line=$(check_line "$REPO_STALE" C13-test-mirror)
  if [[ "$line" == *FAIL* && "$line" == *"stale: internal/gone/gone.go"* ]]; then
    ok "C13: a baseline entry for a deleted source file FAILs naming it"
  else
    bad "C13: expected FAIL naming the stale entry" "$line"
  fi
  printf 'internal/loud/loud.go 1\nhttp:internal/gone/gone.go 2\n' > "$REPO_STALE/.arch/bare-log.txt"
  line=$(check_line "$REPO_STALE" C23-bare-log)
  if [[ "$line" == *FAIL* && "$line" == *"stale: internal/gone/gone.go"* ]]; then
    ok "C23: a counted baseline entry for a deleted file FAILs naming it, a kind prefix stripped"
  else
    bad "C23: expected FAIL naming the stale counted entry" "$line"
  fi
else
  bad "stale baseline: could not build the git fixture"
fi

shtest_end
