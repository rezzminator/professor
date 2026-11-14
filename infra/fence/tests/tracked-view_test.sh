#!/usr/bin/env bash
# infra/fence/tracked-view.sh against a real namespace and overlay: in the fence the command sees a
# fixture checkout without its ignored paths and its file-less untracked directories, an untracked
# file and every tracked file kept, while the tree outside the command stays whole; off the fence it
# runs as is; a list git cannot read refuses before the command runs; a bad argument exits 2. It needs
# root with CAP_SYS_ADMIN, as the shared fence container grants.
#   bash infra/fence/tests/tracked-view_test.sh
set -uo pipefail
unset PFM_DEV_REPO_GIT_DIR PFM_DEV_REPO_WORK_TREE

FENCE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${TRACKED_VIEW_SUT:-$FENCE/tracked-view.sh}"
SHTEST_TAG=fence-tracked-view-test
# shellcheck source=/dev/null
. "$FENCE/../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "tracked-view_test: no tracked-view.sh at $SUT" >&2; exit 2; }
shtest_as_root "${BASH_SOURCE[0]}" "$@"
eq() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected: $2" "actual:   $3"; fi; }

CO="$T/co"
mkdir -p "$CO/pfm/internal" "$CO/pfm/harness-prompts/generated" "$CO/pfm/tmp" "$CO/tmp/bin" "$CO/wip"
printf '/pfm.config.json\n/tmp/\n/pfm/harness-prompts/generated/\n/docs/generated/\n' > "$CO/.gitignore"
printf 'package internal\n' > "$CO/pfm/internal/a.go"
printf 'part\n' > "$CO/pfm/harness-prompts/part.md"
mkdir -p "$CO/docs/generated"
git -C "$CO" init -q && git -C "$CO" add -A && git -C "$CO" -c user.name=t -c user.email=t@example.invalid commit -qm fixture ||
  { echo "tracked-view_test: the fixture repository could not be made" >&2; exit 1; }
printf '{"ask":{"engine":"codex"}}\n' > "$CO/pfm.config.json"
printf 'planted\n' > "$CO/pfm/harness-prompts/generated/claude.md"
printf 'planted\n' > "$CO/docs/generated/x.md"
printf 'planted\n' > "$CO/tmp/bin/pfm.dev"
printf 'new\n' > "$CO/wip/new.go"
MARKER="$T/dockerenv"
: > "$MARKER"
list='cd "$1" && find . -path ./.git -prune -o -print | LC_ALL=C sort | paste -sd " " -'
whole='. ./.gitignore ./docs ./docs/generated ./docs/generated/x.md ./pfm ./pfm.config.json ./pfm/harness-prompts ./pfm/harness-prompts/generated ./pfm/harness-prompts/generated/claude.md ./pfm/harness-prompts/part.md ./pfm/internal ./pfm/internal/a.go ./pfm/tmp ./tmp ./tmp/bin ./tmp/bin/pfm.dev ./wip ./wip/new.go'

got="$(PFM_DEV_FENCE=1 TRACKED_VIEW_MARKER="$MARKER" bash "$SUT" "$CO" -- bash -c "$list" _ "$CO" 2>&1)"
eq 'fence: the command sees the tracked tree and the untracked file, no ignored path (a directory holding only ignored files whole), no file-less untracked directory' \
  '. ./.gitignore ./pfm ./pfm/harness-prompts ./pfm/harness-prompts/part.md ./pfm/internal ./pfm/internal/a.go ./wip ./wip/new.go' "$got"
eq 'fence: the tree outside the command stays whole' "$whole" "$(bash -c "$list" _ "$CO")"
got="$(PFM_DEV_FENCE=1 TRACKED_VIEW_MARKER="$MARKER" bash "$SUT" "$CO" -- bash -c 'printf "x" > "$1/pfm/internal/b.go"' _ "$CO" 2>&1)"
eq 'fence: the view is read-only' "_: line 1: $CO/pfm/internal/b.go: Read-only file system" "$got"
got="$(cd "$CO/pfm" && PFM_DEV_FENCE=1 TRACKED_VIEW_MARKER="$MARKER" bash "$SUT" "$CO" -- bash -c 'ls -A' 2>&1 | paste -sd " " -)"
eq 'fence: a cwd inside the checkout is entered again under the view' 'harness-prompts internal' "$got"

got="$(PFM_DEV_FENCE=1 TRACKED_VIEW_MARKER="$T/absent" bash "$SUT" "$CO" -- bash -c "$list" _ "$CO" 2>&1)"
eq 'off the fence: the command runs on the tree as it is' "$whole" "$got"

mkdir -p "$T/plain"
rc=0; got="$(PFM_DEV_FENCE=1 TRACKED_VIEW_MARKER="$MARKER" bash "$SUT" "$T/plain" -- bash -c 'echo RAN' 2>&1)" || rc=$?
eq 'fence: a list git cannot read exits 1 naming it, the command never run' \
  "1 tracked-view: git could not list the ignored paths of $T/plain" "$rc $(tail -n 1 <<< "$got")"

rc=0; got="$(bash "$SUT" "$CO" echo RAN 2>&1)" || rc=$?
eq 'a missing -- exits 2 with the usage line' '2 usage: tracked-view.sh CHECKOUT -- COMMAND [ARG…]' "$rc $got"

shtest_end
