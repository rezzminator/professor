#!/usr/bin/env bash
# lib/observe.sh: two-witness matcher (L15), transcript reads, plugin rows, bypass lines, process env.
set -uo pipefail
# shellcheck source=../testlib.sh
source "$(dirname "$0")/../testlib.sh"
lab_lib run drive observe
out=$(witness_match '[{"event":"session.compact","decision":"pass","tokens":91200}]' '[93000]'); rc=$?
expect_eq 'L15 1.9% matches' "$rc" 0
out=$(witness_match '[{"tokens":85000}]' '[93000]'); rc=$?
expect_eq 'L15 8.6% fails' "$rc" 1
expect_eq 'L15 8.6% names both sides' "$out" "$(printf 'FAIL unmatched plugin row tokens=85000\nFAIL unmatched boundary preTokens=93000')"
out=$(witness_match '[]' '[93000]'); rc=$?
expect_eq 'L15 boundary with no row' "$rc $out" '1 FAIL boundary without plugin row preTokens=93000'
out=$(witness_match '[{"tokens":91000}]' '[]'); rc=$?
expect_eq 'row with no boundary' "$rc $out" '1 FAIL plugin row without boundary tokens=91000'
expect_eq '4.9% matches' "$(witness_match '[{"tokens":95100}]' '[100000]' >/dev/null; echo $?)" 0
expect_eq '5.1% fails' "$(witness_match '[{"tokens":94900}]' '[100000]' >/dev/null; echo $?)" 1
expect_eq 'compact-unseen rows use before' "$(witness_match '[{"event":"compact-unseen","before":93000}]' '[93000]' >/dev/null; echo $?)" 0
out=$(witness_match '[{"event":"session.compact"}]' '[93000]'); rc=$?
expect_eq 'a row without tokens is could-not-look' "$rc ${out:0:24}" '2 ERROR witness-unreadable'

tr=$T/t.jsonl
printf '{"type":"user"}\n{"type":"system","subtype":"compact_boundary","compactMetadata":{"preTokens":93000}}\n' > "$tr"
expect_eq 'boundaries' "$(transcript_boundaries "$tr")" '[93000]'
printf 'not json\n' >> "$tr"
out=$(transcript_boundaries "$tr" 2>&1); rc=$?
expect_eq 'unreadable line named' "$rc $out" "2 ERROR transcript-unreadable $tr:3"
# the engine witness of a boundary is the last reply's context before it, the tokens the compaction ask carries;
# preTokens adds the tool results read since
printf '%s\n' '{"type":"assistant","message":{"usage":{"input_tokens":3,"cache_read_input_tokens":69000,"cache_creation_input_tokens":984}}}' \
  '{"type":"system","subtype":"compact_boundary","compactMetadata":{"preTokens":80729}}' \
  '{"type":"assistant","message":{"usage":{"input_tokens":60077}}}' \
  '{"type":"system","subtype":"compact_boundary","compactMetadata":{"preTokens":65428}}' > "$T/ask.jsonl"
expect_eq 'boundary witness is the last reply before it' "$(transcript_boundaries "$T/ask.jsonl")" '[69987,60077]'

