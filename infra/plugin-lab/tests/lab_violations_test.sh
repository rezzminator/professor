#!/usr/bin/env bash
# End to end, hermetic, the second half of lab_test.sh: each planted violation (its FAIL or STOPPED),
# the interrupted run's ledger row, the operator verbs and the token canary.
set -uo pipefail
# shellcheck source=lab_setup.sh
source "$(dirname "$0")/lab_setup.sh"

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
FAKE_MODE=old-rows runlab hello
expect_has 'L11 no row since the start' "$(row vital/first-row)" $'FAIL\tno row since'
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
