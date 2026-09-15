#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-}"
shift || true
MANIFEST="$ROOT/.professor/manifest.json"
STATE_VERSION="$ROOT/.professor/VERSION"
SOURCE_VERSION="$ROOT/VERSION"

failures=0
fail() {
  echo "self-hosted-manifest: $*" >&2
  failures=$((failures + 1))
}

repo_git() {
  if [[ -n "${PFM_DEV_REPO_GIT_DIR:-}" && -n "${PFM_DEV_REPO_WORK_TREE:-}" ]]; then
    git --git-dir="$PFM_DEV_REPO_GIT_DIR" --work-tree="$PFM_DEV_REPO_WORK_TREE" \
      -c safe.directory="$PFM_DEV_REPO_WORK_TREE" "$@"
  else
    git -C "$ROOT" "$@"
  fi
}

# An empty ROOT is a CALLER error, not a missing repository: without this the
# loop below reports `missing ` and `missing /VERSION` — absolute paths nobody
# ever asked about — which reads as a broken install instead of a call with no
# argument.
if [[ -z "$ROOT" ]]; then
  echo "self-hosted-manifest: NO-ROOT — usage: $0 <repo-root> [scoped-project...]; nothing was verified" >&2
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

TMP="$(mktemp -d)"
trap 'rm -rf -- "$TMP"' EXIT
printf '%s\n' "$@" | LC_ALL=C sort -u >"$TMP/want-roster"
if ! jq -r '.answers.roster[]?.dir' "$MANIFEST" | LC_ALL=C sort -u >"$TMP/got-roster"; then
  fail "cannot enumerate answers.roster"
elif ! diff -u "$TMP/want-roster" "$TMP/got-roster"; then
  fail "answers.roster does not match the development roster"
fi

# Coverage is the set of tracked files under the installed surface that can
# actually BE hashed. A tracked path that is not a regular file — a symlink to
# a directory, say — cannot be sha256'd and would make this gate unsatisfiable:
# the coverage check would demand it while the verification loop's own -f test
# rejects it. Such a path is excluded and NAMED on stderr, never dropped in
# silence: "covered by nothing" and "excluded because unhashable" are different
# facts, and only one of them is safe to leave unsaid.
repo_git ls-files | while IFS= read -r path; do
  case "$path" in
    .claude/*|.codex/*|.opencode/*|.gitignore|AGENTS.md|CLAUDE.md|pfm/AGENTS.md|pfm/CLAUDE.md|docs/commands/pfm/references/*|docs/commands/pcm/references/*)
      if [[ -f "$ROOT/$path" ]]; then
        printf '%s\n' "$path"
      elif [[ ! -e "$ROOT/$path" ]]; then
        # Tracked but gone from disk: a deletion awaiting its commit, not an
        # unhashable file type. Both are excluded from coverage and neither is
        # silent, but they are different facts and never print the same line.
        echo "self-hosted-manifest: TRACKED-DELETED $path (tracked but absent from disk — a deletion awaiting its commit; excluded from file_hashes coverage)" >&2
      else
        echo "self-hosted-manifest: NOT-HASHABLE $path (tracked under the installed surface but not a regular file — excluded from file_hashes coverage, verified by nothing)" >&2
      fi
      ;;
  esac
done | LC_ALL=C sort -u >"$TMP/want-files"
if ! jq -r '.file_hashes | keys[]' "$MANIFEST" | LC_ALL=C sort -u >"$TMP/got-files"; then
  fail "cannot enumerate file_hashes"
elif ! diff -u "$TMP/want-files" "$TMP/got-files"; then
  fail "file_hashes coverage does not match the installed tracked surface"
fi

# Descriptive installed arrays are a contract too: hashes alone cannot expose
# a retired command still advertised as installed.
for category in agents commands scripts; do
  source_dir="$category"
  repo_git ls-files ".claude/$source_dir" | while IFS= read -r path; do
    # A path tracked but absent from disk is a deletion awaiting its commit —
    # it is NOT installed, so it must not be demanded of the manifest. The
    # file_hashes loop above already printed TRACKED-DELETED for it by name, so
    # this exclusion is announced once rather than twice; it is never silent.
    [[ -e "$ROOT/$path" ]] || continue
    relative="${path#".claude/$source_dir/"}"
    case "$category" in
      scripts) printf '%s\n' "$relative" ;;
      commands) [[ "$relative" == *.md ]] && printf '%s\n' "${relative%.md}" | tr '/' ':' ;;
      *) [[ "$relative" == *.md ]] && printf '%s\n' "${relative%.md}" ;;
    esac
  done | LC_ALL=C sort -u >"$TMP/want-$category"
  jq -r --arg category "$category" '.installed[$category][]?' "$MANIFEST" | LC_ALL=C sort -u >"$TMP/got-$category"
  if ! diff -u "$TMP/want-$category" "$TMP/got-$category"; then
    fail "installed.$category does not match canonical Claude sources"
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

digest() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    return 127
  fi
}

while IFS=$'\t' read -r path expected; do
  case "$path" in
    /*|../*|*/../*) fail "unsafe file_hashes path=$path"; continue ;;
  esac
  if [[ ! -f "$ROOT/$path" ]]; then
    fail "hashed file missing: $path"
    continue
  fi
  if ! actual="$(digest "$ROOT/$path")"; then
    fail "TOOLCHAIN-MISSING sha256sum or shasum"
    break
  fi
  [[ "$actual" == "$expected" ]] || fail "hash mismatch: $path"
done < <(jq -r '.file_hashes | to_entries[] | [.key,.value] | @tsv' "$MANIFEST")

if (( failures > 0 )); then
  echo "self-hosted-manifest: $failures failure(s)" >&2
  exit 1
fi
echo "self-hosted-manifest: clean"
