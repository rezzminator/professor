#!/usr/bin/env bash
# test.sh — the one test command: the affected tests and the static checks of the paths you name, or
# the full gate.
#   .claude/scripts/test.sh <path>[::<selector>]...
#   .claude/scripts/test.sh --full
# A path is any file of the repository, pfm/ and every other unit in one call, resolved by the one
# resolver, .claude/scripts/unit-path.sh (cwd-relative, repository-relative, pfm-relative or absolute;
# a file deleted at HEAD drops silently and still brings its unit's checks). Each path brings its
# affected tests:
#   pfm/**/*_test.go          go test -v of its package (pfm/e2e/ with the e2e tag), -run the
#                             ::selector byte for byte, else every Test and Fuzz func of the file
#                             (TestMain excluded); a test file with none runs its whole package
#   pfm/**/*.go               go test -v of its whole package
#   pfm/scripts/*_test.sh     bash; pfm/scripts/x.sh brings pfm/scripts/x_test.sh when it exists
#   *.test.mjs                node --test (TAP); ::selector is --test-name-pattern; x.mjs brings x.test.mjs
#   *_test.py                 python3 -m unittest; ::selector is -k; x.py brings x_test.py
#   scripts/test-*.py         python3, no selector; scripts/x.{sh,py} brings scripts/test-x.{sh,py}
#   *_test.sh, scripts/test-*.sh  bash, no selector; .claude/scripts/x.sh brings scripts/test-x.sh
#                             (dev.sh: scripts/test-dev-report.sh, format-md.sh: scripts/test-format-md.sh
#                             on that copy); infra/fence/x.sh, infra/fence/lanes/x.sh bring
#                             infra/fence/lanes/tests/x_test.sh; infra/demo/x.sh infra/demo/tests/x_test.sh
# Any other file brings no test, only checks. The checks, every one run even after one fails:
#   pfm paths      fmt (golangci-lint fmt --diff, named .go files), typecheck (go vet of the named .go
#                  files' packages plus every module package importing one, test imports included; with
#                  no named .go file the whole module), lint-new (make lint-new LINT_PKGS=those packages:
#                  FAIL on a finding in a named file or any `(typecheck)` finding; others are burn-down),
#                  arch, launch-literals
#   any path       rumdl (named .md files), leak (named files)
#   other paths    clone, placeholders, scratch-paths, descriptions, manifest, codex-markers
# Everything runs in the shared fence (dev.sh iso run, PFM_FENCE_NET=private: the run's own network
# namespace), in ONE run: tests in the order named, then the checks. Every tool is the fence image's
# (no host tool, no setup step); one it lacks is `CHECK FAIL image (the fence image lacks: …)` and no
# check runs. A runner that exits 0 without proving a test is red: a Go
# run with no `--- PASS` line, `[no tests to run]` or `[no test files]`, or a `--- SKIP` for a test
# pfm/scripts/known-skips.tsv does not list for that package (a subtest by its parent's row); a TAP
# run with `# pass 0`, only the file itself as the passing test, or `# skipped|todo N`; a unittest run
# with `Ran 0 tests`, `skipped=N`, or exit 5 `NO TESTS RAN`. A shell test is judged by its exit code.
# Output: one `TEST <PASS|FAIL|NO-TEST-RAN|UNLISTED-SKIP|SKIPPED> …` line per test invocation, one
# `CHECK <PASS|FAIL|SKIP> …` line per check, then `TESTS …`, `STATIC …`, on red the log's last 40
# other lines, `VERDICT PASS|FAIL`, and last `log: <path>` — /tmp/{project}/test/{UTC}-{pid}.log holds
# the whole run, every runner's own output indented `  | ` so only this run's summary lines sit at
# column 0. Exit 0 PASS, 1 FAIL, 2 usage.
# --full is the full gate (dev.sh iso gate): pfm, templates, e2e and the lane self-tests as concurrent
# steps in one run, the STEPS line, RUN DIR, gate.tsv, the budget and the ledger row. It runs ONCE per
# worktree, right before its merge.
# BROKEN STATE: a missing unit-path.sh or dev.sh errors on source/exec; a usage or path problem exits 2
# with a named line and never reaches the fence; a fence run that dies before its verdict prints
# `VERDICT FAIL (…)` naming why (docker absent, its daemon unreachable, or the run's exit code), never
# a pass, and so does a PASS verdict the run's non-zero exit contradicts; a test that ran nothing or skipped one is red, never green.
set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
usage() { echo 'usage: test.sh <path>[::<selector>]... | test.sh --full' >&2; exit 2; }
# shellcheck source=.claude/scripts/unit-path.sh
source "$ROOT/.claude/scripts/unit-path.sh"
DEV="$ROOT/.claude/scripts/dev.sh"

