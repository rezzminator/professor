# Fixture scenario: an option still passed as pluginConfigs JSON, which the plugin reads nowhere (vital/options-ignored).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=fake-plugin
LAB_ROWS=(R8)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.01
LAB_WALL_S=300
LAB_ENV=()
LAB_LOGGER=yes
lab_options '{"forwardHandoffTool":true,"mainAutoCompact":"90k"}'

lab_scenario() {
  beat ready -- turn 'Reply with the word READY.' 20
}
