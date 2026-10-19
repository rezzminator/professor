---
name: gpt-reviewer-pro
from: reviewer-pro
extends:
  - shared/gpt.md
description: reviewer-pro on GPT sol high
model: gpt-6.1-sol[1m]
effort: high
visibility:
  visible-to:
    include:
      - main
tools: Read, Write, Grep, Glob, Bash, Agent, mcp__sub-agent-compact__forward_handoff, mcp__sub-agent-compact__look_back
unset:
  - effortControl
---
