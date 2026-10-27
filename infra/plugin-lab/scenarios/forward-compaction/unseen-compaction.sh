# S8: a native compaction the plugin never saw (its session.compact bypassed) is logged as
# unseen-compaction from main's context falling to half or less (R6).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=forward-compaction
LAB_ROWS=(R6)
LAB_MODEL=sonnet
LAB_CAP_USD=1.20
LAB_EST_USD=0.55
LAB_WALL_S=600
LAB_ENV=(CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000)
LAB_ALLOW=(Read)
LAB_DENY=(Bash Grep Glob)
LAB_MANAGED='{"cleanupPeriodDays":36500}'
LAB_GAP_IF_NOT_INDUCED='CLAUDE_CODE_MANAGED_SETTINGS_PATH is not read beside an OS policy folder; cc-plugin-sec-default@builtin not seated'
LAB_LOGGER=yes

lab_scenario() {
  expect_bypass forward-compaction session.compact
  for n in 01 02 03 04 05 06 07; do need_corpus "block-$n" 15000; done
  beat ready -- turn 'Reply READY.' 300
  beat seated --needs ready -- bypass_seated cc-plugin-sec-default@builtin
  beat fill --needs seated -- fill_until_boundary block 01 06
  beat re-read --needs fill -- read_turns block 07 07
  beat no-plugin-row --needs re-read -- decision_lacks 'select(.event == "compact")'
  beat unseen --needs re-read -- decision_has 'select(.event == "unseen-compaction" and .party == "main" and .before >= 60000 and .after <= .before / 2)'
  beat unseen-witness --needs unseen -- witness_compactions
}
