#!/usr/bin/env bash
# Fixture-driven tests for infra/fence/engine-sync.sh: engine_sync_build builds
# the mounted engine-sync source's ./cmd/engine-sync onto <out> through a
# temporary name, and an absent source or a failed build is a named failure
# that leaves no binary behind. go is a shell function, so nothing compiles.
#
#   bash infra/fence/lanes/tests/engine-sync_test.sh
#   ENGINE_SYNC_SH_SUT=/tmp/old/engine-sync.sh bash …/engine-sync_test.sh   # red-first
#
# BROKEN STATE: a missing SUT exits 2 before any assertion.
set -uo pipefail

SUT="${ENGINE_SYNC_SH_SUT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)/engine-sync.sh}"
SHTEST_TAG=engine-sync-build-test
# shellcheck source=/dev/null
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "engine-sync_test: no engine-sync.sh at $SUT" >&2; exit 2; }
# shellcheck source=/dev/null
source "$SUT"
unset PFM_DEV_ENGINE_SYNC_MISSING

SRC="$T/src"
mkdir -p "$SRC/cmd/engine-sync"
printf 'module example.test/engine-sync\n' >"$SRC/go.mod"
# The stub records its arguments and GOTOOLCHAIN, writes a binary to the -o
# path, and with STUB_GO_RC set leaves that file half-written and fails.
go() {
  local out="" prev=""
  for arg in "$@"; do [ "$prev" = -o ] && out="$arg"; prev="$arg"; done
  printf 'go %s | GOTOOLCHAIN=%s\n' "$*" "${GOTOOLCHAIN-unset}" >>"$T/go.calls"
  printf '#!/bin/sh\necho built\n' >"$out"
  chmod +x "$out"
  return "${STUB_GO_RC:-0}"
}

# 1 — a build lands on <out> under a temporary name in its directory, with the local toolchain.
OUT="$T/out-ok/engine-sync"
: >"$T/go.calls"
ENGINE_SYNC_FENCE_SRC="$SRC" engine_sync_build "$OUT" >"$T/ok.out" 2>&1
rc=$?
call="$(cat "$T/go.calls")"
left="$(cd "$T/out-ok" && ls -A)"
if [ "$rc" -eq 0 ] && [ -x "$OUT" ] && [ "$left" = engine-sync ] &&
  [[ "$call" =~ ^"go -C $SRC build -o $T/out-ok/engine-sync."[A-Za-z0-9]+" ./cmd/engine-sync | GOTOOLCHAIN=local"$ ]]; then
  ok 'engine_sync_build builds ./cmd/engine-sync to a temporary name beside <out>, then renames it onto <out>'
else bad 'engine_sync_build success' "rc=$rc" "call: $call" "left: $left" "$(cat "$T/ok.out")"; fi

# 2 — the source fence-env.sh found absent is named, and go never runs.
: >"$T/go.calls"
OUT="$T/out-missing/engine-sync"
err="$(PFM_DEV_ENGINE_SYNC_MISSING='/host/plugins/engine-sync: no go.mod' ENGINE_SYNC_FENCE_SRC="$SRC" engine_sync_build "$OUT" 2>&1)"
rc=$?
if [ "$rc" -eq 1 ] && [ "$err" = 'engine-sync: source unavailable: /host/plugins/engine-sync: no go.mod' ] &&
  [ ! -s "$T/go.calls" ] && [ ! -e "$OUT" ]; then
  ok 'an absent source is named from PFM_DEV_ENGINE_SYNC_MISSING, exit 1, nothing built'
else bad 'absent source' "rc=$rc" "err: $err" "calls: $(cat "$T/go.calls")"; fi

# 3 — a mount without go.mod is named by its path, and go never runs.
: >"$T/go.calls"
mkdir -p "$T/empty"
err="$(ENGINE_SYNC_FENCE_SRC="$T/empty" engine_sync_build "$OUT" 2>&1)"
rc=$?
if [ "$rc" -eq 1 ] && [ "$err" = "engine-sync: source unavailable: $T/empty has no go.mod" ] &&
  [ ! -s "$T/go.calls" ] && [ ! -e "$OUT" ]; then
  ok 'a source directory without go.mod is named, exit 1, nothing built'
else bad 'source without go.mod' "rc=$rc" "err: $err" "calls: $(cat "$T/go.calls")"; fi

# 4 — a failed build is named and leaves neither <out> nor its temporary file.
: >"$T/go.calls"
OUT="$T/out-fail/engine-sync"
err="$(STUB_GO_RC=3 ENGINE_SYNC_FENCE_SRC="$SRC" engine_sync_build "$OUT" 2>&1)"
rc=$?
left="$(cd "$T/out-fail" 2>/dev/null && ls -A)"
if [ "$rc" -eq 1 ] && [ "$err" = "engine-sync: build FAILED from $SRC (go exit 3)" ] && [ -z "$left" ]; then
  ok 'a failed go build is named with its exit status and leaves no binary behind'
else bad 'failed build' "rc=$rc" "err: $err" "left: $left"; fi

# 5 — a rebuild replaces the binary already at <out>.
OUT="$T/out-ok/engine-sync"
printf 'old\n' >"$OUT"
ENGINE_SYNC_FENCE_SRC="$SRC" engine_sync_build "$OUT" >/dev/null 2>&1
if grep -q 'echo built' "$OUT" && [ "$(cd "$T/out-ok" && ls -A)" = engine-sync ]; then
  ok 'a rebuild replaces the binary already at <out>'
else bad 'rebuild' "$(cat "$OUT")" "$(cd "$T/out-ok" && ls -A)"; fi

shtest_end
