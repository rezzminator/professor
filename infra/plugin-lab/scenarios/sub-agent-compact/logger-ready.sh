# S1: one turn; the plugin logger is ready under PLUGIN_LOG_DIR and the decision log opens with start (R1).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=sub-agent-compact
LAB_ROWS=(R1)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.02
LAB_WALL_S=300
LAB_ENV=()
LAB_LOGGER=yes
lab_options '{}'

lab_scenario() {
  beat ready -- turn 'Reply with the word READY.' 300
  beat decision-log-start --needs ready -- decision_first start
}
