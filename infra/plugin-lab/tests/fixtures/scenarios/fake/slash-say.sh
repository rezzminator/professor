# Fixture scenario for tests/lab_test.sh: a local slash command, then a turn typed right after it.
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=fake-plugin
LAB_ROWS=(R8)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.01
LAB_WALL_S=300
LAB_ENV=("FAKE_CLAUDE_MODE=${FAKE_MODE:-normal}" "FAKE_LIVE=none")
LAB_LOGGER=no
lab_options '{}'

lab_scenario() {
  beat switch -- slash '/model sonnet'
  beat after-switch --needs switch -- turn 'Reply with the word READY.' 20
  beat replied --needs after-switch -- reply_has READY
}
