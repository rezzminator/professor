---
name: gpt-flights-lander
from: flights-lander
extends:
  - shared/gpt-flights.md
description: flights-lander on GPT sol high
model: gpt-6.1-sol[1m]
effort: high
visibility:
  visible-to:
    include:
      - main
      - gpt-flights-foreman
      - gpt-foreman-pro
add:
  tools:
    - Skill
rename:
  reviewer: gpt-reviewer
  reviewer-pro: gpt-reviewer-pro
---
