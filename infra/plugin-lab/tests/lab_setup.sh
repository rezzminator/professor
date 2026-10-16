#!/usr/bin/env bash
# Shared by the end-to-end lab suites (lab_test.sh, lab_errors_test.sh, lab_violations_test.sh): the hermetic run root, the
# fake live store, plugin and credentials, and runlab/row. Each suite sources it into its own temp root,
# so the gate runs the suites side by side.
# shellcheck source=testlib.sh
source "$(dirname "${BASH_SOURCE[0]}")/testlib.sh"
export HOME=$T/home LAB_ROOT=$T/r LAB_SCENARIOS=$LAB_DIR/tests/fixtures/scenarios
export LAB_SECURITY=$FAKES/security LAB_METER=$FAKES/meter FAKE_SECURITY_LOG=$T/sec.log FAKE_SECURITY_JSON=$T/creds.json
export ANTHROPIC_BASE_URL=http://gateway.invalid CLAUDE_CONFIG_DIR=$T/live TMUX=$T/outer,1,0
mkdir -p "$HOME" "$T/live/plugins" "$T/plugin/.claude-plugin" "$T/flight"
printf '{"version":2,"plugins":{"fake-plugin@fake-mkt":[],"other@fake-mkt":[]}}' > "$T/live/plugins/installed_plugins.json"
printf '{"name":"fake-plugin","version":"1.2.3"}' > "$T/plugin/.claude-plugin/plugin.json"
printf 'fake plugin\nreads FAKE_PLUGIN_FLAG\n' > "$T/plugin/README.md"
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
