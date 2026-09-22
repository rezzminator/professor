#!/usr/bin/env bash
set -euo pipefail

# Regression for the adopter Stop hook's missing-compiler state. The hook must
# make the failed lookup visible and preserve the dirty marker for a later
# compiler-equipped turn — WITHOUT a red exit: a Stop hook that exits nonzero
# shows the user an error the model never sees, so the contract is exit 0 plus a
# JSON systemMessage (codex-sync.sh header, `sync` mode).
#
# When THIS script is broken rather than the hook: a setup step that cannot run
# exits 2 and says so, never a pass. A silent hook, a lost flag, or a nonzero
# exit each fail with the specific expectation that was violated.
HOOK=${1:?usage: test-codex-sync.sh PATH_TO_HOOK}
[[ -f "$HOOK" ]] || { printf 'codex-sync regression: hook not found: %s\n' "$HOOK" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { printf 'codex-sync regression: jq is absent — the hook could not be exercised\n' >&2; exit 2; }

ROOT=$(mktemp -d "${TMPDIR:-/tmp}/codex-sync-regression.XXXXXX")
# The repo basename becomes the scratch namespace, so keep it unique to this run:
# a fixed name would collide with a real project's /tmp/<project>/guard.
REPO="$ROOT/codex-sync-probe-$$"
HOME_DIR="$ROOT/home"
BIN_DIR="$ROOT/bin"
mkdir -p "$REPO" "$HOME_DIR" "$BIN_DIR"

# Derive the flag path the way codex-sync.sh derives it: basename of the repo
# root, leading dot stripped, under /tmp/<project>/guard/.
PROJECT="$(basename "$REPO")"; PROJECT="${PROJECT#.}"
FLAG="/tmp/$PROJECT/guard/codex_dirty"
trap 'rm -rf "$ROOT" "/tmp/$PROJECT"' EXIT
mkdir -p "$(dirname "$FLAG")"
touch "$FLAG"

set +e
OUTPUT=$(HOME="$HOME_DIR" PATH="$BIN_DIR:/usr/bin:/bin" CLAUDE_PROJECT_DIR="$REPO" \
  bash "$HOOK" sync 2>&1)
STATUS=$?
set -e

if (( STATUS != 0 )); then
  printf 'codex-sync regression: a Stop hook must never exit nonzero; got %d\n%s\n' "$STATUS" "$OUTPUT" >&2
  exit 1
fi
if ! MESSAGE=$(printf '%s' "$OUTPUT" | jq -er '.systemMessage' 2>/dev/null); then
  printf 'codex-sync regression: no JSON systemMessage — the failed compiler lookup was silent\n%s\n' "$OUTPUT" >&2
  exit 1
fi
if [[ "$MESSAGE" != *"compiler unavailable"* ]]; then
  printf 'codex-sync regression: systemMessage did not name compiler unavailability\n%s\n' "$MESSAGE" >&2
  exit 1
fi
if [[ "$MESSAGE" != *"dirty flag retained"* ]]; then
  printf 'codex-sync regression: systemMessage did not name dirty-flag retention\n%s\n' "$MESSAGE" >&2
  exit 1
fi
if [[ ! -f "$FLAG" ]]; then
  printf 'codex-sync regression: dirty flag was removed after the compiler lookup failed\n' >&2
  exit 1
fi
if [[ -e "$HOME_DIR/.local/bin/pfm" ]]; then
  printf 'codex-sync regression: isolated HOME unexpectedly acquired a pfm binary\n' >&2
  exit 1
fi

printf 'codex-sync regression: missing compiler is named in a systemMessage, exits 0, and retains the flag\n'
