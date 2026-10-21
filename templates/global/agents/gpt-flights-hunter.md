---
name: gpt-flights-hunter
from: flights-hunter
extends:
- shared/gpt.md
description: "GPT-FLIGHTS-LANDER-ONLY Hunts unsafe behavior \u2014 independent bug hunt over a landing diff. Returns findings with evidence."
model: gpt-6.1-sol[1m]
effort: high
visibility:
  visible-to:
    include:
    - gpt-flights-lander
---
