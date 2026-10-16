# Fixture scenario for the tests/lab_*_test.sh suites: one turn against the fake claude. FAKE_OPTION adds one
# KEY=value to LAB_ENV, FAKE_DECISIONS sets LAB_DECISIONS, FAKE_EXPECT declares an expect_error event.
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=fake-plugin
LAB_ROWS=(R8)
LAB_MODEL=haiku
LAB_CAP_USD=${FAKE_CAP:-0.25}
LAB_EST_USD=0.01
LAB_WALL_S=${FAKE_WALL:-300}
LAB_ENV=("FAKE_CLAUDE_MODE=${FAKE_MODE:-normal}" "FAKE_LIVE=${FAKE_LIVE:-none}" ${FAKE_OPTION:+"$FAKE_OPTION"})
LAB_LOGGER=yes
LAB_DECISIONS=${FAKE_DECISIONS:-}
lab_options '{}'

lab_scenario() {
  [[ -z ${FAKE_EXPECT:-} ]] || expect_error "$FAKE_EXPECT"
  beat ready -- turn 'Reply with the word READY.' "${FAKE_TURN_S:-20}"
}
