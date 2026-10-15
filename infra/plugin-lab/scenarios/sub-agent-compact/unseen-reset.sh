# S8: a native compaction the plugin never saw (its session.compact bypassed) is logged as
# compact-unseen and re-arms the nudges from level 1 (R6).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=sub-agent-compact
LAB_ROWS=(R6)
LAB_MODEL=sonnet
LAB_CAP_USD=1.20
LAB_EST_USD=0.55
LAB_WALL_S=600
LAB_ENV=(CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000)
LAB_ALLOW=(Read mcp__sub-agent-compact__forward_handoff)
LAB_DENY=(Bash Grep Glob)
LAB_MANAGED='{"cleanupPeriodDays":36500}'
LAB_GAP_IF_NOT_INDUCED='CLAUDE_CODE_MANAGED_SETTINGS_PATH is not read beside an OS policy folder; cc-plugin-sec-default@builtin not seated'
LAB_LOGGER=yes
lab_options '{"forwardHandoffTool":true,"mainAutoCompactNudgeStart":"30k","mainAutoCompactNudgeEvery":"10k"}'

lab_scenario() {
  expect_bypass sub-agent-compact session.compact
  for n in 01 02 03 04 05 06 07 08; do need_corpus "block-$n" 15000; done
  beat ready -- turn 'Reply READY.' 300
  beat seated --needs ready -- bypass_seated cc-plugin-sec-default@builtin
  beat fill --needs seated -- fill_until_boundary block 01 06
  beat no-plugin-row --needs fill -- decision_lacks 'select(.event == "session.compact")'
  beat unseen --needs fill -- decision_has 'select(.event == "compact-unseen" and .party == "main" and .before >= 60000 and .after < .before)'
  beat unseen-witness --needs unseen -- witness_compactions
  beat re-read --needs unseen -- read_turns block 07 08
  beat re-armed --needs re-read -- decision_count 'select(.event == "nudge" and .party == "main" and .level == 1 and .tokens >= 30000)' -ge 2
}
