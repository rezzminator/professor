# 2026-10-13 — plugin tools invisible to agents with a fixed tools list

Observed: 23 agent templates list `mcp__sub-agent-compact__compact` in `tools:`; none lists `forward_handoff` or `look_back`, so a real flights-foreman sub-agent cannot call either; the forward-handoff benchmark ran foremen as main chats (agentRole), which hid this.

Amend: a plugin tool meant for sub-agents ships with its name added to every agent's tools list that names the plugin's other tools, in the same pass.
