---
name: gpt-flights-spec-walker
from: flights-spec-walker
extends:
- shared/gpt.md
description: "GPT-FLIGHTS-LANDER-ONLY Walks specification claims \u2014 checks landing changes against the supplied requirements. Returns coverage and findings."
model: gpt-6.1-sol[1m]
effort: high
visibility:
  visible-to:
    include:
    - gpt-flights-lander
---