# ─── fence half ───────────────────────────────────────────────────────────────────────────────────
# test.sh --fence <units> <n> <invocation>×n <file>...: run inside the fence by the host half.
T_TOTAL=0; T_FAILED=0; S_FAIL=0
t_line() { # t_line <word> <label> [detail]
  echo "TEST $1 $2${3:+ $3}"
}
t_done() { # t_done <label> <rc>
  T_TOTAL=$((T_TOTAL + 1))
  if [ "$2" = 0 ]; then t_line PASS "$1"; else T_FAILED=$((T_FAILED + 1)); fi
}
t_out() { # t_out <runner output>: into the log indented, so a nested run's TEST/CHECK/VERDICT lines
  # never read as this run's (the host takes only column-0 summary lines)
  [ -z "$1" ] || sed 's/^/  | /' <<<"$1"
}
t_go() { # t_go <label> <import path> <command>...
  local label=$1 pkg=$2 out c=0 t cls skips=0 unlisted=0; shift 2
  out="$("$@" 2>&1)" || c=$?
  t_out "$out"
  if [ "$c" = 0 ]; then
    while IFS= read -r t; do
      [ -n "$t" ] || continue
      skips=1
      cls="$(awk -F'\t' -v p="$pkg" -v t="$t" -v parent="${t%%/*}" '$1 == p && ($2 == t || $2 == parent) { print $3; exit }' pfm/scripts/known-skips.tsv 2>/dev/null)"
      if [ -z "$cls" ]; then t_line UNLISTED-SKIP "$t" "in $label"; unlisted=1; fi
    done < <(sed -n 's/^[[:space:]]*--- SKIP: \([^ ]*\).*/\1/p' <<<"$out")
    if [ "$skips" = 1 ] && [ ! -r pfm/scripts/known-skips.tsv ]; then
      echo "test.sh: pfm/scripts/known-skips.tsv unreadable — every skip counts as unlisted"
    fi
    if [ "$unlisted" = 1 ]; then c=1
    elif grep -qE '\[no tests to run\]|\[no test files\]' <<<"$out" || ! grep -qE '^[[:space:]]*--- PASS' <<<"$out"; then
      t_line NO-TEST-RAN "$label"; c=1
    fi
  else
    t_line FAIL "$label" "(exit $c)"
  fi
  t_done "$label" "$c"
}
t_sh() { # t_sh <label> <command>...: the exit code alone
  local label=$1 c=0; shift
  "$@" 2>&1 | sed 's/^/  | /'
  c="${PIPESTATUS[0]}"
  [ "$c" = 0 ] || t_line FAIL "$label" "(exit $c)"
  t_done "$label" "$c"
}
t_proof() { # t_proof <label> <tap|unittest> <command>...
  local label=$1 kind=$2 out c=0; shift 2
  out="$("$@" 2>&1)" || c=$?
  t_out "$out"
  if [ "$c" != 0 ]; then
    if [ "$kind" = unittest ] && [ "$c" = 5 ] && grep -Fxq 'NO TESTS RAN' <<<"$out"; then t_line NO-TEST-RAN "$label"; c=1
    else t_line FAIL "$label" "(exit $c)"; fi
  elif [ "$kind" = tap ]; then
    if ! grep -Eq '^# pass [1-9]' <<<"$out" || grep -Fxq "ok 1 - ${label%%::*}" <<<"$out"; then t_line NO-TEST-RAN "$label"; c=1
    elif grep -Eq '^# (skipped|todo) [1-9]' <<<"$out"; then t_line SKIPPED "$label"; c=1; fi
  else
    if ! grep -Eq '^Ran [1-9][0-9]* tests?' <<<"$out"; then t_line NO-TEST-RAN "$label"; c=1
    elif grep -Eq 'skipped=[1-9]' <<<"$out"; then t_line SKIPPED "$label"; c=1; fi
  fi
  t_done "$label" "$c"
}

s_verdict() { # s_verdict <check> <rc> <output> [note]: the tool's output indented, then the CHECK line
  local l
  if [ -n "$3" ]; then while IFS= read -r l; do printf '  | %s\n' "$l"; done <<<"$3"; fi
  if [ "$2" = 0 ]; then echo "CHECK PASS $1${4:+ $4}"; else echo "CHECK FAIL $1"; S_FAIL=1; fi
}
s_check() { # s_check <name> <command>...
  local name="$1" out rc=0; shift
  out="$("$@" 2>&1)" || rc=$?
  s_verdict "$name" "$rc" "$out"
}
s_skip() { echo "CHECK SKIP $1 (no named file it applies to)"; }

static_pfm() { # static_pfm <file>...: pfm's checks, from check-pfm's rules
  local gofiles=() pkgs=() f g d out rc mod lrc listed namedips vetpkgs ip imps i p hit seen burn path named
  for f in "$@"; do case "$f" in pfm/*.go) gofiles+=("${f#pfm/}") ;; esac; done
  if [ "${#gofiles[@]}" = 0 ]; then
    s_skip fmt
  else
    rc=0; out="$(cd pfm && "$S_LINT" fmt --diff "${gofiles[@]}" 2>&1)" || rc=$?
    if [ -n "$out" ] && [ "$rc" = 0 ]; then rc=1; fi
    s_verdict fmt "$rc" "$out"
  fi
  for g in "${gofiles[@]+"${gofiles[@]}"}"; do
    d="$(dirname "$g")"; if [ "$d" = . ]; then d=.; else d="./$d"; fi
    case " ${pkgs[*]+"${pkgs[*]}"} " in *" $d "*) ;; *) pkgs+=("$d") ;; esac
  done
  if [ "${#pkgs[@]}" = 0 ]; then
    s_check typecheck go -C pfm vet ./...
  else
    mod="$(sed -n 's/^module[[:space:]]*//p' pfm/go.mod)"
    lrc=0; listed=
    if [ -z "$mod" ]; then lrc=1; listed='pfm/go.mod names no module path'
    else
      listed="$(go -C pfm list -e -f '{{.ImportPath}}{{"\t"}}{{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}' ./... 2>&1)" || lrc=$?
    fi
    if [ "$lrc" != 0 ]; then
      s_verdict typecheck "$lrc" "go list (the importers of the named packages) failed — no package was vetted:
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
      s_check typecheck go -C pfm vet "${vetpkgs[@]}"
    fi
  fi
  if [ "${#gofiles[@]}" = 0 ]; then
    s_skip lint-new
  else
    rc=0
    out="$(make -C pfm --no-print-directory lint-new "LINT_PKGS=${pkgs[*]}" 2>&1)" || rc=$?
    hit=0; seen=0; burn=()
    while IFS= read -r l; do
      [[ "$l" =~ ^([^[:space:]:]+):[0-9]+(:[0-9]+)?:\  ]] || continue
      seen=$((seen + 1)); path="${BASH_REMATCH[1]}"
      if [[ "$l" =~ \(typecheck\)[[:space:]]*$ ]]; then hit=1; continue; fi
      path="${path#"$ROOT"/}"; path="${path#./}"; path="${path#pfm/}"
      named=0
      for g in "${gofiles[@]}"; do if [ "$path" = "$g" ]; then named=1; fi; done
      if [ "$named" = 1 ]; then hit=1; else burn+=("$l"); fi
    done <<<"$out"
    if [ "$rc" = 0 ]; then s_verdict lint-new 0 "$out"
    elif [ "$seen" = 0 ]; then s_verdict lint-new "$rc" "lint-new exited $rc with no finding line — a tool or config error, not a lint verdict:
$out"
    elif [ "$hit" = 1 ]; then s_verdict lint-new "$rc" "$out"
    else s_verdict lint-new 0 "$out" "(burn-down, not your red: ${#burn[@]} finding(s), none on a file you named)"; fi
  fi
  s_check arch make -C pfm --no-print-directory arch
  s_check launch-literals go -C pfm test ./internal/claudelaunch/ -run TestNoLaunchLiteralOutsideRegistry -count=1
}

# The catalogue shims infra/fence/checks.sh's functions call (dev.sh defines them for itself): a
# fail_step marks the check FAIL.
head_() { printf '\n-- %s\n' "$*"; }
ok() { printf '  PASS  %s\n' "$*"; }
info() { printf '        %s\n' "$*"; }
fail_step() { printf '  FAIL  %s\n' "$*"; echo x >>"$S_FAILMARK"; }
run() { local label="$1"; shift; [[ "${1:-}" == "--" ]] && shift; info "\$ $*"; if "$@"; then ok "$label"; else fail_step "$label (exit $?)"; fi; }
s_shim() { # s_shim <name> <catalogue function>: in a subshell, so a die is a FAIL, not this run's end
  local name="$1" fn="$2" rc=0 out
  : >"$S_FAILMARK"
  out="$( (set -e; "$fn") 2>&1)" || rc=$?
  [ ! -s "$S_FAILMARK" ] || rc=1
  s_verdict "$name" "$rc" "$out"
}

static_files() { # static_files <file>...: rumdl over the named .md files, leak over every named file
  local md=() f rc out note
  for f in "$@"; do case "$f" in *.md) md+=("$f") ;; esac; done
  if [ "${#md[@]}" = 0 ]; then s_skip rumdl
  else s_check rumdl rumdl check --output-format concise "${md[@]}"; fi
  if [ "$#" = 0 ]; then s_skip leak
  else
    rc=0; out="$(bash scripts/leak-check.sh --files "$@" 2>&1)" || rc=$?
    note=; if grep -q 'PRIVATE COVERAGE SKIPPED' <<<"$out"; then note='(structural-only)'; fi
    s_verdict leak "$rc" "$out" "$note"
  fi
}

