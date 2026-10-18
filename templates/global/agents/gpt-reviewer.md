---
name: gpt-reviewer
from: reviewer
extends:
  - shared/gpt.md
description: reviewer on GPT sol high
model: gpt-6.1-sol[1m]
effort: high
visibility:
  visible-to:
    include:
      - main
      - gpt-flights-lander
tools: Read, Write, Grep, Glob, Bash, Agent, mcp__sub-agent-compact__forward_handoff, mcp__sub-agent-compact__look_back
unset:
  - effortControl
---
