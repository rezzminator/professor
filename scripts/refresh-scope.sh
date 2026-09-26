#!/usr/bin/env bash
set -euo pipefail

# refresh-scope.sh — incremental refresh: reads templates/refresh-map.json (template →
# live source paths + SHA-256 as of the last sync), hashes the live sources, and
# reports which templates need LLM re-derivation. UNCHANGED hashes are a mechanical
# untouched-proof — skipped. UNMAPPED live files need a mapping decision. `regen`
# rewrites the map's hashes after a release's template edits land.
#
# A MISSING-SOURCE entry (the live source a template was derived from is gone) is a
# BLOCKING ruling, not a warning: both `scan` and `regen` exit MISSING_SOURCE_EXIT so a
# release cannot proceed and cannot re-baseline until each one is ruled — delete the
# template file AND its refresh-map.json entry, or remap it to the source's new path.
# Re-baselining around a missing source is what keeps a zombie template alive forever.
# ENUMERATION-FAILED exits 1 when a source glob cannot be read or expanded;
# an incomplete scan never emits a clean scope summary.
MISSING_SOURCE_EXIT=3

usage() {
  echo "usage: $(basename "$0") scan|regen <project_root> [map_path]" >&2
  echo "  exit ${MISSING_SOURCE_EXIT}: unruled MISSING-SOURCE entries (see above)" >&2
  exit 1
}

[[ $# -ge 2 ]] || usage
CMD="$1"

PROJECT_ROOT_ARG="$2"
case "$CMD" in
  scan|regen) ;;
  *) usage ;;
esac

[[ -d "$PROJECT_ROOT_ARG" ]] || { echo "refresh-scope: project_root not found: $PROJECT_ROOT_ARG" >&2; exit 1; }
PROJECT_ROOT="$(cd "$PROJECT_ROOT_ARG" && pwd)"

DEFAULT_MAP="$(dirname "$(readlink -f "$0")")/../templates/refresh-map.json"
MAP_PATH="${3:-$DEFAULT_MAP}"

[[ -f "$MAP_PATH" ]] || { echo "refresh-scope: map not found at $MAP_PATH" >&2; exit 1; }

MANIFEST_FILE="$PROJECT_ROOT/.professor/manifest.json"

# Resolves {project:ROLE} (via .professor/manifest.json .interview.projects.ROLE)
# and a leading ~/ (to $HOME) in a map path/glob string.
# Returns 1 (empty output, one stderr note) when a {project:ROLE} cannot resolve —
# manifest absent or key null. Callers classify that: scan/regen count it
# MISSING-SOURCE (still BLOCKING via MISSING_SOURCE_EXIT); glob entries fail
# enumeration and unresolved ignore entries match nothing. A hard exit here would kill the whole scan at the first
# unresolvable source and report nothing about the rest.
resolve_path() {
  local resolved="$1"
  while [[ "$resolved" =~ \{project:([a-zA-Z0-9_-]+)\} ]]; do
    local role="${BASH_REMATCH[1]}"
    [[ -f "$MANIFEST_FILE" ]] || {
      echo "refresh-scope: manifest not found at $MANIFEST_FILE (needed to resolve {project:$role})" >&2
      return 1
    }
    local val
    val="$(jq -r --arg r "$role" '.interview.projects[$r] // empty' "$MANIFEST_FILE")"
    [[ -n "$val" ]] || {
      echo "refresh-scope: manifest .interview.projects.$role is missing/null" >&2
      return 1
    }
    local placeholder="{project:$role}"
    resolved="${resolved%%"$placeholder"*}${val}${resolved#*"$placeholder"}"
  done
  case "$resolved" in
    "~/"*) resolved="${HOME}/${resolved#\~/}" ;;
  esac
  printf '%s\n' "$resolved"
}

abspath_under_project() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s\n' "$PROJECT_ROOT/$1" ;;
  esac
}

source_hash() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "refresh-scope: TOOLCHAIN-MISSING — sha256sum or shasum is required" >&2
    return 1
  fi
}

is_ignored() {
  local path="$1" entry resolved_entry
  while IFS= read -r entry; do
    [[ -z "$entry" ]] && continue
    resolved_entry="$(resolve_path "$entry")" || continue
    if [[ "$resolved_entry" == */ ]]; then
      [[ "$path" == "$resolved_entry"* ]] && return 0
    else
      [[ "$path" == "$resolved_entry" ]] && return 0
    fi
  done < <(jq -r '.ignore_sources[]? // empty' "$MAP_PATH")
  return 1
}

