# S9: a relative FORWARD_COMPACTION_LOG_FILE and a bad size in FORWARD_COMPACTION_CEILING are each named once as an
# error; logging falls back to the default path (R7f, R7g, R7n). The option errors are found before the start row,
# so they precede it in the log.
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=forward-compaction
LAB_ROWS=(R7f R7g R7n)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.03
LAB_WALL_S=300
LAB_ENV=(FORWARD_COMPACTION_LOG_FILE=decisions.jsonl FORWARD_COMPACTION_CEILING=bogus)
LAB_LOGGER=yes

lab_scenario() {
  expect_error option-error
  beat ready -- turn 'Reply READY.' 300
  beat again --needs ready -- turn 'Reply AGAIN.' 300
  beat logfile-named-once --row R7g --needs again -- level_count errors 'select(.event == "option-error" and (tostring | contains("FORWARD_COMPACTION_LOG_FILE is invalid")))' -eq 1
  beat bad-size-named-once --row R7n --needs again -- level_count errors 'select(.event == "option-error" and (tostring | contains("FORWARD_COMPACTION_CEILING is invalid")))' -eq 1
  beat default-log --row R7f --needs ready -- decision_has 'select(.event == "start")'
  beat no-relative-file --row R7f --needs ready -- file_absent '{run}/work/decisions.jsonl'
}
