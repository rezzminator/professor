#!/usr/bin/env bash
# Exercise .claude/scripts/check-pfm.sh — pfm's static-check command — against a fake repository:
# its usage text, the resolver's FAIL path lines, the offline install check (module cache only, go's
# output in the log), the SKIP wording, the files fmt receives and the formatter it picks (the pinned
# one tools.sh names, else PATH), the unit-wide typecheck (go vet over the whole module, on every
# selection, a deletion-only one included), lint-new scoped to the named files (a lint finding on
# another file is burn-down, a typecheck finding on any file or a tool error with no finding is a
# FAIL), the check order, every check running after one fails, the exit code, the log, and the host's fence verdicts (the docker probe after a fence
# failure, a fence run with no check line, a PASS followed by a non-zero exit). dev.sh, go, make,
# golangci-lint and docker are stubs in the fake root that record their argv and exit as the test
# tells them; `iso run` executes its command string with PFM_DEV_FENCE=1 like the real fence.
#   bash scripts/test-check-pfm.sh [path to check-pfm.sh]
# BROKEN STATE: a missing check-pfm.sh fails every case (the copy error is printed), never 0 failed.
set -euo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${1:-$HERE/.claude/scripts/check-pfm.sh}"
SHTEST_TAG=test-check-pfm
# shellcheck source=scripts/shtest.sh
source "$HERE/scripts/shtest.sh"
shtest_isolate_host

NAME="cpfm$$"
# shellcheck source=scripts/stub-root.sh
source "$HERE/scripts/stub-root.sh"
for s in go make golangci-lint; do ln -s .stub "$BIN/$s"; done
# The SUT runs with PATH=$BIN alone: the host's go, make, docker and golangci-lint are never reachable;
# docker exists only while a test links it.
for t in bash env sh git realpath dirname basename date mkdir cat grep sed awk tail head rm wc tr sort mktemp cp chmod ls cut uniq tee; do
  p="$(command -v "$t")" || continue
  ln -s "$p" "$BIN/$t"
done

cp "$SUT" "$R/.claude/scripts/check-pfm.sh" || true
cp "$HERE/.claude/scripts/unit-path.sh" "$R/.claude/scripts/unit-path.sh"
chmod +x "$R/.claude/scripts/check-pfm.sh" 2>/dev/null || true

# --- fixture repository ---------------------------------------------------------------------------
mkdir -p "$R/pfm/internal/a" "$R/pfm/docs" "$R/docs"
printf 'package a\n' >"$R/pfm/internal/a/a.go"
printf 'package a\n' >"$R/pfm/internal/a/a_test.go"
printf 'package a\n' >"$R/pfm/internal/a/gone.go"
echo x >"$R/pfm/docs/x.md"
echo x >"$R/docs/x.md"
git -C "$R" init -q
git -C "$R" add -A
git -C "$R" -c user.name=t -c user.email=t@t commit -qm base
rm "$R/pfm/internal/a/gone.go"
R="$(cd "$R" && pwd -P)"

