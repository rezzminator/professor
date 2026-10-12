---
name: "gpt-lander"
from: "flights-lander"
extends:
  - "gpt-flights"
description: "Gates one whole flight — after gpt-foreman through an OpenAI-compatible gateway; spawn by name without a model override. Returns PASS, FIXED or FAIL and the gate path."
model: "gpt-6.1-sol[1m]"
effort: "high"
visibility:
  visible-to:
    include:
      - "main"
      - "gpt-foreman"
      - "gpt-foreman-pro"
add:
  tools:
    - "Skill"
rename:
  reviewer: "gpt-reviewer"
  reviewer-pro: "gpt-reviewer-pro"
---
