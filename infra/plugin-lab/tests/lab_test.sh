#!/usr/bin/env bash
# End to end, hermetic: the real `lab run` with a real private tmux server and the
# fake claude, security and meter. One case per could-not-look input (its named
# ERROR) and per planted violation (its FAIL or STOPPED); a canary for the token.
set -uo pipefail
# shellcheck source=testlib.sh
source "$(dirname "$0")/testlib.sh"
export HOME=$T/home LAB_ROOT=$T/r LAB_SCENARIOS=$LAB_DIR/tests/fixtures/scenarios
export LAB_SECURITY=$FAKES/security LAB_METER=$FAKES/meter FAKE_SECURITY_LOG=$T/sec.log FAKE_SECURITY_JSON=$T/creds.json
export ANTHROPIC_BASE_URL=http://gateway.invalid CLAUDE_CONFIG_DIR=$T/live TMUX=$T/outer,1,0
mkdir -p "$HOME" "$T/live/plugins" "$T/plugin/.claude-plugin" "$T/flight"
printf '{"version":2,"plugins":{"fake-plugin@fake-mkt":[],"other@fake-mkt":[]}}' > "$T/live/plugins/installed_plugins.json"
printf '{"name":"fake-plugin","version":"1.2.3"}' > "$T/plugin/.claude-plugin/plugin.json"
printf 'fake plugin\n' > "$T/plugin/README.md"
git -C "$T/plugin" init -q && git -C "$T/plugin" add -A && git -C "$T/plugin" -c user.name=t -c user.email=t@example.invalid commit -qm fixture
creds() { # <minutes left>
  printf '{"claudeAiOauth":{"accessToken":"lab-canary-7f3e","refreshToken":"lab-refresh-0000","expiresAt":%s,"scopes":["user:inference"],"subscriptionType":"max","rateLimitTier":"tier_x"}}' \
    "$(( ($(date +%s) + $1 * 60) * 1000 ))" > "$T/creds.json"
}
creds 300
LEDGER=$T/flight/lab-ledger.tsv
runlab() { # <scenario> [lab args…]: sets OUT RC RUN
  OUT=$("$LAB_DIR/lab" run "fake/$1" --plugin "$T/plugin" --auth-from "$T/live" --claude "$FAKES/claude" --ledger "$LEDGER" "${@:2}" 2>&1); RC=$?
  RUN=$(sed -n 's/.* · run \(.*\)$/\1/p' <<< "$OUT" | head -n 1)
}
row() { awk -F '\t' -v c="$1" '$1 == c { print $3 "\t" $4 }' "$RUN/checks.tsv" 2>/dev/null; }

# normal run: every passport and vital check PASS; the token reached fd 3 and nowhere else
runlab hello
expect_eq 'normal run exits 0' "$RC" 0
expect_has 'preflight line' "$OUT" 'auth: 300 min left · budget: 10.00 left of 10.00 · cap 0.25 · run '
for c in passport/env-allowlist passport/live-store-untouched passport/keychain-clean passport/hook-bypass passport/identity passport/installed-copy-off passport/spend vital/logger-ready vital/no-new-errors hello/ready; do
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

