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

# 6-9 — engine_sync_link_stable links the plugin wrapper's stable path onto <bin>, the stable
# path resolved as pfm's enginesync.DataHome does: $ENGINE_SYNC_HOME, else
# $XDG_DATA_HOME/engine-sync, else $HOME/.local/share/engine-sync; a rerun repoints it.
BIN="$T/libexec/engine-sync"
mkdir -p "$T/libexec" && printf '#!/bin/sh\necho built\n' >"$BIN" && chmod +x "$BIN"
while IFS='|' read -r name es_home xdg want; do
  home="$T/home-$name"
  mkdir -p "$home"
  [ "$name" != relink ] || { mkdir -p "$home/.local/share/engine-sync/bin" && ln -s "$T/old" "$home/.local/share/engine-sync/bin/engine-sync"; }
  err="$(HOME="$home" ENGINE_SYNC_HOME="${es_home:+$home/$es_home}" XDG_DATA_HOME="${xdg:+$home/$xdg}" engine_sync_link_stable "$BIN" 2>&1)"
  rc=$?
  got="$(readlink "$home/$want" 2>/dev/null)"
  if [ "$rc" -eq 0 ] && [ -z "$err" ] && [ "$got" = "$BIN" ]; then
    ok "engine_sync_link_stable ($name) links ~/$want onto <bin>"
  else bad "engine_sync_link_stable ($name)" "rc=$rc" "err: $err" "~/$want -> ${got:-nothing}"; fi
done <<'CASES'
default|||.local/share/engine-sync/bin/engine-sync
xdg||xdg|xdg/engine-sync/bin/engine-sync
engine-sync-home|es|xdg|es/bin/engine-sync
relink|||.local/share/engine-sync/bin/engine-sync
CASES

# 10 — a stable directory that cannot be made is named, return 1, no link.
home="$T/home-blocked"
mkdir -p "$home/.local/share" && : >"$home/.local/share/engine-sync"
err="$(HOME="$home" ENGINE_SYNC_HOME='' XDG_DATA_HOME='' engine_sync_link_stable "$BIN" 2>&1)"
rc=$?
if [ "$rc" -eq 1 ] && [ "$(tail -1 <<<"$err")" = "engine-sync: cannot link $home/.local/share/engine-sync/bin/engine-sync to $BIN" ] &&
  [ -f "$home/.local/share/engine-sync" ]; then
  ok 'engine_sync_link_stable names a stable path it cannot create, exit 1, nothing linked'
else bad 'engine_sync_link_stable blocked parent' "rc=$rc" "err: $err"; fi

# 11 — like DataHome, a relative $ENGINE_SYNC_HOME or $XDG_DATA_HOME is ignored: the link lands
# at the default path, never under the working directory, where pfm install would not look.
home="$T/home-relative"
mkdir -p "$home"
err="$(cd "$home" && HOME="$home" ENGINE_SYNC_HOME=es XDG_DATA_HOME=xdg engine_sync_link_stable "$BIN" 2>&1)"
rc=$?
if [ "$rc" -eq 0 ] && [ -z "$err" ] && [ "$(readlink "$home/.local/share/engine-sync/bin/engine-sync")" = "$BIN" ] &&
  [ ! -e "$home/es" ] && [ ! -e "$home/xdg" ]; then
  ok 'engine_sync_link_stable ignores a relative ENGINE_SYNC_HOME and XDG_DATA_HOME, as DataHome does'
else bad 'engine_sync_link_stable relative data homes' "rc=$rc" "err: $err" "$(cd "$home" && find . -name engine-sync)"; fi

# 12 — engine_sync_build_once: the gate's mirror, agent and OpenCode steps share one binary per run.
# Three concurrent callers build it once; the others wait for it and use it.
: >"$T/go.calls"
OUT="$T/out-once/engine-sync"
for i in 1 2 3; do
  ( ENGINE_SYNC_FENCE_SRC="$SRC" engine_sync_build_once "$OUT" >"$T/once.$i.out" 2>&1; echo "$?" >"$T/once.$i.rc" ) &
done
wait
rcs="$(cat "$T/once.1.rc" "$T/once.2.rc" "$T/once.3.rc" 2>/dev/null | tr '\n' ' ')"
if [ "$rcs" = '0 0 0 ' ] && [ "$(grep -c ' build -o ' "$T/go.calls")" = 1 ] && [ -x "$OUT" ]; then
  ok 'engine_sync_build_once: three concurrent callers, one build, all exit 0'
else bad 'engine_sync_build_once concurrency' "rcs=$rcs" "calls: $(cat "$T/go.calls")" "$(cat "$T"/once.*.out 2>/dev/null)"; fi
: >"$T/go.calls"
ENGINE_SYNC_FENCE_SRC="$SRC" engine_sync_build_once "$OUT" >/dev/null 2>&1; rc=$?
if [ "$rc" = 0 ] && [ ! -s "$T/go.calls" ]; then ok 'engine_sync_build_once: a later caller reuses the built binary'
else bad 'engine_sync_build_once reuse' "rc=$rc" "calls: $(cat "$T/go.calls")"; fi
# 13 — a failed first build marks nothing: the next caller builds again, and its failure is its own.
: >"$T/go.calls"
OUT="$T/out-once-fail/engine-sync"
err="$(STUB_GO_RC=3 ENGINE_SYNC_FENCE_SRC="$SRC" engine_sync_build_once "$OUT" 2>&1)"; rc=$?
ENGINE_SYNC_FENCE_SRC="$SRC" engine_sync_build_once "$OUT" >/dev/null 2>&1; rc2=$?
if [ "$rc" = 1 ] && [ "$err" = "engine-sync: build FAILED from $SRC (go exit 3)" ] && [ "$rc2" = 0 ] &&
  [ "$(grep -c ' build -o ' "$T/go.calls")" = 2 ] && [ -x "$OUT" ]; then
  ok 'engine_sync_build_once: a failed build is named and the next caller builds again'
else bad 'engine_sync_build_once after a failure' "rc=$rc rc2=$rc2" "err: $err" "calls: $(cat "$T/go.calls")"; fi

# Lane A.09 evaluates this predicate on the first line of each installed role.
marker_pattern=$(sed -n "s/.*grep -q '\(\^# Generated by .*\)' <<<.*/\1/p" "$(dirname "$SUT")/lanes/A.sh" | tail -1)
if printf '%s\n' '# Generated by engine-sync from agents/fixture.md — edit the source, then run: engine-sync build' | grep -q "$marker_pattern" &&
  ! printf '%s\n' '# My personal role' | grep -q "$marker_pattern"; then
  ok 'Lane A.09 accepts an engine-sync role marker and rejects an unmarked role'
else bad 'Lane A.09 generated role ownership predicate' "pattern=$marker_pattern"; fi

shtest_end
