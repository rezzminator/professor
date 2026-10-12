#!/usr/bin/env bash
# check-pfm.sh — pfm's static-check command: name the files you changed, run it once at the end.
#   check-pfm.sh <file>...
# The pinned Go tools live in the fence image, not on the host: on the host this resolves the files
# (unit-path.sh; a bad one is a `FAIL path` line), takes the host lock
# /tmp/{project}/check-pfm/fence.lock (one run in the fence at a time across every worktree; a run
# that waits says so on stderr), re-runs itself in one fence container (`dev.sh iso run`; a TERM or
# INT to this script terminates that run's process tree, which removes its container, exit 143/130,
# PFM_DEV_FENCE set there), prints that run's GAP/PASS/FAIL/SKIP lines and the log
# path /tmp/{project}/check-pfm/{UTC timestamp}-{pid}.log. In the fence it checks, offline:
# install — every tool a check runs (go, make, and golangci-lint when a .go file is named: the pinned
# one `infra/fence/tools.sh --print-bin` names, else the one on PATH), then the module cache (`go mod
# download` from the cache alone — a code error in the tree cannot trip it; its output goes to the log).
# Not installed: only `GAP install (pfm is missing: <each missing tool> ...)`, the Go modules among them
# when go's offline download names a module it cannot look up, no check run, exit 3 — a missing tool
# is a gap in this machine, never a failed check (exit 1); any other download failure is `FAIL
# install`, exit 1. Then every one of: fmt (golangci-lint fmt --diff on the named .go files), typecheck (`go vet` over the named .go files' packages plus every module package importing
# one of them directly, test imports included — so an API change that breaks an importer nobody named
# reads FAIL; with no named .go file, a deletion-only selection included, `go vet ./...` over the
# whole module), lint-new (make lint-new LINT_PKGS={the named .go files' packages}:
# FAIL when a lint finding line names one of them, or when any finding line on ANY file ends
# `(typecheck)`; lint findings on other files are burn-down, listed in the log, a PASS; a non-zero
# exit with no finding line at all is a tool error, FAIL; SKIP when no .go file is named), arch
# (make arch), launch-literals (the claudelaunch registry test), in that order. Every check runs even
# after one fails; exit 1 when any line is FAIL, else 3 on the GAP line, else 0. Landing checks the owner places, never here:
# make prompts (regenerate-and-compare), dev.sh build pfm, the darwin vet, dev.sh iso gate pfm.
# When the fence call dies before any check ran, only then is docker probed to name why.
# BROKEN STATE: a missing tool is `GAP install` naming it, exit 3, never a PASS or a bare FAIL; a
# missing unit-path.sh or dev.sh errors on source/exec; a fence that never starts
# prints a `FAIL fence (...)` line naming the cause (never a PASS) and exits 1; a fence call that
# dies mid-run or prints no check line is a FAIL fence line too; a host lock that cannot be taken
# (no flock or perl, an unopenable lock file) is a FAIL fence line before any fence call; a go list
# that fails is FAIL typecheck with its output, never a vet of the named packages alone.
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
  # One fence run at a time across every worktree of this repository: the lock sits beside the logs,
  # under the main checkout's /tmp/{project}; only this process holds fd 9 (the fence call gets it
  # closed), so the kernel drops the lock the moment this process exits or is killed.
  lock="${UNIT_LOG%/*}/fence.lock"
  exec 9>"$lock" || { echo "check-pfm.sh: the lock file $lock could not be opened" >&2; exit 1; }
  fence_lock() { # fence_lock <try|wait>: flock fd 9; 1 = held by another run, other non-zero = no lock
    if command -v flock >/dev/null 2>&1; then
      if [ "$1" = try ]; then flock -n 9; else flock 9; fi
    elif command -v perl >/dev/null 2>&1; then
      perl -MFcntl=:flock -e 'open(my $fh, ">&=", 9) or die "fd 9: $!\n";
        flock($fh, LOCK_EX | ($ARGV[0] eq "try" ? LOCK_NB : 0)) and exit 0; $!{EWOULDBLOCK} ? exit 1 : die "flock: $!\n"' "$1"
    else
      echo "check-pfm.sh: neither flock nor perl is on PATH to take $lock" >&2; return 3
    fi
  }
  lrc=0; fence_lock try || lrc=$?
  if [ "$lrc" = 1 ]; then
    echo "check-pfm.sh: waiting for another check-pfm.sh run to leave the fence ($lock)" >&2
    lrc=0; fence_lock wait || lrc=$?
  fi
  if [ "$lrc" != 0 ]; then echo "FAIL fence (the host lock $lock could not be taken, exit $lrc)"; echo "log: $UNIT_LOG"; exit 1; fi
  # A TERM or INT to this run (a caller's timeout, Ctrl-C) takes its fence run down with it: TERM to
  # the dev.sh → docker compose tree stops and removes the container. A SIGKILL cannot be caught.
  kill_tree() { local c; for c in $(pgrep -P "$1" 2>/dev/null); do kill_tree "$c"; done; kill -TERM "$1" 2>/dev/null || true; }
  rc=0
  "$DEV" iso run "$cmd" >"$UNIT_LOG" 2>&1 9>&- &
  fence_pid=$!
  trap 'kill_tree "$fence_pid"; wait "$fence_pid" 2>/dev/null; exit 143' TERM
  trap 'kill_tree "$fence_pid"; wait "$fence_pid" 2>/dev/null; exit 130' INT
  wait "$fence_pid" || rc=$?
  trap - TERM INT
  lines="$(grep -E '^(GAP|PASS|FAIL|SKIP) ' "$UNIT_LOG" || true)"
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
  elif [ "$rc" != 0 ] && ! grep -q '^FAIL ' <<<"$lines" && ! { [ "$rc" = 3 ] && grep -q '^GAP install ' <<<"$lines"; }; then
    extra="FAIL fence (dev.sh iso run exited $rc after $(grep -c '' <<<"$lines") check line(s) — see the log)"
  fi
  [ -z "$extra" ] || echo "$extra"
  echo "log: $UNIT_LOG"
  if [ "$path_rc" != 0 ] || [ -n "$extra" ] || grep -q '^FAIL ' <<<"$lines"; then exit 1; fi
  if grep -q '^GAP install ' <<<"$lines"; then exit 3; fi
  exit 0
