#!/usr/bin/env bash
# lib/verdict.sh: checks.tsv → report.md (L17), exit codes (D13), ledger row, beats and blocked-by.
set -uo pipefail
# shellcheck source=../testlib.sh
source "$(dirname "$0")/../testlib.sh"
lab_lib run meter verdict
LAB_RUN=$T/long-session-261015-031200; mkdir -p "$LAB_RUN"
jq -n '{run_id:"long-session-261015-031200",scenario:"long-session",plugin:"sub-agent-compact",version:"0.7.0-dev.23",rev:"abc123",dirty:"d1e2",usd:"0.83",cap:"1.80",started:"2026-10-15T03:12:00Z",wall_s:"1210",claude:"2.1.300"}' > "$LAB_RUN/meta.json"
printf 'passport/env-allowlist\tR8\tPASS\t24 keys\nvital/logger-ready\tR1\tPASS\t%s\nlong-session/skip-fold\tR4\tFAIL\t%s\n' "$LAB_RUN/logs/a" "$LAB_RUN/logs/b" > "$LAB_RUN/checks.tsv"
out=$(lab_render_report "$LAB_RUN"); rc=$?
expect_eq 'L17 exit 1 on a FAIL' "$rc" 1
r=$(cat "$LAB_RUN/report.md")
expect_eq 'stdout is the report' "$out" "$r"
expect_has 'passport block' "$r" '- passport/env-allowlist PASS 24 keys'
expect_has 'table header' "$r" '| check | row | verdict | evidence |'
expect_has 'table row 1' "$r" "| vital/logger-ready | R1 | PASS | $LAB_RUN/logs/a |"
expect_has 'table row 2' "$r" "| long-session/skip-fold | R4 | FAIL | $LAB_RUN/logs/b |"
expect_lacks 'passport rows stay out of the table' "$r" '| passport/env-allowlist'
expect_eq 'verdict line last' "$(tail -n 1 "$LAB_RUN/report.md")" 'verdict FAIL · $0.83 / 1.80'
lab_ledger_record "$T/flight/lab-ledger.tsv"
expect_eq 'L17 ledger row' "$(cat "$T/flight/lab-ledger.tsv")" "$(printf 'long-session-261015-031200\tlong-session\tsub-agent-compact\tabc123\td1e2\tFAIL\t0.83\t1.80\t2026-10-15T03:12:00Z\t1210\tlab-reports/long-session-261015-031200.md')"
expect_eq 'report copy' "$(cmp -s "$T/flight/lab-reports/long-session-261015-031200.md" "$LAB_RUN/report.md" && echo same)" same
# D23 identity: HEAD plus the path and content of every modified or untracked file under the plugin folder
g=$T/repo; mkdir -p "$g/plug/.claude-plugin" "$g/plug/src"
printf 'a\n' > "$g/plug/src/x.ts"; printf 'b\n' > "$g/plug/README.md"; printf 'top\n' > "$g/top.md"
git -C "$g" init -q && git -C "$g" add -A && git -C "$g" -c user.name=t -c user.email=t@example.invalid commit -qm fixture
clean=$(lab_identity "$g/plug")
expect_eq 'identity is rev and a 12-hex hash' "$(grep -cE '^[0-9a-f]{40} [0-9a-f]{12}$' <<< "$clean")" 1
printf 'a2\n' > "$g/plug/src/x.ts"; one=$(lab_identity "$g/plug")
printf 'a3\n' > "$g/plug/src/x.ts"; two=$(lab_identity "$g/plug")
expect_eq 'a modified file changes the hash' "$([[ $one != "$clean" ]] && echo differs)" differs
expect_eq 'same status letters, new content: the hash changes' "$([[ $two != "$one" ]] && echo differs)" differs
printf '{}\n' > "$g/plug/tsconfig.json"; mkdir -p "$g/plug/.claude-plugin/types"; printf 'x\n' > "$g/plug/.claude-plugin/types/t.d.ts"
printf 'top2\n' > "$g/top.md"
expect_eq 'engine-written tsconfig.json, .claude-plugin/types and files outside the folder leave it' "$(lab_identity "$g/plug")" "$two"
printf 'new\n' > "$g/plug/src/y.ts"; three=$(lab_identity "$g/plug")
printf 'new2\n' > "$g/plug/src/y.ts"
expect_eq 'untracked content counts' "$([[ $(lab_identity "$g/plug") != "$three" ]] && echo differs)" differs
git -C "$g" checkout -q -- . && rm -f "$g/plug/src/y.ts" "$g/plug/README.md"
expect_eq 'a deleted file changes the hash' "$([[ $(lab_identity "$g/plug") != "$clean" ]] && echo differs)" differs
git -C "$g" checkout -q -- .
expect_eq 'a restored tree hashes as clean' "$(lab_identity "$g/plug")" "$clean"
out=$(lab_identity "$T/not-a-repo" 2>&1); rc=$?
expect_eq 'git that cannot read the folder is rc 2' "$rc" 2
out=$(lab_ledger_record "$T/flight/lab-ledger.tsv/x" 2>&1); rc=$?
expect_eq 'unwritable ledger' "$rc ${out%% *}" '2 ERROR'
expect_has 'unwritable ledger named' "$out" 'ERROR ledger'
# the ledger is append-only: the final row joins the RUNNING one and the sum takes it in its place; a linked ledger stays linked
printf 'long-session-261015-031200\tlong-session\tsub-agent-compact\tabc123\td1e2\tRUNNING\t?\t1.80\tt\t?\tx\n' > "$T/real-ledger.tsv"
ln -s "$T/real-ledger.tsv" "$T/link-ledger.tsv"
lab_ledger_record "$T/link-ledger.tsv"
expect_eq 'record appends through a linked ledger' "$([[ -L $T/link-ledger.tsv ]] && echo link) $(wc -l < "$T/real-ledger.tsv" | tr -d ' ') $(lab_ledger_sum "$T/real-ledger.tsv")" 'link 2 0.83'
out=$(lab_ledger_reserve "$T/flight/lab-ledger.tsv" 1.80 10.00 2>&1); rc=$?
expect_eq 'a run id already in the ledger is refused' "$rc $out" "2 ERROR ledger already holds run id long-session-261015-031200 in $T/flight/lab-ledger.tsv"
# runs reserving together: each budget check sees the others' RUNNING rows, so 9.60 + 0.25 admits one run, never more
printf 'z\ts\tp\tr\td\tPASS\t9.60\t1.00\tt\t1\tx\n' > "$T/conc.tsv"
for i in 1 2 3 4 5 6; do
  (LAB_RUN=$T/conc-$i; mkdir -p "$LAB_RUN"; jq --arg id "conc-$i" '.run_id = $id' "$T/long-session-261015-031200/meta.json" > "$LAB_RUN/meta.json"
   lab_ledger_reserve "$T/conc.tsv" 0.25 10.00 >/dev/null 2>&1) &
