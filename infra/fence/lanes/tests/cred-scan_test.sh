#!/usr/bin/env bash
# Registered fixtures are the only credential bodies the lanes admit.
set -uo pipefail
LANES="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${CRED_SCAN_SUT:-$LANES/cred-scan.sh}"
SHTEST_TAG=lane-cred-scan-test
# shellcheck source=/dev/null
source "$LANES/../../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "cred-scan_test: missing scanner $SUT" >&2; exit 2; }
FIXTURES="$T/fixtures"
mkdir -p "$FIXTURES" "$T/root"
cp "$LANES/fixtures/"*.json "$FIXTURES/"
run_scan() { OUT="$(bash "$SUT" --fixtures "$FIXTURES" "$@" 2>&1)"; RC=$?; }

run_scan "$T/root"
if [ "$RC" -eq 0 ] && grep -q 'clean — 0 credential file(s)' <<<"$OUT"; then ok "empty root is enumerated clean"
else bad "empty root" "rc=$RC" "$OUT"; fi
for f in "$FIXTURES/"*.json; do
  mkdir -p "$T/root/$(basename "$f" .json)"
  jq . "$f" >"$T/root/$(basename "$f" .json)/auth.json"
done
run_scan "$T/root"
if [ "$RC" -eq 0 ] && grep -q 'clean — 4 credential file(s), every one a registered fixture' <<<"$OUT"; then ok "four re-serialised fixtures pass"
else bad "registered fixtures" "rc=$RC" "$OUT"; fi

printf '{"claudeAiOauth":{"accessToken":"sk-ant-oat01-invented-refusal"}}\n' >"$T/root/.credentials.json"
printf '{"tokens":{"access_token":"invented-private-token"}}\n' >"$T/root/auth.json"
cp "$T/root/.credentials.json" "$T/root/.credentials.json.bak"
run_scan "$T/root"
if [ "$RC" -eq 1 ] && [ "$(grep -c 'CREDENTIAL-REFUSED' <<<"$OUT")" -eq 3 ] &&
  grep -qF "$T/root/.credentials.json.bak — not a registered fixture" <<<"$OUT" &&
  ! grep -qE 'sk-ant-oat01|invented-private-token|clean' <<<"$OUT"; then
  ok "Claude, auth and renamed credentials are refused by path without contents"
else bad "credential refusal" "rc=$RC" "$OUT"; fi

run_scan "$T/absent"
if [ "$RC" -eq 2 ] && grep -q 'SCAN-FAILED' <<<"$OUT" && ! grep -q clean <<<"$OUT"; then ok "missing root is a scan failure"
else bad "missing root" "rc=$RC" "$OUT"; fi
old="$FIXTURES"; FIXTURES="$T/no-fixtures"
run_scan "$T/root"
if [ "$RC" -eq 2 ] && grep -q 'SCAN-FAILED' <<<"$OUT" && ! grep -q clean <<<"$OUT"; then ok "missing fixtures fail closed"
else bad "missing fixtures" "rc=$RC" "$OUT"; fi
mkdir -p "$FIXTURES"
run_scan "$T/root"
if [ "$RC" -eq 2 ] && grep -q 'SCAN-FAILED' <<<"$OUT" && ! grep -q clean <<<"$OUT"; then ok "empty fixtures fail closed"
else bad "empty fixtures" "rc=$RC" "$OUT"; fi
FIXTURES="$old"
mkdir -p "$T/no-jq" "$T/broken-find"
ln -s "$(command -v dirname)" "$T/no-jq/dirname"
OUT="$(PATH="$T/no-jq" /bin/bash "$SUT" --fixtures "$FIXTURES" "$T/root" 2>&1)"; RC=$?
if [ "$RC" -eq 2 ] && grep -q 'SCAN-FAILED.*jq' <<<"$OUT" && ! grep -q clean <<<"$OUT"; then ok "missing jq fails closed"
else bad "missing jq" "rc=$RC" "$OUT"; fi
printf '#!/bin/sh\nexit 1\n' >"$T/broken-find/find"
chmod +x "$T/broken-find/find"
OUT="$(PATH="$T/broken-find:$PATH" bash "$SUT" --fixtures "$FIXTURES" "$T/root" 2>&1)"; RC=$?
if [ "$RC" -eq 2 ] && grep -q 'SCAN-FAILED' <<<"$OUT" && ! grep -q clean <<<"$OUT"; then ok "unreadable enumeration fails closed"
else bad "unreadable enumeration" "rc=$RC" "$OUT"; fi
shtest_end
