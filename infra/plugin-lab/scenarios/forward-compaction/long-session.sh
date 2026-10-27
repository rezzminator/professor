# S6: one long sonnet session: look_back before any compaction, a background notification, asks held
# past Claude Code's ask point while no handoff is stored, a stored forward_compaction handoff installed
# at the end of its turn (one compaction), the append-only decision log, and look_back over the
# compacted part (R4, R7a, R7k).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=forward-compaction
LAB_ROWS=(R4 R7a R7k)
LAB_MODEL=sonnet
LAB_CAP_USD=1.80
LAB_EST_USD=0.80
LAB_WALL_S=1800
LAB_ENV=(CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000 FORWARD_COMPACTION_CEILING=90k FORWARD_COMPACTION_NUDGE_START=88k FORWARD_COMPACTION_NUDGE_EVERY=1k)
LAB_ALLOW=(Read Task mcp__forward-compaction__look_back mcp__forward-compaction__forward_compaction)
LAB_DENY=(Bash Grep Glob)
LAB_LOGGER=yes

lab_scenario() {
  local results='select(.type == "user") | .message.content[]? | objects | select(.type == "tool_result") | {err: (.is_error == true), text: (.content | if type == "array" then map(.text? // "") | join("\n") else tostring end)}'
  need_agent lab-echo haiku Read
  for n in 01 02 03; do need_corpus "block-$n" 6000; done
  for n in $(seq -w 1 20); do need_corpus "slip-$n" 1200; done
  beat look-back-before-compaction --row R7k -- turn 'Call mcp__forward-compaction__look_back with no arguments and quote its answer.' 300
  beat look-back-nothing-yet --row R7k --needs look-back-before-compaction -- transcript_has "$results | select((.err | not) and (.text | contains(\"Nothing has been compacted yet\")))"
  beat look-back-row-clean --row R7k --needs look-back-before-compaction -- decision_has 'select(.event == "look-back" and .error == null and .hits == 0)'
  beat background-notification --row R7a -- turn 'Start a background Task with subagent_type lab-echo whose only job is to reply MARIGOLD-4417; wait for its notification, then reply OK.' 600
  beat background-done --row R7a --needs background-notification -- wait_subagent lab-echo 600
  beat background-delivered --row R7a --needs background-done -- turn 'Reply OK.' 300
  beat notification-entry --row R7a --needs background-delivered -- transcript_has 'select(.type != "assistant" and (tostring | contains("MARIGOLD-4417")) and (tostring | contains("task-notification")))'
  beat fill -- read_turns block 01 03
  beat prefix-saved --needs fill -- decision_prefix_save
  beat held-asks --needs fill -- read_turns slip 01 20 84000
  beat asks-held --needs held-asks -- decision_has 'select(.event == "compact" and .party == "main" and .decision == "skip" and .why == "waiting")'
  beat forward-store --row R7a --needs held-asks -- turn "Call forward_compaction with handoff set to these lines: '## Objective', 'lab objective TEAL-2210', '## Done', 'fill', '## Remaining', 'read slips', '## Next', 'reply OK'. Then reply OK." 300
  beat forward-stored --row R7a --needs forward-store -- decision_has 'select(.event == "stored" and .party == "main")'
  beat settled --row R7a --needs forward-stored -- turn 'Reply OK.' 300
  beat compaction --row R7a --needs settled -- witness_compactions
  beat one-compaction --needs settled -- decision_count 'select(.event == "compact" and .party == "main" and .decision != "skip")' -eq 1
  beat handoff-installed --row R7a --needs settled -- decision_has 'select(.event == "compact" and .party == "main" and .trigger == "plugin" and .decision == "install")'
  beat wake-text --row R7a --needs settled -- transcript_has 'select(.type != "assistant" and ([.message.content[]? | objects | select(.type == "tool_result")] | length == 0) and (tostring | contains("[forward-compaction: handoff]")) and (tostring | contains("TEAL-2210")))'
  beat decision-start -- decision_first start
  beat append-only --needs prefix-saved -- decision_prefix_kept
  beat look-back-notification --row R7a --needs settled -- turn 'Call look_back with query MARIGOLD-4417.' 300
  beat notification-hit --row R7a --needs look-back-notification -- transcript_has "$results | select((.text | contains(\"MARIGOLD-4417\")) and (.text | contains(\"task-notification\")) and (.text | contains(\"unsupported entry type\") | not))"
  beat look-back-hits --row R7a --needs look-back-notification -- decision_has 'select(.event == "look-back" and ((.query // "") | contains("MARIGOLD-4417")) and (.hits // 0) >= 1)'
  beat no-unsupported-entry --row R7a --needs look-back-notification -- decision_lacks 'select(.event == "look-back" and ((.error // "") | contains("unsupported entry type")))'
  beat look-back-entry-null --row R7k --needs settled -- turn "Call look_back with query 'TEAL-2210' and entry null." 300
  beat entry-null-accepted --row R7k --needs look-back-entry-null -- decision_has 'select(.event == "look-back" and ((.query // "") | contains("TEAL-2210")) and .error == null)'
}