# could-not-look inputs, each its named ERROR and exit 2
case_error() { # <name> <want line> <scenario> [lab args…]
  local name=$1 want=$2; shift 2
  runlab "$@"
  expect_eq "$name exit 2" "$RC" 2
  expect_has "$name named" "$OUT" "$want"
}
FAKE_SECURITY_SERVICE=nope case_error auth-not-found 'ERROR auth-not-found service=Claude Code-credentials-' hello
creds 20; FAKE_WALL=1500 case_error auth-expiring 'ERROR auth-expiring 20 min left, need 35' hello; creds 300
LAB_HANDOFF_S=2 case_error auth-handoff 'ERROR auth-handoff' no-launch
FAKE_MODE=no-start LAB_BOOT_S=3 case_error startup-blocked 'ERROR startup-blocked' hello
expect_has 'startup-blocked keeps the pane' "$(cat "$RUN/pane/boot.txt" 2>&1)" 'fake claude ready'
FAKE_MODE=no-input LAB_INPUT_S=3 case_error input-not-accepted 'ERROR input-not-accepted' hello
expect_eq 'input-not-accepted keeps the pane' "$([[ -s $RUN/pane/ready.txt ]] && echo kept)" kept
FAKE_MODE=slow FAKE_TURN_S=2 case_error turn-timeout 'ERROR turn-timeout' hello
FAKE_MODE=no-logger case_error LOG-ABSENT "ERROR LOG-ABSENT $LAB_ROOT/" hello
expect_has 'LOG-ABSENT names the file' "$OUT" '/logs/fake-plugin/info.jsonl'
FAKE_MODE=bad-transcript case_error transcript-unreadable 'ERROR transcript-unreadable' compact
FAKE_METER_FAIL=1 case_error meter 'ERROR meter' hello
FAKE_METER_UNPRICED=2 case_error meter-unpriced 'ERROR meter unpriced calls 2' hello
LAB_ROOT=$T/$(printf 'd%.0s' $(seq 1 80)) case_error socket-path-long 'ERROR socket-path-long' hello
LAB_BASH_MAJOR=3 LAB_BASH_CANDIDATES='' case_error bash-too-old 'ERROR bash-too-old' hello
LAB_BASH_MAJOR=4 LAB_BASH_MINOR=3 LAB_BASH_CANDIDATES='' case_error bash-4.3 'ERROR bash-too-old' hello
mkdir -p "$T/open" && chmod 0777 "$T/open"
LAB_ROOT=$T/open/r case_error unsafe-root "ERROR run-dir unsafe root" hello
expect_has 'budget without a ledger refused' "$("$LAB_DIR/lab" run fake/hello --plugin "$T/plugin" --budget 5 2>&1)" 'ERROR usage: --budget needs --ledger'
mkdir -p "$T/pr/hello-200101-000000" "$T/pr/keep-me" && touch -t 202001010000 "$T/pr/hello-200101-000000" "$T/pr/keep-me"
LAB_ROOT=$T/pr "$LAB_DIR/lab" prune --days 1 >/dev/null
expect_eq 'prune takes run folders only' "$(ls "$T/pr")" keep-me
printf 'file\n' > "$T/ro"; runlab hello --ledger "$T/ro/lab-ledger.tsv"
expect_eq 'ledger unwritable exit 2' "$RC" 2
expect_has 'ledger unwritable named' "$OUT" 'ERROR ledger'
expect_lacks 'an unwritable ledger refuses the run before any spend' "$OUT" 'verdict'

# planted violations, each its FAIL
FAKE_MODE=leak-env runlab hello
expect_eq 'leak-env exit 1' "$RC" 1
expect_has 'leak-env named' "$(row passport/env-allowlist)" $'FAIL\tCLAUDE_FAKE_LEAK'
FAKE_MODE=touch-live-store FAKE_LIVE=$T/live runlab hello
expect_has 'touch-live-store' "$(row passport/live-store-untouched)" "FAIL"
expect_has 'touch-live-store path' "$(row passport/live-store-untouched)" "$T/live/projects/x/"
FAKE_MODE=bypass-line runlab hello
expect_has 'bypass-line' "$(row passport/hook-bypass)" 'FAIL'
expect_has 'bypass-line quoted' "$(row passport/hook-bypass)" 'session.compact bypassed by cc-plugin-sec-default'
runlab compact
expect_has 'two witnesses agree' "$(row compact/compaction)" PASS
FAKE_MODE=boundary-without-row runlab compact
expect_eq 'boundary-without-row exit 1' "$RC" 1
expect_has 'boundary-without-row named' "$(row compact/compaction)" 'boundary without plugin row'
runlab tree-change
expect_has 'L16 tree changed' "$(row passport/identity)" $'FAIL\ttree-changed'
git -C "$T/plugin" checkout -q -- README.md
# D23 content hash: an already-modified file edited again keeps its status letters, never its hash
printf 'dirty before the run\n' >> "$T/plugin/README.md"
runlab tree-change
expect_has 'L16 content change with the same status letters' "$(row passport/identity)" $'FAIL\ttree-changed start'
git -C "$T/plugin" checkout -q -- README.md
# the engine hot-reloads a --plugin-dir hooks module on any edit: an already-dirty file edited again changes the content hash (S7 live)
FAKE_MODE=plugin-reload runlab hello
expect_has 'L16 hot reload is tree-changed' "$(row passport/identity)" $'FAIL\ttree-changed hot-reload'
printf '1.2.2' > "$T/plugin/fake-logger-version"; runlab hello; rm -f "$T/plugin/fake-logger-version"
expect_has 'L11 version drift' "$(row vital/logger-ready)" $'FAIL\tversion-drift'
FAKE_SECURITY_PRESENT=1 runlab hello
expect_has 'L13 created and deleted' "$(row passport/keychain-clean)" 'PASS'$'\t''created and deleted'

