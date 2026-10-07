#!/usr/bin/env bash
# check-templates.sh — the static checks of the templates unit (every path outside pfm/), on the host.
#   .claude/scripts/check-templates.sh <file>...
# Installed tools only, never one that fetches a tool. Not installed (node, python3 + PyYAML, jq, git,
# rumdl, jscpd at the pinned version (scripts/clone-check.sh --resolve)): only `FAIL install (...)`, no check run, exit 1. Else one PASS/FAIL/SKIP line per check,
# every check run even after one fails: file-scoped rumdl (named .md files) and leak (every named file);
# unit-wide clone, placeholders, scratch-paths, descriptions, manifest, codex-markers,
# opencode-writer-refs. Output goes to /tmp/{project}/check-templates/{UTC}-{pid}.log (path printed).
# Exit 1 on any FAIL line, else 0. None of these checks builds, serves or reaches the network, so none
# takes a lock. Its scratch is private to the run: the directory ${log%.log}.d (made after the log name,
# removed on exit after a run with no FAIL, kept after a run with one so a FAIL line's pointer into it
# resolves) holds the catalogue's TMP_BASE and this script's own files, so two runs never share
# a file. GAPS (landing checks the owner places, never run here): the engine mirrors generate/opencode
# and check-map (they build pfm), agent-roster (scripts/check-agent-roster.mjs: it reads the generated
# mirrors .codex/agents and .opencode/agent, which `pfm codex|opencode build` writes, and exits 3
# NOT GENERATED without them), the node/python/shell test suites (test-templates.sh),
# `dev.sh iso gate templates`.
# BROKEN STATE: a missing unit-path.sh errors on source; a missing infra/fence/checks.sh is FAIL on both
# checks that need it; a check whose tool dies is FAIL, never PASS; a log or scratch directory that
# cannot be created is exit 1 naming it.
set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
# shellcheck source=.claude/scripts/unit-path.sh
source "$ROOT/.claude/scripts/unit-path.sh"

usage() { echo 'Name the files you changed.' >&2; exit 2; }

RUMDL="$(command -v rumdl || true)"
[ -n "$RUMDL" ] || { [ -x "$HOME/.local/share/uv/tools/rumdl/bin/rumdl" ] && RUMDL="$HOME/.local/share/uv/tools/rumdl/bin/rumdl"; } || true
jscpd_resolvable() { # clone-check.sh's own resolver: installed and at the pinned version
  bash "$ROOT/scripts/clone-check.sh" --resolve > /dev/null 2>&1
}
installed() {
  local t
  for t in node python3 jq git; do command -v "$t" > /dev/null 2>&1 || return 1; done
  python3 -c 'import yaml' > /dev/null 2>&1 || return 1
  [ -n "$RUMDL" ] && jscpd_resolvable
}
install_fail() {
  echo 'FAIL install (templates has no installed dependencies — run: pfm install --yes && bash infra/fence/tools.sh)'
  exit 1
}

# The log name first: the scratch directory is named after it. With no log name the install state decides
# the line (git is one of the tools).
if ! unit_log check-templates; then
  installed || install_fail
  echo "check-templates.sh: the log directory could not be created" >&2; exit 1
fi
SCRATCH="${UNIT_LOG%.log}.d"
mkdir -p -- "$SCRATCH" || { echo "check-templates.sh: the scratch directory $SCRATCH could not be created" >&2; exit 1; }
FAILED=0
trap 'if [ "$FAILED" = 0 ]; then rm -rf -- "$SCRATCH"; fi' EXIT
PATH_LINES="$SCRATCH/path-lines"; FAILMARK="$SCRATCH/failmark"
unit_paths check templates "$@" > "$PATH_LINES"
installed || install_fail

cd "$ROOT"
cat "$PATH_LINES"
cat "$PATH_LINES" >> "$UNIT_LOG"
[ "$UNIT_PATHS_RC" = 0 ] || FAILED=1

verdict() { # verdict <name> <rc>
  if [ "$2" = 0 ]; then echo "PASS $1"; else echo "FAIL $1"; FAILED=1; fi
}
run_check() { # run_check <name> <command...>: set +e so the command's own -e rules apply, as in dev.sh
  local name="$1" rc; shift
  { printf '\n== %s: %s\n' "$name" "$*"; } >> "$UNIT_LOG"
  set +e; "$@" >> "$UNIT_LOG" 2>&1; rc=$?; set -e
  verdict "$name" "$rc"
}
skip_check() { echo "SKIP $1 (no named file it applies to)"; }

MD=()
for f in "${UNIT_PATHS[@]}"; do case "$f" in *.md) MD+=("$f") ;; esac; done
if [ "${#MD[@]}" = 0 ]; then skip_check rumdl
else run_check rumdl "$RUMDL" check --output-format concise "${MD[@]}"; fi

if [ "${#UNIT_PATHS[@]}" = 0 ]; then skip_check leak
else
  if [ -z "${LEAK_TERMS:-}" ] && common="$(git -C "$ROOT" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)"; then
    LEAK_TERMS="$(dirname "$common")/scripts/leak-terms.txt"; export LEAK_TERMS
  fi
  run_check leak bash scripts/leak-check.sh --files "${UNIT_PATHS[@]}"
fi

run_check clone bash scripts/clone-check.sh

# placeholders and scratch-paths are the catalogue's own functions, sourced with the helpers dev.sh
# defines (head_, ok, info, run, fail_step, repo_git): a fail_step marks the check FAIL.
REPO_ROOT="$ROOT"; TMP_BASE="$SCRATCH"
head_() { printf '\n-- %s\n' "$*"; }
ok() { printf '  PASS  %s\n' "$*"; }
info() { printf '        %s\n' "$*"; }
fail_step() { printf '  FAIL  %s\n' "$*"; echo x >> "$FAILMARK"; }
run() { local label="$1"; shift; [[ "${1:-}" == "--" ]] && shift; info "\$ $*"; if "$@"; then ok "$label"; else fail_step "$label (exit $?)"; fi; }
repo_git() { git -C "$ROOT" "$@"; }
shim_check() { # shim_check <name> <catalogue function>: in a subshell, so a die is a FAIL, not this script's end
  : > "$FAILMARK"
  local name="$1" fn="$2" rc
  { printf '\n== %s: %s\n' "$name" "$fn"; } >> "$UNIT_LOG"
  set +e; ( set -e; "$fn" ) >> "$UNIT_LOG" 2>&1; rc=$?; set -e
  [ ! -s "$FAILMARK" ] || rc=1
  verdict "$name" "$rc"
}
CATALOGUE=0
# shellcheck source=infra/fence/checks.sh
if source "$ROOT/infra/fence/checks.sh" 2>> "$UNIT_LOG"; then CATALOGUE=1; fi
for pair in placeholders:checks_templates_placeholders scratch-paths:checks_templates_scratch_paths; do
  if [ "$CATALOGUE" = 1 ]; then shim_check "${pair%%:*}" "${pair#*:}"
  else
    echo "check-templates.sh: infra/fence/checks.sh could not be sourced — ${pair%%:*} did not run" >> "$UNIT_LOG"
    verdict "${pair%%:*}" 1
  fi
done

run_check descriptions bash scripts/description-check.sh
run_check manifest bash infra/check-self-hosted-manifest.sh "$ROOT" templates pfm
run_check codex-markers node scripts/check-codex-markers.mjs
run_check opencode-writer-refs node scripts/check-opencode-writer.mjs

echo "log: $UNIT_LOG"
[ "$FAILED" = 0 ]
