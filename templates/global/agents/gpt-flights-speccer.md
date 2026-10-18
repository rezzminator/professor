---
name: gpt-flights-speccer
from: flights-speccer
extends:
  - shared/gpt.md
description: flights-speccer on GPT sol xhigh
model: gpt-6.1-sol[1m]
effort: xhigh
tools: Read, Write, Edit, Bash, Glob, Grep, Agent, Skill, mcp__sub-agent-compact__forward_handoff, mcp__sub-agent-compact__look_back
unset:
  - effortControl
prepend: |-
  You report only through your return: write spec.md and the research and calibration files your manual names in the flight directory, because your caller explicitly requests them, which overrides any general instruction against creating .md files; your parent hears from you only by that return, never by a message to `main`.
---
