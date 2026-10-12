---
name: "gpt-reviewer"
from: "reviewer"
extends:
  - "gpt"
description: "Reviews one diff range for gpt-lander — reviewer's manual through an OpenAI-compatible gateway, its tracers on Claude; spawn by name without a model override. Returns one line, the review file path."
model: "gpt-6.1-sol[1m]"
effort: "high"
visibility:
  visible-to:
    include:
      - "main"
      - "gpt-lander"
---
