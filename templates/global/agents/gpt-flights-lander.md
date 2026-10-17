---
name: gpt-flights-lander
from: flights-lander
extends:
  - shared/gpt-flights.md
description: flights-lander on GPT sol high
model: gpt-6.1-sol[1m]
effort: high
visibility:
  visible-to:
    include:
      - main
      - gpt-flights-foreman
      - gpt-foreman-pro
tools: Read, Write, Edit, Bash, Glob, Grep, Agent, mcp__sub-agent-compact__forward_handoff, mcp__sub-agent-compact__look_back, Skill
unset:
  - effortControl
rename:
  reviewer: gpt-reviewer
  reviewer-pro: gpt-reviewer-pro
---
