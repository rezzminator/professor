#!/usr/bin/env bash
set -euo pipefail

# Regression for the Stop hook's missing compiler and local dev-binary freshness. The hook must
# make the failed lookup visible and preserve the dirty marker for a later
# compiler-equipped turn — WITHOUT a red exit: a Stop hook that exits nonzero
# shows the user an error the model never sees, so the contract is exit 0 plus a
# JSON systemMessage (codex-sync.sh header, `sync` mode).
#
# When THIS script is broken rather than the hook: a setup step that cannot run
# exits 2 and says so, never a pass. A silent hook, a lost flag, or a nonzero
# exit each fail with the specific expectation that was violated.
HOOK=${CODEX_SYNC_SUT:-${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/.claude/scripts/codex-sync.sh}}
if [[ "${1:-}" == */templates/project/scripts/codex-sync.sh ]]; then HOOK="$1"; fi
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
  bash "$HOOK" sync 2>&1 </dev/null)
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

# Only this repo's hook selects a fence-built binary; the adopter hook uses PATH.
case "${1:-$HOOK}" in */templates/project/scripts/codex-sync.sh) exit 0 ;; esac

DEV_BIN="/tmp/$PROJECT/timing/pfm-dev-bin"
CALLS="$ROOT/calls"
mkdir -p "$(dirname "$DEV_BIN")"
cat > "$BIN_DIR/pfm" <<'PFM'
#!/usr/bin/env bash
[[ "${1:-}" != --version ]] || exit 0
printf '%s %s\n' "${0##*/}" "$*" >> "$SYNC_CALLS"
if [[ "${SYNC_FAIL:-0}" == 1 && "$1 $2" == 'codex check' ]]; then
  echo 'fixture check failure'
  exit 1
fi
PFM
cp "$BIN_DIR/pfm" "$DEV_BIN"
cat > "$BIN_DIR/git" <<'GIT'
#!/usr/bin/env bash
[[ "${SYNC_GIT_MODE:-ok}" != unavailable ]] || exit 127
[[ "$*" == "-C $CLAUDE_PROJECT_DIR log -1 --format=%ct -- pfm" ]] || exit 2
printf '1700000000\n'
GIT
chmod +x "$BIN_DIR/pfm" "$BIN_DIR/git" "$DEV_BIN"

FAILURES=0
for CASE in stale fresh equal git-unavailable stale-block stale-warning; do
  MTIME=1699999999; EXPECTED=pfm; GIT_MODE=ok; FAIL_STAGE=0; STOP_ACTIVE=false
  case "$CASE" in
    fresh) MTIME=1700000001; EXPECTED=pfm-dev-bin ;;
    equal) MTIME=1700000000; EXPECTED=pfm-dev-bin ;;
    git-unavailable) MTIME=1700000001; GIT_MODE=unavailable ;;
    stale-block) FAIL_STAGE=1 ;;
    stale-warning) FAIL_STAGE=1; STOP_ACTIVE=true ;;
  esac
  touch -d "@$MTIME" "$DEV_BIN"
  touch "$FLAG"
  : > "$CALLS"
  STATUS=0
  OUTPUT=$(printf '{"stop_hook_active":%s}\n' "$STOP_ACTIVE" | \
    HOME="$HOME_DIR" PATH="$BIN_DIR:/usr/bin:/bin" CLAUDE_PROJECT_DIR="$REPO" \
    SYNC_CALLS="$CALLS" SYNC_GIT_MODE="$GIT_MODE" SYNC_FAIL="$FAIL_STAGE" \
    bash "$HOOK" sync 2>&1) || STATUS=$?
  EXPECTED_CALLS=$(printf '%s %s %s\n' "$EXPECTED" 'codex build' "$REPO" \
    "$EXPECTED" 'codex check' "$REPO" "$EXPECTED" 'opencode build' "$REPO" \
    "$EXPECTED" 'opencode check' "$REPO")
  if [[ "$STATUS" != 0 || "$(cat "$CALLS")" != "$EXPECTED_CALLS" ]]; then
    printf 'FAIL codex-sync %s: expected all four stages through %s, rc=%s; calls:\n%s\n' \
      "$CASE" "$EXPECTED" "$STATUS" "$(cat "$CALLS")" >&2
    FAILURES=$((FAILURES + 1))
    continue
  fi
  if [[ "$FAIL_STAGE" == 1 ]]; then
    if [[ "$STOP_ACTIVE" == true ]]; then
      MESSAGE=$(printf '%s' "$OUTPUT" | jq -er '.systemMessage' 2>/dev/null) || MESSAGE=
    else
      MESSAGE=$(printf '%s' "$OUTPUT" | jq -er 'select(.decision == "block") | .reason' 2>/dev/null) || MESSAGE=
    fi
    if [[ "$MESSAGE" != *"$BIN_DIR/pfm"* || "$MESSAGE" != *'codex check'* || ! -f "$FLAG" ]]; then
      printf 'FAIL codex-sync %s: failure must name installed binary and failed stage and retain flag; message: %s\n' "$CASE" "$MESSAGE" >&2
      FAILURES=$((FAILURES + 1))
      continue
    fi
  elif [[ -n "$OUTPUT" || -f "$FLAG" ]]; then
    printf 'FAIL codex-sync %s: successful stages must clear flag silently; output: %s\n' "$CASE" "$OUTPUT" >&2
    FAILURES=$((FAILURES + 1))
    continue
  fi
  printf 'PASS codex-sync %s: all four stages through %s\n' "$CASE" "$EXPECTED"
done
[[ "$FAILURES" == 0 ]]
