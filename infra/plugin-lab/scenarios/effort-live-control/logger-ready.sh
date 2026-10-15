# S2: one turn; the plugin logger is ready under PLUGIN_LOG_DIR and the pane shows no chmod noise (R1).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=effort-live-control
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
  beat logger-where --needs ready -- level_has info 'select(.event == "logger-ready" and .where == "hooks/effort-live-control.ts#session.start")'
  beat no-chmod-noise --needs ready -- pane_lacks 'chmod: --'
}