static_templates() { # the templates unit's unit-wide checks
  s_check clone bash scripts/clone-check.sh
  REPO_ROOT="$ROOT"; TMP_BASE="$S_SCRATCH"
  # repo_git: the catalogue's git, the run's mounted Git directory (dev.sh sources it for itself).
  # shellcheck source=pfm/scripts/repo-git.sh
  if out="$(source "$ROOT/pfm/scripts/repo-git.sh" 2>&1 && source "$ROOT/infra/fence/checks.sh" 2>&1)" &&
    source "$ROOT/pfm/scripts/repo-git.sh" && source "$ROOT/infra/fence/checks.sh"; then
    s_shim placeholders checks_templates_placeholders
    s_shim scratch-paths checks_templates_scratch_paths
  else
    s_verdict placeholders 1 "pfm/scripts/repo-git.sh or infra/fence/checks.sh could not be sourced: $out"
    s_verdict scratch-paths 1 "pfm/scripts/repo-git.sh or infra/fence/checks.sh could not be sourced"
  fi
  s_check descriptions bash scripts/description-check.sh
  s_check manifest bash infra/check-self-hosted-manifest.sh "$ROOT" templates pfm
  s_check codex-markers node scripts/check-codex-markers.mjs
}

missing_tools() { # missing_tools <units> <has md> <has go>: one name per line
  local t
  S_LINT=
  case ",$1," in *,pfm,*)
    for t in go make; do command -v "$t" >/dev/null 2>&1 || echo "$t"; done
    if [ "$3" = 1 ]; then
      S_LINT="$(command -v golangci-lint || true)"
      [ -n "$S_LINT" ] || echo golangci-lint
    fi ;;
  esac
  case ",$1," in *,templates,*)
    for t in node python3 jq git; do command -v "$t" >/dev/null 2>&1 || echo "$t"; done
    if command -v python3 >/dev/null 2>&1 && ! python3 -c 'import yaml' >/dev/null 2>&1; then echo PyYAML; fi
    bash scripts/clone-check.sh --resolve >/dev/null 2>&1 || echo jscpd ;;
  esac
  if [ "$2" = 1 ]; then command -v rumdl >/dev/null 2>&1 || echo rumdl; fi
}

