#!/usr/bin/env bash
set -euo pipefail

# Verifies (and, with --write, restamps) .professor/manifest.json — the
# tracked ledger of this repo's own self-hosted install. Plain mode reports
# every mismatch between the manifest and the live tree and exits nonzero;
# nothing on disk is touched. --write recomputes ONLY the derived fields
# (file_hashes, installed.agents, installed.commands, installed.scripts) from
# the same enumeration the check uses, writes the manifest atomically (a
# temp file beside it, then `mv`), then runs the full check against the
# result and exits with the check's status — so --write never masks a
# non-derived mismatch (version, roster, installed_from, skills_source_fetched):
# those still fail, named, after the restamp. A check whose only failures are
# derived-field drift says so and names `--write` as the fix.
#
# Usage: check-self-hosted-manifest.sh [--write] <repo-root> [scoped-project...]
#
# What this script's own broken state reports:
#   - no ROOT given: NO-ROOT, exit 2, nothing verified.
#   - git/jq/sort missing: TOOLCHAIN-MISSING <tool>, exit 1.
#   - repo-root/manifest/VERSION files absent: `missing <path>`, exit 1.
#   - manifest is not valid JSON: `unreadable JSON: <path>`, exit 1.
#   - a tracked, installed-surface file that cannot be hashed (not a regular
#     file) or is tracked but deleted on disk: named on stderr as
#     NOT-HASHABLE / TRACKED-DELETED and excluded from coverage — never
#     silently dropped and never conflated with each other.
#   - any other drift: one `fail` line per mismatch, a failure count, exit 1.
#   - fully consistent: `self-hosted-manifest: clean`, exit 0.

WRITE=0
if [[ "${1:-}" == "--write" ]]; then
  WRITE=1
  shift
fi

ROOT="${1:-}"
shift || true
MANIFEST="$ROOT/.professor/manifest.json"
STATE_VERSION="$ROOT/.professor/VERSION"
SOURCE_VERSION="$ROOT/VERSION"

failures=0
derived_failures=0
fail() {
  echo "self-hosted-manifest: $*" >&2
  failures=$((failures + 1))
}
# A "derived" failure is one --write can fix by itself: file_hashes coverage
# or content, or the installed.{agents,commands,scripts} arrays. Tracking
# these separately from `failures` lets the summary tell a fixable drift
# apart from a mismatch --write is not allowed to touch (version, roster,
# installed_from, skills_source_fetched).
fail_derived() {
  fail "$@"
  derived_failures=$((derived_failures + 1))
}

repo_git() {
  if [[ -n "${PFM_DEV_REPO_GIT_DIR:-}" && -n "${PFM_DEV_REPO_WORK_TREE:-}" ]]; then
    git --git-dir="$PFM_DEV_REPO_GIT_DIR" --work-tree="$PFM_DEV_REPO_WORK_TREE" \
      -c safe.directory="$PFM_DEV_REPO_WORK_TREE" "$@"
  else
    git -C "$ROOT" "$@"
  fi
}

digest() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    return 127
  fi
}

