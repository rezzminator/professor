#!/usr/bin/env bash
# Every scenario file (D8): header fields, row ids, caps against the budget,
# beat ids unique, and a lab_scenario body made of lab verbs only.
set -uo pipefail
# shellcheck source=testlib.sh
source "$(dirname "$0")/testlib.sh"
lab_lib run verdict
files=("$LAB_DIR"/scenarios/*/*.sh)
expect_eq 'ten scenario files' "${#files[@]}" 10
total=0
for f in "${files[@]}"; do
  id=${f#"$LAB_DIR"/scenarios/}; id=${id%.sh}
  facts=$( (
    lab_options() { LAB_OPTIONS=$1; }
    # shellcheck source=/dev/null
    source "$f"
    LAB_OPTIONS=${LAB_OPTIONS:-\{\}}
    jq -en --arg o "$LAB_OPTIONS" '$o | fromjson' >/dev/null || echo 'BAD options'
    # sub-agent-compact reads no options JSON: its options are SUB_AGENT_COMPACT_* variables in LAB_ENV, each held at run
    # time to the plugin under test (lab's ERROR option-unknown)
    [[ $LAB_PLUGIN != sub-agent-compact || $LAB_OPTIONS == '{}' ]] || echo 'BAD options JSON for sub-agent-compact'
    declare -F lab_scenario >/dev/null || echo 'BAD no lab_scenario'
    printf '%s|%s|%s|%s|%s|%s|%s\n' "$LAB_PLUGIN" "${LAB_ROWS[*]}" "$LAB_MODEL" "$LAB_CAP_USD" "$LAB_EST_USD" "$LAB_WALL_S" "$LAB_LOGGER"
    declare -f lab_scenario
  ) 2>&1 )
  IFS='|' read -r plugin rows model cap est wall logger <<< "$(grep -m1 '|' <<< "$facts")"
  expect_lacks "$id loads" "$facts" BAD
  expect_eq "$id plugin is its directory" "$plugin" "${id%%/*}"
  expect_eq "$id rows" "$(tr ' ' '\n' <<< "$rows" | grep -cvE '^R[0-9]+[a-n]?$')" 0
  expect_eq "$id model, wall, logger" "$([[ -n $model && $wall =~ ^[0-9]+$ && $logger =~ ^(yes|no)$ ]] && echo ok)" ok
  expect_eq "$id 0 < est <= cap" "$(awk -v e="$est" -v c="$cap" 'BEGIN { print (e > 0 && e <= c) ? "ok" : "bad" }')" ok
  total=$(awk -v t="$total" -v c="$cap" 'BEGIN { printf "%.2f", t + c }')
  body=$(sed -n '/^lab_scenario ()/,$p' <<< "$facts")
  words=$(awk 'NR > 2 { sub(/^[ \t]+/, ""); sub(/;$/, ""); print $1 }' <<< "$body" | grep -vE '^(\{|\}|for|do|done|if|then|else|fi|local|\(\(|$)' | grep -vE '^[A-Za-z_]+=' | sort -u)
  stray=$(comm -23 <(printf '%s\n' "$words") <(tr ' ' '\n' <<< "$LAB_VERBS" | sort -u) | tr '\n' ' ')
  expect_eq "$id uses lab verbs only" "$stray" ''
  verbs=$(grep -oE 'beat [a-z0-9-]+( --[a-z]+ [^ ]+)* -- [a-z_]+' <<< "$body" | awk '{ print $NF }' | sort -u)
  stray=$(comm -23 <(printf '%s\n' "$verbs") <(tr ' ' '\n' <<< "$LAB_VERBS" | sort -u) | tr '\n' ' ')
  expect_eq "$id beat verbs are lab verbs" "$stray" ''
  expect_eq "$id beat slugs unique" "$(grep -oE 'beat [a-z0-9-]+' <<< "$body" | sort | uniq -d)" ''
done
expect_eq "caps sum $total within the 10.00 budget" "$(awk -v t="$total" 'BEGIN { print (t <= 10) ? "ok" : "over" }')" ok
expect_eq 'lab list line' "$("$LAB_DIR/lab" list | grep '^sub-agent-compact/long-session ')" 'sub-agent-compact/long-session R4 R7a R7k sonnet est 0.80 cap 1.80'
gaps=$("$LAB_DIR/lab" list --gaps)
for g in R6 R7b R7c R7d R7h R7i R7l R7m R2; do expect_has "gap $g listed" "$gaps" "$g GAP "; done
expect_has 'gap reason' "$gaps" 'R7b GAP config-dir switch of a live parent is forbidden by ruling; unit tests carry it'
# S8's wall bounds its run and its token need (lib/auth.sh lab_auth_check): ceil(600/60) + 10 = 20 min
lab_lib auth
s8wall=$( (lab_options() { :; }; source "$LAB_DIR/scenarios/sub-agent-compact/unseen-reset.sh"; printf '%s' "$LAB_WALL_S") )
LAB_AUTH_JSON=$(printf '{"claudeAiOauth":{"expiresAt":%s}}' "$(( ($(date +%s) + 19 * 60) * 1000 ))")
expect_has 'S8 needs 20 minutes of token' "$(lab_auth_check "$s8wall" 2>&1)" 'auth-expiring 19 min left, need 20'
# Execute the shipped scenario against the producer's error row, without a live chat or pane.
logger_checks=$( (
  lab_lib observe
  lab_options() { :; }
  # shellcheck source=/dev/null
  source "$LAB_DIR/scenarios/auto-effort-control/logger-ready.sh"
  LAB_RUN=$T/logger-ready; LAB_SCENARIO='logger-ready'
  LAB_STARTED=2026-01-01T00:00:00Z; LAB_EXPECT_ERRORS=()
  mkdir -p "$LAB_RUN/logs/$LAB_PLUGIN"
  printf '%s\n' '{"ts":"2026-01-01T00:00:01Z","plugin":"auto-effort-control","level":"error","event":"option-error","msg":"floor is invalid","where":"hooks/auto-effort-control.ts#start"}' > "$LAB_RUN/logs/$LAB_PLUGIN/errors.jsonl"
  : > "$LAB_RUN/logs/$LAB_PLUGIN/warnings.jsonl"
  turn() { return 0; }
  pane_lacks() { return 0; }
  lab_scenario >/dev/null
  vital_no_new_errors >/dev/null
  cat "$LAB_RUN/checks.tsv"
) )
expect_eq 'logger-ready observes the producer option-error' "$(awk -F '\t' '$1 == "logger-ready/option-error-row" { print $3 }' <<< "$logger_checks")" PASS
expect_eq 'logger-ready declares its induced option-error' "$(awk -F '\t' '$1 == "vital/no-new-errors" { print $3 }' <<< "$logger_checks")" PASS
finish