# plugin rows over a synthetic decision log (run id synthetic)
LAB_RUN=$T/synthetic; LAB_PLUGIN=p; LAB_SID=synthetic
mkdir -p "$LAB_RUN/logs/p/decisions"
printf '{"event":"start"}\n{"event":"nudge","agentId":"a1","level":1}\n{"event":"session.compact","decision":"skip"}\n{"event":"nudge","agentId":"a2","level":2}\n' > "$LAB_RUN/logs/p/decisions/synthetic.jsonl"
expect_eq 'rows by filter' "$(decision_rows 'select(.event=="nudge" and .agentId=="a2") | .level')" 2
out=$(LAB_SID=other decision_rows . 2>&1); rc=$?
expect_eq 'absent log is LOG-ABSENT' "$rc $out" "2 ERROR LOG-ABSENT $LAB_RUN/logs/p/decisions/other.jsonl"
: > "$LAB_RUN/logs/p/decisions/empty.jsonl"
LAB_SID=empty decision_first start; rc=$?
expect_eq 'an empty decision log has no line 1' "$rc $LAB_EV" "2 could-not-look $LAB_RUN/logs/p/decisions/empty.jsonl is empty"
printf 'not json\n' > "$LAB_RUN/logs/p/decisions/torn.jsonl"
LAB_SID=torn decision_first start; rc=$?
expect_eq 'an unparseable line 1 is could-not-look' "$rc ${LAB_EV%%:*}" "2 unreadable $LAB_RUN/logs/p/decisions/torn.jsonl line 1"
printf '\n{"event":"start"}\n' > "$LAB_RUN/logs/p/decisions/blank.jsonl"
LAB_SID=blank decision_first start; rc=$?
expect_eq 'a blank line 1 is could-not-look' "$rc $LAB_EV" "2 could-not-look $LAB_RUN/logs/p/decisions/blank.jsonl line 1 has no event"
printf '{"event":"plugin-log-failed"}\n' > "$LAB_RUN/decisions.synthetic.jsonl"
expect_eq 'LAB_DECISIONS expands {run} and {sid}' "$(LAB_DECISIONS='{run}/decisions.{sid}.jsonl' decision_rows '.event' 2>&1)" plugin-log-failed
printf '{"event":"said","level":"info"}\n' > "$LAB_RUN/logs/p/info.jsonl"
expect_eq 'level rows' "$(level_rows info '.event')" said
# the logger writes a level file at its first row: none in a log folder holding rows holds no row (S7 live)
LAB_EV=""; level_lacks warnings 'select(.event == "compact-unseen")'; rc=$?
expect_eq 'absent level file in a folder holding rows lacks the row' "$rc $LAB_EV" "0 0 rows warnings select(.event == \"compact-unseen\") (no warnings.jsonl in $LAB_RUN/logs/p, which holds rows)"
LAB_EV=""; LAB_PLUGIN=q level_lacks warnings 'select(.event == "compact-unseen")'; rc=$?
expect_eq 'absent level file with no logger output is LOG-ABSENT' "$rc $LAB_EV" "2 LOG-ABSENT $LAB_RUN/logs/q/warnings.jsonl"
LAB_EV=""; level_count info 'select(.event == "said")' -eq 1; rc=$?
expect_eq 'level count' "$rc $LAB_EV" '0 1 rows info select(.event == "said") (want -eq 1)'
level_count info 'select(.event == "said")' -eq 2; rc=$?
expect_eq 'level count off by one' "$rc" 1
level_precondition info 'select(.event == "said")' 2; rc=$?
expect_eq 'level precondition short is ERROR' "$rc $LAB_EV" '2 precondition 1 rows info select(.event == "said")'
level_precondition info 'select(.event == "said")'; rc=$?
expect_eq 'level precondition met' "$rc" 0
decision_precondition 'select(.event == "cleared")'; rc=$?
expect_eq 'decision precondition absent is ERROR' "$rc $LAB_EV" '2 precondition 0 rows select(.event == "cleared")'

# a skip decision is no compaction: only pass/handoff rows and compact-unseen rows face the boundaries
printf '{"event":"start"}\n{"event":"session.compact","party":"main","agentId":null,"decision":"skip","tokens":60000}\n{"event":"session.compact","party":"main","agentId":null,"decision":"pass","tokens":91200}\n' > "$LAB_RUN/logs/p/decisions/w.jsonl"
LAB_TRANSCRIPT=$T/w.jsonl
printf '{"type":"system","subtype":"compact_boundary","compactMetadata":{"preTokens":93000}}\n' > "$LAB_TRANSCRIPT"
LAB_SID=w witness_compactions; rc=$?
expect_eq 'skip rows are no compaction' "$rc $LAB_EV" '0 PASS 1 matched'
LAB_TRANSCRIPT=$T/none.jsonl reply_has READY; rc=$?
expect_eq 'an unreadable transcript has no reply' "$rc $LAB_EV" "2 transcript-unreadable $T/none.jsonl"