# --- harness ----------------------------------------------------------------------------------------
rule() { printf '%s\001%s\001%s\001%s\n' "$1" "$2" "$3" "${4-}" >>"$RULES"; } # rule <tool> <argv substring> <rc> [output]
reset() { : >"$RULES"; }
docker_on() { ln -sf .stub "$BIN/docker"; }
docker_off() { rm -f "$BIN/docker"; }
run() { # run <cwd> <arg>...: OUT holds stdout+stderr, RC the exit code
  local cwd="$1"; shift
  : >"$REC"; : >"$REC.env"
  OUT="$(cd "$cwd" && env -u PFM_DEV_FENCE PATH="$BIN" REC="$REC" RULES="$RULES" STUBLIB="$STUBLIB" \
    "$R/.claude/scripts/check-pfm.sh" "$@" 2>&1)" && RC=0 || RC=$?
}
run_fence() { # run_fence <arg>...: the fence-mode run, inside the fake root
  : >"$REC"; : >"$REC.env"
  OUT="$(cd "$R" && env PFM_DEV_FENCE=1 PATH="$BIN" REC="$REC" RULES="$RULES" STUBLIB="$STUBLIB" \
    "$R/.claude/scripts/check-pfm.sh" "$@" 2>&1)" && RC=0 || RC=$?
}
expect() { # expect <name> <rc> <line that must be in OUT>
  if [ "$RC" = "$2" ] && grep -qxF -- "$3" <<<"$OUT"; then ok "$1"; else bad "$1" "rc=$RC want $2, want line: $3" "$OUT"; fi
}
rc_is() { if [ "$RC" = "$2" ]; then ok "$1"; else bad "$1" "rc=$RC want $2" "$OUT"; fi; }
out_has() { if grep -qxF -- "$2" <<<"$OUT"; then ok "$1"; else bad "$1" "no output line: $2" "$OUT"; fi; }
out_lacks() { if grep -qxF -- "$2" <<<"$OUT"; then bad "$1" "unexpected output line: $2" "$OUT"; else ok "$1"; fi; }
verdicts() { grep -E '^(PASS|FAIL|SKIP) ' <<<"$OUT" || true; }
rec_has() { # rec_has <name> <tool> <arg>...
  local name="$1" want
  shift; want="$1$(printf ' %q' "${@:2}")"
  if grep -qxF -- "$want" "$REC"; then ok "$name"; else bad "$name" "call not recorded: $want" "$(cat "$REC")"; fi
}
rec_lacks() { # rec_lacks <name> <tool-prefix>
  if grep -q "^$2" "$REC"; then bad "$1" "recorded: $2" "$(cat "$REC")"; else ok "$1"; fi
}

A=pfm/internal/a/a.go
U='Name the files you changed.'
LINT_NEW=(make -C pfm --no-print-directory lint-new)
ARCH=(make -C pfm --no-print-directory arch)
TYPECHECK=(go -C pfm vet ./...)
LAUNCH=(go -C pfm test ./internal/claudelaunch/ -run TestNoLaunchLiteralOutsideRegistry -count=1)
INSTALL=(go -C pfm mod download)
INSTALL_FAIL='FAIL install (pfm has no installed dependencies — run: .claude/scripts/dev.sh iso install pfm)'

# --- the usage rule -------------------------------------------------------------------------------
run "$R"
expect "no argument prints the usage line, exit 2" 2 "$U"
for a in '' -x --all; do
  run "$R" "$a"
  expect "argument '$a' prints the usage line, exit 2" 2 "$U"
done
run "$R" "$A" ''
expect "an empty argument beside a file prints the usage line, exit 2" 2 "$U"
rec_lacks "a usage exit never reaches dev.sh" dev.sh

# --- the green run: one fence call, one line per check -----------------------------------------------
docker_off; reset
run "$R" "$A"
rc_is "all checks green exit 0" 0
for c in fmt typecheck lint-new arch launch-literals; do out_has "green run prints PASS $c" "PASS $c"; done
out_lacks "the whole-module vet is named typecheck, never vet" "PASS vet"
if [ "$(verdicts | cut -d' ' -f2 | tr '\n' ' ')" = 'fmt typecheck lint-new arch launch-literals ' ]; then ok "the checks run in the order fmt, typecheck, lint-new, arch, launch-literals"; else bad "the checks run in the order fmt, typecheck, lint-new, arch, launch-literals" "$(verdicts)"; fi
rec_has "typecheck is go vet over the whole module" "${TYPECHECK[@]}"
if [ "$(grep -c '^dev.sh iso run ' "$REC")" = 1 ]; then ok "the host re-runs itself in one fence call"; else bad "the host re-runs itself in one fence call" "$(cat "$REC")"; fi
rec_has "install is the module-cache download" "${INSTALL[@]}"
rec_lacks "install never loads the tree's packages" "go -C pfm list"
if grep -qE 'GOPROXY=off -C pfm mod download$' "$REC.env"; then ok "the install check runs offline"; else bad "the install check runs offline" "$(cat "$REC.env")"; fi
if grep -q -- '-mod=mod' "$REC.env"; then bad "the install check sets no -mod flag (it has no effect on mod download)" "$(cat "$REC.env")"; else ok "the install check sets no -mod flag (it has no effect on mod download)"; fi
rec_has "lint-new runs the make target, whole" "${LINT_NEW[@]}"
rec_has "arch runs the make target, whole" "${ARCH[@]}"
rec_has "launch-literals runs its one test, whole" "${LAUNCH[@]}"
rec_has "fmt gets the named go file relative to pfm" golangci-lint fmt --diff internal/a/a.go
rec_lacks "the docker probe is never called on a green run" docker
if grep -q '^log: /tmp/'"$NAME"'/check-pfm/[0-9]\{8\}T[0-9]\{6\}Z-[0-9]\+\.log$' <<<"$OUT"; then ok "the log path is printed under /tmp/{project}/check-pfm"; else bad "the log path is printed under /tmp/{project}/check-pfm" "$OUT"; fi