# The ONE enumeration of the installed surface eligible for file_hashes
# coverage: every tracked path under the case pattern below. Both the check
# and --write call this — a writer with its own copy would be a near-copy
# that drifts. Prints eligible, hashable paths on stdout; announces (on
# stderr, never silently) a tracked-but-deleted or tracked-but-unhashable
# path instead of printing it.
enumerate_file_hashes_paths() {
  repo_git ls-files | while IFS= read -r path; do
    case "$path" in
      .claude/*|.codex/*|.opencode/*|.gitignore|AGENTS.md|CLAUDE.md|pfm/AGENTS.md|pfm/CLAUDE.md|docs/commands/pfm/references/*|docs/commands/pcm/references/*)
        if [[ -f "$ROOT/$path" ]]; then
          printf '%s\n' "$path"
        elif [[ ! -e "$ROOT/$path" ]]; then
          # Tracked but gone from disk: a deletion awaiting its commit, not an
          # unhashable file type. Both are excluded from coverage and neither
          # is silent, but they are different facts and never print the same
          # line.
          echo "self-hosted-manifest: TRACKED-DELETED $path (tracked but absent from disk — a deletion awaiting its commit; excluded from file_hashes coverage)" >&2
        else
          echo "self-hosted-manifest: NOT-HASHABLE $path (tracked under the installed surface but not a regular file — excluded from file_hashes coverage, verified by nothing)" >&2
        fi
        ;;
    esac
  done
}

# The ONE enumeration of installed.{agents,commands,scripts}: the canonical
# Claude sources under .claude/<category>, tracked and present on disk.
# Descriptive arrays are a contract too — hashes alone cannot expose a
# retired command still advertised as installed.
enumerate_installed() {
  local category="$1"
  repo_git ls-files ".claude/$category" | while IFS= read -r path; do
    # A path tracked but absent from disk is a deletion awaiting its commit —
    # it is NOT installed, so it must not be demanded of the manifest.
    # enumerate_file_hashes_paths already announces TRACKED-DELETED for it by
    # name, so this exclusion is silent here on purpose: it was announced
    # once already, not twice.
    [[ -e "$ROOT/$path" ]] || continue
    relative="${path#".claude/$category/"}"
    case "$category" in
      scripts) printf '%s\n' "$relative" ;;
      commands) [[ "$relative" == *.md ]] && printf '%s\n' "${relative%.md}" | tr '/' ':' ;;
      *) [[ "$relative" == *.md ]] && printf '%s\n' "${relative%.md}" ;;
    esac
  done
}

# An empty ROOT is a CALLER error, not a missing repository: without this the
# loop below reports `missing ` and `missing /VERSION` — absolute paths nobody
# ever asked about — which reads as a broken install instead of a call with no
# argument.
if [[ -z "$ROOT" ]]; then
  echo "self-hosted-manifest: NO-ROOT — usage: $0 [--write] <repo-root> [scoped-project...]; nothing was verified" >&2
  exit 2
fi

for tool in git jq sort; do
  command -v "$tool" >/dev/null 2>&1 || { fail "TOOLCHAIN-MISSING $tool"; }
done
if (( failures > 0 )); then exit 1; fi
for path in "$ROOT" "$MANIFEST" "$STATE_VERSION" "$SOURCE_VERSION"; do
  [[ -e "$path" ]] || fail "missing $path"
done
if (( failures > 0 )); then exit 1; fi
if ! jq -e . "$MANIFEST" >/dev/null; then
  fail "unreadable JSON: $MANIFEST"
  exit 1
fi

TMP="$(mktemp -d)"
WRITE_TMP=""
cleanup() {
  rm -rf -- "$TMP"
  [[ -z "$WRITE_TMP" ]] || rm -f -- "$WRITE_TMP"
}
trap cleanup EXIT

if (( WRITE )); then
  enumerate_file_hashes_paths | LC_ALL=C sort -u >"$TMP/write-files"
  : >"$TMP/write-hashes.tsv"
  digest_failed=0
  while IFS= read -r path; do
    [[ -n "$path" ]] || continue
    if h="$(digest "$ROOT/$path")"; then
      printf '%s\t%s\n' "$path" "$h" >>"$TMP/write-hashes.tsv"
    else
      digest_failed=1
    fi
  done <"$TMP/write-files"
  if (( digest_failed )); then
    fail "TOOLCHAIN-MISSING sha256sum or shasum"
    exit 1
  fi
  for category in agents commands scripts; do
    enumerate_installed "$category" | LC_ALL=C sort -u >"$TMP/write-$category"
  done

  jq \
    --rawfile hashes "$TMP/write-hashes.tsv" \
    --rawfile agents "$TMP/write-agents" \
    --rawfile commands "$TMP/write-commands" \
    --rawfile scripts "$TMP/write-scripts" \
    '
    def lines: split("\n") | map(select(length > 0));
    .file_hashes = ($hashes | lines | map(split("\t") | {key: .[0], value: .[1]}) | from_entries)
    | .installed.agents = ($agents | lines)
    | .installed.commands = ($commands | lines)
    | .installed.scripts = ($scripts | lines)
    ' "$MANIFEST" >"$TMP/write-manifest.json"
  jq -e . "$TMP/write-manifest.json" >/dev/null || { fail "--write produced unreadable JSON (not applied)"; exit 1; }

  WRITE_TMP="$(mktemp "$ROOT/.professor/manifest.json.XXXXXX")"
  cp -- "$TMP/write-manifest.json" "$WRITE_TMP"
  mv -f -- "$WRITE_TMP" "$MANIFEST"
  WRITE_TMP=""
fi

mode="$(jq -r '.installed_from.mode // empty' "$MANIFEST")"
[[ "$mode" == "self-hosted" ]] || fail "installed_from.mode=$mode, want self-hosted"
if jq -e '.installed_from | has("source_sha")' "$MANIFEST" >/dev/null; then
  fail "installed_from.source_sha must be absent for live self-hosted source"
fi
source_version="$(<"$SOURCE_VERSION")"
state_version="$(<"$STATE_VERSION")"
manifest_version="$(jq -r '.installed_from.version // empty' "$MANIFEST")"
[[ "$state_version" == "$source_version" ]] || fail ".professor/VERSION=$state_version, root VERSION=$source_version"
[[ "$manifest_version" == "$source_version" ]] || fail "manifest version=$manifest_version, root VERSION=$source_version"

printf '%s\n' "$@" | LC_ALL=C sort -u >"$TMP/want-roster"
if ! jq -r '.answers.roster[]?.dir' "$MANIFEST" | LC_ALL=C sort -u >"$TMP/got-roster"; then
  fail "cannot enumerate answers.roster"
elif ! diff -u "$TMP/want-roster" "$TMP/got-roster"; then
  fail "answers.roster does not match the development roster"
fi

# Coverage is the set of tracked files under the installed surface that can
# actually BE hashed; enumerate_file_hashes_paths() is the single source of
# that set for both this check and --write.
enumerate_file_hashes_paths | LC_ALL=C sort -u >"$TMP/want-files"
if ! jq -r '.file_hashes | keys[]' "$MANIFEST" | LC_ALL=C sort -u >"$TMP/got-files"; then
  fail_derived "cannot enumerate file_hashes"
elif ! diff -u "$TMP/want-files" "$TMP/got-files"; then
  fail_derived "file_hashes coverage does not match the installed tracked surface"
fi

for category in agents commands scripts; do
  enumerate_installed "$category" | LC_ALL=C sort -u >"$TMP/want-$category"
  jq -r --arg category "$category" '.installed[$category][]?' "$MANIFEST" | LC_ALL=C sort -u >"$TMP/got-$category"
  if ! diff -u "$TMP/want-$category" "$TMP/got-$category"; then
    fail_derived "installed.$category does not match canonical Claude sources"
  fi
done
if ! diff -u <(jq -S '.source_fetched' "$ROOT/templates/project/skills/sources.json") <(jq -S '.installed.skills_source_fetched' "$MANIFEST"); then
  fail "installed.skills_source_fetched does not match project source registry"
fi

# Output styles are retired, and their absence is ASSERTED rather than assumed:
# every Claude launch now pins `--settings {"outputStyle":"default"}`, so a style
# file that reappeared would be INERT rather than wrong. Nothing would fail, no
# persona would change, and the only symptom would be a file everyone believes
# is doing something. A gate that looks is the only way that surfaces.
if repo_git ls-files | grep -q '^\.claude/output-styles/'; then
  fail "tracked .claude/output-styles/ exists — output styles are retired and every launch pins outputStyle=default, so anything there is inert"
fi
if [[ -d "$ROOT/.claude/output-styles" ]]; then
  fail "$ROOT/.claude/output-styles exists on disk — output styles are retired; remove it"
fi
if jq -e '(.installed // {}) | has("output_styles")' "$MANIFEST" >/dev/null; then
  fail "manifest advertises installed.output_styles — output styles are retired; drop the key"
fi
for settings in "$ROOT/.claude/settings.json" "$ROOT/templates/project/settings.json" "$ROOT/templates/project/settings-global.json"; do
  [[ -f "$settings" ]] || continue
  if jq -e 'has("outputStyle")' "$settings" >/dev/null 2>&1; then
    fail "$settings sets outputStyle — output styles are retired and every launch pins default"
  fi
done

while IFS=$'\t' read -r path expected; do
  case "$path" in
    /*|../*|*/../*) fail_derived "unsafe file_hashes path=$path"; continue ;;
  esac
  if [[ ! -f "$ROOT/$path" ]]; then
    fail_derived "hashed file missing: $path"
    continue
  fi
  if ! actual="$(digest "$ROOT/$path")"; then
    fail "TOOLCHAIN-MISSING sha256sum or shasum"
    break
  fi
  [[ "$actual" == "$expected" ]] || fail_derived "hash mismatch: $path"
done < <(jq -r '.file_hashes | to_entries[] | [.key,.value] | @tsv' "$MANIFEST")

if (( failures > 0 )); then
  echo "self-hosted-manifest: $failures failure(s)" >&2
  if (( WRITE == 0 && derived_failures > 0 && derived_failures == failures )); then
    echo "self-hosted-manifest: every failure is derived-field drift (file_hashes / installed.agents|commands|scripts) — run: $0 --write $ROOT $* to restamp it" >&2
  fi
  exit 1
fi
echo "self-hosted-manifest: clean"
