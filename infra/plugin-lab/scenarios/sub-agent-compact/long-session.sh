# S6: one long sonnet session: look_back before any compaction, a background notification, a skip
# streak past Claude Code's ask point, a stored forward handoff installed at the next ask (one
# compaction), the append-only decision log, and look_back over the compacted part (R4, R7a, R7k).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=sub-agent-compact
LAB_ROWS=(R4 R7a R7k)
LAB_MODEL=sonnet
LAB_CAP_USD=1.80
LAB_EST_USD=0.80
LAB_WALL_S=1800
LAB_ENV=(CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000)
LAB_ALLOW=(Read Task mcp__sub-agent-compact__look_back mcp__sub-agent-compact__forward_handoff)
LAB_DENY=(Bash Grep Glob)
LAB_LOGGER=yes
lab_options '{"forwardHandoffTool":true,"mainAutoCompact":"90k","mainAutoCompactNudgeStart":"88k","mainAutoCompactNudgeEvery":"1k"}'

lab_scenario() {
  local results='select(.type == "user") | .message.content[]? | objects | select(.type == "tool_result") | {err: (.is_error == true), text: (.content | if type == "array" then map(.text? // "") | join("\n") else tostring end)}'
  need_agent lab-echo haiku Read
  for n in 01 02 03; do need_corpus "block-$n" 6000; done
  for n in $(seq -w 1 30); do need_corpus "slip-$n" 1200; done
  beat look-back-before-compaction --row R7k -- turn 'Call mcp__sub-agent-compact__look_back with no arguments and quote its answer.' 300
  beat look-back-nothing-yet --row R7k --needs look-back-before-compaction -- transcript_has "$results | select((.err | not) and (.text | contains(\"Nothing has been compacted yet\")))"
  beat look-back-row-clean --row R7k --needs look-back-before-compaction -- decision_has 'select(.event == "look-back" and .error == null and .hits == 0)'
  beat background-notification --row R7a -- turn 'Start a background Task with subagent_type lab-echo whose only job is to reply MARIGOLD-4417; wait for its notification, then reply OK.' 600
  beat background-done --row R7a --needs background-notification -- wait_subagent lab-echo 600
  beat background-delivered --row R7a --needs background-done -- turn 'Reply OK.' 300
  beat notification-entry --row R7a --needs background-delivered -- transcript_has 'select(.type != "assistant" and (tostring | contains("MARIGOLD-4417")) and (tostring | contains("task-notification")))'
  beat fill -- read_turns block 01 03
  beat prefix-saved --needs fill -- decision_prefix_save
  beat skip-streak --needs fill -- read_turns slip 01 20 84000
  beat forward-store --row R7a --needs skip-streak -- turn "Call forward_handoff with objective 'lab objective TEAL-2210', done ['fill'], remaining ['read slips'], next 'reply OK'." 300
  beat forward-stored --row R7a --needs forward-store -- decision_has 'select(.event == "forward-stored")'
  beat boundary --row R7a --needs forward-stored -- fill_until_boundary slip 21 30
  beat compaction --row R7a --needs boundary -- witness_compactions
  beat one-compaction --needs boundary -- decision_count 'select(.event == "session.compact" and .agentId == null and .decision != "skip")' -eq 1
  beat handoff-installed --row R7a --needs compaction -- decision_has 'select(.event == "session.compact" and .agentId == null and .decision == "handoff")'
  beat forward-installed --row R7a --needs compaction -- decision_has 'select(.event == "forward-installed" and .party == "main")'
  beat wake-text --row R7a --needs compaction -- transcript_has 'select(.type != "assistant" and ([.message.content[]? | objects | select(.type == "tool_result")] | length == 0) and (tostring | contains("[forward handoff")) and (tostring | contains("TEAL-2210")))'
  beat decision-start -- decision_first start
  beat append-only --needs prefix-saved -- decision_prefix_kept
  beat skip-rows-few --needs boundary -- decision_count 'select(.event == "session.compact" and .agentId == null and .decision == "skip" and .why != "handoff-waits")' -le 3
  beat skips-streak --needs boundary -- decision_has 'select(.event == "skips" and .party == "main" and .count >= 10)'
  beat look-back-notification --row R7a --needs compaction -- turn 'Call look_back with query MARIGOLD-4417.' 300
  beat notification-hit --row R7a --needs look-back-notification -- transcript_has "$results | select((.text | contains(\"MARIGOLD-4417\")) and (.text | contains(\"task-notification\")) and (.text | contains(\"unsupported entry type\") | not))"
  beat look-back-hits --row R7a --needs look-back-notification -- decision_has 'select(.event == "look-back" and ((.query // "") | contains("MARIGOLD-4417")) and (.hits // 0) >= 1)'
  beat no-unsupported-entry --row R7a --needs look-back-notification -- decision_lacks 'select(.event == "look-back" and ((.error // "") | contains("unsupported entry type")))'
  beat look-back-entry-null --row R7k --needs compaction -- turn "Call look_back with query 'TEAL-2210' and entry null." 300
  beat entry-null-accepted --row R7k --needs look-back-entry-null -- decision_has 'select(.event == "look-back" and ((.query // "") | contains("TEAL-2210")) and .error == null)'
  beat own-call-not-first --row R7k --needs look-back-entry-null -- transcript_has "$results | select((.err | not) and (.text | contains(\"TEAL-2210\")) and (.text | test(\"(?m)^#[0-9]+ \")) and ((.text | match(\"(?m)^#[0-9]+ .*$\").string) | contains(\"forward_handoff\") | not))"
}
