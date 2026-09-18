#!/usr/bin/env bash
set -euo pipefail

# Regression for the adopter Stop hook's missing-compiler state. The hook must
# make the failed lookup visible and preserve the dirty marker for a later
# compiler-equipped turn.
HOOK=${1:?usage: test-codex-sync.sh PATH_TO_HOOK}
[[ -f "$HOOK" ]] || { printf 'codex-sync regression: hook not found: %s\n' "$HOOK" >&2; exit 2; }

ROOT=$(mktemp -d "${TMPDIR:-/tmp}/codex-sync-regression.XXXXXX")
trap 'rm -rf "$ROOT"' EXIT
REPO="$ROOT/repo"
HOME_DIR="$ROOT/home"
BIN_DIR="$ROOT/bin"
mkdir -p "$REPO/tmp" "$HOME_DIR" "$BIN_DIR"
FLAG="$REPO/tmp/professor_codex_dirty"
touch "$FLAG"

set +e
OUTPUT=$(HOME="$HOME_DIR" PATH="$BIN_DIR:/usr/bin:/bin" CLAUDE_PROJECT_DIR="$REPO" \
  bash "$HOOK" sync 2>&1)
STATUS=$?
set -e

if (( STATUS == 0 )); then
  printf 'codex-sync regression: missing compiler returned 0\n%s\n' "$OUTPUT" >&2
  exit 1
fi
if [[ "$OUTPUT" != *"compiler unavailable"* ]]; then
  printf 'codex-sync regression: stderr did not name compiler unavailability\n%s\n' "$OUTPUT" >&2
  exit 1
fi
if [[ "$OUTPUT" != *"dirty flag retained"* ]]; then
  printf 'codex-sync regression: stderr did not name dirty-flag retention\n%s\n' "$OUTPUT" >&2
  exit 1
fi
if [[ ! -f "$FLAG" ]]; then
  printf 'codex-sync regression: dirty flag was removed after compiler lookup failed\n' >&2
  exit 1
fi
if [[ -e "$HOME_DIR/.local/bin/pfm" ]]; then
  printf 'codex-sync regression: isolated HOME unexpectedly acquired a pfm binary\n' >&2
  exit 1
fi

printf 'codex-sync regression: missing compiler is named, nonzero, and flag-retaining\n'
