---
name: "gpt-executor"
from: "flights-mechanical-executor"
extends:
  - "gpt-flights"
description: "Applies one fixed bulk edit — from gpt-foreman through an OpenAI-compatible gateway; spawn by name without a model override. Returns DONE, SPEC-DRIFT, FAILED or BLOCKED."
model: "gpt-6-luna[1m]"
effort: "high"
visibility:
  visible-to:
    include:
      - "main"
      - "gpt-foreman"
      - "gpt-foreman-pro"
---
