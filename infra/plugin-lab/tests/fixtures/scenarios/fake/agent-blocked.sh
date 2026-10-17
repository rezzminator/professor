# Fixture scenario for tests/lab_test.sh: beats naming the old session id and the sub-agent id
# after the verbs that set them never passed.
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=fake-plugin
LAB_ROWS=(R8)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.01
LAB_WALL_S=300
LAB_ENV=("FAKE_CLAUDE_MODE=normal" "FAKE_LIVE=none")
LAB_LOGGER=no
lab_options '{}'

lab_scenario() {
  beat ready -- turn 'Reply with the word READY.' 20
  beat agent --needs ready -- wait_subagent nobody 1
  beat prev-rows --needs agent -- jsonl_has "{run}/logs/fake-plugin/decisions/$LAB_SID_PREV.jsonl" 'select(.event == "cleared")'
  beat agent-rows --needs agent -- decision_lacks "select(.agentId == \"$LAB_AGENT_ID\")"
}
