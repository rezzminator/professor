# S9: a relative logFile and an unknown key each warn once; logging falls back to the default path
# (R7f, R7g, R7n). The option errors are found before the start row, so they precede it in the log.
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=sub-agent-compact
LAB_ROWS=(R7f R7g R7n)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.03
LAB_WALL_S=300
LAB_ENV=()
LAB_LOGGER=yes
lab_options '{"logFile":"decisions.jsonl","bogusKey":1}'

lab_scenario() {
  beat ready -- turn 'Reply READY.' 300
  beat again --needs ready -- turn 'Reply AGAIN.' 300
  beat logfile-warned-once --row R7g --needs again -- level_count warnings 'select(.event == "option-error" and (tostring | contains("logFile")))' -eq 1
  beat unknown-key-warned-once --row R7n --needs again -- level_count warnings 'select(.event == "option-error" and (tostring | contains("bogusKey")))' -eq 1
  beat default-log --row R7f --needs ready -- decision_has 'select(.event == "start")'
  beat no-relative-file --row R7f --needs ready -- file_absent '{run}/work/decisions.jsonl'
}