fi

# --- fence: the checks, offline, from the worktree root ---------------------------------------------
cd "$ROOT"
gofiles=()
for f in "${files[@]}"; do
  case "$f" in pfm/*.go) gofiles+=("${f#pfm/}") ;; esac
done
# The formatter `make -C pfm fmt-check` uses: the pinned binary in the dir tools.sh names, else the one
# on PATH; needed only when a .go file is named.
lint=
if [ "${#gofiles[@]}" != 0 ]; then
  tools_rc=0; tools_out="$(bash "$ROOT/infra/fence/tools.sh" --print-bin 2>&1)" || tools_rc=$?
  if [ "$tools_rc" = 0 ] && [ -x "$tools_out/golangci-lint" ]; then lint="$tools_out/golangci-lint"; fi
  [ -n "$lint" ] || lint="$(command -v golangci-lint || true)"
  if [ -z "$lint" ] && [ "$tools_rc" != 0 ]; then printf '  | tools.sh --print-bin exited %s: %s\n' "$tools_rc" "$tools_out"; fi
fi
# install: every missing tool, then the modules go's offline download cannot look up, named on one GAP line.
missing=()
for t in go make; do command -v "$t" >/dev/null 2>&1 || missing+=("$t"); done
if [ "${#gofiles[@]}" != 0 ] && [ -z "$lint" ]; then missing+=(golangci-lint); fi
inst_rc=0
if command -v go >/dev/null 2>&1; then
  inst_out="$(GOPROXY=off go -C pfm mod download 2>&1)" || inst_rc=$?
  if [ -n "$inst_out" ]; then while IFS= read -r l; do printf '  | %s\n' "$l"; done <<<"$inst_out"; fi
  if [ "$inst_rc" != 0 ] && grep -q 'module lookup disabled by GOPROXY=off' <<<"$inst_out"; then
    missing+=("Go modules"); inst_rc=0
  fi
fi
if [ "${#missing[@]}" != 0 ]; then
  list="$(printf '%s, ' "${missing[@]}")"
  echo "GAP install (pfm is missing: ${list%, } — no check ran, this is a missing tool and not a failed check; run: .claude/scripts/dev.sh iso install pfm, and make -C pfm tools for golangci-lint)"
fi
if [ "$inst_rc" != 0 ]; then
  echo 'FAIL install (go mod download failed offline and named no missing module — see the log)'
  exit 1
fi
[ "${#missing[@]}" = 0 ] || exit 3
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

if [ "${#gofiles[@]}" = 0 ]; then
  echo 'SKIP fmt (no named file it applies to)'
else
  out=; rc=0
  out="$(cd pfm && "$lint" fmt --diff "${gofiles[@]}" 2>&1)" || rc=$?
  if [ -n "$out" ] && [ "$rc" = 0 ]; then rc=1; fi
  verdict fmt "$rc" "$out"
fi
# The named .go files' packages, pfm-relative (./internal/a, . for the module root).
pkgs=()
for g in "${gofiles[@]}"; do
  d="$(dirname "$g")"; if [ "$d" = . ]; then d=.; else d="./$d"; fi
  case " ${pkgs[*]} " in *" $d "*) ;; *) pkgs+=("$d") ;; esac
done
# typecheck: with no named .go file (a deletion or a non-Go file), the whole module — it compiles what
# a deletion left behind. Otherwise the named packages plus every module package importing one of them
# directly (test imports too), so an API change that breaks an unnamed importer still reads FAIL.
if [ "${#pkgs[@]}" = 0 ]; then
  check typecheck go -C pfm vet ./...
else
  mod="$(sed -n 's/^module[[:space:]]*//p' pfm/go.mod)"
  lrc=0; listed=
  if [ -z "$mod" ]; then
    lrc=1; listed='pfm/go.mod names no module path'
  else
    listed="$(go -C pfm list -e -f '{{.ImportPath}}{{"\t"}}{{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}' ./... 2>&1)" || lrc=$?
  fi
  if [ "$lrc" != 0 ]; then
    verdict typecheck "$lrc" "go list (the importers of the named packages) failed — no package was vetted:
$listed"
  else
    namedips=" "; for p in "${pkgs[@]}"; do if [ "$p" = . ]; then namedips+="$mod "; else namedips+="$mod/${p#./} "; fi; done
    vetpkgs=("${pkgs[@]}")
    while IFS=$'\t' read -r ip imps; do
      for i in $imps; do
        case "$namedips" in *" $i "*)
          if [ "$ip" = "$mod" ]; then p=.; else p="./${ip#"$mod"/}"; fi
          case " ${vetpkgs[*]} " in *" $p "*) ;; *) vetpkgs+=("$p") ;; esac
          break ;;
        esac
      done
    done <<<"$listed"
    check typecheck go -C pfm vet "${vetpkgs[@]}"
  fi
fi
if [ "${#gofiles[@]}" = 0 ]; then
  echo 'SKIP lint-new (no named file it applies to)'
else
  # golangci-lint prints `path/to/file.go:LINE:COL: message (linter)`, relative to pfm (its cwd under
  # make -C pfm); the repository-relative and absolute spellings are accepted too.
  rc=0
  out="$(make -C pfm --no-print-directory lint-new "LINT_PKGS=${pkgs[*]}" 2>&1)" || rc=$?
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