list_glob_files() {
  local pattern resolved prefix root found relative
  pattern="$1"
  resolved="$(resolve_path "$pattern")" || return 1
  # bash 3.2 — the macOS system bash — has no globstar, and without it `**`
  # silently degrades to a single-level `*`: a recursive glob would then report
  # only its top directory and every deeper file would read as "nothing there".
  # A trailing `/**` is expanded with find instead, and any OTHER `**` shape is
  # refused BY NAME rather than quietly mismatched. find starts inside the prefix
  # and prunes dot-entries below it, matching glob semantics exactly — a glob
  # never descends into a dot-directory unless dotglob is set.
  case "$resolved" in
    */'**')
      prefix="${resolved%/'**'}"
      if [[ "$prefix" == *'**'* ]]; then
        printf 'UNSUPPORTED-GLOB %s — only one trailing "/**" expands under bash %s\n' \
          "$pattern" "$BASH_VERSION" >&2
        return 1
      fi
      root="$(abspath_under_project "$prefix")"
      [[ -d "$root" ]] || return 0
      if ! found="$(cd "$root" && find . -mindepth 1 -name '.*' -prune -o -type f -print)"; then
        printf 'ENUMERATION-FAILED %s — recursive source scan failed\n' "$pattern" >&2
        return 1
      fi
      while IFS= read -r relative; do
        [[ -z "$relative" ]] && continue
        printf '%s/%s\n' "$prefix" "${relative#./}"
      done <<< "$found"
      return 0
      ;;
    *'**'*)
      printf 'UNSUPPORTED-GLOB %s — only a trailing "/**" expands under bash %s\n' \
        "$pattern" "$BASH_VERSION" >&2
      return 1
      ;;
  esac
  (
    cd "$PROJECT_ROOT"
    shopt -s nullglob
    IFS=$'\n'
    for f in $resolved; do
      if [[ -f "$f" ]]; then printf '%s\n' "$f"; fi
    done
  )
}

scan() {
  local c=0 u=0 k=0 m=0 x=0

  k="$(jq -r '.templates | to_entries[] | select(.value.curated == true) | .key' "$MAP_PATH" | wc -l | tr -d ' ')"

  local mapped_sources_file
  mapped_sources_file="$(mktemp)"

  # bash 3.2 has no associative arrays: both sets are newline-delimited strings,
  # and a template is "ok" until something names it bad.
  local template_bad=$'\n'
  local template_seen=$'\n'

  while IFS=$'\t' read -r tmpl src expected; do
    [[ -z "$tmpl" ]] && continue
    if [[ "$template_seen" != *$'\n'"$tmpl"$'\n'* ]]; then
      template_seen+="$tmpl"$'\n'
    fi

    local resolved_rel abs
    if ! resolved_rel="$(resolve_path "$src")"; then
      echo "MISSING-SOURCE ${tmpl} <= ${src}"
      x=$((x + 1))
      if [[ "$template_bad" != *$'\n'"$tmpl"$'\n'* ]]; then
        template_bad+="$tmpl"$'\n'
      fi
      continue
    fi
    abs="$(abspath_under_project "$resolved_rel")"
    printf '%s\n' "$resolved_rel" >> "$mapped_sources_file"

    if [[ ! -f "$abs" ]]; then
      echo "MISSING-SOURCE ${tmpl} <= ${src}"
      x=$((x + 1))
      if [[ "$template_bad" != *$'\n'"$tmpl"$'\n'* ]]; then
        template_bad+="$tmpl"$'\n'
      fi
      continue
    fi

    local actual
    actual="$(source_hash "$abs")"
    if [[ "$actual" != "$expected" ]]; then
      echo "CHANGED ${tmpl} <= ${src}"
      c=$((c + 1))
      if [[ "$template_bad" != *$'\n'"$tmpl"$'\n'* ]]; then
        template_bad+="$tmpl"$'\n'
      fi
    fi
  done < <(jq -r '.templates | to_entries[] | select(.value.sources) | .key as $t | .value.sources | to_entries[] | [$t, .key, .value] | @tsv' "$MAP_PATH")

  while IFS= read -r tmpl; do
    [[ -z "$tmpl" ]] && continue
    [[ "$template_bad" != *$'\n'"$tmpl"$'\n'* ]] && u=$((u + 1))
  done <<< "$template_seen"

  # bash 3.2 has no mapfile.
  MAPPED_SOURCES=()
  while IFS= read -r ms_line; do
    MAPPED_SOURCES+=("$ms_line")
  done < <(sort -u "$mapped_sources_file")
  rm -f "$mapped_sources_file"

  # Capture every enumerator status in this shell. A process substitution
  # loses its producer's exit code and can turn a failed scan into empty scope.
  local source_globs glob glob_files all_glob_files=""
  if ! source_globs="$(jq -r '.source_globs[]? // empty' "$MAP_PATH")"; then
    echo "refresh-scope: ENUMERATION-FAILED — cannot read source_globs" >&2
    return 1
  fi
  while IFS= read -r glob; do
    [[ -z "$glob" ]] && continue
    if ! glob_files="$(list_glob_files "$glob")"; then
      printf 'refresh-scope: ENUMERATION-FAILED %s — scope is incomplete\n' "$glob" >&2
      return 1
    fi
    [[ -z "$glob_files" ]] || all_glob_files+="$glob_files"$'\n'
  done <<< "$source_globs"
  if ! all_glob_files="$(printf '%s' "$all_glob_files" | sort -u)"; then
    echo "refresh-scope: ENUMERATION-FAILED — cannot sort source paths" >&2
    return 1
  fi
  ALL_GLOB_FILES=()
  while IFS= read -r gf_line; do
    [[ -z "$gf_line" ]] || ALL_GLOB_FILES+=("$gf_line")
  done <<< "$all_glob_files"

  for f in ${ALL_GLOB_FILES[@]+"${ALL_GLOB_FILES[@]}"}; do
    local is_mapped=0 ms
    for ms in ${MAPPED_SOURCES[@]+"${MAPPED_SOURCES[@]}"}; do
      if [[ "$f" == "$ms" ]]; then
        is_mapped=1
        break
      fi
    done
    (( is_mapped )) && continue
    is_ignored "$f" && continue
    echo "UNMAPPED-LIVE ${f}"
    m=$((m + 1))
  done

  echo "refresh-scope: ${c} changed, ${u} unchanged (skip re-derivation), ${k} curated, ${m} unmapped-live, ${x} missing-source"

  if (( x > 0 )); then
    echo "refresh-scope: BLOCKED — ${x} missing-source entr$( (( x == 1 )) && echo y || echo ies) must be ruled before releasing: delete the template file AND its refresh-map.json entry, or remap it" >&2
    return "$MISSING_SOURCE_EXIT"
  fi
}

