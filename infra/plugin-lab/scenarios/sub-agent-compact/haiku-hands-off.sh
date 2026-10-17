# S5: a Haiku sub-agent and a Haiku main are left to Claude Code's own compaction (R3).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=sub-agent-compact
LAB_ROWS=(R3)
LAB_MODEL=haiku
LAB_CAP_USD=0.50
LAB_EST_USD=0.05
LAB_WALL_S=1200
LAB_ENV=(CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000 SUB_AGENT_COMPACT_FORWARD_HANDOFF_TOOL=true)
LAB_ALLOW=(Read Task mcp__sub-agent-compact__forward_handoff)
LAB_LOGGER=yes
lab_options '{}'

lab_scenario() {
  need_agent lab-haiku-reader haiku 'Read, mcp__sub-agent-compact__forward_handoff' '{nudgeFrom: 15k, nudgeEvery: 5k, forceAt: 40k}'
  for n in 01 02 03 04 05; do need_corpus "block-$n" 15000; done
  beat delegate -- turn 'Use the Task tool with subagent_type lab-haiku-reader. It must read corpus/block-01.txt through block-05.txt, one file per step, then reply DONE.' 900
  beat subagent-stop --needs delegate -- wait_subagent lab-haiku-reader 900
  beat engine-boundary --needs subagent-stop -- agent_transcript_has 'select(.type == "system" and .subtype == "compact_boundary")'
  beat pass-haiku-only --needs engine-boundary -- decision_count "select(.event == \"session.compact\" and .agentId == \"$LAB_AGENT_ID\" and (.decision != \"pass\" or .why != \"haiku\"))" -eq 0
  beat haiku-limit --needs engine-boundary -- decision_has "select(.event == \"session.compact\" and .agentId == \"$LAB_AGENT_ID\" and .limit == 967000)"
  beat two-witness --needs engine-boundary -- witness_compactions --agent lab-haiku-reader
  beat hands-off --needs subagent-stop -- decision_lacks "select((.agentId == \"$LAB_AGENT_ID\" or .party == \"$LAB_AGENT_ID\") and (.event == \"nudge\" or .event == \"armed\" or .event == \"brief-seated\" or .event == \"forward-stored\" or .event == \"turn.complete\"))"
  beat main-forward -- turn "Call mcp__sub-agent-compact__forward_handoff with objective 'lab objective HAIKU-5502', done ['x'], remaining ['y'], next 'z'." 300
  beat forward-refused --needs main-forward -- decision_has 'select(.event == "forward-refused" and .party == "main" and .why == "haiku")'
  beat nothing-stored --needs main-forward -- decision_lacks 'select(.event == "forward-stored")'
}