fence_half() {
  local units="$1" n="$2" inv i files f has_md=0 has_go=0 missing="" tool inst_out inst_rc=0
  shift 2
  cd "$ROOT"
  for ((i = 0; i < n; i++)); do
    inv="$1"; shift
    eval "$inv"
  done
  files=("$@")
  if [ "$T_TOTAL" = 0 ]; then echo 'TESTS NONE (no test file covers the named paths)'
  elif [ "$T_FAILED" = 0 ]; then echo "TESTS PASS $T_TOTAL"
  else echo "TESTS FAIL $T_FAILED of $T_TOTAL"; fi
  for f in "${files[@]+"${files[@]}"}"; do
    case "$f" in *.md) has_md=1 ;; esac
    case "$f" in pfm/*.go) has_go=1 ;; esac
  done
  S_SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/test-sh.XXXXXX")"; S_FAILMARK="$S_SCRATCH/failmark"
  missing_tools "$units" "$has_md" "$has_go" >"$S_SCRATCH/missing" # in this shell: it sets S_LINT
  while IFS= read -r tool; do [ -z "$tool" ] || missing+="${missing:+, }$tool"; done <"$S_SCRATCH/missing"
  case ",$units," in *,pfm,*)
    if command -v go >/dev/null 2>&1; then
      inst_out="$(GOPROXY=off go -C pfm mod download 2>&1)" || inst_rc=$?
      if [ -n "$inst_out" ]; then while IFS= read -r l; do printf '  | %s\n' "$l"; done <<<"$inst_out"; fi
    fi ;;
  esac
  if [ -n "$missing" ]; then
    echo "CHECK FAIL image (the fence image lacks: $missing — infra/fence/pfm-dev.Dockerfile installs every check's tool; no check ran)"
    S_FAIL=1
  elif [ "$inst_rc" != 0 ]; then
    if grep -q 'module lookup disabled by GOPROXY=off' <<<"$inst_out"; then
      echo "CHECK FAIL install (Go modules the fence's module cache lacks — the run's start downloads them; see the log)"
    else echo 'CHECK FAIL install (go mod download failed offline — see the log)'; fi
    S_FAIL=1
  else
    case ",$units," in *,pfm,*) static_pfm "${files[@]+"${files[@]}"}" ;; esac
    static_files "${files[@]+"${files[@]}"}"
    case ",$units," in *,templates,*) static_templates ;; esac
  fi
  rm -rf "$S_SCRATCH"
  if [ "$S_FAIL" = 1 ]; then echo 'STATIC FAIL'; else echo 'STATIC PASS'; fi
  if [ "$T_FAILED" != 0 ] || [ "$S_FAIL" = 1 ]; then echo 'VERDICT FAIL'; return 1; fi
  echo 'VERDICT PASS'
}

if [ "${1:-}" = --fence ]; then
  [ -n "${PFM_DEV_FENCE:-}" ] || { echo 'test.sh: --fence runs inside the fence only' >&2; exit 2; }
  shift
  rc=0; fence_half "$@" || rc=$?
  exit "$rc"
fi

# ─── host half ────────────────────────────────────────────────────────────────────────────────────
[ "$#" -gt 0 ] || usage
if [ "$1" = --full ]; then
  [ "$#" = 1 ] || usage
  export PFM_FENCE_WATCH_PIDS="$$${PFM_FENCE_WATCH_PIDS:+ $PFM_FENCE_WATCH_PIDS}"
  exec "$DEV" iso gate
fi
for arg in "$@"; do
  case "$arg" in --*) usage ;; esac
  case "${arg%%::*}" in *[\*\?\[]*) usage ;; esac
  if [[ "$arg" == *::* && -z "${arg#*::}" ]]; then usage; fi
done
unit_paths test any "$@"

INVS=(); FILES=(); UNITS=""
add_unit() { case ",$UNITS," in *",$1,"*) ;; *) UNITS="${UNITS:+$UNITS,}$1" ;; esac; }
add_inv() { # add_inv <word>...: one invocation, its words quoted; a repeat is dropped
  local s
  printf -v s '%q ' "$@"; add_raw "${s% }"
}
add_raw() { # add_raw <invocation string>
  local e
  for e in "${INVS[@]+"${INVS[@]}"}"; do [ "$e" != "$1" ] || return 0; done
  INVS+=("$1")
}
pfm_module="$(sed -n 's/^module[[:space:]][[:space:]]*//p' "$ROOT/pfm/go.mod" 2>/dev/null | head -1)"
go_pkg_inv() { # go_pkg_inv <label> <pfm-relative dir> [-run selector]: one go test of a package
  local label="$1" dir="$2" pkg pkgpath tags=()
  shift 2
  [ -n "$pfm_module" ] || { echo "test.sh: pfm/go.mod names no module" >&2; exit 2; }
  pkg="./"; [ -z "$dir" ] || pkg="./$dir/"
  pkgpath="$pfm_module"; [ -z "$dir" ] || pkgpath="$pfm_module/$dir"
  case "$dir" in e2e | e2e/*) tags=(-tags e2e) ;; esac
  add_inv t_go "$label" "$pkgpath" go -C pfm test -v ${tags[@]+"${tags[@]}"} -count=1 "$pkg" "$@"
}
sut_rows() { # sut_rows <test file>: the scripts a SUT-taking test runs against, one per line
  case "$1" in
    scripts/test-format-md.sh) printf '%s\n' templates/project/scripts/format-md.sh .claude/scripts/format-md.sh ;;
    scripts/test-dev-report.sh) printf '%s\n' .claude/scripts/dev.sh ;;
  esac
}
shell_test() { # shell_test <test file> [the one SUT]: a shell test, through its SUT rows
  local s rows
  rows="$(sut_rows "$1")"
  if [ -z "$rows" ]; then add_inv t_sh "$1" bash "$1"; return; fi
  while IFS= read -r s; do
    [ -z "${2:-}" ] || [ "$s" = "$2" ] || continue
    # The script under test as an absolute path in the fence: "$PWD" expands there, after the cd.
    add_raw "$(printf '%q ' t_sh "$1 $s" bash "$1")\"\$PWD\"/$(printf '%q' "$s")"
  done <<<"$rows"
}
no_sel() { [ "$2" = 0 ] || { echo "test.sh: $1: a $3 takes no selector" >&2; usage; }; }
derive() { # derive <repository-relative path> <has selector 0|1> <selector>
  local f="$1" has="$2" sel="$3" dir base names fn sib
  case "$f" in
    pfm/*_test.go)
      dir="${f%/*}"; dir="${dir#pfm}"; dir="${dir#/}"
      if [ "$has" = 1 ]; then go_pkg_inv "$f::$sel" "$dir" -run "$sel"; return; fi
      names=()
      while IFS= read -r fn; do [ "$fn" = TestMain ] || names+=("$fn"); done \
        < <(grep -oE '^func ((Test|Fuzz)[A-Za-z0-9_]*)\(' "$ROOT/$f" | sed 's/^func //; s/($//' || true)
      if [ "${#names[@]}" = 0 ]; then go_pkg_inv "pfm/$dir${dir:+/}" "$dir"; return; fi
      sel='^('; for fn in "${names[@]}"; do sel+="$fn|"; done; sel="${sel%|})\$"
      go_pkg_inv "$f" "$dir" -run "$sel" ;;
    pfm/*.go)
      no_sel "$f" "$has" "non-test Go file (its whole package runs)"
      dir="${f%/*}"; dir="${dir#pfm}"; dir="${dir#/}"
      go_pkg_inv "pfm/$dir${dir:+/}" "$dir" ;;
    pfm/scripts/*_test.sh) no_sel "$f" "$has" "shell test"; add_inv t_sh "$f" bash "$f" ;;
    pfm/scripts/*.sh)
      no_sel "$f" "$has" "script (its test runs)"; sib="${f%.sh}_test.sh"
      [ ! -f "$ROOT/$sib" ] || add_inv t_sh "$sib" bash "$sib" ;;
    pfm/*) [ "$has" = 0 ] || { echo "test.sh: $f::$sel: only a test file takes a selector" >&2; usage; } ;;
    *.test.mjs)
      if [ "$has" = 1 ]; then add_inv t_proof "$f::$sel" tap node --test --test-reporter=tap "--test-name-pattern=$sel" "$f"
      else add_inv t_proof "$f" tap node --test --test-reporter=tap "$f"; fi ;;
    *_test.py)
      if [ "$has" = 1 ]; then add_inv t_proof "$f::$sel" unittest python3 -m unittest "$f" -k "$sel"
      else add_inv t_proof "$f" unittest python3 -m unittest "$f"; fi ;;
    scripts/test-*.py) no_sel "$f" "$has" "scripts/test-*.py"; add_inv t_proof "$f" unittest python3 "$f" ;;
    *_test.sh | scripts/test-*.sh) no_sel "$f" "$has" "shell test"; shell_test "$f" ;;
    *)
      [ "$has" = 0 ] || { echo "test.sh: $f::$sel: only a test file takes a selector" >&2; usage; }
      base="${f##*/}"
      case "$f" in
        .claude/scripts/test.sh) shell_test scripts/test-test.sh ;;
        .claude/scripts/dev.sh) shell_test scripts/test-dev-report.sh .claude/scripts/dev.sh ;;
        .claude/scripts/format-md.sh | templates/project/scripts/format-md.sh) shell_test scripts/test-format-md.sh "$f" ;;
        .claude/scripts/*.sh | scripts/*.sh | scripts/*.py)
          for sib in "scripts/test-${base%.*}.sh" "scripts/test-${base%.*}.py"; do
            [ -f "$ROOT/$sib" ] || continue
            case "$sib" in *.py) add_inv t_proof "$sib" unittest python3 "$sib" ;; *) shell_test "$sib" ;; esac
          done ;;
        infra/fence/*.sh | infra/fence/lanes/*.sh)
          sib="infra/fence/lanes/tests/${base%.sh}_test.sh"; [ ! -f "$ROOT/$sib" ] || shell_test "$sib" ;;
        infra/demo/*.sh)
          sib="infra/demo/tests/${base%.sh}_test.sh"; [ ! -f "$ROOT/$sib" ] || shell_test "$sib" ;;
        *.mjs) sib="${f%.mjs}.test.mjs"; [ ! -f "$ROOT/$sib" ] || add_inv t_proof "$sib" tap node --test --test-reporter=tap "$sib" ;;
        *.py) sib="${f%.py}_test.py"; [ ! -f "$ROOT/$sib" ] || add_inv t_proof "$sib" unittest python3 -m unittest "$sib" ;;
      esac ;;
  esac
}
for p in "${UNIT_PATHS[@]+"${UNIT_PATHS[@]}"}"; do
  f="${p%%::*}"; sel=; has=0
  if [[ "$p" == *::* ]]; then has=1; sel="${p#*::}"; fi
  FILES+=("$f")
  case "$f" in pfm/*) add_unit pfm ;; *) add_unit templates ;; esac
  derive "$f" "$has" "$sel"
done
for d in "${UNIT_DROPPED[@]+"${UNIT_DROPPED[@]}"}"; do
  case "$d" in pfm/*) add_unit pfm ;; *) add_unit templates ;; esac
done

cmd="$(printf '%q ' ./.claude/scripts/test.sh --fence "$UNITS" "${#INVS[@]}" ${INVS[@]+"${INVS[@]}"} ${FILES[@]+"${FILES[@]}"})"
unit_log test || { echo "test.sh: the log directory could not be created" >&2; exit 1; }
rc=0
PFM_FENCE_NET=private PFM_FENCE_WATCH_PIDS="$$${PFM_FENCE_WATCH_PIDS:+ $PFM_FENCE_WATCH_PIDS}" \
  "$DEV" iso run "$cmd" >"$UNIT_LOG" 2>&1 || rc=$?
# Only column-0 lines are this run's summary: every runner's own output sits indented (t_out).
grep -E '^(TEST|CHECK|TESTS|STATIC) ' "$UNIT_LOG" || true
verdict="$(grep -E '^VERDICT (PASS|FAIL)$' "$UNIT_LOG" | tail -n 1 || true)"
if [ -z "$verdict" ]; then
  if ! command -v docker >/dev/null 2>&1; then verdict='VERDICT FAIL (fence: docker not found)'
  elif ! docker info >/dev/null 2>&1; then verdict='VERDICT FAIL (fence: the docker daemon is not reachable — start Docker, then rerun)'
  else verdict="VERDICT FAIL (fence: dev.sh iso run exited $rc before a verdict — see the log)"; fi
elif [ "$verdict" = 'VERDICT PASS' ] && [ "$rc" != 0 ]; then
  verdict="VERDICT FAIL (fence: dev.sh iso run exited $rc after its PASS verdict — see the log)"
fi
# On red, the log's last 40 lines past the summary lines already printed above.
if [ "$verdict" != 'VERDICT PASS' ]; then grep -vE '^(TEST|CHECK|TESTS|STATIC|VERDICT) ' "$UNIT_LOG" | tail -n 40 || true; fi
echo "$verdict"
echo "log: $UNIT_LOG"
case "$verdict" in
  'VERDICT PASS') exit 0 ;;
  *) exit 1 ;;
esac
