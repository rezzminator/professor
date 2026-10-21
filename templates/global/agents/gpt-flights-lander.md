---
name: gpt-flights-lander
from: flights-lander
extends:
  - shared/gpt-flights.md
description: flights-lander on GPT sol high
rename:
  flights-hunter: gpt-flights-hunter
  flights-spec-walker: gpt-flights-spec-walker
  flights-sub-lander: gpt-flights-sub-lander
model: gpt-6.1-sol[1m]
effort: high
visibility:
  visible-to:
    include:
      - main
tools: Read, Write, Edit, Bash, Glob, Grep, Agent, SendMessage, mcp__sub-agent-compact__forward_handoff, mcp__sub-agent-compact__look_back
---