regen() {
  local frag_file n=0 x=0

  # Refuse to re-baseline while any mapped source is missing — writing fresh hashes
  # around a gone source silently keeps its template alive as a zombie. Nothing is
  # written until every mapped source resolves.
  while IFS=$'\t' read -r tmpl src expected; do
    [[ -z "$tmpl" ]] && continue
    local resolved_rel abs
    if ! resolved_rel="$(resolve_path "$src")"; then
      echo "MISSING-SOURCE ${tmpl} <= ${src}" >&2
      x=$((x + 1))
      continue
    fi
    abs="$(abspath_under_project "$resolved_rel")"
    if [[ ! -f "$abs" ]]; then
      echo "MISSING-SOURCE ${tmpl} <= ${src}" >&2
      x=$((x + 1))
    fi
  done < <(jq -r '.templates | to_entries[] | select(.value.sources) | .key as $t | .value.sources | to_entries[] | [$t, .key, .value] | @tsv' "$MAP_PATH")

  if (( x > 0 )); then
    echo "refresh-scope: REFUSING to regen — ${x} missing-source entr$( (( x == 1 )) && echo y || echo ies); ${MAP_PATH} left unchanged. Rule each first: delete the template file AND its refresh-map.json entry, or remap it" >&2
    return "$MISSING_SOURCE_EXIT"
  fi

  frag_file="$(mktemp)"

  while IFS=$'\t' read -r tmpl src expected; do
    [[ -z "$tmpl" ]] && continue
    local resolved_rel abs
    resolved_rel="$(resolve_path "$src")" || {
      echo "refresh-scope: BUG — ${src} passed the missing-source preflight but failed to resolve; ${MAP_PATH} left unchanged" >&2
      exit 1
    }
    abs="$(abspath_under_project "$resolved_rel")"
    local actual
    actual="$(source_hash "$abs")"
    jq -nc --arg t "$tmpl" --arg s "$src" --arg h "$actual" '{t:$t,s:$s,h:$h}' >> "$frag_file"
    n=$((n + 1))
  done < <(jq -r '.templates | to_entries[] | select(.value.sources) | .key as $t | .value.sources | to_entries[] | [$t, .key, .value] | @tsv' "$MAP_PATH")

  local tmp
  tmp="$(mktemp "${MAP_PATH}.XXXXXX")"
  jq --slurpfile updates "$frag_file" '
    reduce $updates[] as $u (.; .templates[$u.t].sources[$u.s] = $u.h)
  ' "$MAP_PATH" > "$tmp"
  mv "$tmp" "$MAP_PATH"
  rm -f "$frag_file"

  echo "refresh-scope: regenerated ${n} hashes into ${MAP_PATH}"
}

case "$CMD" in
  scan) scan ;;
  regen) regen ;;
esac
