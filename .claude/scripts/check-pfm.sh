#!/usr/bin/env bash
# check-pfm.sh — pfm's static-check command: name the files you changed, run it once at the end.
#   check-pfm.sh <file>...
# The pinned Go tools live in the fence image, not on the host: on the host this resolves the files
# (unit-path.sh; a bad one is a `FAIL path` line), re-runs itself in one fence container
# (`dev.sh iso run`, PFM_DEV_FENCE set there), prints that run's PASS/FAIL/SKIP lines and the log
# path /tmp/{project}/check-pfm/{UTC timestamp}-{pid}.log. In the fence it checks, offline:
# install (`go mod download` from the module cache alone — a code error in the tree cannot trip it;
# its output goes to the log), then every one of: fmt (golangci-lint fmt --diff on the named .go
# files; the formatter is the pinned one `infra/fence/tools.sh --print-bin` names, else the one on
# PATH), typecheck (`go vet ./...` over the whole pfm module — it type-checks every package and its
# tests, so a change that breaks a file nobody named, or a deleted .go file, reads FAIL; it runs on
# every selection, a deletion-only one included), lint-new (make lint-new over the named .go files:
# FAIL when a lint finding line names one of them, or when any finding line on ANY file ends
# `(typecheck)`; lint findings on other files are burn-down, listed in the log, a PASS; a non-zero
# exit with no finding line at all is a tool error, FAIL; SKIP when no .go file is named), arch
# (make arch), launch-literals (the claudelaunch registry test), in that order. Every check runs even
# after one fails; exit 1 when any line is FAIL,
# else 0. Landing checks the owner places, never here:
# make prompts (regenerate-and-compare), dev.sh build pfm, the darwin vet, dev.sh iso gate pfm.
# When the fence call dies before any check ran, only then is docker probed to name why.
# BROKEN STATE: a missing unit-path.sh or dev.sh errors on source/exec; a fence that never starts
# prints a `FAIL fence (...)` line naming the cause (never a PASS) and exits 1; a fence call that
# dies mid-run or prints no check line is a FAIL fence line too.
set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
usage() { echo 'Name the files you changed.' >&2; exit 2; }
# shellcheck source=.claude/scripts/unit-path.sh
source "$ROOT/.claude/scripts/unit-path.sh"
DEV="$ROOT/.claude/scripts/dev.sh"

files=(); path_rc=0
if [ -n "${PFM_DEV_FENCE:-}" ] && [ "$#" = 0 ]; then
  : # the host's fence call with every named file a deletion: the unit-wide checks still run
else
  [ "$#" -gt 0 ] || usage
  unit_paths check pfm "$@"
  files=("${UNIT_PATHS[@]}"); path_rc="$UNIT_PATHS_RC"
fi

if [ -z "${PFM_DEV_FENCE:-}" ]; then
  # --- host: re-run in the fence, print its verdict lines ---------------------------------------
  if [ "${#files[@]}" = 0 ] && [ "$path_rc" != 0 ]; then exit 1; fi
  cmd="$(printf '%q ' ./.claude/scripts/check-pfm.sh "${files[@]}")"
  unit_log check-pfm || { echo "check-pfm.sh: the log directory could not be created" >&2; exit 1; }
  rc=0
  "$DEV" iso run "$cmd" >"$UNIT_LOG" 2>&1 || rc=$?
  lines="$(grep -E '^(PASS|FAIL|SKIP) ' "$UNIT_LOG" || true)"
  [ -z "$lines" ] || echo "$lines"
  extra=
  if [ -z "$lines" ] && [ "$rc" != 0 ]; then
    if ! command -v docker >/dev/null 2>&1; then
      extra='FAIL fence (probe failed: docker not found)'
    elif ! docker info >/dev/null 2>&1; then
      extra='FAIL fence (docker daemon not reachable — start the Docker daemon, then rerun .claude/scripts/check-pfm.sh)'
    else
      extra="FAIL fence (dev.sh iso run exited $rc before any check ran — see the log)"
    fi
  elif [ -z "$lines" ]; then
    extra='FAIL fence (dev.sh iso run printed no check line — see the log)'
  elif [ "$rc" != 0 ] && ! grep -q '^FAIL ' <<<"$lines"; then
    extra="FAIL fence (dev.sh iso run exited $rc after $(grep -c '' <<<"$lines") check line(s) — see the log)"
  fi
  [ -z "$extra" ] || echo "$extra"
  echo "log: $UNIT_LOG"
  if [ "$path_rc" != 0 ] || [ -n "$extra" ] || grep -q '^FAIL ' <<<"$lines"; then exit 1; fi
  exit 0
