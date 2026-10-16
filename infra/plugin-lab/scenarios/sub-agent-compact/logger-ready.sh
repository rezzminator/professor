# S1: one turn; the plugin's first row under PLUGIN_LOG_DIR is the decision log's start (R1).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=sub-agent-compact
LAB_ROWS=(R1)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.02
LAB_WALL_S=300
LAB_ENV=()
LAB_LOGGER=yes

lab_scenario() {
  beat ready -- turn 'Reply with the word READY.' 300
  beat decision-log-start --needs ready -- decision_first start
}
