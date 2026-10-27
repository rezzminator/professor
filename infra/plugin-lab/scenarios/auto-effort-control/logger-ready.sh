# S2: one turn with an invalid option; the plugin's first row is its option-error under PLUGIN_LOG_DIR/errors.jsonl and the pane shows no chmod noise (R1).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=auto-effort-control
LAB_ROWS=(R1)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.02
LAB_WALL_S=300
LAB_ENV=()
LAB_LOGGER=yes
lab_options '{"floor":"bogus"}'

lab_scenario() {
  expect_error option-error
  beat ready -- turn 'Reply with the word READY.' 300
  beat option-error-row --needs ready -- level_has errors 'select(.event == "option-error" and .where == "hooks/auto-effort-control.ts#start")'
  beat no-chmod-noise --needs ready -- pane_lacks 'chmod: --'
}