# --- the formatter: the pinned one tools.sh names, else PATH ------------------------------------------------
mkdir -p "$R/infra/fence" "$R/pinbin"
# shellcheck disable=SC2016 # the fixtures' bodies are literal: $PINBIN, $* and $REC expand when they run
printf '#!/usr/bin/env bash\ncase "$1" in --print-bin) echo "$PINBIN" ;; *) echo "tools.sh stub: refused${*:+ $*}" >&2; exit 3 ;; esac\n' >"$R/infra/fence/tools.sh"
# shellcheck disable=SC2016 # as above
printf '#!/usr/bin/env bash\necho "pinned-lint $*" >>"$REC"\n' >"$R/pinbin/golangci-lint"
chmod +x "$R/pinbin/golangci-lint"
reset; : >"$REC"
OUT="$(cd "$R" && env PFM_DEV_FENCE=1 PINBIN="$R/pinbin" PATH="$BIN" REC="$REC" RULES="$RULES" STUBLIB="$STUBLIB" "$R/.claude/scripts/check-pfm.sh" "$A" 2>&1)" && RC=0 || RC=$?
if grep -qxF 'pinned-lint fmt --diff internal/a/a.go' "$REC"; then ok "fmt runs the pinned formatter tools.sh --print-bin names"; else bad "fmt runs the pinned formatter tools.sh --print-bin names" "$(cat "$REC")"; fi
rec_lacks "the pinned formatter wins over the one on PATH" golangci-lint
OUT="$(cd "$R" && env PFM_DEV_FENCE=1 PINBIN="$R/nowhere" PATH="$BIN" REC="$REC" RULES="$RULES" STUBLIB="$STUBLIB" "$R/.claude/scripts/check-pfm.sh" "$A" 2>&1)" && RC=0 || RC=$?
rec_has "no pinned formatter in the printed dir: fmt falls back to PATH" golangci-lint fmt --diff internal/a/a.go
rm -f "$BIN/golangci-lint"
: >"$REC"
OUT="$(cd "$R" && env PFM_DEV_FENCE=1 PINBIN="$R/nowhere" PATH="$BIN" REC="$REC" RULES="$RULES" STUBLIB="$STUBLIB" "$R/.claude/scripts/check-pfm.sh" "$A" 2>&1)" && RC=0 || RC=$?
out_has "no formatter anywhere is FAIL fmt, never a PASS" "FAIL fmt"
out_has "no formatter anywhere names the missing toolchain" "  | LINT: TOOLCHAIN-MISSING — run 'make tools' (infra/fence/tools.env)"
ln -s .stub "$BIN/golangci-lint"
rm -rf "$R/infra" "$R/pinbin"

# --- path spellings reach fmt resolved ------------------------------------------------------------------
run "$R" internal/a/a.go
rec_has "a unit-relative path reaches fmt resolved" golangci-lint fmt --diff internal/a/a.go
run "$R/pfm" internal/a/a.go
rec_has "a cwd-relative path reaches fmt resolved" golangci-lint fmt --diff internal/a/a.go
run "$R/pfm/internal" "$R/pfm/internal/a/a.go"
rec_has "an absolute path reaches fmt resolved" golangci-lint fmt --diff internal/a/a.go
run "$R" docs/x.md
expect "a spelling that resolves to two files exits 2 naming both" 2 "check-pfm.sh: docs/x.md resolves to two files: docs/x.md and pfm/docs/x.md"
run "$R" pfm/internal/a/gone.go "$A"
rc_is "a file deleted at HEAD drops silently beside a live one" 0
out_lacks "a dropped deletion prints no FAIL path line" "FAIL path pfm/internal/a/gone.go (no such file in pfm)"

