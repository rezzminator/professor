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
add:
  hooks:
    Stop:
      - hooks:
          - type: command
            command: python3 -I "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/hooks/gpt-agent-stop.py" foreman
rename:
  flights-foreman: gpt-flights-foreman
  flights-mechanical-executor: gpt-flights-mechanical-executor
  flights-lander: gpt-flights-lander
  flights-speccer: gpt-flights-speccer
---
