#!/usr/bin/env bash
# End to end, hermetic: the real `lab run` with a real private tmux server and the
# fake claude, security and meter. One case per could-not-look input (its named
# ERROR) and per planted violation (its FAIL or STOPPED); a canary for the token.
# This file holds the normal run and the budget refusal; the could-not-look inputs are
# lab_errors_test.sh, the planted violations, the cap stop and the operator verbs lab_violations_test.sh.
set -uo pipefail
# shellcheck source=lab_setup.sh
source "$(dirname "$0")/lab_setup.sh"

# normal run: every passport and vital check PASS; the token reached fd 3 and nowhere else
runlab hello
expect_eq 'normal run exits 0' "$RC" 0
expect_has 'preflight line' "$OUT" 'auth: 300 min left · budget: 10.00 left of 10.00 · cap 0.25 · run '
for c in passport/env-allowlist passport/live-store-untouched passport/keychain-clean passport/hook-bypass passport/identity passport/installed-copy-off passport/spend vital/first-row vital/no-new-errors vital/options-ignored hello/ready; do
  expect_has "$c PASS" "$(row "$c")" PASS
done
expect_eq 'L4 fd 3 carried the token' "$(cat "$RUN/fake/fd3.sha" 2>&1)" "$(sha256_of lab-canary-7f3e)"
expect_eq 'L4 fd variable' "$(cat "$RUN/fake/fd-var" 2>&1)" 3
expect_eq 'L4 canary nowhere on disk' "$(grep -rl lab-canary-7f3e "$RUN" "$T/flight" 2>/dev/null | wc -l | tr -d ' ')" 0
expect_eq 'L4 fifo removed' "$([[ -e $RUN/sec/oauth.fifo ]] && echo present || echo gone)" gone
keys=$(tr '\n' ' ' < "$RUN/fake/env-keys" 2>&1)
expect_eq 'L5 env is exactly the allowlist plus LAB_ENV' "$keys" 'CLAUDE_CODE_DISABLE_CLAUDE_MDS CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC CLAUDE_CODE_ENABLE_FUNCTION_HOOKS CLAUDE_CODE_MANAGED_SETTINGS_PATH CLAUDE_CODE_OAUTH_SCOPES CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR CLAUDE_CODE_RATE_LIMIT_TIER CLAUDE_CODE_SUBSCRIPTION_TYPE CLAUDE_CODE_TMPDIR CLAUDE_CONFIG_DIR DISABLE_AUTOUPDATER ENABLE_CLAUDEAI_MCP_SERVERS FAKE_CLAUDE_MODE FAKE_LIVE HOME LANG PATH PLUGIN_LOG_DIR TEMP TERM TMP TMPDIR XDG_CACHE_HOME XDG_CONFIG_HOME XDG_DATA_HOME XDG_STATE_HOME '
expect_has 'L5 passport counts the keys' "$(row passport/env-allowlist)" '26 keys'
expect_has 'L12 two stores searched' "$(row passport/live-store-untouched)" '2 stores'
expect_has 'L13 nothing created' "$(row passport/keychain-clean)" 'none created'
expect_has 'D10 live id off' "$(jq -c .enabledPlugins "$RUN/settings.json")" '"fake-plugin@fake-mkt":false'
expect_eq 'ledger: the RUNNING row, then the final row' "$(cut -f2,3,6 "$LEDGER")" "$(printf 'hello\tfake-plugin\tRUNNING\nhello\tfake-plugin\tPASS')"
expect_eq 'the sum takes the final row in place of the RUNNING one' "$(source "$LAB_DIR/lib/run.sh"; lab_ledger_sum "$LEDGER")" "$(cut -f7 "$LEDGER" | tail -n 1)"
expect_eq 'report copy kept beside the ledger' "$(ls "$T/flight/lab-reports" | wc -l | tr -d ' ')" 1
expect_has 'report verdict' "$OUT" 'verdict PASS · $0.01 / 0.25'
expect_has 'lab report re-renders' "$("$LAB_DIR/lab" report "$RUN")" 'verdict PASS'
expect_eq 'tmux server gone after the run' "$(tmux -S "$RUN/t.sock" ls >/dev/null 2>&1 && echo alive || echo gone)" gone

# a Stop that ends another turn (a background agent's notification) never ends the typed line's turn
FAKE_MODE=early-stop runlab queued
expect_has 'queued line waits for its own turn' "$(row queued/replied)" PASS

# a line typed just after a local slash command is dropped by the composer remount: slash waits for the
# command's output, say retypes the lost line once
FAKE_MODE=drop-after-slash runlab slash-say
expect_has 'slash evidence is the command output' "$(row slash-say/switch)" $'PASS\t/model: Set model to sonnet'
expect_has 'the dropped line is retyped' "$(row slash-say/replied)" PASS
# a slash with no awaited event still polls the meter: over the cap its beat is STOPPED, never PASS
FAKE_METER_TOTAL=0.26 runlab slash-say
expect_has 'a slash over the cap is STOPPED' "$(row slash-say/switch)" $'STOPPED\tbudget 0.26/0.25'

# the meter can run before claude writes its first transcript: the run's projects root exists from the start
FAKE_MODE=late-projects runlab hello
expect_eq 'late projects folder exit 0' "$RC" 0
expect_lacks 'late projects folder is no meter error' "$OUT" 'ERROR meter'

# a beat naming the old session id or the sub-agent id before the verb that sets it passed is blocked,
# never an unbound-variable abort
runlab agent-blocked
expect_lacks 'an unset id never aborts the run' "$OUT" 'unbound variable'
expect_has 'blocked old-session beat' "$(row agent-blocked/prev-rows)" $'ERROR\tblocked-by agent-blocked/agent'
expect_has 'blocked beat after the wait' "$(row agent-blocked/agent-rows)" $'ERROR\tblocked-by agent-blocked/agent'
expect_has 'the report still prints' "$OUT" 'verdict ERROR'

# L1: the budget refuses before anything starts
: > "$LEDGER.over"; printf 'x\ts\tp\tr\td\tPASS\t9.80\t1.00\tt\t1\tx\n' > "$LEDGER.over"
before=$(ls "$LAB_ROOT" | wc -l)
OUT=$("$LAB_DIR/lab" run fake/hello --plugin "$T/plugin" --auth-from "$T/live" --claude "$FAKES/claude" --ledger "$LEDGER.over" 2>&1); RC=$?
expect_eq 'L1 refused rc' "$RC" 2
expect_has 'L1 refused line' "$OUT" 'ERROR budget-exhausted spent=9.80 cap=0.25 budget=10.00'
expect_eq 'L1 no run folder, no socket' "$(ls "$LAB_ROOT" | wc -l)" "$before"

expect_eq 'canary nowhere after every case' "$(grep -rl lab-canary-7f3e "$LAB_ROOT" "$T/flight" 2>/dev/null | wc -l | tr -d ' ')" 0
finish
