---
name: gpt-documenter
from: documenter
extends:
- shared/gpt.md
description: "MANDATORY Updates permanent docs \u2014 update, audit, registry or epic [name] with a current-state handoff. Returns per-surface lines, DEPENDENT rows or an audit. Prompt files \u2192 gpt-pcm."
model: gpt-6.1-sol[1m]
effort: high
rename:
  documenter: gpt-documenter
  pcm: gpt-pcm
unset:
- effortControl
tools: Read, Write, Edit, Bash, Glob, Grep, Agent, Skill, mcp__sub-agent-compact__forward_handoff, mcp__sub-agent-compact__look_back
---