fi

# --- fence: the checks, offline, from the worktree root ---------------------------------------------
cd "$ROOT"
inst_rc=0
inst_out="$(GOPROXY=off go -C pfm mod download 2>&1)" || inst_rc=$?
if [ -n "$inst_out" ]; then while IFS= read -r l; do printf '  | %s\n' "$l"; done <<<"$inst_out"; fi
if [ "$inst_rc" != 0 ]; then
  echo 'FAIL install (pfm has no installed dependencies — run: .claude/scripts/dev.sh iso install pfm)'
  exit 1
fi
failed=0
verdict() { # verdict <check> <rc> <output>: the tool's output indented, then the one verdict line
  local l
  if [ -n "$3" ]; then while IFS= read -r l; do printf '  | %s\n' "$l"; done <<<"$3"; fi
  if [ "$2" = 0 ]; then echo "PASS $1"; else echo "FAIL $1"; failed=1; fi
}
check() { # check <name> <command>...
  local name="$1" out rc=0; shift
  out="$("$@" 2>&1)" || rc=$?
  verdict "$name" "$rc" "$out"
}

gofiles=()
for f in "${files[@]}"; do
  case "$f" in pfm/*.go) gofiles+=("${f#pfm/}") ;; esac
done
if [ "${#gofiles[@]}" = 0 ]; then
  echo 'SKIP fmt (no named file it applies to)'
else
  # The formatter `make -C pfm fmt-check` uses: the pinned binary in the dir tools.sh names, else the
  # one on PATH.
  tools_rc=0; tools_out="$(bash "$ROOT/infra/fence/tools.sh" --print-bin 2>&1)" || tools_rc=$?
  lint=
  if [ "$tools_rc" = 0 ] && [ -x "$tools_out/golangci-lint" ]; then lint="$tools_out/golangci-lint"; fi
  [ -n "$lint" ] || lint="$(command -v golangci-lint || true)"
  if [ -z "$lint" ]; then
    msg="LINT: TOOLCHAIN-MISSING — run 'make tools' (infra/fence/tools.env)"
    if [ "$tools_rc" != 0 ]; then msg+="
tools.sh --print-bin exited $tools_rc: $tools_out"; fi
    verdict fmt 1 "$msg"
  else
    out=; rc=0
    out="$(cd pfm && "$lint" fmt --diff "${gofiles[@]}" 2>&1)" || rc=$?
    if [ -n "$out" ] && [ "$rc" = 0 ]; then rc=1; fi
    verdict fmt "$rc" "$out"
  fi
fi
# The whole module, on every selection: it compiles what nobody named, and what a deletion left behind.
check typecheck go -C pfm vet ./...
if [ "${#gofiles[@]}" = 0 ]; then
  echo 'SKIP lint-new (no named file it applies to)'
else
  # golangci-lint prints `path/to/file.go:LINE:COL: message (linter)`, relative to pfm (its cwd under
  # make -C pfm); the repository-relative and absolute spellings are accepted too.
  rc=0
  out="$(make -C pfm --no-print-directory lint-new 2>&1)" || rc=$?
  hit=0; seen=0; burn=()
  while IFS= read -r l; do
    [[ "$l" =~ ^([^[:space:]:]+):[0-9]+(:[0-9]+)?:\  ]] || continue
    seen=$((seen + 1)); path="${BASH_REMATCH[1]}"
    if [[ "$l" =~ \(typecheck\)[[:space:]]*$ ]]; then hit=1; continue; fi # a compile error anywhere is yours
    path="${path#"$ROOT"/}"; path="${path#./}"; path="${path#pfm/}"
    named=0
    for g in "${gofiles[@]}"; do if [ "$path" = "$g" ]; then named=1; fi; done
    if [ "$named" = 1 ]; then hit=1; else burn+=("$l"); fi
  done <<<"$out"
  if [ "$rc" = 0 ]; then
    verdict lint-new 0 "$out"
  elif [ "$seen" = 0 ]; then
    verdict lint-new "$rc" "lint-new exited $rc with no finding line — a tool or config error, not a lint verdict:
$out"
  elif [ "$hit" = 1 ]; then
    verdict lint-new "$rc" "$out"
  else
    verdict lint-new 0 "$out
burn-down, not your red: ${#burn[@]} finding(s), none on a file you named"
  fi
fi
check arch make -C pfm --no-print-directory arch
check launch-literals go -C pfm test ./internal/claudelaunch/ -run TestNoLaunchLiteralOutsideRegistry -count=1
exit "$failed"
