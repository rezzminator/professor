---
name: gpt-flights-sub-lander
from: flights-sub-lander
extends:
- shared/gpt.md
description: "GPT-FLIGHTS-LANDER-ONLY Walks assigned threads \u2014 upstream and downstream review for a landing slice. Returns findings and coverage."
model: gpt-6.1-sol[1m]
effort: high
visibility:
  visible-to:
    include:
    - gpt-flights-lander
---
