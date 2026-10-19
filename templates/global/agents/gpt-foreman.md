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
  Effort: sha256:766815d76a287fd2cd296814fa411174f1b33cb941bfe6f28dbc4b55a051eb18
---

## Effort

Your effort is set by your definition; you hold no effort tools.
