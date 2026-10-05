# shellcheck shell=bash
# unit-path.sh — the one path resolver of the test-{unit}.sh and check-{unit}.sh scripts; sourced, never run.
#
#   unit_paths <check|test> <pfm|templates> <arg>...
#
# Units: pfm is pfm/; templates is every other path of the repository. Needs ROOT (the physical
# repository root, `pwd -P`) and the caller's usage() (prints the usage, exits 2).
# An arg is a file, optionally <file>::<selector>. Candidates for <file>, each collapsed with
# `realpath -m`: an absolute path itself; else cwd-relative, unit-relative (pfm/<file>, pfm only) and
# repository-relative. The existing file among them is the target:
#   - an empty arg or a flag → usage (exit 2);
#   - two different existing files → `{script}: {arg} resolves to two files: {a} and {b}`, then usage;
#   - a target outside the unit (the full path decides, never its first segment) → `{script}: {arg} is
#     outside {unit}`, then usage;
#   - a directory → check: `FAIL path {arg} (not a file)`; test: named on stderr, then usage;
#   - nothing exists: a file (blob) tracked at HEAD inside the unit is a deletion and drops silently (test mode: a
#     selection left empty by drops names them, then usage); otherwise check: `FAIL path {arg} (no such
#     file in {unit})`; test: named on stderr, then usage.
# Sets UNIT_PATHS (repository-relative, in arg order; test mode re-appends ::<selector> byte for byte)
# and UNIT_PATHS_RC (1 after any FAIL path line — the caller's run is then not green — else 0).
# unit_log <purpose> sets UNIT_LOG to /tmp/{project}/{purpose}/{UTC timestamp}-{pid}.log and creates
# its directory; {project} is the main checkout's directory name minus any leading dot.
# BROKEN STATE: scripts/test-unit-path.sh fails a case; a caller sourcing a missing file errors on source.

# shellcheck disable=SC2034 # UNIT_PATHS, UNIT_PATHS_RC and UNIT_LOG are read by the sourcing script

unit_in() { # unit_in <unit> <absolute path>: 0 when the path lies inside the unit
  case "$1" in
    pfm) [[ "$2/" == "$ROOT/pfm/"* ]] ;;
    templates) [[ "$2/" == "$ROOT/"* && "$2/" != "$ROOT/pfm/"* ]] ;;
    *) return 1 ;;
  esac
}

unit_paths() {
  local mode="$1" unit="$2"; shift 2
  local name="${0##*/}" cwd arg file suffix c target other
  local cands=() dropped=()
  UNIT_PATHS=(); UNIT_PATHS_RC=0
  [ "$#" -gt 0 ] || usage
  cwd="$(pwd -P)"
  for arg in "$@"; do
    case "$arg" in '' | -*) usage ;; esac
    file="$arg"; suffix=
    if [[ "$arg" == *::* ]]; then file="${arg%%::*}"; suffix="${arg#"$file"}"; fi
    [ -n "$file" ] || usage
    if [[ "$file" == /* ]]; then
      cands=("$file")
    else
      cands=("$cwd/$file")
      [ "$unit" != pfm ] || cands+=("$ROOT/pfm/$file")
      cands+=("$ROOT/$file")
    fi
    target=; other=
    for c in "${!cands[@]}"; do
      cands[c]="$(realpath -m -- "${cands[c]}")"
      [ -f "${cands[c]}" ] || continue
      if [ -z "$target" ]; then target="${cands[c]}"
      elif [ -z "$other" ] && [ "${cands[c]}" != "$target" ]; then other="${cands[c]}"; fi
    done
    if [ -n "$other" ]; then
      echo "$name: $arg resolves to two files: ${target#"$ROOT"/} and ${other#"$ROOT"/}" >&2; usage
    fi
    if [ -z "$target" ]; then
      for c in "${cands[@]}"; do
        if [ -d "$c" ]; then target="$c"; break; fi
      done
    fi
    if [ -n "$target" ]; then
      unit_in "$unit" "$target" || { echo "$name: $arg is outside $unit" >&2; usage; }
      if [ ! -f "$target" ]; then
        if [ "$mode" = check ]; then echo "FAIL path $arg (not a file)"; UNIT_PATHS_RC=1; continue; fi
        echo "$name: $arg is not a file" >&2; usage
      fi
      if [ "$mode" = test ]; then UNIT_PATHS+=("${target#"$ROOT"/}$suffix"); else UNIT_PATHS+=("${target#"$ROOT"/}"); fi
      continue
    fi
    for c in "${cands[@]}"; do
      if unit_in "$unit" "$c" && [ "$(git -C "$ROOT" cat-file -t "HEAD:${c#"$ROOT"/}" 2>/dev/null)" = blob ]; then
        dropped+=("$arg"); continue 2
      fi
    done
    if [ "$mode" = check ]; then echo "FAIL path $arg (no such file in $unit)"; UNIT_PATHS_RC=1; continue; fi
    echo "$name: $arg is no such file in $unit" >&2; usage
  done
  if [ "$mode" = test ] && [ "${#UNIT_PATHS[@]}" = 0 ]; then
    echo "$name: no file left to run — deleted at HEAD: ${dropped[*]}" >&2; usage
  fi
}

unit_log() {
  local common project
  common="$(git -C "$ROOT" rev-parse --path-format=absolute --git-common-dir)" || return 1
  project="$(basename "$(dirname "$common")")"; project="${project#.}"
  mkdir -p "/tmp/$project/$1" || return 1
  UNIT_LOG="/tmp/$project/$1/$(date -u +%Y%m%dT%H%M%SZ)-$$.log"
}
