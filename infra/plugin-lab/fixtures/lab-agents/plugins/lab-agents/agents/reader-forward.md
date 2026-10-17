---
name: reader-forward
description: plugin-lab fixture agent; reads the files it is given, one per step
model: sonnet
tools: Read, mcp__sub-agent-compact__forward_handoff
autoCompact:
  nudgeFrom: 15k
---

# reader-forward

Follow the task you are given exactly, using only your tools. Reply briefly.