# --- FAIL path lines, kept ------------------------------------------------------------------------------
run "$R" pfm/internal/a/nope.go "$A"
out_has "a missing file prints FAIL path" "FAIL path pfm/internal/a/nope.go (no such file in pfm)"
rc_is "a FAIL path line exits 1 though every check is green" 1
out_has "the live file is still checked beside a FAIL path" "PASS lint-new"
run "$R" pfm/internal/a/nope.go
expect "every named file a FAIL path: exit 1 with the line" 1 "FAIL path pfm/internal/a/nope.go (no such file in pfm)"
rec_lacks "every named file a FAIL path: the fence is skipped" dev.sh

# --- SKIP and fmt's files ---------------------------------------------------------------------------------
run "$R" pfm/docs/x.md
out_has "no named go file prints exactly the SKIP fmt line" "SKIP fmt (no named file it applies to)"
out_lacks "no named go file never prints PASS fmt" "PASS fmt"
rec_lacks "no named go file never calls the formatter" golangci-lint
out_has "no named go file prints exactly the SKIP lint-new line" "SKIP lint-new (no named file it applies to)"
out_lacks "no named go file never prints PASS lint-new" "PASS lint-new"
out_lacks "no named go file never prints FAIL lint-new" "FAIL lint-new"
rec_lacks "no named go file never runs lint-new" "make -C pfm --no-print-directory lint-new"
for c in typecheck arch launch-literals; do out_has "no named go file still runs $c" "PASS $c"; done
rc_is "a SKIP is no failure" 0
reset; rule make lint-new 1 'internal/a/a.go:3:1: finding on a file nobody named here (lll)'
run "$R" pfm/docs/x.md
out_has "a lint finding never fails a selection with no go file" "SKIP lint-new (no named file it applies to)"
rc_is "no go file named: lint findings elsewhere exit 0" 0
reset
run "$R" "$A" pfm/docs/x.md pfm/internal/a/a_test.go
rec_has "fmt gets only the named go files, in one call" golangci-lint fmt --diff internal/a/a.go internal/a/a_test.go

# --- the fmt verdict ---------------------------------------------------------------------------------------
reset; rule golangci-lint fmt 0 'diff a/internal/a/a.go b/internal/a/a.go'
run "$R" "$A"
out_has "a formatter diff is FAIL fmt" "FAIL fmt"
rc_is "a formatter diff exits 1" 1
reset; rule golangci-lint fmt 3
run "$R" "$A"
out_has "a formatter that exits non-zero is FAIL fmt" "FAIL fmt"

# --- typecheck: the whole module, on every selection --------------------------------------------------------
reset; rule go ' vet ' 1 'internal/b/b.go:5:25: undefined: a.F'
run "$R" "$A"
out_has "a go vet failure anywhere is FAIL typecheck" "FAIL typecheck"
rc_is "a FAIL typecheck exits 1" 1
for c in fmt lint-new arch launch-literals; do out_has "after typecheck fails, $c still runs" "PASS $c"; done
out_lacks "a tree code error is no install FAIL" "$INSTALL_FAIL"
run "$R" pfm/docs/x.md
out_has "a failing typecheck is FAIL even when no go file is named" "FAIL typecheck"
run "$R" pfm/internal/a/gone.go
rc_is "a deletion-only selection still runs typecheck: a break it causes is red" 1
out_has "a deletion-only selection prints FAIL typecheck" "FAIL typecheck"
reset
run "$R" pfm/internal/a/gone.go
rc_is "a deletion-only selection with a green tree exits 0" 0
out_has "a deletion-only selection prints PASS typecheck" "PASS typecheck"
out_has "a deletion-only selection skips fmt" "SKIP fmt (no named file it applies to)"
out_has "a deletion-only selection skips lint-new" "SKIP lint-new (no named file it applies to)"
rec_has "a deletion-only selection runs go vet over the module" "${TYPECHECK[@]}"