# L10: over the cap the claude process dies behind its identity check; a live chat on the same binary lives
FAKE_METER_TOTAL=1.81 FAKE_CAP=1.80 FAKE_MODE=slow runlab hello
expect_eq 'STOPPED exit 2' "$RC" 2
expect_has 'STOPPED named' "$OUT" 'STOPPED budget 1.81/1.80'
pid=$(cat "$RUN/fake/pid" 2>/dev/null || echo none)
expect_eq 'fake claude killed' "$(kill -0 "$pid" 2>/dev/null && echo alive || echo gone)" gone
expect_eq 'tmux server gone' "$(tmux -S "$RUN/t.sock" ls >/dev/null 2>&1 && echo alive || echo gone)" gone
# a stale claude.pid the OS gave to a live chat on the same binary: lab stop leaves it running
bash -c 'exec -a "$0" sleep 300' "$FAKES/claude --setting-sources project --settings $T/live/settings.json" & decoy=$!
printf '%s\n' "$decoy" > "$RUN/claude.pid"
"$LAB_DIR/lab" stop "$RUN" >/dev/null 2>&1
expect_eq 'live chat on the same binary survives lab stop' "$(kill -0 "$decoy" 2>/dev/null && echo alive || echo gone)" alive
ps -o args= -p "$decoy" | grep -qF "$FAKES/claude --setting-sources project --settings $T/live/settings.json 300" && kill "$decoy"

# D9: a run interrupted after boot is in the ledger at its cap (its own ledger: a run id from another root's run
# in the same second is refused in a shared one)
ILEDGER=$T/flight/interrupted.tsv
FAKE_MODE=slow LAB_ROOT=$T/r1 "$LAB_DIR/lab" run fake/hello --plugin "$T/plugin" --auth-from "$T/live" --claude "$FAKES/claude" --ledger "$ILEDGER" >/dev/null 2>&1 & labpid=$!
for _ in $(seq 1 100); do compgen -G "$T/r1/*/claude.pid" >/dev/null && break; sleep 0.1; done
ps -o args= -p "$labpid" | grep -qF "run fake/hello --plugin $T/plugin" && kill -TERM "$labpid"
wait "$labpid" 2>/dev/null
expect_eq 'interrupted run leaves one ledger row' "$(wc -l < "$ILEDGER" | tr -d ' ')" 1
expect_eq 'interrupted run counts at its cap' "$(awk -F '\t' '{ print $6, $7, $8 }' "$ILEDGER")" 'RUNNING ? 0.25'

# L18 operator verbs
expect_eq 'list' "$("$LAB_DIR/lab" list | grep '^fake/hello ')" 'fake/hello R8 haiku est 0.01 cap 0.25'
expect_has 'attach without a session' "$("$LAB_DIR/lab" attach "$T/none" 2>&1)" 'ERROR no-session'
expect_eq 'canary nowhere after every case' "$(grep -rl lab-canary-7f3e "$LAB_ROOT" "$T/flight" 2>/dev/null | wc -l | tr -d ' ')" 0
finish
