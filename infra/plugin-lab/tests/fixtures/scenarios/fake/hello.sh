# Fixture scenario for the tests/lab_*_test.sh suites: one turn against the fake claude.
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=fake-plugin
LAB_ROWS=(R8)
LAB_MODEL=haiku
LAB_CAP_USD=${FAKE_CAP:-0.25}
LAB_EST_USD=0.01
LAB_WALL_S=${FAKE_WALL:-300}
LAB_ENV=("FAKE_CLAUDE_MODE=${FAKE_MODE:-normal}" "FAKE_LIVE=${FAKE_LIVE:-none}")
LAB_LOGGER=yes
lab_options '{}'

lab_scenario() {
  beat ready -- turn 'Reply with the word READY.' "${FAKE_TURN_S:-20}"
}