# --- lint-new reports only the named files ------------------------------------------------------------------
reset; rule make lint-new 1 'internal/a/a.go:3:1: a finding on the named file (lll)'
run "$R" "$A"
out_has "a finding on the named file (pfm-relative form) is FAIL lint-new" "FAIL lint-new"
rc_is "a finding on the named file exits 1" 1
reset; rule make lint-new 1 'pfm/internal/a/a.go:3:1: a finding on the named file (lll)'
run "$R" "$A"
out_has "a finding on the named file (repository-relative form) is FAIL lint-new" "FAIL lint-new"
reset; rule make lint-new 1 "$R/pfm/internal/a/a.go:3:1: a finding on the named file (lll)"
run "$R" "$A"
out_has "a finding on the named file (absolute form) is FAIL lint-new" "FAIL lint-new"
reset; rule make lint-new 1 'internal/a/a.go:3:1: finding on the named file (lll)\ninternal/b/b.go:9:9: finding elsewhere (lll)'
run "$R" "$A"
out_has "a finding on the named file beside one elsewhere is FAIL lint-new" "FAIL lint-new"
reset; rule make lint-new 1 'internal/b/b.go:9:9: a finding on a file nobody named (lll)\ninternal/a/aa.go:1:1: a longer name sharing the prefix (lll)'
run "$R" "$A"
out_has "findings only on other files are burn-down: PASS lint-new" "PASS lint-new"
out_lacks "burn-down is not FAIL lint-new" "FAIL lint-new"
rc_is "burn-down findings exit 0" 0
LOG="$(sed -n 's/^log: //p' <<<"$OUT")"
if [ -f "$LOG" ] && grep -q 'internal/b/b.go:9:9' "$LOG"; then ok "burn-down findings are listed in the log"; else bad "burn-down findings are listed in the log" "$LOG"; fi
reset; rule make lint-new 1 'internal/b/b.go:5:25: undefined: a.F (typecheck)'
run "$R" "$A"
out_has "a typecheck finding on a file nobody named is FAIL lint-new" "FAIL lint-new"
rc_is "a typecheck finding on an unnamed file exits 1" 1
out_lacks "a typecheck finding on an unnamed file is no burn-down PASS" "PASS lint-new"
reset; rule make lint-new 1 'internal/b/b.go:5:25: undefined: a.F (typecheck)\ninternal/c/c.go:1:1: a lint finding elsewhere (lll)'
run "$R" "$A"
out_has "a typecheck finding beside burn-down findings is FAIL lint-new" "FAIL lint-new"
reset; rule make lint-new 1 'internal/a/a.go:3:1: finding on the named file (typecheck)'
run "$R" "$A"
out_has "a typecheck finding on the named file is FAIL lint-new" "FAIL lint-new"
reset; rule make lint-new 2 'Error: can'\''t load config: no such file'
run "$R" "$A"
out_has "a tool error with no finding line is FAIL lint-new" "FAIL lint-new"
rc_is "a lint-new tool error exits 1" 1
out_lacks "a lint-new tool error is never PASS" "PASS lint-new"
LOG="$(sed -n 's/^log: //p' <<<"$OUT")"
if [ -f "$LOG" ] && grep -q "can't load config" "$LOG"; then ok "a lint-new tool error's cause is in the log"; else bad "a lint-new tool error's cause is in the log" "$LOG"; fi
reset; rule make lint-new 1
run "$R" "$A"
out_has "a non-zero lint-new with no output at all is FAIL lint-new" "FAIL lint-new"
reset