printf 'x\n2026 [DEBUG] sub-agent-compact: session.compact bypassed by cc-plugin-sec-default (tier user)\ny\n' > "$T/debug.txt"
expect_eq 'bypass lines quoted' "$(lab_bypass_lines "$T/debug.txt")" '2026 [DEBUG] sub-agent-compact: session.compact bypassed by cc-plugin-sec-default (tier user)'
out=$(lab_bypass_lines "$T/none.txt" 2>&1); rc=$?
expect_eq 'debug file absent' "$rc $out" '2 ERROR could-not-look'
expect_eq 'ps env keys after the argv' "$(lab_ps_env_keys '/x/claude --model haiku HOME=/h CLAUDE_CODE_OAUTH_SCOPES=a b TERM=x' '/x/claude --model haiku' | tr '\n' ' ')" 'CLAUDE_CODE_OAUTH_SCOPES HOME TERM '
# a read turn proves its whole Read of the corpus file (S6 live: Bash tail answered every turn, nothing filled)
LAB_TRANSCRIPT=$T/fill.jsonl
turn() { return 0; }; reply_has() { return 0; }
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"tail -n 3 /x/work/corpus/slip-01.txt"}}],"usage":{"input_tokens":10}}}' > "$LAB_TRANSCRIPT"
read_turns slip 01 01 >/dev/null; rc=$?
expect_eq 'a read turn without Read is no fill' "$rc $LAB_EV" '2 fill-not-read slip-01: Bash'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/x/work/corpus/slip-01.txt","offset":40}}],"usage":{"input_tokens":12}}}' >> "$LAB_TRANSCRIPT"
read_turns slip 01 01 >/dev/null; rc=$?
expect_eq 'a partial Read is no fill' "$rc $LAB_EV" '2 fill-not-read slip-01: Bash,Read(part)'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/x/work/corpus/slip-01.txt"}}],"usage":{"input_tokens":14}}}' >> "$LAB_TRANSCRIPT"
read_turns slip 01 01 >/dev/null; rc=$?
expect_eq 'a whole Read fills' "$rc $LAB_EV" '0 read slip-01..01, context 14'
cp "$LAB_TRANSCRIPT" "$T/torn.jsonl"; printf '{"type":\n' >> "$T/torn.jsonl"
LAB_TRANSCRIPT=$T/torn.jsonl read_turns slip 01 01 >/dev/null; rc=$?
expect_eq 'an unreadable transcript has no context' "$rc $LAB_EV" "2 transcript-unreadable $T/torn.jsonl"
fill_until_boundary slip 01 01 >/dev/null; rc=$?
expect_eq 'fill_until_boundary reads the same way' "$rc $LAB_EV" '1 no compact_boundary after slip-01'
LAB_BEAT=gone pane_lacks 'never there'; rc=$?
expect_eq 'a failed pane capture is could-not-look, never a PASS' "$rc $LAB_EV" "2 could-not-look pane capture failed $LAB_RUN/pane/gone.txt"
fill_until_boundary slip 03 01 >/dev/null; rc=$?
expect_eq 'a backwards fill range is could-not-look, never a boundary' "$rc $LAB_EV" '2 fill range slip-03..01 is empty'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"slip-02-END"}],"usage":{"input_tokens":16}}}' >> "$LAB_TRANSCRIPT"
fill_until_boundary slip 02 02 >/dev/null; rc=$?
expect_eq 'fill_until_boundary without the Read is no fill' "$rc $LAB_EV" '2 fill-not-read slip-02: no tool named it'
# a declared bypass never induced is a GAP, boundary or none: unseated, the plugin answers the ask itself (S8)
LAB_EXPECT_BYPASS=('sub-agent-compact: session.compact bypassed by') LAB_GAP_IF_NOT_INDUCED='seat absent'
fill_until_boundary slip 01 01 >/dev/null; rc=$?
expect_eq 'no debug log is could-not-look, never a GAP' "$rc $LAB_EV" "2 could-not-look $LAB_RUN/debug.txt"
printf '2026 [DEBUG] fake boot\n' > "$LAB_RUN/debug.txt"
fill_until_boundary slip 01 01 >/dev/null; rc=$?
expect_eq 'an uninduced bypass without a boundary is a GAP' "$rc $LAB_EV" '3 not-induced seat absent'
rm -f "$LAB_RUN/debug.txt"
# the seat is read at boot, before any fill is paid for
sec=cc-plugin-sec-default@builtin
bypass_seated "$sec"; rc=$?
expect_eq 'no debug log is could-not-look' "$rc $LAB_EV" "2 could-not-look $LAB_RUN/debug.txt"
printf '2026 [DEBUG] cc-plugin-sec-default@builtin not seated: managed prependPlugins does not list it\n' > "$LAB_RUN/debug.txt"
bypass_seated "$sec"; rc=$?
expect_eq 'an unseated builtin is a GAP' "$rc $LAB_EV" '3 not-induced seat absent: cc-plugin-sec-default@builtin not seated: managed prependPlugins does not list it'
printf '2026 [DEBUG] plugin.register: cc-plugin-sec-default (builtin, cc-plugin-sec-default@builtin), judged by core alone: admitted\n' > "$LAB_RUN/debug.txt"
bypass_seated "$sec"; rc=$?
expect_eq 'a registered builtin is seated' "$rc $LAB_EV" '0 seated: plugin.register: cc-plugin-sec-default (builtin, cc-plugin-sec-default@builtin), judged by core alone: admitted'
printf '2026 [DEBUG] fake boot\n' > "$LAB_RUN/debug.txt"
bypass_seated "$sec"; rc=$?
expect_eq 'neither line is seat-unknown' "$rc $LAB_EV" "2 seat-unknown $sec in $LAB_RUN/debug.txt"
LAB_EXPECT_BYPASS=() LAB_GAP_IF_NOT_INDUCED=''
finish
