# Fixture scenario: a turn whose cue word makes the fake write a compact boundary.
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=fake-plugin
LAB_ROWS=(R4)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.01
LAB_WALL_S=300
LAB_ENV=("FAKE_CLAUDE_MODE=${FAKE_MODE:-normal}")
LAB_LOGGER=yes
lab_options '{}'

lab_scenario() {
  beat fill -- turn 'COMPACT now' 20
  beat compaction --needs fill -- witness_compactions
}
