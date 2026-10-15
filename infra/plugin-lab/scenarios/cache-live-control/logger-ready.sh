# S3: one turn; a healthy session writes no plugin log row, so no folder appears under PLUGIN_LOG_DIR (R1).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=cache-live-control
LAB_ROWS=(R1)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.02
LAB_WALL_S=300
LAB_ENV=()
LAB_LOGGER=no
lab_options '{}'

lab_scenario() {
  beat ready -- turn 'Reply with the word READY.' 300
  beat no-startup-row --needs ready -- file_absent '{run}/logs/cache-live-control'
}
