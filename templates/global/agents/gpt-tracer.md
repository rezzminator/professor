---
name: gpt-tracer
from: tracer
extends:
  - shared/gpt.md
description: tracer on GPT sol high
model: gpt-6.1-sol[1m]
effort: high
visibility:
  visible-to:
    include:
      - main
      - gpt-reviewer
      - gpt-reviewer-pro
      - gpt-foreman
      - gpt-flights-lander
      - gpt-clone-distiller
      - gpt-changelogger
rename:
  sub-tracer: gpt-sub-tracer
  Haiku: GPT luna
---
