---
name: "gpt-foreman"
from: "flights-foreman"
extends:
  - "gpt-flights"
description: "Builds one request — GPT flights through an OpenAI-compatible gateway; spawn by name without a model override. gpt-foreman → gpt-executor, gpt-lander. Returns DONE, PARTIAL, FAILED or BLOCKED."
model: "gpt-6.1-sol[1m]"
effort: "high"
add:
  hooks:
    PreToolUse:
      - matcher: "Bash"
        hooks:
          - type: "command"
            command: "python3 -I \"${CLAUDE_CONFIG_DIR:-$HOME/.claude}/hooks/gpt-return-watch.py\""
    Stop:
      - hooks:
          - type: "command"
            command: "python3 -I \"${CLAUDE_CONFIG_DIR:-$HOME/.claude}/hooks/gpt-agent-stop.py\" foreman"
rename:
  flights-foreman: "gpt-foreman"
  flights-mechanical-executor: "gpt-executor"
  flights-lander: "gpt-lander"
---
