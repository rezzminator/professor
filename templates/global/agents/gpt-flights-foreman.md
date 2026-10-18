---
name: gpt-flights-foreman
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
  flights-foreman: gpt-flights-foreman
  flights-foreman-pro: gpt-flights-foreman-pro
---
