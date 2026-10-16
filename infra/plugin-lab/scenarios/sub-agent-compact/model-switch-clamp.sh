# S7: a 1M main switched to a 200k model is clamped to Claude Code's ask point, and /clear leaves no
# false unseen compaction behind (R5, R7n). Opus 5.5 and Sonnet 5.5 are 1M on Claude Code 2.1.300
# (probe-window, probe-opus-window), so the 200k model is Sonnet 4.5.
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=sub-agent-compact
LAB_ROWS=(R5 R7n)
LAB_MODEL=sonnet
LAB_CAP_USD=1.00
LAB_EST_USD=0.45
LAB_WALL_S=2400
LAB_ENV=(CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000 SUB_AGENT_COMPACT_MAIN_AUTO_COMPACT=600k)
LAB_ALLOW=(Read)
LAB_DENY=(Bash Grep Glob)
LAB_LOGGER=yes

lab_scenario() {
  for n in 01 02 03 04 05; do need_corpus "block-$n" 15000; done
  beat start-1m -- turn 'Reply READY.' 300
  beat window-1m --needs start-1m -- decision_precondition 'select(.event == "start" and .mainWindow == 1000000)'
  beat switch --needs window-1m -- slash '/model claude-sonnet-4-5' '' 30 'Yes, switch'
  beat after-switch --needs switch -- turn 'Reply READY.' 300
  beat sonnet-4-5-request --needs after-switch -- transcript_has 'select(.type == "assistant" and ((.message.model // "") | contains("sonnet-4-5")))'
  beat window-changed --needs after-switch -- decision_has 'select(.event == "window-changed" and .party == "main" and .from == 1000000 and .to == 200000)'
  beat fill --needs after-switch -- read_turns block 01 05 67000
  beat ask --needs fill -- turn 'Reply READY.' 300
  beat clamped-skip --needs ask -- decision_has 'select(.event == "session.compact" and .agentId == null and .decision == "skip" and .limit == 167000 and .limitSet == 600000)'
  beat clamp-said --needs ask -- decision_has 'select(.event == "warning" and .key == "clamped:main")'
  beat clamp-once --needs ask -- level_count warnings 'select(.event == "warning" and (.msg | contains("main limit 600000 is past")))' -eq 1
  beat clear --row R7n --needs fill -- slash '/clear' SessionStart 60
  beat clear-source --row R7n --needs clear -- mark_has 'select(.event == "SessionStart" and .source == "clear")'
  beat cleared-row --row R7n --needs clear -- jsonl_has "{run}/logs/sub-agent-compact/decisions/$LAB_SID_PREV.jsonl" 'select(.event == "cleared" and .reason == "clear")'
  beat after-clear --row R7n --needs clear -- turn 'Reply READY.' 300
  beat no-unseen-after-clear --row R7n --needs after-clear -- level_lacks warnings 'select(.event == "compact-unseen")'
}
