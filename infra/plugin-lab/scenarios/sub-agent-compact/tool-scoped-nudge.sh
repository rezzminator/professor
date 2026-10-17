# S10: a marketplace plugin's agent is governed by its namespaced policy, and a party without the
# forward_handoff tool is never nudged (R7j, R7e).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=sub-agent-compact
LAB_ROWS=(R7j R7e)
LAB_MODEL=haiku
LAB_CAP_USD=0.60
LAB_EST_USD=0.25
LAB_WALL_S=1500
LAB_ENV=(SUB_AGENT_COMPACT_FORWARD_HANDOFF_TOOL=true SUB_AGENT_COMPACT_SUBAGENT_AUTO_COMPACT_NUDGE_START=120k)
LAB_ALLOW=(Read Task mcp__sub-agent-compact__forward_handoff)
LAB_LOGGER=yes
lab_options '{}'

lab_scenario() {
  need_fixture_plugin lab-agents lab-fixtures
  need_agent reader-bare sonnet Read '{nudgeFrom: 15k}'
  for n in 01 02; do need_corpus "block-$n" 15000; done
  beat fixture-only -- jsonl_has '{run}/config/plugins/installed_plugins.json' 'select((.plugins | keys) == ["lab-agents@lab-fixtures"])'
  beat namespaced-task --row R7e -- turn 'Use the Task tool with subagent_type lab-agents:reader-forward. It must read corpus/block-01.txt and corpus/block-02.txt, one file per step, then reply DONE.' 900
  beat namespaced-stop --row R7e --needs namespaced-task -- wait_subagent lab-agents:reader-forward 900
  beat namespaced-policy --row R7e --needs namespaced-stop -- decision_has 'select(.event == "nudge" and .type == "lab-agents:reader-forward" and .nudgeStart == 15000)'
  beat bare-task -- turn 'Use the Task tool with subagent_type reader-bare. It must read corpus/block-01.txt and corpus/block-02.txt, one file per step, then reply DONE.' 900
  beat bare-stop --needs bare-task -- wait_subagent reader-bare 900
  beat bare-context --needs bare-stop -- agent_transcript_has 'select(.type == "assistant" and (((.message.usage.input_tokens // 0) + (.message.usage.cache_read_input_tokens // 0) + (.message.usage.cache_creation_input_tokens // 0)) >= 15000))'
  beat tool-less-party --needs bare-context -- decision_lacks "select(.event == \"nudge\" and (.agentId == \"$LAB_AGENT_ID\" or .party == \"$LAB_AGENT_ID\"))"
}