# --- every check runs after one fails ---------------------------------------------------------------------
reset; rule make lint-new 1 'internal/a/a.go:1:1: FAIL fake tool line (lll)'
run "$R" "$A"
out_has "a failing lint-new prints FAIL lint-new" "FAIL lint-new"
for c in fmt arch launch-literals; do out_has "after lint-new fails, $c still runs" "PASS $c"; done
rc_is "any FAIL line exits 1" 1
out_lacks "a tool's own output never counts as a verdict line" "FAIL fake tool line"
if [ "$(verdicts | grep -c '^FAIL ')" = 1 ]; then ok "one FAIL line for one failed check"; else bad "one FAIL line for one failed check" "$OUT"; fi
rec_lacks "a check failure never calls the docker probe" docker
LOG="$(sed -n 's/^log: //p' <<<"$OUT")"
if [ -f "$LOG" ] && grep -q 'FAIL fake tool line' "$LOG"; then ok "the tool's full output is in the log"; else bad "the tool's full output is in the log" "$LOG"; fi
reset; rule make 'arch' 1
run "$R" "$A"
out_has "a failing arch prints FAIL arch" "FAIL arch"
out_has "after arch fails, lint-new still runs" "PASS lint-new"
reset; rule go 'claudelaunch' 1
run "$R" "$A"
out_has "a failing launch test prints FAIL launch-literals" "FAIL launch-literals"
out_has "after launch-literals fails, arch still runs" "PASS arch"
reset

# --- the offline install check ----------------------------------------------------------------------------
rule go 'mod download' 1 'go: example.test/dep@v1.0.0: module lookup disabled by GOPROXY=off'
run "$R" "$A"
expect "an uninstalled unit prints the install FAIL line, exit 1" 1 "$INSTALL_FAIL"
if [ "$(verdicts)" = "$INSTALL_FAIL" ]; then ok "the install FAIL line is the only verdict line"; else bad "the install FAIL line is the only verdict line" "$(verdicts)"; fi
LOG="$(sed -n 's/^log: //p' <<<"$OUT")"
if [ -f "$LOG" ] && grep -q 'module lookup disabled' "$LOG"; then ok "go's own output is in the log after an install FAIL"; else bad "go's own output is in the log after an install FAIL" "$LOG"; fi
rec_lacks "no make check runs after an install FAIL" make
rec_lacks "no formatter runs after an install FAIL" golangci-lint
if [ "$(grep -c '^go ' "$REC")" = 1 ]; then ok "no go check runs after an install FAIL"; else bad "no go check runs after an install FAIL" "$(cat "$REC")"; fi
reset
rule go ' vet ' 1 'pfm/x.go:1:1: undefined: y'
run "$R" "$A"
out_lacks "a code error in the tree is no install FAIL" "$INSTALL_FAIL"
out_has "a code error in the tree is FAIL typecheck" "FAIL typecheck"
out_has "a code error in the tree still runs every other check" "PASS arch"
reset

# --- the docker probe, only after the fence failed before any check -----------------------------------------
docker_on; rule dev.sh 'iso run' 4
run "$R" "$A"
expect "a daemon that is up: the fence died before any check" 1 "FAIL fence (dev.sh iso run exited 4 before any check ran — see the log)"
rec_has "the probe asks the daemon" docker info
rule docker info 1
run "$R" "$A"
expect "a daemon that is down is named, with the command that starts it" 1 "FAIL fence (docker daemon not reachable — start the Docker daemon, then rerun .claude/scripts/check-pfm.sh)"
docker_off
run "$R" "$A"
expect "no docker is named as a failed probe" 1 "FAIL fence (probe failed: docker not found)"
reset; rule dev.sh 'iso run' 0 'noise, no verdict'
run "$R" "$A"
expect "a fence run that exits 0 with no check line is a FAIL fence" 1 "FAIL fence (dev.sh iso run printed no check line — see the log)"
out_lacks "a fence run with no check line is never a PASS" "PASS fmt"
reset; rule dev.sh 'iso run' 4 'PASS fmt'
run "$R" "$A"
expect "a PASS line then a non-zero fence exit is a FAIL fence" 1 "FAIL fence (dev.sh iso run exited 4 after 1 check line(s) — see the log)"
reset

# --- fence mode -------------------------------------------------------------------------------------------------
run_fence "$A"
rc_is "fence mode runs the checks itself, exit 0 when green" 0
out_has "fence mode prints PASS lint-new" "PASS lint-new"
rec_lacks "fence mode never re-enters the fence" dev.sh

shtest_end
