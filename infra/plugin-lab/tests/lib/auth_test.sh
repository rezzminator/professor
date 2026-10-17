#!/usr/bin/env bash
# lib/auth.sh: service name (L3), expiry (L2), keychain clean (L13), read errors.
set -uo pipefail
# shellcheck source=../testlib.sh
source "$(dirname "$0")/../testlib.sh"
lab_lib run auth

dir=$T/cc/2
expect_eq 'service for a non-default dir' "$(lab_auth_service "$dir")" "Claude Code-credentials-$(sha256_of "$dir" | cut -c1-8)"
expect_eq 'service for the default dir' "$(HOME=$T/h lab_auth_service "$T/h/.claude")" 'Claude Code-credentials'

now_ms=$(( $(date +%s) * 1000 ))
creds() { # <expiresAt ms>
  printf '{"claudeAiOauth":{"accessToken":"lab-canary-7f3e","refreshToken":"lab-refresh-0000","expiresAt":%s,"scopes":["user:inference","user:profile"],"subscriptionType":"max","rateLimitTier":"tier_x"}}' "$1" > "$T/creds.json"
}
export LAB_SECURITY=$FAKES/security FAKE_SECURITY_JSON=$T/creds.json FAKE_SECURITY_LOG=$T/sec.log
creds $(( now_ms + 20 * 60000 ))
lab_auth_read "$dir"; out=$(lab_auth_check 1500 2>&1); rc=$?
expect_eq 'L2 expiring rc' "$rc" 2
expect_eq 'L2 expiring line' "$out" 'ERROR auth-expiring 20 min left, need 35'
creds $(( now_ms + 36 * 60000 ))
lab_auth_read "$dir"; out=$(lab_auth_check 1500 2>&1); rc=$?
expect_eq 'L2 enough time' "$rc $out" '0 auth: 36 min left'
creds $(( now_ms + 35 * 60000 ))
lab_auth_read "$dir"; expect_eq 'L2 exactly the need passes' "$(lab_auth_check 1500 >/dev/null 2>&1; echo $?)" 0
creds $(( now_ms + 34 * 60000 ))
lab_auth_read "$dir"; expect_eq 'L2 one minute short refuses' "$(lab_auth_check 1500 >/dev/null 2>&1; echo $?)" 2
expect_eq 'auth env keys carry no token' "$(lab_auth_env | sed 's/=.*//' | sort | tr '\n' ' ')" 'CLAUDE_CODE_OAUTH_SCOPES CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR CLAUDE_CODE_RATE_LIMIT_TIER CLAUDE_CODE_SUBSCRIPTION_TYPE '
expect_has 'scopes are space-joined' "$(lab_auth_env)" 'CLAUDE_CODE_OAUTH_SCOPES=user:inference user:profile'
expect_lacks 'auth env never holds the token' "$(lab_auth_env)" lab-canary-7f3e
out=$(FAKE_SECURITY_SERVICE=other lab_auth_read "$dir" 2>&1); rc=$?
expect_eq 'keychain miss' "$rc $out" "2 ERROR auth-not-found service=$(lab_auth_service "$dir")"

# token hand-off through a FIFO: the reader gets the access token, never the refresh token
mkdir -p "$T/sec"; chmod 700 "$T/sec"
lab_auth_read "$dir"; lab_auth_handoff "$T/sec/oauth.fifo"
got=$(cat "$T/sec/oauth.fifo")
expect_eq 'fifo carries exactly the access token' "$got" lab-canary-7f3e
LAB_HANDOFF_S=2 lab_auth_handoff_wait; expect_eq 'handoff wait returns once read' "$?" 0
mkfifo "$T/sec/stall.fifo"; LAB_SELF=$0; lab_auth_handoff "$T/sec/stall.fifo"
out=$(LAB_HANDOFF_S=1 lab_auth_handoff_wait 2>&1); rc=$?
expect_eq 'handoff never opened' "$rc $out" '2 ERROR auth-handoff'

# L13: only the run's own service is ever named; present → deleted → re-checked
run=$T/run; mkdir -p "$run/config"; own="Claude Code-credentials-$(sha256_of "$run/config" | cut -c1-8)"
: > "$FAKE_SECURITY_LOG"
expect_eq 'L13 absent' "$(lab_keychain_clean "$run")" 'PASS none created'
expect_eq 'L13 absent argv' "$(cat "$FAKE_SECURITY_LOG")" "find-generic-password -s $own"
: > "$FAKE_SECURITY_LOG"
expect_eq 'L13 present' "$(FAKE_SECURITY_PRESENT=1 lab_keychain_clean "$run")" 'PASS created and deleted'
expect_eq 'L13 present argv' "$(cat "$FAKE_SECURITY_LOG")" "$(printf 'find-generic-password -s %s\ndelete-generic-password -s %s\nfind-generic-password -s %s' "$own" "$own" "$own")"
: > "$FAKE_SECURITY_LOG"
out=$(FAKE_SECURITY_PRESENT=1 FAKE_SECURITY_DELETE_FAIL=1 lab_keychain_clean "$run"); rc=$?
expect_eq 'L13 delete fails' "$rc $out" '2 ERROR keychain-clean'
finish
