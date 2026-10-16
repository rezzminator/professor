# Fixture scenario: the same options as stale-options, set as environment variables (vital/options-ignored passes).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=fake-plugin
LAB_ROWS=(R8)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.01
LAB_WALL_S=300
LAB_ENV=(SUB_AGENT_COMPACT_FORWARD_HANDOFF_TOOL=1 SUB_AGENT_COMPACT_MAIN_AUTO_COMPACT=90k)
LAB_LOGGER=yes

lab_scenario() {
  beat ready -- turn 'Reply with the word READY.' 20
}