done
wait
expect_eq 'concurrent reserves admit one run within the budget' "$(awk -F '\t' '$6 == "RUNNING"' "$T/conc.tsv" | wc -l | tr -d ' ')" 1
# a STOPPED run takes the final read too: the calls in flight at the kill reach its spend
mkdir -p "$LAB_RUN/config/projects"; : > "$LAB_RUN/checks.tsv"
out=$(LAB_METER=$FAKES/meter LAB_STOPPED='budget 1.81/1.80' LAB_USD=1.81 LAB_CAP=1.80; export FAKE_METER_TOTAL=1.95; passport_spend >/dev/null; printf '%s' "$LAB_USD")
expect_eq 'STOPPED spend is the final read' "$out $(cut -f3,4 "$LAB_RUN/checks.tsv")" "1.95 STOPPED"$'\t'"budget 1.81/1.80; final 1.95/1.80"
: > "$LAB_RUN/checks.tsv"

for v in 'PASS 0' 'GAP 0' 'FAIL 1' 'ERROR 2' 'STOPPED 2'; do
  printf 'a\tR1\tPASS\tx\nb\tR1\t%s\tx\n' "${v% *}" > "$LAB_RUN/checks.tsv"
  lab_render_report "$LAB_RUN" >/dev/null; expect_eq "exit for ${v% *}" "$?" "${v#* }"
done
printf 'a\tR1\tFAIL\tx\nb\tR1\tERROR\tx\n' > "$LAB_RUN/checks.tsv"
lab_render_report "$LAB_RUN" >/dev/null; expect_eq 'ERROR outranks FAIL' "$?" 2

# beats: verb rc → verdict; a needed red beat blocks; evidence travels
: > "$LAB_RUN/checks.tsv"; LAB_SCENARIO=sc; LAB_ROWS=(R4 R7a)
v_pass() { LAB_EV='seen'; return 0; }
v_fail() { LAB_EV='not seen'; return 1; }
v_gap() { LAB_EV='not induced'; return 3; }
beat one -- v_pass
beat two --row R7a -- v_fail
beat three --needs two -- v_pass
beat four -- v_gap
expect_eq 'beat rows' "$(cat "$LAB_RUN/checks.tsv")" "$(printf 'sc/one\tR4\tPASS\tseen\nsc/two\tR7a\tFAIL\tnot seen\nsc/three\tR4\tERROR\tblocked-by sc/two\nsc/four\tR4\tGAP\tnot induced')"

# a failed prelude is a named setup ERROR, and every later beat is blocked by it
: > "$LAB_RUN/checks.tsv"
lab_need_fail 'need_corpus block-01: cannot write x' >/dev/null; rc=$?
beat after -- v_pass >/dev/null
expect_eq 'setup rows' "$rc $(cat "$LAB_RUN/checks.tsv")" "2 $(printf 'sc/setup\tR4\tERROR\tneed_corpus block-01: cannot write x\nsc/after\tR4\tERROR\tblocked-by sc/setup')"
LAB_DEAD='' LAB_DEAD_VERDICT=''
finish
