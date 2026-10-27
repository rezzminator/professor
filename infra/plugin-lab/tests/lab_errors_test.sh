#!/usr/bin/env bash
# End to end, hermetic, the could-not-look inputs of the lab: each input lab run cannot read or trust
# exits 2 with its named ERROR. The normal run is lab_test.sh; the planted violations are
# lab_violations_test.sh.
set -uo pipefail
# shellcheck source=lab_setup.sh
source "$(dirname "$0")/lab_setup.sh"

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
expect_has 'LOG-ABSENT names the folder' "$OUT" '/logs/fake-plugin'
expect_has 'no plugin log: the options vital could not look' "$(row vital/options-ignored)" $'ERROR\tcould-not-look: no plugin log'
FAKE_OPTION=FAKE_PLUGIN_BOGUS=1 case_error option-unknown 'ERROR option-unknown FAKE_PLUGIN_BOGUS: named nowhere in ' hello
# the vital's own blind spots are ERRORs, never a PASS: a settings source the plugin could not read, a LAB_DECISIONS it cannot glob
FAKE_MODE=settings-blind runlab hello
expect_eq 'settings-blind exit 2' "$RC" 2
expect_has 'settings-blind is could-not-look' "$(row vital/options-ignored)" $'ERROR\tcould-not-look: the plugin could not read a settings source'
FAKE_DECISIONS='{run}/{sid}/decisions.jsonl' runlab hello
expect_eq 'sid-in-directory LAB_DECISIONS exit 2' "$RC" 2
expect_has 'sid-in-directory LAB_DECISIONS is could-not-look' "$(row vital/options-ignored)" $'ERROR\tcould-not-look: LAB_DECISIONS'
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

expect_eq 'canary nowhere after every case' "$(grep -rl lab-canary-7f3e "$LAB_ROOT" "$T/flight" 2>/dev/null | wc -l | tr -d ' ')" 0
finish
