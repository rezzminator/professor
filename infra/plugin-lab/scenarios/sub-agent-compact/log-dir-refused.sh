# S4: PLUGIN_LOG_DIR is refused (mode 500); the setup failure reaches the absolute decision log,
# which the plugin names per session: {SUB_AGENT_COMPACT_LOG_FILE stem}.{sid}.jsonl (R1).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=sub-agent-compact
LAB_ROWS=(R1)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.02
LAB_WALL_S=300
LAB_ENV=('PLUGIN_LOG_DIR={run}/logs-ro' 'SUB_AGENT_COMPACT_LOG_FILE={run}/decisions.jsonl')
LAB_LOGGER=no
LAB_DECISIONS='{run}/decisions.{sid}.jsonl'

lab_scenario() {
  need_dir_mode '{run}/logs-ro' 500
  beat ready -- turn 'Reply with the word READY.' 300
  beat log-failed-row --needs ready -- decision_has "select(.event == \"plugin-log-failed\" and ((.dir // \"\") | startswith(\"$LAB_RUN/logs-ro\")) and ((.error // \"\") | length > 0))"
}
