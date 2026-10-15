# Fixture scenario: the plugin tree changes mid-run (L16).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=fake-plugin
LAB_ROWS=(R8)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.01
LAB_WALL_S=300
LAB_ENV=()
LAB_LOGGER=yes
lab_options '{}'

lab_scenario() {
  beat ready -- turn 'Reply with the word READY.' 20
  printf 'changed\n' >> "$LAB_PLUGIN_DIR/README.md"
}
