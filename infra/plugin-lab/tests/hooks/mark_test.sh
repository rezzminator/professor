#!/usr/bin/env bash
# hooks/mark.sh (L8): one compact marks line per event, never a byte of stdout.
set -uo pipefail
# shellcheck source=../testlib.sh
source "$(dirname "$0")/../testlib.sh"
export LAB_MARKS=$T/marks.jsonl
out=$(printf '{"session_id":"s1","transcript_path":"/t","hook_event_name":"Stop"}' | "$LAB_DIR/hooks/mark.sh" Stop); rc=$?
expect_eq 'exit 0, no stdout' "$rc:$out" '0:'
expect_eq 'one line' "$(wc -l < "$LAB_MARKS" | tr -d ' ')" 1
expect_eq 'fields' "$(jq -c 'del(.ts)' "$LAB_MARKS")" '{"event":"Stop","session_id":"s1","transcript_path":"/t"}'
expect_eq 'ts is set' "$(jq -r '.ts | type' "$LAB_MARKS")" string
printf '{"session_id":"s1","agent_id":"a7","agent_type":"lab-echo","agent_transcript_path":"/t/a7.jsonl"}' | "$LAB_DIR/hooks/mark.sh" SubagentStop
expect_eq 'sub-agent fields' "$(tail -n 1 "$LAB_MARKS" | jq -c 'del(.ts)')" '{"event":"SubagentStop","session_id":"s1","agent_id":"a7","agent_type":"lab-echo","agent_transcript_path":"/t/a7.jsonl"}'
printf '{"session_id":"s1","source":"clear"}' | "$LAB_DIR/hooks/mark.sh" SessionStart
expect_eq 'session start source' "$(tail -n 1 "$LAB_MARKS" | jq -r .source)" clear
out=$(printf 'not json' | "$LAB_DIR/hooks/mark.sh" Stop); rc=$?
expect_eq 'unparseable stdin: exit 0, no stdout' "$rc:$out" '0:'
expect_eq 'unparseable stdin row' "$(tail -n 1 "$LAB_MARKS" | jq -c 'del(.ts)')" '{"event":"Stop","error":"stdin-unparseable"}'
finish
