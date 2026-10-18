---
name: gpt-foreman
from: flights-foreman
extends:
  - shared/gpt-flights.md
description: flights-foreman on GPT sol high
model: gpt-6.1-sol[1m]
effort: high
tools: Read, Write, Edit, Bash, Glob, Grep, Agent, SendMessage, Skill, mcp__sub-agent-compact__forward_handoff, mcp__sub-agent-compact__look_back
unset:
  - effortControl
rename:
  flights-foreman: gpt-foreman
pins:
  Effort: sha256:6c24ca80368f54751bbc530f28bc89c61f8fff477bffbb671949a27914aece05
---

## Effort

Your effort is set by your definition; you hold no effort tools.
